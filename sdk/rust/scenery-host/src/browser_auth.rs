use std::time::{Duration, SystemTime};

use anyhow::Context;
use base64::Engine as _;
use base64::engine::general_purpose::STANDARD;
use hyper::body::Incoming;
use hyper::{HeaderMap, Method, Request, StatusCode};
use serde_json::Value;
use sha2::{Digest, Sha256};
use tokio_postgres::GenericClient;

use crate::auth::{Claims, DevBootstrap, cookie};
use crate::connections::browser as connection_browser;
use crate::database::Session;
use crate::host::{Fault, Host, Reply, header, json_reply};
use crate::{auth_google, auth_organizations, auth_sessions, transport, values};

pub(crate) type AuthResult<T> = Result<T, Fault>;

#[derive(Clone, Copy)]
pub(crate) enum Route<'a> {
    Signup,
    VerifyEmail,
    Login,
    Refresh,
    Logout,
    Me,
    Switch,
    Members(&'a str),
    Invite(&'a str),
    Accept,
    Role(&'a str, &'a str),
    Disable(&'a str),
    Impersonate,
    StopImpersonation,
    GoogleStart,
    GoogleCallback,
    Connection(connection_browser::Route<'a>),
}

impl Route<'_> {
    fn authenticated(self) -> bool {
        matches!(
            self,
            Self::Me
                | Self::Switch
                | Self::Members(_)
                | Self::Invite(_)
                | Self::Accept
                | Self::Role(_, _)
                | Self::Disable(_)
                | Self::Impersonate
                | Self::StopImpersonation
                | Self::Connection(
                    connection_browser::Route::Start(_)
                        | connection_browser::Route::Status(_)
                        | connection_browser::Route::Disconnect(_)
                )
        )
    }
}

pub(crate) fn route<'a>(host: &Host, method: &Method, path: &'a str) -> Option<Route<'a>> {
    let config = host.bundle.auth.as_ref().filter(|plan| plan.enabled)?;
    let path: Vec<_> = path.strip_prefix('/')?.split('/').collect();
    match (method, path.as_slice()) {
        (&Method::POST, ["auth", "signup", "email"]) => Some(Route::Signup),
        (&Method::POST, ["auth", "email-verification", "confirm"]) => Some(Route::VerifyEmail),
        (&Method::POST, ["auth", "login", "email"]) => Some(Route::Login),
        (&Method::POST, ["auth", "refresh"]) => Some(Route::Refresh),
        (&Method::POST, ["auth", "logout"]) => Some(Route::Logout),
        (&Method::GET, ["auth", "me"]) => Some(Route::Me),
        (&Method::POST, ["auth", "organizations", "switch"]) => Some(Route::Switch),
        (&Method::GET, ["auth", "organizations", tenant, "members"]) => {
            Some(Route::Members(tenant))
        }
        (&Method::POST, ["auth", "organizations", tenant, "invites"]) => {
            Some(Route::Invite(tenant))
        }
        (&Method::POST, ["auth", "invites", "accept"]) => Some(Route::Accept),
        (&Method::PATCH, ["auth", "organizations", tenant, "members", user]) => {
            Some(Route::Role(tenant, user))
        }
        (&Method::POST, ["auth", "organizations", tenant, "members", "disable"]) => {
            Some(Route::Disable(tenant))
        }
        (&Method::POST, ["auth", "impersonation", "start"]) => Some(Route::Impersonate),
        (&Method::POST, ["auth", "impersonation", "stop"]) => Some(Route::StopImpersonation),
        (&Method::GET, ["auth", "google", "start"]) if config.google_o_auth.enabled => {
            Some(Route::GoogleStart)
        }
        (&Method::GET, ["auth", "google", "callback"]) if config.google_o_auth.enabled => {
            Some(Route::GoogleCallback)
        }
        _ => connection_browser::Route::select(method, &path, &config.connections)
            .map(Route::Connection),
    }
}

pub(crate) async fn serve(
    host: &Host,
    route: Route<'_>,
    request: Request<Incoming>,
    mount: &str,
) -> Reply {
    let result = dispatch(host, route, request, mount).await;
    let mut result = result.unwrap_or_else(|fault| fault.reply(host, None));
    header(result.headers_mut(), "cache-control", "no-store");
    result
}

async fn dispatch(
    host: &Host,
    route: Route<'_>,
    request: Request<Incoming>,
    mount: &str,
) -> AuthResult<Reply> {
    let (parts, body) = request.into_parts();
    let has_body = !matches!(
        route,
        Route::GoogleStart
            | Route::GoogleCallback
            | Route::Refresh
            | Route::Logout
            | Route::Me
            | Route::Members(_)
            | Route::StopImpersonation
            | Route::Connection(
                connection_browser::Route::Callback(_)
                    | connection_browser::Route::Status(_)
                    | connection_browser::Route::Disconnect(_)
            )
    );
    if !has_body {
        crate::http_timeout::request_body_finished(&parts.extensions);
    }
    let browser = Browser::new(host, &parts.headers, &parts.uri, mount);
    if matches!(route, Route::GoogleStart | Route::GoogleCallback) {
        return auth_google::serve(&browser, route, parts.uri.query()).await;
    }
    let user = if route.authenticated() {
        browser.principal().await?
    } else {
        Claims::default()
    };
    let input = if !has_body {
        Value::Null
    } else {
        // A cross-site form can post JSON-shaped text without a preflight,
        // but not with this media type: no session is created for it.
        crate::http_codec::require_json(&parts.headers)?;
        let raw = transport::read(body, 64 << 10).await;
        crate::http_timeout::request_body_finished(&parts.extensions);
        values::parse(&raw.map_err(|_| invalid("invalid request body"))?)
            .ok()
            .filter(Value::is_object)
            .ok_or_else(|| invalid("invalid request body"))?
    };
    match route {
        Route::Connection(route) => {
            return connection_browser::serve(&browser, route, &user, input, parts.uri.query())
                .await;
        }
        // Password hashing waits for a slot without holding a session.
        Route::Signup => return auth_sessions::signup(&browser, &input).await,
        Route::Login => return auth_sessions::login(&browser, &input).await,
        _ => {}
    }
    let mut db = browser.connect().await?;
    match route {
        Route::VerifyEmail => auth_sessions::verify_email(&browser, &mut db, &input).await,
        Route::Refresh => auth_sessions::refresh(&browser, &mut db).await,
        Route::Logout => auth_sessions::logout(&browser, &db).await,
        Route::Me => auth_sessions::me(&browser, &db, &user).await,
        _ => auth_organizations::serve(&browser, &mut db, &user, route, &input).await,
    }
}

pub(crate) async fn dev_bootstrap(host: &Host, request: Request<Incoming>) -> Reply {
    let result = async {
        let (parts, body) = request.into_parts();
        let input = transport::read(body, 8 << 10).await;
        crate::http_timeout::request_body_finished(&parts.extensions);
        let input = input.map_err(|_| invalid("invalid request body"))?;
        let input = values::parse(&input).map_err(|_| invalid("invalid request body"))?;
        match host
            .auth
            .bootstrap(&input)
            .map_err(|_| denied("development identity is unavailable"))?
        {
            DevBootstrap::Token(token) => Ok(ok(&serde_json::json!({"token": token}))),
            DevBootstrap::Email { email, tenant } => {
                let browser = Browser::new(host, &parts.headers, &parts.uri, "");
                let mut db = browser.connect().await?;
                auth_sessions::dev_email_session(&browser, &mut db, email, &tenant).await
            }
        }
    }
    .await;
    result.unwrap_or_else(|fault| fault.reply(host, None))
}

pub(crate) struct Browser<'a> {
    pub host: &'a Host,
    pub(crate) headers: &'a HeaderMap,
    pub(crate) authority: String,
    pub(crate) mount: &'a str,
}

impl<'a> Browser<'a> {
    fn new(host: &'a Host, headers: &'a HeaderMap, uri: &hyper::Uri, mount: &'a str) -> Self {
        let authority = uri.authority().map_or_else(
            || header_value(headers, "host"),
            |authority| authority.as_str(),
        );
        Self {
            host,
            headers,
            authority: authority.into(),
            mount,
        }
    }

    /// Checks a session out of the shared pool. A saturated pool answers
    /// 503 instead of opening one more database backend per request.
    pub(crate) async fn connect(&self) -> AuthResult<Session> {
        self.host
            .ensure_auth_schema()
            .await
            .map_err(Fault::internal)?;
        self.host
            .sessions
            .acquire()
            .await
            .map_err(|_| Fault::new(503, "unavailable", "service is unavailable"))
    }

    async fn principal(&self) -> AuthResult<Claims> {
        let claims = self
            .host
            .auth
            .credentials(self.headers, false)
            .map_err(|_| unauthorized("invalid or missing authentication"))?;
        if [&claims.user, &claims.tenant, &claims.session]
            .iter()
            .any(|id| !crate::connections::valid_uuid(id))
        {
            return Err(unauthorized("invalid or missing authentication"));
        }
        self.host
            .ensure_auth_schema()
            .await
            .map_err(Fault::internal)?;
        let conn = self
            .host
            .engine
            .connection()
            .await
            .map_err(Fault::internal)?;
        if !claims.check_session(&conn).await.map_err(Fault::internal)? {
            return Err(unauthorized("invalid or missing authentication"));
        }
        Ok(claims)
    }

    pub(crate) fn tls(&self) -> bool {
        let configured = config_url("SCENERY_API_BASE_URL").ok().flatten();
        tls_with_api(self.host.development, self.headers, configured.as_ref())
    }

    pub(crate) fn request_hostname_matches(&self, url: &url::Url) -> bool {
        let actual = url::Url::parse(&format!("http://{}", self.authority)).ok();
        actual
            .as_ref()
            .and_then(url::Url::host_str)
            .zip(url.host_str())
            .is_some_and(|(actual, expected)| actual.eq_ignore_ascii_case(expected))
    }

    pub(crate) fn cookie(&self, name: &str) -> &str {
        cookie(self.headers, name).unwrap_or_default()
    }

    pub(crate) fn external_origin(&self) -> String {
        let configured = config_url("SCENERY_API_BASE_URL").ok().flatten();
        external_origin_with_api(
            self.host.development,
            self.headers,
            &self.authority,
            configured.as_ref(),
        )
    }

    pub(crate) fn prefix(&self) -> String {
        let configured = config_url("SCENERY_API_BASE_URL").ok().flatten();
        prefix_with_api(self.mount, self.headers, configured.as_ref())
    }

    fn cookie_name(&self) -> String {
        if self.host.development {
            let configured = std::env::var("SCENERY_API_BASE_URL")
                .ok()
                .and_then(|value| url::Url::parse(&value).ok());
            let authority = configured
                .as_ref()
                .map_or(self.authority.as_str(), |url| url.authority());
            if !authority.is_empty() && !authority.to_ascii_lowercase().contains("scenery") {
                let suffix: String = authority
                    .chars()
                    .map(|ch| {
                        if ch.is_ascii_alphanumeric() {
                            ch.to_ascii_lowercase()
                        } else {
                            '_'
                        }
                    })
                    .collect();
                return format!("app_refresh_{suffix}");
            }
        }
        "app_refresh".into()
    }

    pub(crate) fn refresh_token(&self) -> &str {
        cookie(self.headers, &self.cookie_name())
            .unwrap_or_default()
            .trim()
    }

    pub(crate) fn set_cookie(&self, reply: &mut Reply, raw: &str, seconds: u64) {
        let expires = if seconds == 0 {
            SystemTime::UNIX_EPOCH
        } else {
            SystemTime::now() + Duration::from_secs(seconds)
        };
        let mut value = format!(
            "{}={raw}; Path={}/auth; Expires={}; Max-Age={seconds}; HttpOnly; SameSite=Lax",
            self.cookie_name(),
            self.prefix(),
            httpdate::fmt_http_date(expires)
        );
        if self.tls() {
            value.push_str("; Secure");
        }
        let domain = self.string("auth.cookie_domain");
        if !domain.is_empty()
            && !domain.to_ascii_lowercase().contains("scenery")
            && domain
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'.'))
        {
            value.push_str("; Domain=");
            value.push_str(domain);
        }
        header(reply.headers_mut(), "set-cookie", &value);
    }

    pub(crate) fn string(&self, key: &str) -> &str {
        text(&self.host.configuration["values"], key)
    }

    pub(crate) fn secret(&self, key: &str) -> String {
        STANDARD
            .decode(text(&self.host.configuration["secrets"], key))
            .ok()
            .and_then(|bytes| String::from_utf8(bytes).ok())
            .map(|value| value.trim().to_owned())
            .unwrap_or_default()
    }

    pub(crate) fn user_agent(&self) -> &str {
        header_value(self.headers, "user-agent")
    }

    pub(crate) fn ip_hash(&self) -> String {
        let ip = header_value(self.headers, "x-forwarded-for");
        let ip = if ip.is_empty() {
            header_value(self.headers, "x-real-ip")
        } else {
            ip
        };
        if ip.is_empty() {
            String::new()
        } else {
            token_hash(ip)
        }
    }
}

pub(crate) fn header_value<'a>(headers: &'a HeaderMap, name: &str) -> &'a str {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .split(',')
        .next()
        .unwrap_or_default()
        .trim()
}

pub(crate) fn token_hash(value: &str) -> String {
    values::base64url(&Sha256::digest(value.trim().as_bytes()))
}

pub(crate) fn random<const N: usize>() -> AuthResult<String> {
    let mut bytes = [0u8; N];
    getrandom::fill(&mut bytes).map_err(Fault::internal)?;
    Ok(values::base64url(&bytes))
}

pub(crate) fn safe_redirect(value: &str) -> &str {
    if value.starts_with('/')
        && !value.starts_with("//")
        && !value.contains(['\\', '\r', '\n', '\0'])
        && !value.to_ascii_lowercase().contains("scenery")
    {
        value
    } else {
        "/"
    }
}

pub(crate) fn text<'a>(input: &'a Value, key: &str) -> &'a str {
    input[key].as_str().unwrap_or_default().trim()
}

pub(crate) fn email(value: &str) -> AuthResult<String> {
    let address = value
        .rsplit_once('<')
        .and_then(|(_, tail)| tail.strip_suffix('>'))
        .unwrap_or(value)
        .trim();
    let (name, domain) = address
        .split_once('@')
        .ok_or_else(|| invalid("email is invalid"))?;
    if name.is_empty()
        || domain.is_empty()
        || domain.contains('@')
        || address.len() > 254
        || address
            .contains(|ch: char| ch.is_whitespace() || ch.is_control() || matches!(ch, '<' | '>'))
    {
        return Err(invalid("email is invalid"));
    }
    Ok(address.to_lowercase())
}

pub(crate) fn uuid<'a>(value: &'a str, message: &'static str) -> AuthResult<&'a str> {
    crate::connections::valid_uuid(value)
        .then_some(value)
        .ok_or_else(|| invalid(message))
}

pub(crate) fn invalid(message: &'static str) -> Fault {
    Fault::new(400, "invalid_argument", message)
}
pub(crate) fn unauthorized(message: &'static str) -> Fault {
    Fault::new(401, "unauthenticated", message)
}
pub(crate) fn denied(message: &'static str) -> Fault {
    Fault::new(403, "permission_denied", message)
}
pub(crate) fn precondition(message: &'static str) -> Fault {
    Fault::new(400, "failed_precondition", message)
}
pub(crate) fn ok(value: &Value) -> Reply {
    json_reply(StatusCode::OK, value)
}

pub(crate) async fn audit(
    db: &impl GenericClient,
    browser: &Browser<'_>,
    event: &str,
    claims: &Claims,
    metadata: &Value,
) -> AuthResult<()> {
    db.execute("INSERT INTO scenery.scenery_auth_auth_events (id,event_type,user_id,actor_user_id,tenant_id,session_id,ip_hash,user_agent,metadata)
        VALUES (gen_random_uuid(),$1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,$7,$8)",
        &[&event,&claims.user,&claims.actor,&claims.tenant,&claims.session,&browser.ip_hash(),&browser.user_agent(),&metadata.to_string().as_bytes()])
        .await.map_err(Fault::internal)?;
    Ok(())
}

pub(crate) async fn issue_once(
    db: &impl GenericClient,
    purpose: &str,
    claims: &Claims,
    raw_email: &str,
    normalized: &str,
    metadata: Value,
    seconds: i32,
) -> AuthResult<String> {
    let token = random::<32>()?;
    db.execute("INSERT INTO scenery.scenery_auth_one_time_tokens (id,purpose,token_hash,user_id,tenant_id,email,normalized_email,metadata,expires_at)
        VALUES (gen_random_uuid(),$1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$7,now()+make_interval(secs => $8::int))",
        &[&purpose,&token_hash(&token),&claims.user,&claims.tenant,&raw_email,&normalized,&metadata.to_string().as_bytes(),&seconds])
        .await.map_err(Fault::internal)?;
    Ok(token)
}

pub(crate) async fn consume_once(
    db: &impl GenericClient,
    purpose: &str,
    token: &str,
) -> AuthResult<tokio_postgres::Row> {
    db.query_opt("UPDATE scenery.scenery_auth_one_time_tokens SET consumed_at=now()
        WHERE purpose=$1 AND token_hash=$2 AND consumed_at IS NULL AND expires_at>now()
        RETURNING COALESCE(user_id::text,''), COALESCE(tenant_id::text,''), normalized_email, metadata",
        &[&purpose,&token_hash(token)]).await.map_err(Fault::internal)?
        .ok_or_else(|| invalid("token is invalid or expired"))
}

pub(crate) fn tls_with_api(
    development: bool,
    headers: &HeaderMap,
    configured: Option<&url::Url>,
) -> bool {
    !development
        || header_value(headers, "x-forwarded-proto").eq_ignore_ascii_case("https")
        || header_value(headers, "origin")
            .to_ascii_lowercase()
            .starts_with("https://")
        || configured.is_some_and(|url| url.scheme() == "https")
}

pub(crate) fn has_forwarded_origin(headers: &HeaderMap) -> bool {
    !header_value(headers, "x-forwarded-proto").is_empty()
        || !header_value(headers, "x-forwarded-host").is_empty()
        || !header_value(headers, "x-forwarded-prefix").is_empty()
}

pub(crate) fn external_origin_with_api(
    development: bool,
    headers: &HeaderMap,
    authority: &str,
    configured: Option<&url::Url>,
) -> String {
    let forwarded = header_value(headers, "x-forwarded-host");
    let authority = if forwarded.is_empty() {
        authority.trim()
    } else {
        forwarded
    };
    let proto = header_value(headers, "x-forwarded-proto");
    let scheme = if proto.eq_ignore_ascii_case("http") {
        "http"
    } else if proto.eq_ignore_ascii_case("https") || tls_with_api(development, headers, configured)
    {
        "https"
    } else {
        "http"
    };
    url::Url::parse(&format!("{scheme}://{authority}"))
        .ok()
        .filter(|url| url.path() == "/" && url.username().is_empty() && url.password().is_none())
        .map_or_else(String::new, |url| url.origin().ascii_serialization())
}

fn normalize_prefix(raw: &str) -> Option<String> {
    let prefix = raw.trim_matches('/');
    if prefix.is_empty()
        || prefix.contains([';', '\\', '?', '#'])
        || prefix.to_ascii_lowercase().contains("scenery")
    {
        return None;
    }
    Some(format!("/{prefix}"))
}

pub(crate) fn prefix_with_api(
    mount: &str,
    headers: &HeaderMap,
    configured: Option<&url::Url>,
) -> String {
    let forwarded = header_value(headers, "x-forwarded-prefix");
    if !forwarded.is_empty() {
        return normalize_prefix(forwarded).unwrap_or_default();
    }
    if !mount.is_empty() {
        return normalize_prefix(mount).unwrap_or_default();
    }
    configured
        .and_then(|url| normalize_prefix(url.path()))
        .unwrap_or_default()
}

pub(crate) fn config_url(name: &str) -> anyhow::Result<Option<url::Url>> {
    std::env::var(name)
        .ok()
        .filter(|value| !value.is_empty())
        .map(|value| url::Url::parse(&value).context("invalid browser base URL"))
        .transpose()
}
