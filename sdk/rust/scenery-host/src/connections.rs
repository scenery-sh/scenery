use std::time::Duration;

use anyhow::{Context, Result, ensure};
use base64::Engine;
use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use reqwest::{Client as HttpClient, StatusCode};
use ring::aead::{AES_256_GCM, Aad, LessSafeKey, Nonce, UnboundKey};
use ring::signature::{self, RsaPublicKeyComponents};
use scenery_wire::ConnectionTokenRequest;
use serde::Deserialize;
use serde_json::Value;
use sha2::{Digest, Sha256};
use time::OffsetDateTime;
use tokio_postgres::Client;

use crate::database::Session;
use crate::host::{Fault, Host, Scope};
use crate::plan::Auth;
use crate::values;

#[path = "connection_browser.rs"]
pub(crate) mod browser;

#[derive(Deserialize)]
pub(crate) struct Provider {
    id: String,
    #[serde(default)]
    preset: String,
    #[serde(default)]
    protocol: String,
    #[serde(default)]
    issuer: String,
    #[serde(default)]
    resource: String,
    #[serde(default)]
    authorize_url: String,
    #[serde(default)]
    token_url: String,
    #[serde(default)]
    revoke_url: String,
    #[serde(default)]
    account_url: String,
    #[serde(default)]
    subject_field: String,
    #[serde(default)]
    email_field: String,
    #[serde(default)]
    jwks_url: String,
    #[serde(default)]
    client_auth: String,
    allowed_scopes: Vec<String>,
}

impl Provider {
    fn issuer(&self) -> &str {
        if self.preset == "google" {
            "https://accounts.google.com"
        } else {
            &self.issuer
        }
    }

    fn authorize_url(&self) -> &str {
        if self.preset == "google" && self.authorize_url.is_empty() {
            "https://accounts.google.com/o/oauth2/v2/auth"
        } else {
            &self.authorize_url
        }
    }

    fn revoke_url(&self) -> &str {
        if self.preset == "google" && self.revoke_url.is_empty() {
            "https://oauth2.googleapis.com/revoke"
        } else {
            &self.revoke_url
        }
    }

    fn client_auth(&self) -> &str {
        match (self.preset.as_str(), self.client_auth.as_str()) {
            ("google", _) => "post",
            (_, "") => "basic",
            (_, method) => method,
        }
    }

    fn token_url(&self) -> &str {
        if self.preset == "google" && self.token_url.is_empty() {
            "https://oauth2.googleapis.com/token"
        } else {
            &self.token_url
        }
    }

    fn jwks_url(&self) -> &str {
        if self.preset == "google" && self.jwks_url.is_empty() {
            "https://www.googleapis.com/oauth2/v3/certs"
        } else {
            &self.jwks_url
        }
    }

    fn matches_issuer(&self, value: &Value) -> bool {
        value == self.issuer() || self.preset == "google" && value == "accounts.google.com"
    }

    fn oidc(&self) -> bool {
        self.protocol == "oidc" || self.preset == "google"
    }
}

pub(crate) async fn token(
    host: &Host,
    scope: &Scope,
    request: ConnectionTokenRequest<String>,
) -> Result<String, Fault> {
    let user = scope
        .call
        .principal
        .as_ref()
        .map(|claims| claims.user.as_str())
        .filter(|id| valid_uuid(id))
        .ok_or_else(Fault::forbidden)?;
    let provider = provider(host, &request.provider)?;
    let required = normalize_scopes(request.scopes).ok_or_else(Fault::invalid)?;
    if !required
        .iter()
        .all(|scope| provider.allowed_scopes.contains(scope))
    {
        return Err(Fault::forbidden());
    }
    let key = cipher_key(&host.configuration, host.development)
        .map_err(|_| configured("connection credentials cannot be decrypted"))?;
    host.ensure_auth_schema()
        .await
        .map_err(|_| configured("connection schema requires explicit migration"))?;
    // Keep one database session throughout the refresh checkpoint. A request
    // that is dropped while it holds the lock closes the session, which
    // releases the lock; otherwise the session returns to the pool unlocked.
    let mut database = host.sessions.acquire().await.map_err(|_| unavailable())?;
    lock_owner_provider(&mut database, user, provider).await?;
    let result = issue(
        &mut database,
        provider,
        user,
        &key,
        &host.configuration,
        &required,
    )
    .await;
    let _ = database.release_session_state().await;
    result
}

fn provider<'a>(host: &'a Host, id: &str) -> Result<&'a Provider, Fault> {
    host.bundle
        .auth
        .as_ref()
        .and_then(|auth: &Auth| auth.connections.iter().find(|provider| provider.id == id))
        .ok_or_else(|| {
            Fault::new(
                412,
                "connection_not_configured",
                "connection provider is not configured",
            )
        })
}

async fn lock_owner_provider(
    database: &mut Session,
    user: &str,
    provider: &Provider,
) -> Result<(), Fault> {
    let lock = format!(
        "scenery.connection:{}:{}",
        user.to_ascii_lowercase(),
        provider.id
    );
    // The lock belongs to the session, not to a transaction.
    database.hold_session_state();
    database
        .execute("SELECT pg_advisory_lock(hashtextextended($1, 0))", &[&lock])
        .await
        .map_err(|_| unavailable())?;
    Ok(())
}

async fn issue(
    database: &mut Client,
    provider: &Provider,
    user: &str,
    key: &LessSafeKey,
    configuration: &Value,
    required: &[String],
) -> Result<String, Fault> {
    let tx = database.transaction().await.map_err(|_| unavailable())?;
    let row = tx.query_opt(
        "SELECT c.issuer,c.resource,c.provider_subject,c.token_type,c.scopes,c.status,
                c.refresh_token_ciphertext,c.access_token_ciphertext,
                c.access_token_expires_at IS NULL,
                COALESCE(c.access_token_expires_at > now() +
                    CASE WHEN c.refresh_token_ciphertext IS NULL THEN interval '0 seconds' ELSE interval '60 seconds' END,
                    true)
         FROM scenery.scenery_auth_connections c
         JOIN scenery.scenery_auth_users u ON u.id=c.user_id
         WHERE c.user_id=CAST($1::text AS uuid) AND c.provider_id=$2 AND u.disabled_at IS NULL
         FOR UPDATE OF c",
        &[&user, &provider.id],
    ).await.map_err(|_| unavailable())?
        .ok_or_else(|| Fault::new(412, "connection_not_connected", "connection is not connected"))?;
    let issuer: String = row.get(0);
    let resource: String = row.get(1);
    let subject: String = row.get(2);
    let token_type: String = row.get(3);
    let raw_scopes: String = row.get(4);
    let status: String = row.get(5);
    let refresh: Option<Vec<u8>> = row.get(6);
    let access: Option<Vec<u8>> = row.get(7);
    let unknown_expiry: bool = row.get(8);
    let usable: bool = row.get(9);
    if issuer != provider.issuer()
        || resource != provider.resource
        || !token_type.eq_ignore_ascii_case("bearer")
    {
        return Err(configured("connection configuration changed; reconnect"));
    }
    if status != "active" {
        return Err(reauthorize());
    }
    let granted = parse_scopes(&raw_scopes).ok_or_else(|| configured("invalid stored scopes"))?;
    if !required.iter().all(|scope| granted.contains(scope)) {
        return Err(missing_scope());
    }
    if usable
        && let Some(access) = access
        && !access.is_empty()
    {
        let access = decrypt(key, access)
            .map_err(|_| configured("connection credentials cannot be decrypted"))?;
        if unknown_expiry {
            tx.execute(
                "UPDATE scenery.scenery_auth_connections SET access_token_ciphertext=NULL,updated_at=now()
                 WHERE user_id=CAST($1::text AS uuid) AND provider_id=$2",
                &[&user, &provider.id],
            ).await.map_err(|_| unavailable())?;
        }
        tx.commit().await.map_err(|_| unavailable())?;
        return Ok(access);
    }
    let Some(refresh) = refresh.filter(|value| !value.is_empty()) else {
        tx.execute(
            "UPDATE scenery.scenery_auth_connections SET status='reauth_required',
              last_refresh_error='refresh_credential_missing', updated_at=now()
             WHERE user_id=CAST($1::text AS uuid) AND provider_id=$2",
            &[&user, &provider.id],
        )
        .await
        .map_err(|_| unavailable())?;
        tx.commit().await.map_err(|_| unavailable())?;
        return Err(reauthorize());
    };
    let refresh = decrypt(key, refresh)
        .map_err(|_| configured("connection credentials cannot be decrypted"))?;
    let credentials = provider.credentials(configuration)?;
    let token_url = trusted_url(provider.token_url())
        .map_err(|_| configured("connection token URL is invalid"))?;
    tx.execute(
        "UPDATE scenery.scenery_auth_connections SET status='refresh_pending',
            last_refresh_error='unknown_outcome', updated_at=now()
         WHERE user_id=CAST($1::text AS uuid) AND provider_id=$2",
        &[&user, &provider.id],
    )
    .await
    .map_err(|_| unavailable())?;
    tx.commit().await.map_err(|_| unavailable())?;
    let grant = refresh_grant(provider, token_url, &credentials, &refresh, &subject).await;
    let grant = match grant {
        Ok(grant) => grant,
        Err(error) => {
            if let Some((status, reason)) = error.record() {
                database.execute(
                    "UPDATE scenery.scenery_auth_connections SET status=$3,last_refresh_error=$4,updated_at=now()
                     WHERE user_id=CAST($1::text AS uuid) AND provider_id=$2 AND status='refresh_pending'",
                    &[&user, &provider.id, &status, &reason],
                ).await.map_err(|_| unavailable())?;
            }
            return Err(error.fault());
        }
    };
    let scopes = match grant.scope.as_deref() {
        Some(raw) => parse_scopes(raw).ok_or_else(unavailable)?,
        None => granted,
    };
    let scope_match = required.iter().all(|scope| scopes.contains(scope));
    let is_bearer = grant.token_type.eq_ignore_ascii_case("bearer");
    let scope_list = scopes.join(" ");
    let access = if grant.expires_in.is_none() && is_bearer && scope_match {
        None
    } else {
        Some(
            encrypt(key, &grant.access_token)
                .map_err(|_| configured("connection credentials cannot be encrypted"))?,
        )
    };
    let refresh = grant
        .refresh_token
        .filter(|token| !token.is_empty())
        .map(|token| encrypt(key, &token))
        .transpose()
        .map_err(|_| configured("connection credentials cannot be encrypted"))?;
    let changed = database.execute(
        "UPDATE scenery.scenery_auth_connections SET status='active',last_refresh_error='',
           last_refresh_at=now(),updated_at=now(),refresh_token_ciphertext=COALESCE($3,refresh_token_ciphertext),
           access_token_ciphertext=$4,access_token_expires_at=now()+($5::bigint * interval '1 second'),
           scopes=$6, token_type=$7
         WHERE user_id=CAST($1::text AS uuid) AND provider_id=$2 AND status='refresh_pending'
            AND EXISTS (SELECT 1 FROM scenery.scenery_auth_users WHERE id=CAST($1::text AS uuid) AND disabled_at IS NULL)",
        &[&user, &provider.id, &refresh, &access, &grant.expires_in, &scope_list, &grant.token_type],
    ).await.map_err(|_| unavailable())?;
    if changed != 1 {
        return Err(Fault::forbidden());
    }
    if !is_bearer {
        return Err(configured("connection token type is unsupported"));
    }
    if !scope_match {
        return Err(missing_scope());
    }
    Ok(grant.access_token)
}

fn configured(message: &'static str) -> Fault {
    Fault::new(412, "connection_configuration", message)
}
fn unavailable() -> Fault {
    Fault::new(503, "connection_unavailable", "connection is unavailable")
}
fn reauthorize() -> Fault {
    Fault::new(
        412,
        "connection_reauth_required",
        "connection requires reauthorization",
    )
}
fn missing_scope() -> Fault {
    Fault::new(
        403,
        "connection_scope_missing",
        "connection is missing required scopes",
    )
}

enum RefreshFailure {
    Unknown,
    Revoked,
    Misconfigured,
    Temporary,
}

impl RefreshFailure {
    fn record(&self) -> Option<(&'static str, &'static str)> {
        match self {
            Self::Unknown => None,
            Self::Revoked => Some(("reauth_required", "revoked")),
            Self::Misconfigured => Some(("active", "misconfigured")),
            Self::Temporary => Some(("active", "temporarily_unavailable")),
        }
    }

    fn fault(&self) -> Fault {
        match self {
            Self::Unknown | Self::Temporary => unavailable(),
            Self::Revoked => reauthorize(),
            Self::Misconfigured => configured("connection provider configuration is invalid"),
        }
    }
}

struct Credentials<'a> {
    id: &'a str,
    secret: String,
}

impl Provider {
    fn credentials<'a>(&self, configuration: &'a Value) -> Result<Credentials<'a>, Fault> {
        let fault = || configured("connection client configuration is unavailable");
        let id = configuration["values"][format!("auth.connections.{}.client_id", self.id)]
            .as_str()
            .filter(|value| !value.is_empty())
            .ok_or_else(fault)?;
        let secret = configuration_secret(
            configuration,
            &format!("auth.connections.{}.client_secret", self.id),
        )
        .ok()
        .and_then(|value| String::from_utf8(value).ok())
        .ok_or_else(fault)?;
        if self.client_auth() != "none" && secret.is_empty() {
            return Err(fault());
        }
        Ok(Credentials { id, secret })
    }
}

fn provider_http() -> Result<HttpClient> {
    Ok(HttpClient::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .timeout(Duration::from_secs(15))
        .build()?)
}

fn post_form<'a>(
    client: &HttpClient,
    url: url::Url,
    provider: &'a Provider,
    credentials: &'a Credentials<'a>,
    mut form: Vec<(&str, &'a str)>,
) -> Result<reqwest::RequestBuilder> {
    let mut request = client.post(url);
    match provider.client_auth() {
        "basic" => {
            let escape = |value: &str| {
                url::form_urlencoded::byte_serialize(value.as_bytes()).collect::<String>()
            };
            request = request.basic_auth(escape(credentials.id), Some(escape(&credentials.secret)));
        }
        "post" => form.extend([
            ("client_id", credentials.id),
            ("client_secret", &credentials.secret),
        ]),
        "none" => form.push(("client_id", credentials.id)),
        _ => anyhow::bail!("invalid client authentication"),
    }
    let encoded = url::form_urlencoded::Serializer::new(String::new())
        .extend_pairs(form)
        .finish();
    Ok(request
        .header("content-type", "application/x-www-form-urlencoded")
        .body(encoded))
}

fn token_request<'a>(
    client: &HttpClient,
    url: url::Url,
    provider: &'a Provider,
    credentials: &'a Credentials<'a>,
    mut form: Vec<(&str, &'a str)>,
) -> Result<reqwest::RequestBuilder> {
    if !provider.resource.is_empty() {
        form.push(("resource", &provider.resource));
    }
    post_form(client, url, provider, credentials, form)
}

#[derive(Deserialize)]
struct Grant {
    access_token: String,
    token_type: String,
    refresh_token: Option<String>,
    expires_in: Option<i64>,
    scope: Option<String>,
    id_token: Option<String>,
    #[serde(default)]
    resource: String,
}

impl Grant {
    fn valid(&self, provider: &Provider) -> bool {
        !self.access_token.is_empty()
            && !self.access_token.contains(['\r', '\n'])
            && !self.token_type.is_empty()
            && self
                .expires_in
                .is_none_or(|value| (1..=315360000).contains(&value))
            && self
                .scope
                .as_deref()
                .is_none_or(|scope| parse_scopes(scope).is_some())
            && (self.resource.is_empty() || self.resource == provider.resource)
    }
}

async fn refresh_grant(
    provider: &Provider,
    token_url: url::Url,
    credentials: &Credentials<'_>,
    refresh: &str,
    subject: &str,
) -> Result<Grant, RefreshFailure> {
    let client = provider_http().map_err(|_| RefreshFailure::Misconfigured)?;
    let mut response = token_request(
        &client,
        token_url,
        provider,
        credentials,
        vec![("grant_type", "refresh_token"), ("refresh_token", refresh)],
    )
    .map_err(|_| RefreshFailure::Misconfigured)?
    .send()
    .await
    .map_err(|_| RefreshFailure::Unknown)?;
    let status = response.status();
    let data = limited(&mut response, 64 << 10)
        .await
        .map_err(|_| RefreshFailure::Unknown)?;
    let payload = values::parse(&data).map_err(|_| RefreshFailure::Unknown)?;
    if !status.is_success() {
        return Err(match payload["error"].as_str() {
            Some("invalid_grant") => RefreshFailure::Revoked,
            Some("invalid_client" | "unauthorized_client" | "unsupported_grant_type") => {
                RefreshFailure::Misconfigured
            }
            Some("temporarily_unavailable") => RefreshFailure::Temporary,
            _ if status == StatusCode::TOO_MANY_REQUESTS => RefreshFailure::Temporary,
            _ => RefreshFailure::Unknown,
        });
    }
    let grant: Grant = serde_json::from_value(payload).map_err(|_| RefreshFailure::Unknown)?;
    if !grant.valid(provider) {
        return Err(RefreshFailure::Unknown);
    }
    if provider.oidc()
        && let Some(token) = grant.id_token.as_deref().filter(|token| !token.is_empty())
    {
        check_id_token(&client, provider, credentials.id, subject, token)
            .await
            .map_err(|_| RefreshFailure::Unknown)?;
    }
    Ok(grant)
}

pub(crate) async fn limited(response: &mut reqwest::Response, limit: usize) -> Result<Vec<u8>> {
    let mut data = Vec::new();
    while let Some(chunk) = response.chunk().await? {
        ensure!(
            data.len().saturating_add(chunk.len()) <= limit,
            "provider reply exceeds limit"
        );
        data.extend_from_slice(&chunk);
    }
    Ok(data)
}

async fn check_id_token(
    client: &HttpClient,
    provider: &Provider,
    audience: &str,
    subject: &str,
    token: &str,
) -> Result<()> {
    let claims = verify_id_token(client, provider.jwks_url(), token).await?;
    ensure!(
        provider.matches_issuer(&claims["iss"])
            && claims["sub"] == subject
            && valid_audience(&claims, audience),
        "invalid identity claims"
    );
    Ok(())
}

pub(crate) async fn verify_id_token(
    client: &HttpClient,
    jwks_url: &str,
    token: &str,
) -> Result<Value> {
    let (message, sig) = token.rsplit_once('.').context("invalid identity token")?;
    let (header, claims) = message.split_once('.').context("invalid identity token")?;
    let header = values::parse(&URL_SAFE_NO_PAD.decode(header)?)?;
    ensure!(header["alg"] == "RS256", "invalid identity token algorithm");
    let kid = header["kid"]
        .as_str()
        .filter(|value| !value.is_empty())
        .context("missing identity key id")?;
    let mut response = client
        .get(trusted_url(jwks_url)?)
        .send()
        .await?
        .error_for_status()?;
    let keys = values::parse(&limited(&mut response, 64 << 10).await?)?;
    let key = keys["keys"]
        .as_array()
        .and_then(|keys| {
            keys.iter().find(|key| {
                key["kid"] == kid
                    && key["kty"] == "RSA"
                    && key["alg"].as_str().is_none_or(|alg| alg == "RS256")
                    && key["use"].as_str().is_none_or(|use_| use_ == "sig")
            })
        })
        .context("identity key unavailable")?;
    let n = URL_SAFE_NO_PAD.decode(key["n"].as_str().context("invalid identity key")?)?;
    let e = URL_SAFE_NO_PAD.decode(key["e"].as_str().context("invalid identity key")?)?;
    RsaPublicKeyComponents { n: &n, e: &e }
        .verify(
            &signature::RSA_PKCS1_2048_8192_SHA256,
            message.as_bytes(),
            &URL_SAFE_NO_PAD.decode(sig)?,
        )
        .map_err(|_| anyhow::anyhow!("invalid identity signature"))?;
    let claims = values::parse(&URL_SAFE_NO_PAD.decode(claims)?)?;
    let now = OffsetDateTime::now_utc().unix_timestamp();
    ensure!(
        claims["exp"].as_i64().is_some_and(|exp| exp > now)
            && claims["nbf"].as_i64().is_none_or(|nbf| nbf <= now),
        "invalid identity token expiry"
    );
    Ok(claims)
}

pub(crate) fn valid_audience(claims: &Value, audience: &str) -> bool {
    let aud = &claims["aud"];
    aud == audience
        || aud.as_array().is_some_and(|values| {
            values.iter().any(|value| value == audience)
                && (values.len() == 1 || claims["azp"] == audience)
        })
}

fn trusted_url(raw: &str) -> Result<url::Url> {
    let url = url::Url::parse(raw)?;
    ensure!(
        url.host().is_some()
            && url.username().is_empty()
            && url.password().is_none()
            && url.fragment().is_none()
            && (url.scheme() == "https"
                || (url.scheme() == "http"
                    && matches!(url.host_str(), Some("127.0.0.1" | "localhost" | "[::1]")))),
        "invalid provider URL"
    );
    Ok(url)
}

pub(crate) fn valid_uuid(value: &str) -> bool {
    value.len() == 36
        && value.bytes().enumerate().all(|(position, byte)| {
            if matches!(position, 8 | 13 | 18 | 23) {
                byte == b'-'
            } else {
                byte.is_ascii_hexdigit()
            }
        })
}

fn normalize_scopes(mut scopes: Vec<String>) -> Option<Vec<String>> {
    if scopes.iter().any(|scope| {
        scope.is_empty()
            || scope
                .bytes()
                .any(|b| !(0x21..=0x7e).contains(&b) || matches!(b, b'"' | b'\\'))
    }) {
        return None;
    }
    scopes.sort_unstable();
    scopes.dedup();
    Some(scopes)
}

fn parse_scopes(raw: &str) -> Option<Vec<String>> {
    normalize_scopes(if raw.is_empty() {
        vec![]
    } else {
        raw.split(' ').map(str::to_owned).collect()
    })
}

fn configuration_secret(config: &Value, key: &str) -> Result<Vec<u8>> {
    Ok(STANDARD.decode(config["secrets"][key].as_str().unwrap_or_default())?)
}

fn cipher_key(config: &Value, development: bool) -> Result<LessSafeKey> {
    let raw = configuration_secret(config, "auth.token_cipher_key")?;
    let configured = std::str::from_utf8(&raw)?.trim();
    let bytes = if configured.is_empty() {
        ensure!(development, "connection encryption key is missing");
        let seed = configuration_secret(config, "auth.jwt_secret")?;
        let seed = std::str::from_utf8(&seed)?.trim();
        let seed = if seed.is_empty() {
            "scenery-local-development-secret"
        } else {
            seed
        };
        Sha256::digest(format!("scenery-google-token:{seed}").as_bytes()).to_vec()
    } else {
        STANDARD.decode(configured)?
    };
    UnboundKey::new(&AES_256_GCM, &bytes)
        .map(LessSafeKey::new)
        .map_err(|_| anyhow::anyhow!("invalid connection encryption key"))
}

fn encrypt(key: &LessSafeKey, token: &str) -> Result<Vec<u8>> {
    let mut raw = [0; 12];
    getrandom::fill(&mut raw)?;
    let mut storage = token.as_bytes().to_vec();
    key.seal_in_place_append_tag(
        Nonce::assume_unique_for_key(raw),
        Aad::empty(),
        &mut storage,
    )
    .map_err(|_| anyhow::anyhow!("encrypt connection token"))?;
    let mut envelope = vec![1];
    envelope.extend_from_slice(&raw);
    envelope.extend_from_slice(&storage);
    Ok(envelope)
}

fn decrypt(key: &LessSafeKey, envelope: Vec<u8>) -> Result<String> {
    ensure!(
        envelope.len() >= 29 && envelope[0] == 1,
        "invalid connection ciphertext"
    );
    let nonce = Nonce::try_assume_unique_for_key(&envelope[1..13])
        .map_err(|_| anyhow::anyhow!("invalid connection nonce"))?;
    let mut ciphertext = envelope[13..].to_vec();
    let plain = key
        .open_in_place(nonce, Aad::empty(), &mut ciphertext)
        .map_err(|_| anyhow::anyhow!("decrypt connection token"))?;
    Ok(std::str::from_utf8(plain)?.to_owned())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn encrypts_versioned_tokens_and_rejects_tampering() {
        let config = json!({ "secrets": { "auth.token_cipher_key": STANDARD.encode(STANDARD.encode([9; 32])) } });
        let cipher = cipher_key(&config, false).unwrap();
        let mut encrypted = encrypt(&cipher, "secret bearer").unwrap();
        assert_eq!(encrypted[0], 1);
        assert_eq!(
            decrypt(&cipher, encrypted.clone()).unwrap(),
            "secret bearer"
        );
        encrypted[17] ^= 1;
        assert!(decrypt(&cipher, encrypted).is_err());
    }
}
