use std::time::Duration;

use anyhow::{Context, Result};
use hyper::StatusCode;
use reqwest::Client;
use serde_json::Value;
use url::Url;

use crate::auth_sessions::{display_name, sign_in};
use crate::browser_auth::{
    AuthResult, Browser, Route, config_url, email, random, safe_redirect, token_hash,
};
use crate::connections::{limited, valid_audience, verify_id_token};
use crate::host::{Fault, Reply, empty, header};
use crate::values;

const AUTH: &str = "https://accounts.google.com/o/oauth2/v2/auth";
const TOKEN: &str = "https://oauth2.googleapis.com/token";
const JWKS: &str = "https://www.googleapis.com/oauth2/v3/certs";

pub(crate) async fn serve(
    browser: &Browser<'_>,
    route: Route<'_>,
    query: Option<&str>,
) -> AuthResult<Reply> {
    let query = values::query_pairs(query);
    let get = |key: &str| {
        query
            .get(key)
            .and_then(|items| items.first())
            .map_or("", |value| value.trim())
    };
    let client = browser.string("auth.google_client_id");
    let secret = browser.secret("auth.google_client_secret");
    let api = config_url("SCENERY_API_BASE_URL").map_err(Fault::internal)?;
    let app = config_url("SCENERY_PUBLIC_APP_URL").map_err(Fault::internal)?;
    let origin = browser.external_origin();
    let api_url = api.as_ref().map_or(origin.as_str(), Url::as_str);
    let app_url = app.as_ref().map_or(origin.as_str(), Url::as_str);
    let api_url = api_url.trim_end_matches('/');
    let app_url = app_url.trim_end_matches('/');
    let fallback = || redirect(&format!("{app_url}/sign-in?error=google_internal"));
    let callback = if api.is_some() {
        format!("{api_url}/auth/google/callback")
    } else {
        format!("{api_url}{}/auth/google/callback", browser.prefix())
    };

    if matches!(route, Route::GoogleStart) {
        if client.is_empty() || secret.is_empty() || api_url.is_empty() {
            return Err(Fault::new(
                503,
                "unavailable",
                "Google OAuth is unavailable",
            ));
        }
        let state = random::<32>()?;
        let verifier = random::<48>()?;
        let nonce = random::<24>()?;
        let (db, _driver) = browser.connect().await?;
        db.execute(
            "DELETE FROM scenery.scenery_auth_oauth_states WHERE expires_at<now()",
            &[],
        )
        .await
        .map_err(Fault::internal)?;
        db.execute("INSERT INTO scenery.scenery_auth_oauth_states (id,state_hash,pkce_verifier,nonce_hash,redirect_path,expires_at)
            VALUES(gen_random_uuid(),$1,$2,$3,$4,now()+interval '10 minutes')",
            &[&token_hash(&state),&verifier,&token_hash(&nonce),&safe_redirect(get("redirect_path"))])
            .await.map_err(Fault::internal)?;
        return redirect(authorize(client, &callback, &state, &nonce, &verifier)?.as_str());
    }
    let error = |code: &str| redirect(&format!("{app_url}/sign-in?error={code}"));
    if !get("error").is_empty() {
        return error("google_oauth");
    }
    let (state, code) = (get("state"), get("code"));
    if state.is_empty() || code.is_empty() {
        return error("oauth_state");
    }
    if client.is_empty() || secret.is_empty() || api_url.is_empty() {
        return fallback();
    }
    let Ok((mut db, _driver)) = browser.connect().await else {
        return fallback();
    };
    let state = match db
        .query_opt(
            "UPDATE scenery.scenery_auth_oauth_states SET consumed_at=now()
        WHERE state_hash=$1 AND purpose='login' AND consumed_at IS NULL AND expires_at>now()
        RETURNING pkce_verifier,nonce_hash,redirect_path",
            &[&token_hash(state)],
        )
        .await
    {
        Ok(Some(row)) => row,
        Ok(None) => return error("oauth_state"),
        Err(_) => return fallback(),
    };
    let http = Client::builder()
        .timeout(Duration::from_secs(15))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(Fault::internal)?;
    let token = match exchange(&http, TOKEN, client, &secret, &callback, code, state.get(0)).await {
        Ok(token) => token,
        Err(_) => return error("google_token"),
    };
    let claims = match verify_id_token(&http, JWKS, &token).await {
        Ok(claims) if valid_claims(&claims, client, state.get(1)) => claims,
        _ => return error("google_id_token"),
    };
    if claims["email_verified"] != true {
        return error("google_email_unverified");
    }
    let auth = match finish(browser, &mut db, &claims).await {
        Ok(auth) => auth,
        Err(fault) if fault.code == "failed_precondition" => {
            return error("google_link_precondition");
        }
        Err(_) => return fallback(),
    };
    let mut response = redirect(&format!("{app_url}{}", safe_redirect(state.get(2))))?;
    if let Some(cookie) = auth.headers().get("set-cookie") {
        response.headers_mut().insert("set-cookie", cookie.clone());
    }
    Ok(response)
}

fn authorize(
    client: &str,
    callback: &str,
    state: &str,
    nonce: &str,
    verifier: &str,
) -> AuthResult<Url> {
    let mut url = Url::parse(AUTH).map_err(Fault::internal)?;
    url.query_pairs_mut().extend_pairs([
        ("client_id", client),
        ("redirect_uri", callback),
        ("response_type", "code"),
        ("scope", "openid email profile"),
        ("state", state),
        ("nonce", nonce),
        ("code_challenge", &token_hash(verifier)),
        ("code_challenge_method", "S256"),
        ("prompt", "select_account"),
    ]);
    Ok(url)
}

async fn exchange(
    http: &Client,
    endpoint: &str,
    client: &str,
    secret: &str,
    callback: &str,
    code: &str,
    verifier: &str,
) -> Result<String> {
    let mut response = http
        .post(endpoint)
        .form(&[
            ("code", code),
            ("client_id", client),
            ("client_secret", secret),
            ("redirect_uri", callback),
            ("grant_type", "authorization_code"),
            ("code_verifier", verifier),
        ])
        .send()
        .await?
        .error_for_status()?;
    let json = values::parse(&limited(&mut response, 64 << 10).await?)?;
    json["id_token"]
        .as_str()
        .filter(|token| !token.is_empty())
        .map(str::to_owned)
        .context("provider did not return an identity token")
}

fn valid_claims(claims: &Value, audience: &str, nonce_hash: &str) -> bool {
    matches!(
        claims["iss"].as_str(),
        Some("accounts.google.com" | "https://accounts.google.com")
    ) && valid_audience(claims, audience)
        && claims["sub"]
            .as_str()
            .is_some_and(|sub| !sub.trim().is_empty())
        && claims["nonce"]
            .as_str()
            .is_some_and(|nonce| token_hash(nonce) == nonce_hash)
}

async fn finish(
    browser: &Browser<'_>,
    db: &mut tokio_postgres::Client,
    claims: &Value,
) -> AuthResult<Reply> {
    use crate::browser_auth::{precondition, text};
    let address = text(claims, "email");
    let normalized = email(address)?;
    let subject = text(claims, "sub");
    let name = text(claims, "name");
    let avatar = text(claims, "picture");
    let tx = db.transaction().await.map_err(Fault::internal)?;
    // Serialize linking by verified address before inspecting existing identities.
    tx.execute(
        "SELECT pg_advisory_xact_lock(hashtextextended($1, 733))",
        &[&normalized],
    )
    .await
    .map_err(Fault::internal)?;
    let linked = tx.query_opt("SELECT user_id::text FROM scenery.scenery_auth_auth_identities WHERE provider='google' AND provider_subject=$1",
        &[&subject]).await.map_err(Fault::internal)?;
    let user: String = if let Some(linked) = linked {
        let id: String = linked.get(0);
        tx.execute("UPDATE scenery.scenery_auth_users SET display_name=COALESCE(NULLIF($2,''),display_name),
            avatar_url=COALESCE(NULLIF($3,''),avatar_url),updated_at=now() WHERE id=$1::text::uuid",
            &[&id,&name,&avatar]).await.map_err(Fault::internal)?;
        id
    } else {
        let existing = tx
            .query_opt(
                "SELECT id::text,email_verified_at IS NOT NULL AS verified
            FROM scenery.scenery_auth_users WHERE normalized_primary_email=$1 FOR UPDATE",
                &[&normalized],
            )
            .await
            .map_err(Fault::internal)?;
        let id: String = if let Some(existing) = existing {
            let id: String = existing.get(0);
            if !existing.get::<_, bool>("verified") {
                let linked: bool = tx.query_one("SELECT EXISTS(SELECT 1 FROM scenery.scenery_auth_auth_identities WHERE user_id=$1::text::uuid)",
                    &[&id]).await.map_err(Fault::internal)?.get(0);
                if linked {
                    return Err(precondition(
                        "verify the existing account before linking Google",
                    ));
                }
                tx.execute("UPDATE scenery.scenery_auth_users SET email_verified_at=now(),updated_at=now() WHERE id=$1::text::uuid",
                    &[&id]).await.map_err(Fault::internal)?;
            }
            id
        } else {
            tx.query_one("INSERT INTO scenery.scenery_auth_users (id,primary_email,normalized_primary_email,display_name,avatar_url,email_verified_at)
                VALUES(gen_random_uuid(),$1,$2,$3,$4,now()) RETURNING id::text",
                &[&address,&normalized,&display_name(&normalized,name),&avatar]).await.map_err(Fault::internal)?.get(0)
        };
        tx.execute("INSERT INTO scenery.scenery_auth_auth_identities (id,user_id,provider,provider_subject,email,normalized_email)
            VALUES(gen_random_uuid(),$1::text::uuid,'google',$2,$3,$4)",
            &[&id,&subject,&address,&normalized]).await.map_err(Fault::internal)?;
        id
    };
    let reply = sign_in(browser, &tx, &user, "login_google").await?;
    tx.commit().await.map_err(Fault::internal)?;
    Ok(reply)
}

fn redirect(location: &str) -> AuthResult<Reply> {
    if location.to_ascii_lowercase().contains("scenery")
        || location.contains(['\r', '\n', '\0', '\\'])
    {
        return Err(Fault::new(
            503,
            "unavailable",
            "browser redirect is unavailable",
        ));
    }
    let mut reply = empty(StatusCode::FOUND);
    header(reply.headers_mut(), "location", location);
    Ok(reply)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[tokio::test]
    async fn authorization_exchange_and_redirect() -> Result<()> {
        use tokio::io::{AsyncReadExt, AsyncWriteExt};
        let callback = "http://localhost/auth/google/callback";
        let url = authorize(
            "example",
            callback,
            "test-state",
            "test-nonce",
            "test-verifier",
        )
        .map_err(|_| anyhow::anyhow!("build authorization URL"))?;
        let query: std::collections::BTreeMap<_, _> = url.query_pairs().collect();
        assert_eq!(query["redirect_uri"], callback);
        assert_eq!(query["code_challenge"], token_hash("test-verifier"));
        assert_eq!(query["state"], "test-state");

        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await?;
        let endpoint = format!("http://{}/token", listener.local_addr()?);
        let server = tokio::spawn(async move {
            let (mut socket, _) = listener.accept().await?;
            let mut request = Vec::new();
            loop {
                let mut chunk = [0; 1024];
                let read = socket.read(&mut chunk).await?;
                if read == 0 {
                    break;
                }
                request.extend_from_slice(&chunk[..read]);
                if request
                    .windows(b"code_verifier=test-verifier".len())
                    .any(|value| value == b"code_verifier=test-verifier")
                {
                    break;
                }
            }
            assert!(String::from_utf8_lossy(&request).contains("grant_type=authorization_code"));
            let body = r#"{"id_token":"fixture"}"#;
            let response = format!(
                "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                body.len()
            );
            socket.write_all(response.as_bytes()).await
        });
        let client = Client::builder().timeout(Duration::from_secs(2)).build()?;
        assert_eq!(
            exchange(
                &client,
                &endpoint,
                "example",
                "secret",
                callback,
                "sample-code",
                "test-verifier"
            )
            .await?,
            "fixture"
        );
        server.await??;
        let claims =
            json!({"iss":"accounts.google.com","sub":"user","aud":"example","nonce":"test-nonce"});
        assert!(valid_claims(&claims, "example", &token_hash("test-nonce")));
        assert!(!valid_claims(
            &claims,
            "example",
            &token_hash("different-nonce")
        ));
        let reply = redirect(&format!(
            "https://example.test{}",
            safe_redirect("/profile")
        ))
        .map_err(|_| anyhow::anyhow!("build callback redirect"))?;
        assert_eq!(reply.headers()["location"], "https://example.test/profile");
        assert_eq!(safe_redirect("//other.test"), "/");
        Ok(())
    }
}
