use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use hyper::body::Incoming;
use hyper::header::HeaderValue;
use hyper::{HeaderMap, Method, Request, StatusCode};
use percent_encoding::percent_decode_str;
use serde_json::{Map, Value, json};
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;

use crate::abi::Outcome;
use crate::host::{Call, Fault, Host, Reply, body, empty, header, json_reply};
use crate::http_codec::{decode_piece, decode_request, encode_response};
use crate::plan::{Binding, Policy, Route, Service};
use crate::supervisor::Generation;
use crate::transport::read;
use crate::values;

struct Match<'a> {
    service: &'a Service,
    binding: &'a Binding,
    route: &'a Route,
    path: BTreeMap<&'a str, &'a str>,
    rank: Vec<u8>,
}

fn route<'a>(host: &'a Host, path: &'a str, method: &str) -> Option<Match<'a>> {
    host.bundle
        .host
        .workers
        .iter()
        .flat_map(|service| {
            service.bindings.iter().filter_map(move |binding| {
                let plan = binding.http.as_ref()?;
                if plan.method != method || plan.access == "private" {
                    return None;
                }
                let (path, rank) = match_path(plan, path)?;
                Some(Match {
                    service,
                    binding,
                    route: plan,
                    path,
                    rank,
                })
            })
        })
        .max_by(|a, b| a.rank.cmp(&b.rank))
}

fn match_path<'a>(plan: &'a Route, path: &'a str) -> Option<(BTreeMap<&'a str, &'a str>, Vec<u8>)> {
    let mut fields = BTreeMap::new();
    let mut rank = Vec::new();
    let mut remainder = path.strip_prefix('/')?;
    for part in plan.path.strip_prefix('/')?.split('/') {
        if let Some(tail) = part.strip_prefix('*') {
            let descriptor = plan.path_tail.as_ref()?;
            let count = if remainder.is_empty() {
                0
            } else {
                remainder.split('/').count()
            };
            if count < descriptor.minimum_segments {
                return None;
            }
            // The request codec decides what an empty tail constructs.
            fields.insert(tail, remainder);
            rank.push(1);
            return Some((fields, rank));
        }
        let (segment, next) = remainder.split_once('/').unwrap_or((remainder, ""));
        if let Some(key) = part.strip_prefix(':') {
            if segment.is_empty() {
                return None;
            }
            fields.insert(key, segment);
            rank.push(3);
        } else {
            if segment != part && decode_piece(segment).ok().as_deref() != Some(part) {
                return None;
            }
            rank.push(4);
        }
        remainder = next;
    }
    let depth = |value: &str| value.bytes().filter(|byte| *byte == b'/').count();
    if !remainder.is_empty() || depth(path) != depth(&plan.path) {
        return None;
    }
    rank.push(2);
    Some((fields, rank))
}

pub(crate) async fn public(host: &Arc<Host>, request: Request<Incoming>) -> Reply {
    let Some(generation) = host.generation() else {
        return empty(StatusCode::SERVICE_UNAVAILABLE);
    };
    ingress(host, generation, request, false).await
}

pub(crate) async fn ingress(
    host: &Arc<Host>,
    generation: Arc<Generation>,
    request: Request<Incoming>,
    private: bool,
) -> Reply {
    let path = request.uri().path().to_owned();
    if path.to_ascii_lowercase().contains("scenery")
        || path.starts_with("/host/v1/")
        || path.starts_with("/worker/v1/")
    {
        return empty(StatusCode::NOT_FOUND);
    }
    let method = request.method().clone();
    let mounted_path = if !private && host.bundle.frontend.is_some() {
        path.strip_prefix("/api")
            .filter(|tail| tail.starts_with('/'))
    } else {
        None
    };
    // Development sign-in is public only while the host develops an app; an
    // identity with explicit claims stays on the private control path.
    if (private || host.development) && method == Method::POST && path == "/users/dev-bootstrap" {
        let mut response = crate::browser_auth::dev_bootstrap(host, request, private).await;
        header(response.headers_mut(), "cache-control", "no-store");
        return attest(response, &generation, None);
    }
    if (path == "/_app/public-config" || mounted_path == Some("/_app/public-config"))
        && matches!(method, Method::GET | Method::HEAD)
    {
        let mut public = Map::new();
        if let Some(keys) = host.configuration["public"].as_array() {
            for key in keys.iter().filter_map(Value::as_str) {
                if let Some(value) = host.configuration["values"]
                    .get(key)
                    .filter(|value| !value.is_null())
                {
                    public.insert(key.into(), value.clone());
                }
            }
        }
        let mut reply = json_reply(
            StatusCode::OK,
            &json!({
                "kind": "app.public-config", "revision": host.configuration["revision"].as_str().unwrap_or_default(), "values": public,
            }),
        );
        header(reply.headers_mut(), "cache-control", "no-store");
        if method == Method::HEAD {
            *reply.body_mut() = body(Bytes::new());
        }
        return attest(reply, &generation, None);
    }
    if path == "/users/dev-bootstrap" {
        return attest(empty(StatusCode::NOT_FOUND), &generation, None);
    }
    let cors_origin = request.headers().get("origin").cloned();
    // A preflight asks about the method it announces, not about OPTIONS.
    let requested = request
        .headers()
        .get("access-control-request-method")
        .filter(|_| method == Method::OPTIONS)
        .and_then(|value| Method::from_bytes(value.as_bytes()).ok());
    let preflight = requested.is_some();
    let asked = requested.as_ref().unwrap_or(&method);
    let auth_route = crate::browser_auth::route(host, asked, &path)
        .map(|route| (route, ""))
        .or_else(|| {
            crate::browser_auth::route(host, asked, mounted_path?).map(|route| (route, "/api"))
        });
    if let Some((route, mount)) = auth_route {
        let mut response = if preflight {
            cors_preflight(
                &host.origins,
                None,
                cors_origin.as_ref(),
                asked.as_str(),
                request.headers(),
            )
        } else {
            crate::browser_auth::serve(host, route, request, mount).await
        };
        add_cors(
            response.headers_mut(),
            &host.origins,
            None,
            cors_origin.as_ref(),
        );
        return attest(response, &generation, None);
    }
    let select_route =
        |method: &str| route(host, &path, method).or_else(|| route(host, mounted_path?, method));
    let match_method = if preflight {
        asked.as_str()
    } else if method == Method::HEAD && select_route("HEAD").is_none() {
        "GET"
    } else {
        method.as_str()
    };
    let assistant_route = |path| {
        host.bundle
            .host
            .assistants
            .iter()
            .find_map(|plan| plan.route(asked, path).map(|route| (plan, route)))
    };
    if let Some((plan, route, mount)) = assistant_route(&path)
        .map(|(plan, route)| (plan, route, ""))
        .or_else(|| assistant_route(mounted_path?).map(|(plan, route)| (plan, route, "/api")))
    {
        if preflight {
            let reply = cors_preflight(
                &host.origins,
                Some(&plan.policy),
                cors_origin.as_ref(),
                match_method,
                request.headers(),
            );
            return attest(reply, &generation, None);
        }
        if let Some(deadlines) = request
            .extensions()
            .get::<Arc<crate::http_timeout::DeadlineControl>>()
        {
            deadlines.apply_policy(&plan.policy);
        }
        let mut response =
            crate::assistant::Gateway::serve(host, plan, route, request, mount, generation.clone())
                .await;
        add_cors(
            response.headers_mut(),
            &host.origins,
            Some(&plan.policy),
            cors_origin.as_ref(),
        );
        return attest(response, &generation, None);
    }
    let Some(selected) = select_route(match_method) else {
        let response = crate::frontend::serve(host.bundle.frontend.as_ref(), request).await;
        return attest(response, &generation, None);
    };
    let policy = &selected.binding.policy;
    if let Some(deadlines) = request
        .extensions()
        .get::<Arc<crate::http_timeout::DeadlineControl>>()
    {
        deadlines.apply_policy(policy);
    }
    if preflight {
        return attest(
            cors_preflight(
                &host.origins,
                Some(policy),
                cors_origin.as_ref(),
                match_method,
                request.headers(),
            ),
            &generation,
            None,
        );
    }
    if selected.route.request.body.is_none() {
        crate::http_timeout::request_body_finished(request.extensions());
    }
    let mut call = match Call::new(
        generation.clone(),
        request.headers(),
        method.as_str(),
        &path,
        timeout(policy),
    ) {
        Ok(call) => call,
        Err(error) => return Fault::internal(error).reply(host, Some(policy)),
    };
    call.request = Some(Arc::new(worker_request(
        method.as_str(),
        &path,
        selected.route,
        &selected.path,
        request.headers(),
    )));
    let started = Instant::now();
    host.telemetry
        .started(&call, selected.service, &selected.binding.address);
    let result = bounded(
        call.cancel.clone(),
        call.deadline,
        &selected.binding.delivery,
        serve_route(host, request, &selected, &mut call, private),
    )
    .await;
    let (mut response, worker, error) = match result {
        Ok((response, worker, error)) => (response, worker, error),
        Err(fault) => {
            let code = fault.code.into();
            (fault.reply(host, Some(policy)), None, code)
        }
    };
    if method == Method::HEAD {
        *response.body_mut() = body(Bytes::new());
    }
    add_cors(
        response.headers_mut(),
        &host.origins,
        Some(policy),
        cors_origin.as_ref(),
    );
    header(response.headers_mut(), "x-trace-id", &call.trace_id);
    host.telemetry.span(
        &call,
        selected.service,
        &selected.binding.address,
        started.elapsed(),
        &error,
    );
    attest(response, &generation, worker)
}

/// Describes the admitted request to workers, as the invocation metadata
/// carries it: the decoded path, the raw path parameters in route order and
/// the request headers. The credentials the host consumed stay here.
fn worker_request(
    method: &str,
    path: &str,
    route: &Route,
    fields: &BTreeMap<&str, &str>,
    headers: &HeaderMap,
) -> Value {
    let path_params: Vec<Value> = route
        .path
        .split('/')
        .filter_map(|part| part.strip_prefix(':').or_else(|| part.strip_prefix('*')))
        .filter_map(|name| Some(json!({"name": name, "value": fields.get(name)?})))
        .collect();
    let mut forwarded = Map::new();
    for name in headers.keys() {
        if matches!(
            name.as_str(),
            "authorization" | "proxy-authorization" | "cookie" | "host"
        ) || name.as_str().starts_with("x-scenery-")
        {
            continue;
        }
        let values: Vec<_> = headers
            .get_all(name)
            .iter()
            .map(|value| String::from_utf8_lossy(value.as_bytes()).into_owned())
            .collect();
        forwarded.insert(name.as_str().into(), json!(values));
    }
    json!({
        "method": method,
        "path": percent_decode_str(path).decode_utf8_lossy(),
        "path_params": path_params,
        "headers": forwarded,
        "api": {
            "exposed": true,
            "auth_required": route.access == "auth",
        },
    })
}

/// Serves an admitted route until its invocation is cancelled or expires. A
/// binding that waits for a durable execution declares the expiry as
/// `dispatch.wait_timeout`. The wait observes this same deadline, so the
/// outcome must not depend on which of the two notices it first.
async fn bounded<T>(
    cancel: CancellationToken,
    deadline: Instant,
    delivery: &str,
    route: impl Future<Output = Result<T, Fault>>,
) -> Result<T, Fault> {
    tokio::select! {
        () = crate::abi::cancelled(cancel, deadline) => Err(if delivery == "wait" {
            Fault::wait_timeout()
        } else {
            Fault::deadline()
        }),
        result = route => result,
    }
}

async fn serve_route(
    host: &Host,
    request: Request<Incoming>,
    selected: &Match<'_>,
    call: &mut Call,
    private: bool,
) -> Result<(Reply, Option<(u32, String)>, String), Fault> {
    let policy = &selected.binding.policy;
    let headers = request.headers();
    if policy.max_request_header_bytes > 0
        && headers
            .iter()
            .map(|(name, value)| name.as_str().len() + value.as_bytes().len() + 4)
            .sum::<usize>()
            > policy.max_request_header_bytes
    {
        return Err(Fault::new(
            431,
            "transport.request_header_too_large",
            "request headers exceed the declared limit",
        ));
    }
    if selected.route.access == "auth" {
        host.authenticate(call, headers, private).await?;
    }
    let reply_headers = headers.clone();
    let input = decode_request(request, &selected.path, &selected.route.request, call).await?;
    let outcome = host
        .dispatch(call, selected.service, selected.binding, input)
        .await?;
    let key = if outcome.name == "dispatch.enqueued" {
        outcome.name.clone()
    } else {
        format!("{}.{}", outcome.kind, outcome.name)
    };
    let response = selected
        .route
        .responses
        .get(&key)
        .ok_or_else(|| Fault::internal("worker outcome has no HTTP response mapping"))?;
    let worker = outcome.worker.clone();
    let code = if outcome.kind == "error" {
        outcome.name.clone()
    } else {
        String::new()
    };
    Ok((
        encode_response(response, outcome, &reply_headers)?,
        worker,
        code,
    ))
}

pub(crate) async fn dev_binding(
    host: &Host,
    generation: Arc<Generation>,
    request: Request<Incoming>,
    address: &str,
) -> Reply {
    let Some((service, binding)) = host
        .binding(address)
        .filter(|(_, binding)| binding.http.is_none() && !binding.mcp)
    else {
        return empty(StatusCode::NOT_FOUND);
    };
    let policy = &binding.policy;
    let mut call = match Call::new(
        generation.clone(),
        request.headers(),
        "POST",
        request.uri().path(),
        timeout(policy),
    ) {
        Ok(call) => call,
        Err(err) => return Fault::internal(err).reply(host, Some(policy)),
    };
    let started = Instant::now();
    host.telemetry.started(&call, service, &binding.address);
    let result = call_binding(host, &mut call, service, binding, request).await;
    let (mut reply, worker, error) = match result {
        Ok(outcome) if outcome.stream.is_none() => {
            let value = if binding.delivery == "enqueue" {
                outcome.payload.clone()
            } else {
                outcome.json()
            };
            let code = if outcome.kind == "error" {
                outcome.name
            } else {
                String::new()
            };
            (json_reply(StatusCode::OK, &value), outcome.worker, code)
        }
        Ok(_) => (
            Fault::internal("internal binding streamed a response").reply(host, Some(policy)),
            None,
            "system.internal".into(),
        ),
        Err(fault) => {
            let code = fault.code.into();
            (fault.reply(host, Some(policy)), None, code)
        }
    };
    host.telemetry
        .span(&call, service, &binding.address, started.elapsed(), &error);
    header(reply.headers_mut(), "x-trace-id", &call.trace_id);
    attest(reply, &generation, worker)
}

async fn call_binding(
    host: &Host,
    call: &mut Call,
    service: &Service,
    binding: &Binding,
    request: Request<Incoming>,
) -> Result<Outcome, Fault> {
    if request.headers().contains_key("authorization") {
        host.authenticate(call, request.headers(), true).await?;
    }
    let input = read(request.into_body(), scenery_wire::MAX_METADATA)
        .await
        .map_err(|_| Fault::invalid())?;
    let input = values::parse(&input).map_err(|_| Fault::invalid())?;
    host.dispatch(call, service, binding, input).await
}

fn attest(mut response: Reply, generation: &Generation, worker: Option<(u32, String)>) -> Reply {
    let headers = response.headers_mut();
    for (key, value) in [
        ("x-app-generation", generation.number.to_string()),
        ("x-app-host-pid", std::process::id().to_string()),
        (
            "x-app-contract-revision",
            generation.identity.contract_revision.clone(),
        ),
        (
            "x-app-implementation-revision",
            generation.identity.implementation_revision.clone(),
        ),
        ("x-app-target", generation.identity.go_target.clone()),
        (
            "x-app-build-digest",
            generation.identity.build_input_digest.clone(),
        ),
    ] {
        header(headers, key, &value);
    }
    if let Some((pid, revision)) = worker {
        header(headers, "x-app-worker-pid", &pid.to_string());
        header(headers, "x-app-worker-revision", &revision);
    }
    for name in [
        "connection",
        "vary",
        "trailer",
        "access-control-allow-headers",
        "access-control-expose-headers",
    ] {
        scrub_header_list(headers, name);
    }
    let keys: Vec<_> = headers.keys().cloned().collect();
    for key in keys {
        if contains_scenery_bytes(key.as_str().as_bytes()) {
            headers.remove(&key);
            continue;
        }
        let has_scenery = headers
            .get_all(&key)
            .iter()
            .any(|value| contains_scenery_bytes(value.as_bytes()));
        if has_scenery {
            let kept: Vec<_> = headers
                .get_all(&key)
                .iter()
                .filter(|value| !contains_scenery_bytes(value.as_bytes()))
                .cloned()
                .collect();
            headers.remove(&key);
            for value in kept {
                headers.append(key.clone(), value);
            }
        }
    }
    response
}

fn contains_scenery_bytes(bytes: &[u8]) -> bool {
    bytes
        .windows(b"scenery".len())
        .any(|window| window.eq_ignore_ascii_case(b"scenery"))
}

fn scrub_header_list(headers: &mut HeaderMap, name: &'static str) {
    let values: Vec<_> = headers.get_all(name).iter().cloned().collect();
    if values.is_empty() {
        return;
    }
    headers.remove(name);
    for value in values {
        let Ok(text) = std::str::from_utf8(value.as_bytes()) else {
            if !contains_scenery_bytes(value.as_bytes()) {
                headers.append(name, value);
            }
            continue;
        };
        let kept: Vec<_> = text
            .split(',')
            .map(str::trim)
            .filter(|item| !item.is_empty() && !contains_scenery_bytes(item.as_bytes()))
            .collect();
        if !kept.is_empty()
            && let Ok(filtered) = HeaderValue::from_str(&kept.join(", "))
        {
            headers.append(name, filtered);
        }
    }
}

pub(crate) fn timeout(policy: &Policy) -> Duration {
    if policy.total_invocation_timeout_nanos > 0 {
        Duration::from_nanos(policy.total_invocation_timeout_nanos)
    } else {
        Duration::from_secs(30)
    }
}

/// Browser origins the application as a whole trusts. Development trusts
/// every origin; a deployment lists them in `SCENERY_CORS_ALLOW_ORIGINS`.
pub(crate) struct Origins {
    listed: Vec<String>,
    any: bool,
}

impl Origins {
    pub(crate) fn new(list: &str, development: bool) -> Self {
        Self {
            listed: list
                .split(',')
                .map(str::trim)
                .filter(|item| !item.is_empty())
                .map(str::to_owned)
                .collect(),
            any: development,
        }
    }

    fn allows(&self, origin: &str, wildcard: bool) -> bool {
        self.any
            || self
                .listed
                .iter()
                .any(|item| wildcard && item == "*" || item.eq_ignore_ascii_case(origin))
    }
}

/// Decides whether a response may be shared with `origin`. A route without
/// a policy is one of the framework's own `/auth/*` routes: it follows the
/// application list, and never its wildcard, because it sets credentials.
fn allowed_origin(
    origins: &Origins,
    policy: Option<&Policy>,
    origin: Option<&HeaderValue>,
) -> bool {
    let Some(origin) = origin
        .and_then(|value| value.to_str().ok())
        .map(str::trim)
        .filter(|origin| !origin.is_empty())
    else {
        return false;
    };
    match policy.map(|policy| policy.cors.as_str()) {
        None => origins.allows(origin, false),
        Some("" | "none") => false,
        Some("application") => origins.allows(origin, true),
        Some(_) => policy.is_some_and(|policy| {
            policy
                .allowed_origins
                .iter()
                .any(|value| value == "*" || value.eq_ignore_ascii_case(origin))
        }),
    }
}

fn cors_preflight(
    origins: &Origins,
    policy: Option<&Policy>,
    origin: Option<&HeaderValue>,
    method: &str,
    headers: &HeaderMap,
) -> Reply {
    let status = if allowed_origin(origins, policy, origin) {
        StatusCode::NO_CONTENT
    } else {
        StatusCode::FORBIDDEN
    };
    let mut reply = empty(status);
    add_cors(reply.headers_mut(), origins, policy, origin);
    if status == StatusCode::NO_CONTENT {
        header(reply.headers_mut(), "access-control-allow-methods", method);
        if let Some(headers) = headers.get("access-control-request-headers") {
            reply
                .headers_mut()
                .insert("access-control-allow-headers", headers.clone());
        }
    }
    reply
}

fn add_cors(
    headers: &mut HeaderMap,
    origins: &Origins,
    policy: Option<&Policy>,
    origin: Option<&HeaderValue>,
) {
    headers.remove("access-control-allow-origin");
    headers.remove("access-control-allow-credentials");
    let configured = match policy {
        None => origins.any || !origins.listed.is_empty(),
        Some(policy) => !matches!(policy.cors.as_str(), "" | "none"),
    };
    if !configured {
        return;
    }
    // A shared cache must key every response of a CORS route by origin,
    // including the ones that carry no grant.
    if !headers
        .get_all("vary")
        .iter()
        .any(|value| value.as_bytes().eq_ignore_ascii_case(b"origin"))
    {
        headers.append("vary", HeaderValue::from_static("Origin"));
    }
    if let Some(origin) = origin.filter(|value| allowed_origin(origins, policy, Some(value))) {
        headers.insert("access-control-allow-origin", origin.clone());
        headers.insert(
            "access-control-allow-credentials",
            HeaderValue::from_static("true"),
        );
        header(headers, "access-control-expose-headers", "X-Trace-Id");
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn policy(cors: &str, allowed: &[&str]) -> Policy {
        serde_json::from_value(json!({
            "BindingAddress":"api", "AuthorizationStrategy":"public", "AuthorizationRules":[],
            "TotalInvocationTimeoutNanos":0, "MaxRequestHeaderBytes":0, "CORS":cors,
            "AllowedOrigins":allowed, "TransportStatuses":{},
        }))
        .unwrap()
    }

    #[test]
    fn cors_credentials_follow_the_allowed_origin_for_preflight_and_actual_responses() {
        let origins = Origins::new("", false);
        let policy = policy("explicit", &["https://app.example"]);
        let mut request = HeaderMap::new();
        request.insert(
            "access-control-request-headers",
            HeaderValue::from_static("Authorization"),
        );
        for (raw, allowed) in [
            ("https://app.example", true),
            ("https://APP.example", true),
            ("https://elsewhere.example", false),
        ] {
            let origin = HeaderValue::from_static(raw);
            let preflight =
                cors_preflight(&origins, Some(&policy), Some(&origin), "POST", &request);
            assert_eq!(
                preflight.status(),
                if allowed {
                    StatusCode::NO_CONTENT
                } else {
                    StatusCode::FORBIDDEN
                }
            );
            assert_eq!(
                preflight
                    .headers()
                    .get("access-control-allow-methods")
                    .is_some(),
                allowed
            );
            let mut actual = HeaderMap::new();
            actual.insert(
                "access-control-allow-credentials",
                HeaderValue::from_static("false"),
            );
            actual.insert("access-control-allow-origin", HeaderValue::from_static("*"));
            add_cors(&mut actual, &origins, Some(&policy), Some(&origin));
            for headers in [preflight.headers(), &actual] {
                assert_eq!(
                    headers.get("access-control-allow-origin"),
                    allowed.then_some(&origin)
                );
                assert_eq!(
                    headers.get("access-control-allow-credentials"),
                    allowed.then_some(&HeaderValue::from_static("true"))
                );
                // Denied responses vary by origin too, so a shared cache
                // never serves one to an allowed origin.
                assert_eq!(headers.get("vary").unwrap(), "Origin");
            }
        }
    }

    #[test]
    fn application_origins_cover_auth_routes_and_application_policies() {
        let origin = HeaderValue::from_static("https://app.example");
        let other = HeaderValue::from_static("https://other.example");
        let listed = Origins::new(" https://app.example , ", false);
        let wildcard = Origins::new("*", false);
        let none = Origins::new("", false);
        let development = Origins::new("", true);
        let application = policy("application", &[]);
        let disabled = policy("none", &["https://app.example"]);

        // `/auth/*` has no policy: it follows the application list.
        assert!(allowed_origin(&listed, None, Some(&origin)));
        assert!(!allowed_origin(&listed, None, Some(&other)));
        assert!(!allowed_origin(&none, None, Some(&origin)));
        assert!(allowed_origin(&development, None, Some(&other)));
        // Its responses carry credentials, so the wildcard never applies.
        assert!(!allowed_origin(&wildcard, None, Some(&origin)));

        assert!(allowed_origin(&listed, Some(&application), Some(&origin)));
        assert!(!allowed_origin(&listed, Some(&application), Some(&other)));
        assert!(allowed_origin(&wildcard, Some(&application), Some(&other)));
        assert!(!allowed_origin(&listed, Some(&disabled), Some(&origin)));
        assert!(!allowed_origin(&listed, Some(&application), None));

        let preflight = cors_preflight(&listed, None, Some(&origin), "POST", &HeaderMap::new());
        assert_eq!(preflight.status(), StatusCode::NO_CONTENT);
        assert_eq!(preflight.headers()["access-control-allow-origin"], origin);
        assert_eq!(preflight.headers()["access-control-allow-methods"], "POST");
        let denied = cors_preflight(&listed, None, Some(&other), "POST", &HeaderMap::new());
        assert_eq!(denied.status(), StatusCode::FORBIDDEN);

        // Without any application origin the auth routes stay CORS-free.
        let mut headers = HeaderMap::new();
        add_cors(&mut headers, &none, None, Some(&origin));
        assert!(headers.is_empty());
        add_cors(&mut headers, &none, Some(&disabled), Some(&origin));
        assert!(headers.is_empty());
    }

    #[test]
    fn workers_observe_the_admitted_request_without_its_credentials() {
        let route: Route = serde_json::from_value(json!({
            "Path":"/orders/:order/lines/:line", "Method":"PUT", "Access":"auth", "PathTail":null,
            "Request":{"Mappings":[],"ContextMappings":[],"Body":null}, "Responses":{},
        }))
        .unwrap();
        let path = "/orders/42/lines/a%20b";
        let (fields, _) = match_path(&route, path).unwrap();
        let mut headers = HeaderMap::new();
        headers.insert("x-correlation-id", HeaderValue::from_static("corr-1"));
        headers.append("accept-language", HeaderValue::from_static("cs"));
        headers.append("accept-language", HeaderValue::from_static("en"));
        for consumed in [
            "authorization",
            "proxy-authorization",
            "cookie",
            "host",
            "x-scenery-internal",
        ] {
            headers.insert(consumed, HeaderValue::from_static("secret"));
        }
        // Go workers read this shape in `runtime/worker_go.go`.
        assert_eq!(
            worker_request("PUT", path, &route, &fields, &headers),
            json!({
                "method": "PUT", "path": "/orders/42/lines/a b",
                "path_params": [{"name":"order","value":"42"}, {"name":"line","value":"a%20b"}],
                "headers": {"accept-language":["cs","en"], "x-correlation-id":["corr-1"]},
                "api": {"exposed": true, "auth_required": true},
            })
        );
    }

    #[test]
    fn attest_scrubs_header_lists_and_individual_values() {
        let generation = Generation {
            number: 1,
            identity: crate::process::BuildIdentity {
                contract_revision: "c".into(),
                implementation_revision: "i".into(),
                build_input_digest: "d".into(),
                go_target: "worker".into(),
            },
            workers: Default::default(),
            retired: CancellationToken::new(),
        };
        let mut reply = empty(StatusCode::OK);
        let headers = reply.headers_mut();
        headers.append(
            "vary",
            HeaderValue::from_static("Origin, X-Scenery-Internal"),
        );
        headers.append("set-cookie", HeaderValue::from_static("app_session=ok"));
        headers.append("set-cookie", HeaderValue::from_static("scenery_leak=bad"));
        headers.append("x-scenery-secret", HeaderValue::from_static("hidden"));
        let cleaned = attest(reply, &generation, None);
        assert_eq!(cleaned.headers()["vary"], "Origin");
        assert!(!cleaned.headers().contains_key("x-scenery-secret"));
        assert_eq!(
            cleaned
                .headers()
                .get_all("set-cookie")
                .iter()
                .collect::<Vec<_>>(),
            ["app_session=ok"]
        );
    }

    #[tokio::test]
    async fn a_waiting_binding_answers_an_expired_deadline_with_its_declared_outcome() {
        let mut policy = policy("none", &[]);
        policy
            .transport_statuses
            .insert("dispatch.wait_timeout".into(), 524);
        for (delivery, status, code) in [
            ("wait", 524, "dispatch.wait_timeout"),
            ("direct", 504, "deadline_exceeded"),
        ] {
            // The durable wait ends at this same deadline: the route is
            // still pending when the invocation expires.
            let route = std::future::pending::<Result<(), Fault>>();
            let deadline = Instant::now() + Duration::from_millis(5);
            let Err(fault) = bounded(CancellationToken::new(), deadline, delivery, route).await
            else {
                unreachable!("a pending route cannot answer");
            };
            let (answered, problem) = fault.problem(Some(&policy));
            assert_eq!(
                (answered.as_u16(), &problem["code"]),
                (status, &json!(code))
            );
        }
    }

    #[test]
    fn path_tails_capture_the_empty_and_the_nested_remainder() {
        let route: Route = serde_json::from_value(json!({
            "Path":"/files/*rest", "Method":"GET", "Access":"public",
            "PathTail":{"MinimumSegments":0},
            "Request":{"Mappings":[],"ContextMappings":[],"Body":null}, "Responses":{},
        }))
        .unwrap();
        let capture = |path| match_path(&route, path).map(|(fields, _)| fields["rest"]);
        assert_eq!(capture("/files/a/b"), Some("a/b"));
        assert_eq!(capture("/files/"), Some(""));
        assert_eq!(capture("/other/a"), None);
    }
}
