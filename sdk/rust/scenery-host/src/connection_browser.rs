use anyhow::{Context, Result, ensure};
use hyper::{Method, StatusCode};
use serde::Deserialize;
use serde_json::{Value, json};
use tokio_postgres::{Client, GenericClient, Row};
use url::Url;

use super::{
    AbortOnDrop, Credentials, Grant, Provider, cipher_key, configured, decrypt, encrypt, limited,
    lock_owner_provider, missing_scope, normalize_scopes, parse_scopes, post_form, provider,
    provider_http, token_request, trusted_url, unavailable, valid_audience, verify_id_token,
};
use crate::auth::Claims;
use crate::browser_auth::{
    AuthResult, Browser, config_url, denied, invalid, ok, precondition, random, safe_redirect,
    unauthorized,
};
use crate::host::{Fault, Reply, empty, header};
use sha2::{Digest, Sha256};

#[derive(Clone, Copy)]
pub(crate) enum Route<'a> {
    Start(&'a str),
    Callback(&'a str),
    Status(&'a str),
    Disconnect(&'a str),
}

impl<'a> Route<'a> {
    pub(crate) fn select(
        method: &Method,
        path: &[&'a str],
        providers: &[Provider],
    ) -> Option<Self> {
        if providers.is_empty() {
            return None;
        }
        match (method, path) {
            (&Method::POST, ["auth", "connections", id, "start"]) if !id.is_empty() => {
                Some(Self::Start(id))
            }
            (&Method::GET, ["auth", "connections", id, "callback"])
                if providers.iter().any(|p| p.id == *id) =>
            {
                Some(Self::Callback(id))
            }
            (&Method::GET, ["auth", "connections", id]) if !id.is_empty() => Some(Self::Status(id)),
            (&Method::POST, ["auth", "connections", id, "disconnect"]) if !id.is_empty() => {
                Some(Self::Disconnect(id))
            }
            _ => None,
        }
    }
}

pub(crate) async fn serve(
    browser: &Browser<'_>,
    route: Route<'_>,
    user: &Claims,
    input: Value,
    query: Option<&str>,
) -> AuthResult<Reply> {
    tokio::time::timeout(std::time::Duration::from_secs(30), async {
        let id = match route {
            Route::Start(id) | Route::Callback(id) | Route::Status(id) | Route::Disconnect(id) => {
                id
            }
        };
        let provider = provider(browser.host, id)?;
        let (mut db, driver) = browser.connect().await.map_err(|_| unavailable())?;
        let _driver = AbortOnDrop(driver);
        match route {
            Route::Callback(_) => callback(browser, &mut db, provider, query).await,
            _ => {
                lock_owner_provider(&db, &user.user, provider).await?;
                let active = db
                    .query_one(
                        "SELECT EXISTS (
                     SELECT 1 FROM scenery.scenery_auth_refresh_sessions AS s
                     JOIN scenery.scenery_auth_users AS u ON u.id=s.user_id
                     JOIN scenery.scenery_auth_tenants AS t ON t.id=s.active_tenant_id
                     JOIN scenery.scenery_auth_organization_memberships AS m
                          ON m.user_id=s.user_id AND m.tenant_id=t.id
                     WHERE s.id=$1::text::uuid AND u.id=$2::text::uuid AND t.id=$3::text::uuid
                       AND s.revoked_at IS NULL AND s.expires_at>now() AND u.disabled_at IS NULL
                       AND t.deleted_at IS NULL AND m.disabled_at IS NULL)",
                        &[&user.session, &user.user, &user.tenant],
                    )
                    .await
                    .map_err(|_| unavailable())?
                    .get::<_, bool>(0);
                if !active {
                    return Err(unauthorized("connection browser session is unavailable"));
                }
                match route {
                    Route::Start(_) => start(browser, &db, provider, user, input).await,
                    Route::Status(_) => status(&db, provider, &user.user).await,
                    Route::Disconnect(_) => {
                        disconnect(browser, &mut db, provider, &user.user).await
                    }
                    Route::Callback(_) => unreachable!(),
                }
            }
        }
    })
    .await
    .map_err(|_| unavailable())?
}

#[derive(Deserialize)]
struct StartInput {
    scopes: Option<Vec<String>>,
    #[serde(default)]
    redirect_path: String,
}

async fn start(
    browser: &Browser<'_>,
    db: &Client,
    provider: &Provider,
    user: &Claims,
    input: Value,
) -> AuthResult<Reply> {
    let input: StartInput =
        serde_json::from_value(input).map_err(|_| invalid("invalid connection request"))?;
    let scopes = normalize_scopes(input.scopes.unwrap_or_default())
        .ok_or_else(|| invalid("invalid required connection scopes"))?;
    if !scopes
        .iter()
        .all(|scope| provider.allowed_scopes.contains(scope))
    {
        return Err(denied("requested connection scope is not allowed"));
    }
    let callback = callback_url(browser, provider)?;
    if !browser.request_hostname_matches(&callback) {
        return Err(invalid(
            "connection start must use the configured callback hostname",
        ));
    }
    let client = provider.credentials(&browser.host.configuration)?;
    let authorize = checked_authorize_url(provider)
        .map_err(|_| configured("connection provider configuration is invalid"))?;
    let existing = db
        .query_opt(
            "SELECT scopes FROM scenery.scenery_auth_connections
         WHERE user_id=$1::text::uuid AND provider_id=$2 AND status='active'
           AND refresh_token_ciphertext IS NOT NULL",
            &[&user.user, &provider.id],
        )
        .await
        .map_err(|_| unavailable())?;
    let consent = existing
        .as_ref()
        .and_then(|r| parse_scopes(r.get(0)))
        .is_none_or(|granted| !scopes.iter().all(|scope| granted.contains(scope)));

    let state = random::<32>()?;
    let verifier = random::<32>()?;
    let nonce = random::<32>()?;
    let binding = random::<32>()?;
    let scopes_wire = scopes.join(" ");
    let authorize = authorization_url(
        authorize,
        provider,
        &client,
        &callback,
        &scopes,
        [&state, &verifier, &nonce],
        consent,
    );
    if authorize.as_str().to_ascii_lowercase().contains("scenery") {
        return Err(configured("connection provider configuration is invalid"));
    }
    db.execute(
        "DELETE FROM scenery.scenery_auth_connection_states WHERE expires_at<=now()",
        &[],
    )
    .await
    .map_err(|_| unavailable())?;
    db.execute(
        "INSERT INTO scenery.scenery_auth_connection_states
           (id,state_hash,user_id,session_id,provider_id,issuer,resource,client_id,browser_hash,
            callback_uri,requested_scopes,pkce_verifier,nonce_hash,redirect_path,expires_at)
         VALUES (gen_random_uuid(),$1,$2::text::uuid,$3::text::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,now()+interval '10 minutes')",
        &[&hash(&state), &user.user, &user.session, &provider.id, &provider.issuer(), &provider.resource,
          &client.id, &hash(&binding), &callback.as_str(), &scopes_wire, &verifier, &hash(&nonce),
          &safe_redirect(&input.redirect_path)],
    ).await.map_err(|_| unavailable())?;
    let mut reply = ok(&json!({"authorize_url":authorize.as_str()}));
    set_binding_cookie(&mut reply, provider, &callback, &binding);
    Ok(reply)
}

fn checked_authorize_url(provider: &Provider) -> Result<Url> {
    trusted_url(provider.issuer())?;
    trusted_url(provider.token_url())?;
    if provider.oidc() {
        trusted_url(provider.jwks_url())?;
    } else {
        trusted_url(&provider.account_url)?;
    }
    trusted_url(provider.authorize_url())
}

fn authorization_url(
    mut url: Url,
    provider: &Provider,
    client: &Credentials<'_>,
    callback: &Url,
    scopes: &[String],
    [state, verifier, nonce]: [&str; 3],
    consent: bool,
) -> Url {
    let mut query: std::collections::BTreeMap<_, _> = url.query_pairs().into_owned().collect();
    let mut scopes = scopes.to_vec();
    if provider.oidc() {
        scopes.push("openid".into());
        query.insert("nonce".into(), nonce.into());
    }
    if provider.preset == "google" {
        scopes.extend(["email".into(), "profile".into()]);
        query.insert("access_type".into(), "offline".into());
        query.insert("include_granted_scopes".into(), "true".into());
        if consent {
            query.insert("prompt".into(), "consent".into());
        }
    }
    if !provider.resource.is_empty() {
        query.insert("resource".into(), provider.resource.clone());
    }
    let scope = normalize_scopes(scopes)
        .expect("validated scopes")
        .join(" ");
    let challenge = crate::values::base64url(&Sha256::digest(verifier));
    query.extend([
        ("client_id".into(), client.id.into()),
        ("response_type".into(), "code".into()),
        ("redirect_uri".into(), callback.as_str().into()),
        ("state".into(), state.into()),
        ("scope".into(), scope),
        ("code_challenge".into(), challenge),
        ("code_challenge_method".into(), "S256".into()),
    ]);
    url.query_pairs_mut().clear().extend_pairs(query);
    url
}

fn callback_url(browser: &Browser<'_>, provider: &Provider) -> AuthResult<Url> {
    let name = format!("auth.connections.{}.callback_uri", provider.id);
    let suffix = format!("/auth/connections/{}/callback", provider.id);
    let configured_url = browser.string(&name);
    let error = || configured("connection callback URI is invalid");
    let generated;
    let value = if configured_url.is_empty() {
        let api = config_url("SCENERY_API_BASE_URL").map_err(|_| error())?;
        let base = api.map_or_else(
            || format!("{}{}", browser.external_origin(), browser.prefix()),
            |url| url.to_string(),
        );
        generated = format!("{}{suffix}", base.trim_end_matches('/'));
        generated.as_str()
    } else {
        configured_url
    };
    trusted_url(value)
        .ok()
        .filter(|url| {
            url.query().is_none()
                && url.path().ends_with(&suffix)
                && !url.as_str().to_ascii_lowercase().contains("scenery")
        })
        .ok_or_else(error)
}

fn hash(value: &str) -> String {
    crate::process::hex(&Sha256::digest(value))
}

fn cookie_name(provider: &Provider, callback: &Url) -> String {
    format!(
        "app_connection_{}_{}",
        &hash(callback.authority())[..16],
        provider.id
    )
}

fn set_binding_cookie(reply: &mut Reply, provider: &Provider, callback: &Url, value: &str) {
    let expiry = if value.is_empty() {
        "Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:00 GMT"
    } else {
        "Max-Age=600"
    };
    let secure = if callback.scheme() == "https" {
        "; Secure"
    } else {
        ""
    };
    header(
        reply.headers_mut(),
        "set-cookie",
        &format!(
            "{}={value}; Path={}; {expiry}; HttpOnly; SameSite=Lax{secure}",
            cookie_name(provider, callback),
            callback.path(),
        ),
    );
}

async fn callback(
    browser: &Browser<'_>,
    db: &mut Client,
    provider: &Provider,
    query: Option<&str>,
) -> AuthResult<Reply> {
    let callback = callback_url(browser, provider)?;
    let query = crate::values::query_pairs(query);
    let get = |key: &str| {
        query
            .get(key)
            .and_then(|values| values.first())
            .map_or("", String::as_str)
    };
    let token = browser.cookie(&cookie_name(provider, &callback));
    let state = get("state");
    if token.is_empty() || state.is_empty() {
        return Err(invalid("connection authorization state is invalid"));
    }
    if !get("iss").is_empty() && get("iss") != provider.issuer() {
        return Err(invalid("connection issuer does not match"));
    }
    let client = provider.credentials(&browser.host.configuration)?;
    let state = hash(state);
    let invalid_state = || invalid("connection authorization state is invalid or expired");
    let user: String = db
        .query_opt(
            "SELECT user_id::text FROM scenery.scenery_auth_connection_states
          WHERE state_hash=$1 AND provider_id=$2 AND consumed_at IS NULL AND expires_at>now()",
            &[&state, &provider.id],
        )
        .await
        .map_err(|_| unavailable())?
        .ok_or_else(invalid_state)?
        .get(0);
    lock_owner_provider(db, &user, provider).await?;
    // The state consumption commits before network I/O. The session-level lock
    // stays held until the grant is saved, or the request is abandoned.
    let state = db.query_opt(
        "UPDATE scenery.scenery_auth_connection_states AS state SET consumed_at=now()
         WHERE state.state_hash=$1 AND state.provider_id=$2 AND state.browser_hash=$3
           AND state.issuer=$4 AND state.resource=$5 AND state.client_id=$6 AND state.callback_uri=$7
           AND state.purpose='connection' AND state.consumed_at IS NULL AND state.expires_at>now()
           AND EXISTS (
               SELECT 1 FROM scenery.scenery_auth_refresh_sessions AS s
               JOIN scenery.scenery_auth_users AS u ON u.id=s.user_id
               JOIN scenery.scenery_auth_tenants AS t ON t.id=s.active_tenant_id
               JOIN scenery.scenery_auth_organization_memberships AS m
                    ON m.user_id=s.user_id AND m.tenant_id=t.id
               WHERE s.id=state.session_id AND s.user_id=state.user_id
                 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.disabled_at IS NULL
                 AND t.deleted_at IS NULL AND m.disabled_at IS NULL)
         RETURNING pkce_verifier,nonce_hash,requested_scopes,redirect_path",
        &[&state, &provider.id, &hash(token), &provider.issuer(), &provider.resource,
          &client.id, &callback.as_str()],
    ).await.map_err(|_| unavailable())?.ok_or_else(invalid_state)?;
    let redirect = |key, value| {
        let mut result = redirect_reply(&callback, state.get(3), key, value)?;
        set_binding_cookie(&mut result, provider, &callback, "");
        Ok(result)
    };
    if !get("error").is_empty() {
        return redirect("error", "connection_authorization_denied");
    }
    let Some(scopes) = parse_scopes(state.get(2)) else {
        return redirect("error", "connection_configuration");
    };
    let Ok(grant) = exchange(
        provider,
        &client,
        &callback,
        get("code"),
        state.get(0),
        state.get(1),
        &scopes,
    )
    .await
    else {
        return redirect("error", "connection_exchange_failed");
    };
    match save_grant(browser, db, provider, &user, &grant, &scopes).await {
        Ok(()) => redirect("connection_connected", &provider.id),
        Err(fault) => redirect("error", fault.code),
    }
}

struct AccountGrant {
    grant: Grant,
    subject: String,
    email: String,
    scopes: Vec<String>,
}

async fn exchange(
    provider: &Provider,
    credentials: &Credentials<'_>,
    callback: &Url,
    code: &str,
    verifier: &str,
    nonce_hash: &str,
    requested: &[String],
) -> Result<AccountGrant> {
    ensure!(
        !code.is_empty() && !verifier.is_empty(),
        "missing authorization code or verifier"
    );
    let http = provider_http()?;
    let mut response = token_request(
        &http,
        trusted_url(provider.token_url())?,
        provider,
        credentials,
        vec![
            ("grant_type", "authorization_code"),
            ("code", code),
            ("code_verifier", verifier),
            ("redirect_uri", callback.as_str()),
        ],
    )?
    .send()
    .await?
    .error_for_status()?;
    let grant: Grant = serde_json::from_value(crate::values::parse(
        &limited(&mut response, 64 << 10).await?,
    )?)?;
    ensure!(
        grant.valid(provider) && grant.token_type.eq_ignore_ascii_case("bearer"),
        "invalid connection grant"
    );
    let (account, subject_field, email_field) = if provider.oidc() {
        let account = verify_id_token(
            &http,
            provider.jwks_url(),
            grant
                .id_token
                .as_deref()
                .context("missing identity token")?,
        )
        .await?;
        ensure!(
            provider.matches_issuer(&account["iss"])
                && valid_audience(&account, credentials.id)
                && account["nonce"]
                    .as_str()
                    .is_some_and(|nonce| hash(nonce) == nonce_hash),
            "invalid identity claims"
        );
        (account, "sub", "email")
    } else {
        let mut response = http
            .get(trusted_url(&provider.account_url)?)
            .bearer_auth(&grant.access_token)
            .header("accept", "application/json")
            .send()
            .await?
            .error_for_status()?;
        let account = crate::values::parse(&limited(&mut response, 1 << 20).await?)?;
        fn field<'a>(name: &'a str, default: &'static str) -> &'a str {
            if name.is_empty() { default } else { name }
        }
        (
            account,
            field(&provider.subject_field, "sub"),
            field(&provider.email_field, "email"),
        )
    };
    let subject = account[subject_field]
        .as_str()
        .filter(|subject| !subject.is_empty())
        .context("missing account subject")?
        .into();
    let email = if provider.preset == "google" && account["email_verified"] != true {
        String::new()
    } else {
        account[email_field].as_str().unwrap_or_default().into()
    };
    let scopes = grant
        .scope
        .as_deref()
        .and_then(parse_scopes)
        .unwrap_or_else(|| {
            let mut scopes = requested.to_vec();
            if provider.oidc() {
                scopes.push("openid".into());
            }
            if provider.preset == "google" {
                scopes.extend(["email".into(), "profile".into()]);
            }
            normalize_scopes(scopes).expect("validated scopes")
        });
    Ok(AccountGrant {
        grant,
        subject,
        email,
        scopes,
    })
}

async fn save_grant(
    browser: &Browser<'_>,
    db: &mut Client,
    provider: &Provider,
    user: &str,
    grant: &AccountGrant,
    required: &[String],
) -> AuthResult<()> {
    let key = cipher_key(&browser.host.configuration, browser.host.development)
        .map_err(|_| configured("connection credentials cannot be encrypted"))?;
    let cipher = |value| {
        encrypt(&key, value).map_err(|_| configured("connection credentials cannot be encrypted"))
    };
    let refresh = grant
        .grant
        .refresh_token
        .as_deref()
        .filter(|s| !s.is_empty())
        .map(cipher)
        .transpose()?;
    let access = cipher(&grant.grant.access_token)?;
    let tx = db.transaction().await.map_err(|_| unavailable())?;
    let owner = tx.query_opt(
        "SELECT id FROM scenery.scenery_auth_users WHERE id=$1::text::uuid AND disabled_at IS NULL FOR SHARE",
        &[&user],
    ).await.map_err(|_| unavailable())?;
    if owner.is_none() {
        return Err(denied("connection owner is unavailable"));
    }
    let saved = tx.execute(
        "INSERT INTO scenery.scenery_auth_connections AS c
           (id,user_id,provider_id,issuer,resource,provider_subject,email,scopes,token_type,
            access_token_ciphertext,refresh_token_ciphertext,access_token_expires_at)
         VALUES (gen_random_uuid(),$1::text::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,
                 now()+($11::bigint * interval '1 second'))
         ON CONFLICT (user_id,provider_id) DO UPDATE SET
           issuer=EXCLUDED.issuer,resource=EXCLUDED.resource,provider_subject=EXCLUDED.provider_subject,
           email=EXCLUDED.email,scopes=EXCLUDED.scopes,token_type=EXCLUDED.token_type,
           access_token_ciphertext=EXCLUDED.access_token_ciphertext,
           refresh_token_ciphertext=COALESCE(EXCLUDED.refresh_token_ciphertext,
               CASE WHEN c.status='active' THEN c.refresh_token_ciphertext END),
           access_token_expires_at=EXCLUDED.access_token_expires_at,status='active',
           last_refresh_at=NULL,last_refresh_error='',revocation_status='',
           connected_at=now(),disconnected_at=NULL,updated_at=now()
         WHERE c.status='disconnected' OR (c.issuer=$3 AND c.resource=$4 AND c.provider_subject=$5)",
        &[&user, &provider.id, &provider.issuer(), &provider.resource, &grant.subject, &grant.email,
          &grant.scopes.join(" "), &grant.grant.token_type, &access, &refresh, &grant.grant.expires_in],
    ).await.map_err(|_| unavailable())?;
    if saved == 0 {
        return Err(precondition(
            "disconnect the existing account before connecting another account or resource",
        ));
    }
    tx.commit().await.map_err(|_| unavailable())?;
    if !required.iter().all(|scope| grant.scopes.contains(scope)) {
        return Err(missing_scope());
    }
    Ok(())
}

async fn status(db: &Client, provider: &Provider, user: &str) -> AuthResult<Reply> {
    let row = connection_row(db, provider, user).await?;
    if row
        .as_ref()
        .is_some_and(|row| !matches_registration(row, provider))
    {
        return Err(configured("connection configuration changed; reconnect"));
    }
    Ok(ok(&metadata(provider, row.as_ref())))
}

async fn connection_row(
    db: &impl GenericClient,
    provider: &Provider,
    user: &str,
) -> AuthResult<Option<Row>> {
    db.query_opt(
        "SELECT issuer,resource,provider_subject,email,scopes,status,revocation_status,
                last_refresh_error,to_jsonb(connected_at) AS connected_at,
                to_jsonb(last_refresh_at) AS last_refresh_at,refresh_token_ciphertext,access_token_ciphertext
         FROM scenery.scenery_auth_connections WHERE user_id=$1::text::uuid AND provider_id=$2",
        &[&user,&provider.id],
    ).await.map_err(|_| unavailable())
}

fn matches_registration(row: &Row, provider: &Provider) -> bool {
    row.get::<_, &str>("issuer") == provider.issuer()
        && row.get::<_, &str>("resource") == provider.resource
}

fn metadata(provider: &Provider, row: Option<&Row>) -> Value {
    let Some(row) = row else {
        return json!({"provider":provider.id,"status":"disconnected"});
    };
    let status: &str = row.get("status");
    let mut output = json!({"provider":provider.id,"status":status});
    if status != "disconnected" {
        for field in ["issuer", "resource", "subject", "email"] {
            let value: &str = row.get(if field == "subject" {
                "provider_subject"
            } else {
                field
            });
            if !value.is_empty() {
                output[field] = json!(value);
            }
        }
        if let Some(scopes) = parse_scopes(row.get("scopes")).filter(|scopes| !scopes.is_empty()) {
            output["scopes"] = json!(scopes);
        }
    }
    for field in ["connected_at", "last_refresh_at"] {
        if let Some(time) = row.get::<_, Option<Value>>(field) {
            output[field] = time;
        }
    }
    let revocation: &str = row.get("revocation_status");
    if !revocation.is_empty() {
        output["revocation_status"] = json!(revocation);
    }
    if status == "refresh_pending" {
        output["status"] = json!("reauth_required");
        output["reauth_reason"] = json!("unknown_outcome");
    } else if status == "reauth_required" {
        let reason: &str = row.get("last_refresh_error");
        if !reason.is_empty() {
            output["reauth_reason"] = json!(reason);
        }
    }
    output
}

async fn disconnect(
    browser: &Browser<'_>,
    db: &mut Client,
    provider: &Provider,
    user: &str,
) -> AuthResult<Reply> {
    let tx = db.transaction().await.map_err(|_| unavailable())?;
    tx.execute(
        "UPDATE scenery.scenery_auth_connection_states SET consumed_at=now()
         WHERE user_id=$1::text::uuid AND provider_id=$2 AND consumed_at IS NULL",
        &[&user, &provider.id],
    )
    .await
    .map_err(|_| unavailable())?;
    let row = connection_row(&tx, provider, user).await?;
    if row
        .as_ref()
        .is_none_or(|r| r.get::<_, &str>("status") == "disconnected")
    {
        tx.commit().await.map_err(|_| unavailable())?;
        return Ok(ok(&metadata(provider, row.as_ref())));
    }
    tx.execute(
        "UPDATE scenery.scenery_auth_connections
         SET status='disconnected',revocation_status='pending',access_token_ciphertext=NULL,
             refresh_token_ciphertext=NULL,access_token_expires_at=NULL,last_refresh_error='',
             disconnected_at=now(),updated_at=now()
         WHERE user_id=$1::text::uuid AND provider_id=$2",
        &[&user, &provider.id],
    )
    .await
    .map_err(|_| unavailable())?;
    tx.commit().await.map_err(|_| unavailable())?;

    // Stop issuance durably before contacting the provider. The owner lock
    // also prevents revocation from racing a replacement grant.
    let revocation = revoke(browser, provider, &row.expect("connected row")).await;
    db.execute(
        "UPDATE scenery.scenery_auth_connections SET revocation_status=$3
         WHERE user_id=$1::text::uuid AND provider_id=$2",
        &[&user, &provider.id, &revocation],
    )
    .await
    .map_err(|_| unavailable())?;
    let result = connection_row(db, provider, user).await?;
    Ok(ok(&metadata(provider, result.as_ref())))
}

async fn revoke(browser: &Browser<'_>, provider: &Provider, row: &Row) -> &'static str {
    if !matches_registration(row, provider) {
        return "configuration_changed";
    }
    let envelope = row
        .get::<_, Option<Vec<u8>>>("refresh_token_ciphertext")
        .filter(|value| !value.is_empty())
        .or_else(|| row.get("access_token_ciphertext"));
    let Some(token) = envelope.and_then(|value| {
        let key = cipher_key(&browser.host.configuration, browser.host.development).ok()?;
        decrypt(&key, value).ok()
    }) else {
        return "credential_unavailable";
    };
    if provider.revoke_url().is_empty() {
        return "unsupported";
    }
    let result = async {
        let url = trusted_url(provider.revoke_url())?;
        let client = provider_http()?;
        let credentials = provider
            .credentials(&browser.host.configuration)
            .map_err(|_| anyhow::anyhow!("invalid provider credentials"))?;
        let mut form = vec![("token", token.as_str())];
        if provider.client_auth() == "basic" {
            form.push(("client_id", credentials.id));
        }
        let response = post_form(&client, url, provider, &credentials, form)?
            .send()
            .await?;
        ensure!(
            response.status().is_success(),
            "provider rejected revocation"
        );
        Ok::<_, anyhow::Error>(())
    }
    .await;
    if result.is_ok() {
        "succeeded"
    } else {
        "failed"
    }
}

fn redirect_reply(callback: &Url, path: &str, key: &str, value: &str) -> AuthResult<Reply> {
    let base = config_url("SCENERY_PUBLIC_APP_URL")
        .map_err(Fault::internal)?
        .or(config_url("SCENERY_API_BASE_URL").map_err(Fault::internal)?)
        .map_or_else(
            || callback.origin().ascii_serialization(),
            |url| url.to_string(),
        );
    let location = format!("{}{}", base.trim_end_matches('/'), safe_redirect(path));
    let mut url = trusted_url(&location).map_err(Fault::internal)?;
    let mut query: std::collections::BTreeMap<_, _> = url.query_pairs().into_owned().collect();
    query.insert(key.into(), value.into());
    url.query_pairs_mut().clear().extend_pairs(query);
    if url.as_str().to_ascii_lowercase().contains("scenery") {
        return Err(configured("connection redirect URL is invalid"));
    }
    let mut reply = empty(StatusCode::FOUND);
    header(reply.headers_mut(), "location", url.as_str());
    Ok(reply)
}
