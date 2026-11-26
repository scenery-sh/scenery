use anyhow::{Context, Result, ensure};
use base64::Engine;
use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use hmac::{Hmac, KeyInit, Mac};
use hyper::HeaderMap;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::Sha256;
use time::OffsetDateTime;

use crate::plan::Auth;
use crate::values::base64url;

type Signature = Hmac<Sha256>;

#[derive(Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub(crate) struct Claims {
    #[serde(rename = "sub")]
    pub user: String,
    #[serde(rename = "tenant_id")]
    pub tenant: String,
    #[serde(rename = "sid")]
    pub session: String,
    #[serde(rename = "actor_sub")]
    pub actor: String,
    pub impersonation_id: String,
    pub(crate) exp: i64,
    pub(crate) nbf: Option<i64>,
}

pub(crate) struct Authentication {
    secret: Vec<u8>,
    dev_secret: String,
    dev_tenant: Option<String>,
    dev_email: String,
}

pub(crate) enum DevBootstrap<'a> {
    Token(String),
    Email { email: &'a str, tenant: String },
}

/// DEV_USER is the synthetic user a development sign-in without claims names.
const DEV_USER: &str = "dev-user";

impl Authentication {
    pub(crate) fn new(plan: Option<&Auth>, config: &Value, development: bool) -> Result<Self> {
        let mut secret = Vec::new();
        let mut dev_tenant = None;
        let mut dev_email = String::new();
        if let Some(plan) = plan.filter(|plan| plan.enabled) {
            if let Some(encoded) = config["secrets"]["auth.jwt_secret"].as_str() {
                let decoded = STANDARD.decode(encoded)?;
                secret = match std::str::from_utf8(&decoded) {
                    Ok(text) => text.trim().as_bytes().to_vec(),
                    Err(_) => decoded.trim_ascii().to_vec(),
                };
            }
            ensure!(
                development || !secret.is_empty(),
                "authentication requires auth.jwt_secret in the production configuration snapshot"
            );
            if development && plan.dev_bootstrap.enabled {
                dev_email = plan.dev_bootstrap.default_user_email.trim().into();
                let value = plan.dev_bootstrap.default_tenant_id.trim();
                dev_tenant = Some(if value.is_empty() {
                    "00000000-0000-0000-0000-000000000001".into()
                } else {
                    value.into()
                });
            }
        }
        if development && plan.is_some_and(|plan| plan.enabled) && secret.is_empty() {
            secret = crate::process::random_hex::<32>()?.into_bytes();
        }
        Ok(Self {
            secret,
            dev_secret: crate::process::random_hex::<32>()?,
            dev_tenant,
            dev_email,
        })
    }

    pub(crate) fn bootstrap(&self, input: &Value) -> Result<DevBootstrap<'_>> {
        let default = self
            .dev_tenant
            .as_deref()
            .context("development identity is disabled")?;
        let claim = |name, fallback: &str| -> Result<String> {
            let value = input
                .get(name)
                .map(|value| value.as_str().context("invalid identity claim"))
                .transpose()?
                .unwrap_or_default()
                .trim();
            ensure!(
                value.len() <= 200 && !value.contains(char::is_control),
                "invalid identity claim"
            );
            Ok(if value.is_empty() {
                fallback.into()
            } else {
                value.into()
            })
        };
        let user = claim("user_id", "")?;
        let tenant = claim("tenant_id", default)?;
        if user.is_empty() && !self.dev_email.is_empty() {
            return Ok(DevBootstrap::Email {
                email: &self.dev_email,
                tenant,
            });
        }
        let claims = Claims {
            user: if user.is_empty() {
                DEV_USER.into()
            } else {
                user
            },
            tenant,
            exp: OffsetDateTime::now_utc().unix_timestamp() + 86400,
            ..Claims::default()
        };
        Ok(DevBootstrap::Token(sign(
            &claims,
            self.dev_secret.as_bytes(),
        )?))
    }

    pub(crate) fn issue(&self, mut claims: Claims) -> Result<String> {
        ensure!(
            !self.secret.is_empty(),
            "authentication signing is disabled"
        );
        claims.exp = OffsetDateTime::now_utc().unix_timestamp() + 900;
        sign(&claims, &self.secret)
    }

    pub(crate) fn credentials(&self, headers: &HeaderMap, private: bool) -> Result<Claims> {
        let token = headers
            .get("authorization")
            .and_then(|value| value.to_str().ok())
            .and_then(|value| {
                value
                    .strip_prefix("Bearer ")
                    .or_else(|| value.strip_prefix("Token "))
            })
            .map(str::trim)
            .or_else(|| cookie(headers, "app_access").map(str::trim))
            .context("authentication required")?;
        // Explicit development identities stay private; the default one also
        // signs in a developing app's browser.
        if let Some(tenant) = &self.dev_tenant
            && let Ok(claims) = verify(token, self.dev_secret.as_bytes())
            && (private || claims.user == DEV_USER && &claims.tenant == tenant)
        {
            return Ok(claims);
        }
        verify(token, &self.secret)
    }
}

impl Claims {
    pub(crate) fn data(&self) -> Value {
        json!({
            "UserID": self.user, "TenantID": self.tenant, "SessionID": self.session,
            "ActorUserID": self.actor, "ImpersonationID": self.impersonation_id,
        })
    }

    pub(crate) async fn check_session(&self, client: &tokio_postgres::Client) -> Result<bool> {
        let exists: bool = client
            .query_one(
                "SELECT EXISTS (
                SELECT 1 FROM scenery.scenery_auth_refresh_sessions AS s
                JOIN scenery.scenery_auth_users AS u ON s.user_id = u.id
                LEFT JOIN scenery.scenery_auth_users AS a ON s.actor_user_id = a.id
                WHERE s.id = CAST(NULLIF($1::text, '') AS uuid)
                  AND s.user_id::text = $2 AND s.active_tenant_id::text = $3
                  AND COALESCE(s.actor_user_id::text, '') = $4
                  AND COALESCE(s.impersonation_id::text, '') = $5
                  AND s.revoked_at IS NULL AND s.expires_at > now() AND u.disabled_at IS NULL
                  AND (s.actor_user_id IS NULL OR (a.id IS NOT NULL AND a.disabled_at IS NULL))
            )",
                &[
                    &self.session,
                    &self.user,
                    &self.tenant,
                    &self.actor,
                    &self.impersonation_id,
                ],
            )
            .await?
            .get(0);
        Ok(exists)
    }
}

fn sign(claims: &Claims, key: &[u8]) -> Result<String> {
    let header = base64url(br#"{"alg":"HS256","typ":"JWT"}"#);
    let mut payload = serde_json::to_value(claims)?;
    payload["iat"] = json!(OffsetDateTime::now_utc().unix_timestamp());
    let payload = base64url(&serde_json::to_vec(&payload)?);
    let content = format!("{header}.{payload}");
    let mut mac = Signature::new_from_slice(key)?;
    mac.update(content.as_bytes());
    Ok(format!(
        "{content}.{}",
        base64url(&mac.finalize().into_bytes())
    ))
}

fn verify(token: &str, key: &[u8]) -> Result<Claims> {
    ensure!(
        !key.is_empty() && token.len() <= 16 << 10,
        "invalid access token"
    );
    let (message, signature) = token.rsplit_once('.').context("invalid access token")?;
    let mut mac = Signature::new_from_slice(key)?;
    mac.update(message.as_bytes());
    mac.verify_slice(&URL_SAFE_NO_PAD.decode(signature)?)?;
    let (header, payload) = message.split_once('.').context("invalid access token")?;
    let header = crate::values::parse(&URL_SAFE_NO_PAD.decode(header)?)?;
    ensure!(
        header["alg"] == "HS256",
        "unsupported access token algorithm"
    );
    let payload = crate::values::parse(&URL_SAFE_NO_PAD.decode(payload)?)?;
    let mut claims: Claims = serde_json::from_value(payload)?;
    claims.user = claims.user.trim().into();
    claims.tenant = claims.tenant.trim().into();
    claims.session = claims.session.trim().into();
    claims.actor = claims.actor.trim().into();
    claims.impersonation_id = claims.impersonation_id.trim().into();
    let now = OffsetDateTime::now_utc().unix_timestamp();
    ensure!(
        claims.exp > now
            && claims.nbf.is_none_or(|nbf| nbf <= now)
            && !claims.user.is_empty()
            && !claims.tenant.is_empty(),
        "invalid access token"
    );
    Ok(claims)
}

pub(crate) fn cookie<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    headers
        .get_all("cookie")
        .iter()
        .filter_map(|value| value.to_str().ok())
        .flat_map(|value| value.split(';'))
        .filter_map(|pair| pair.trim().split_once('='))
        .find(|(key, _)| *key == name)
        .map(|(_, value)| {
            value
                .strip_prefix('"')
                .and_then(|value| value.strip_suffix('"'))
                .unwrap_or(value)
        })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn explicit_development_identities_are_only_consumed_privately() {
        let auth = Authentication {
            secret: b"prod key".to_vec(),
            dev_secret: "dev key".into(),
            dev_tenant: Some("example".into()),
            dev_email: "developer@example.test".into(),
        };
        let DevBootstrap::Token(token) = auth.bootstrap(&json!({"user_id": "owner"})).unwrap()
        else {
            panic!("explicit user should get a synthetic token");
        };
        let mut headers = HeaderMap::new();
        headers.insert("authorization", format!("Bearer {token}").parse().unwrap());
        let claims = auth.credentials(&headers, true).unwrap();
        assert_eq!(
            (claims.user.as_str(), claims.tenant.as_str()),
            ("owner", "example")
        );
        assert!(auth.credentials(&headers, false).is_err());

        // Without a configured email the default identity is synthetic.
        let auth = Authentication {
            dev_email: String::new(),
            ..auth
        };
        let DevBootstrap::Token(token) = auth.bootstrap(&json!({})).unwrap() else {
            panic!("the default identity should get a synthetic token");
        };
        let mut headers = HeaderMap::new();
        headers.insert("authorization", format!("Bearer {token}").parse().unwrap());
        let claims = auth.credentials(&headers, false).unwrap();
        assert_eq!(
            (claims.user.as_str(), claims.tenant.as_str()),
            (DEV_USER, "example")
        );
    }

    #[test]
    fn production_rejects_whitespace_jwt_secret_and_trims_valid_secret() {
        let plan = Auth {
            bootstrap_sql: String::new(),
            enabled: true,
            google_o_auth: crate::plan::GoogleOAuth { enabled: false },
            dev_bootstrap: crate::plan::Bootstrap {
                enabled: false,
                default_user_email: String::new(),
                default_tenant_id: String::new(),
            },
            connections: vec![],
        };
        let whitespace = json!({"secrets": {"auth.jwt_secret": STANDARD.encode("  \t\n  ")}});
        assert!(Authentication::new(Some(&plan), &whitespace, false).is_err());
        assert!(Authentication::new(Some(&plan), &whitespace, true).is_ok());

        let trailing = json!({"secrets": {"auth.jwt_secret": STANDARD.encode("prod-secret\n")}});
        let trimmed = json!({"secrets": {"auth.jwt_secret": STANDARD.encode("prod-secret")}});
        let issuer = Authentication::new(Some(&plan), &trailing, false).unwrap();
        let verifier = Authentication::new(Some(&plan), &trimmed, false).unwrap();
        let token = issuer
            .issue(Claims {
                user: "user-1".into(),
                tenant: "tenant-1".into(),
                ..Claims::default()
            })
            .unwrap();
        let mut headers = HeaderMap::new();
        headers.insert("authorization", format!("Bearer {token}").parse().unwrap());
        let claims = verifier.credentials(&headers, false).unwrap();
        assert_eq!(claims.user, "user-1");
    }
}
