use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use hyper::body::Incoming;
use hyper::header::HeaderValue;
use hyper::{HeaderMap, Method, Request, StatusCode};
use serde_json::{Map, Value, json};
use tokio::time::Instant;

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
            if count != 0 || descriptor.empty_capture != "omit" {
                fields.insert(tail, remainder);
            }
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
    if private && method == Method::POST && path == "/users/dev-bootstrap" {
        let mut response = crate::browser_auth::dev_bootstrap(host, request).await;
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
    let auth_route = crate::browser_auth::route(host, &method, &path)
        .map(|route| (route, ""))
        .or_else(|| {
            crate::browser_auth::route(host, &method, mounted_path?).map(|route| (route, "/api"))
        });
    if let Some((route, mount)) = auth_route {
        return attest(
            crate::browser_auth::serve(host, route, request, mount).await,
            &generation,
            None,
        );
    }
    let preflight = method == Method::OPTIONS
        && request
            .headers()
            .contains_key("access-control-request-method");
    let select_route =
        |method: &str| route(host, &path, method).or_else(|| route(host, mounted_path?, method));
    let match_method = if preflight {
        request
            .headers()
            .get("access-control-request-method")
            .and_then(|value| value.to_str().ok())
            .unwrap_or_default()
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
            .find_map(|plan| plan.route(&method, path).map(|route| (plan, route)))
    };
    if let Some((plan, route, mount)) = assistant_route(&path)
        .map(|(plan, route)| (plan, route, ""))
        .or_else(|| assistant_route(mounted_path?).map(|(plan, route)| (plan, route, "/api")))
    {
        if let Some(deadlines) = request
            .extensions()
            .get::<Arc<crate::http_timeout::DeadlineControl>>()
        {
            deadlines.apply_policy(&plan.policy);
        }
        let origin = request.headers().get("origin").cloned();
        let mut response =
            crate::assistant::Gateway::serve(host, plan, route, request, mount, generation.clone())
                .await;
        add_cors(response.headers_mut(), &plan.policy, origin.as_ref());
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
    let cors_origin = request.headers().get("origin").cloned();
    if preflight {
        return attest(
            cors_preflight(
                policy,
                cors_origin.as_ref(),
                match_method,
                request.headers(),
            ),
            &generation,
            None,
        );
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
    let started = Instant::now();
    host.telemetry
        .started(&call, selected.service, &selected.binding.address);
    let cancel = call.cancel.clone();
    let deadline = call.deadline;
    let result = tokio::select! {
        () = crate::abi::cancelled(cancel, deadline) => Err(Fault::deadline()),
        result = serve_route(host, request, &selected, &mut call, private) => result,
    };
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
    add_cors(response.headers_mut(), policy, cors_origin.as_ref());
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
    let rejected: Vec<_> = headers
        .iter()
        .filter(|(key, value)| {
            key.as_str().contains("scenery")
                || value
                    .to_str()
                    .is_ok_and(|value| value.to_ascii_lowercase().contains("scenery"))
        })
        .map(|(key, _)| key.clone())
        .collect();
    for key in rejected {
        headers.remove(key);
    }
    response
}

pub(crate) fn timeout(policy: &Policy) -> Duration {
    if policy.total_invocation_timeout_nanos > 0 {
        Duration::from_nanos(policy.total_invocation_timeout_nanos)
    } else {
        Duration::from_secs(30)
    }
}

fn allowed_origin(policy: &Policy, origin: Option<&HeaderValue>) -> bool {
    policy.cors != "none"
        && origin
            .and_then(|value| value.to_str().ok())
            .is_some_and(|origin| {
                policy
                    .allowed_origins
                    .iter()
                    .any(|value| value == origin || value == "*")
            })
}

fn cors_preflight(
    policy: &Policy,
    origin: Option<&HeaderValue>,
    method: &str,
    headers: &HeaderMap,
) -> Reply {
    let status = if allowed_origin(policy, origin) {
        StatusCode::NO_CONTENT
    } else {
        StatusCode::FORBIDDEN
    };
    let mut reply = empty(status);
    add_cors(reply.headers_mut(), policy, origin);
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

fn add_cors(headers: &mut HeaderMap, policy: &Policy, origin: Option<&HeaderValue>) {
    headers.remove("access-control-allow-origin");
    headers.remove("access-control-allow-credentials");
    if let Some(origin) = origin.filter(|value| allowed_origin(policy, Some(value))) {
        headers.insert("access-control-allow-origin", origin.clone());
        headers.insert(
            "access-control-allow-credentials",
            HeaderValue::from_static("true"),
        );
        headers.append("vary", HeaderValue::from_static("Origin"));
        header(headers, "access-control-expose-headers", "X-Trace-Id");
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cors_credentials_follow_the_allowed_origin_for_preflight_and_actual_responses() {
        let policy: Policy = serde_json::from_value(json!({
            "BindingAddress":"api", "AuthorizationStrategy":"public", "AuthorizationRules":[],
            "TotalInvocationTimeoutNanos":0, "MaxRequestHeaderBytes":0, "CORS":"explicit",
            "AllowedOrigins":["https://app.example"], "TransportStatuses":{},
        }))
        .unwrap();
        let mut request = HeaderMap::new();
        request.insert(
            "access-control-request-headers",
            HeaderValue::from_static("Authorization"),
        );
        for (raw, allowed) in [
            ("https://app.example", true),
            ("https://elsewhere.example", false),
        ] {
            let origin = HeaderValue::from_static(raw);
            let preflight = cors_preflight(&policy, Some(&origin), "POST", &request);
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
            add_cors(&mut actual, &policy, Some(&origin));
            for headers in [preflight.headers(), &actual] {
                assert_eq!(
                    headers.get("access-control-allow-origin"),
                    allowed.then_some(&origin)
                );
                assert_eq!(
                    headers.get("access-control-allow-credentials"),
                    allowed.then_some(&HeaderValue::from_static("true"))
                );
            }
        }
    }
}
