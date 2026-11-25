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
    exp: i64,
    nbf: Option<i64>,
}

pub(crate) struct Authentication {
    secret: Vec<u8>,
    dev_secret: String,
    dev_tenant: Option<String>,
}

impl Authentication {
    pub(crate) fn new(plan: Option<&Auth>, config: &Value, development: bool) -> Result<Self> {
        let mut secret = Vec::new();
        let mut dev_tenant = None;
        if let Some(plan) = plan.filter(|plan| plan.enabled) {
            if let Some(encoded) = config["secrets"]["auth.jwt_secret"].as_str() {
                secret = STANDARD.decode(encoded)?;
            }
            ensure!(
                development || !secret.is_empty(),
                "authentication requires auth.jwt_secret in the production configuration snapshot"
            );
            if development && plan.dev_bootstrap.enabled {
                let value = plan.dev_bootstrap.default_tenant_id.trim();
                dev_tenant = Some(if value.is_empty() {
                    "00000000-0000-0000-0000-000000000001".into()
                } else {
                    value.into()
                });
            }
        }
        Ok(Self {
            secret,
            dev_secret: crate::process::random_hex::<32>()?,
            dev_tenant,
        })
    }

    pub(crate) fn bootstrap(&self, input: &Value) -> Result<String> {
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
        let claims = Claims {
            user: claim("user_id", "dev-user")?,
            tenant: claim("tenant_id", default)?,
            exp: OffsetDateTime::now_utc().unix_timestamp() + 86400,
            ..Claims::default()
        };
        let header = base64url(br#"{"alg":"HS256","typ":"JWT"}"#);
        let payload = base64url(&serde_json::to_vec(&claims)?);
        let content = format!("{header}.{payload}");
        let mut mac = Signature::new_from_slice(self.dev_secret.as_bytes())?;
        mac.update(content.as_bytes());
        Ok(format!(
            "{content}.{}",
            base64url(&mac.finalize().into_bytes())
        ))
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
        if private
            && self.dev_tenant.is_some()
            && let Ok(claims) = verify(token, self.dev_secret.as_bytes())
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
    fn development_token_is_only_consumed_privately() {
        let auth = Authentication {
            secret: b"prod key".to_vec(),
            dev_secret: "dev key".into(),
            dev_tenant: Some("example".into()),
        };
        let token = auth.bootstrap(&json!({"user_id": "owner"})).unwrap();
        let mut headers = HeaderMap::new();
        headers.insert("authorization", format!("Bearer {token}").parse().unwrap());
        let claims = auth.credentials(&headers, true).unwrap();
        assert_eq!(
            (claims.user.as_str(), claims.tenant.as_str()),
            ("owner", "example")
        );
        assert!(auth.credentials(&headers, false).is_err());
    }
}
