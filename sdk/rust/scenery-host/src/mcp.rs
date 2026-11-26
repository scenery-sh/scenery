use std::collections::{HashMap, HashSet};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use base64::{
    Engine,
    engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD},
};
use bytes::Bytes;
use http_body_util::{BodyExt, Limited, StreamBody};
use hyper::body::{Frame, Incoming};
use hyper::{HeaderMap, Method, Request, StatusCode};
use serde::Deserialize;
use serde_json::{Value, json, value::RawValue};
use tokio::time::Instant as Deadline;

use crate::assistant::{Descriptor, RunKey};
use crate::host::{Call, Fault, Host, Reply, empty, header, json_reply};
use crate::{process, transport, values};

const PROTOCOL: &str = "2025-11-25";
const MAX_BODY: usize = 16 << 20;
const SESSION_LIMIT: usize = 1024;
const SESSION_TTL: Duration = Duration::from_secs(3600);

#[derive(Deserialize)]
pub(crate) struct Manifest {
    protocol_version: String,
    pub capabilities: Vec<Capability>,
}

#[derive(Deserialize)]
pub(crate) struct Capability {
    pub name: String,
    #[serde(default)]
    title: String,
    #[serde(default)]
    description: String,
    input_schema: Value,
    output_schema: Value,
    pub operation_address: String,
    execution_address: String,
    origin: Origin,
    limits: Limits,
    effect: Effect,
    pub approval: Approval,
}

#[derive(Deserialize)]
struct Origin {
    kind: String,
    address: String,
}

#[derive(Deserialize)]
struct Limits {
    max_input_bytes: usize,
    max_result_bytes: usize,
}

#[derive(Deserialize)]
struct Effect {
    read_only: bool,
    destructive: bool,
    idempotent: bool,
    open_world: bool,
}

#[derive(Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub(crate) enum Approval {
    Never,
    Always,
}

impl Manifest {
    pub(crate) fn verify(&self, workers: &[crate::plan::Service]) -> anyhow::Result<()> {
        anyhow::ensure!(
            self.protocol_version == PROTOCOL,
            "unsupported tool protocol"
        );
        let mut names = HashSet::new();
        for cap in &self.capabilities {
            anyhow::ensure!(
                !cap.name.is_empty()
                    && names.insert(&cap.name)
                    && cap.origin.kind == "local"
                    && (1..=MAX_BODY).contains(&cap.limits.max_input_bytes)
                    && (1..=MAX_BODY).contains(&cap.limits.max_result_bytes)
                    && workers
                        .iter()
                        .flat_map(|service| &service.bindings)
                        .any(|binding| binding.mcp
                            && binding.address == cap.origin.address
                            && binding.operation == cap.operation_address
                            && binding.execution == cap.execution_address),
                "tool does not match a worker binding"
            );
        }
        Ok(())
    }
}

impl Capability {
    fn definition(&self) -> Value {
        let mut data = json!({
            "name": self.name,
            "inputSchema": self.input_schema,
            "outputSchema": self.output_schema,
            "annotations": {
                "readOnlyHint": self.effect.read_only,
                "destructiveHint": self.effect.destructive,
                "idempotentHint": self.effect.idempotent,
                "openWorldHint": self.effect.open_world,
            }
        });
        if !self.title.is_empty() {
            data["title"] = json!(self.title);
        }
        if !self.description.is_empty() {
            data["description"] = json!(self.description);
        }
        data
    }
}

#[derive(Deserialize)]
struct Assertion {
    audience: String,
    assistant_address: String,
    principal: String,
    conversation_digest: String,
    capability_revision: String,
    #[serde(default)]
    run_id: String,
    #[serde(default)]
    trace_context: HashMap<String, String>,
    expires_at: i64,
    #[serde(default)]
    not_before: i64,
    nonce: String,
}

impl Assertion {
    fn signed(token: &str, secret: &[u8], now: i64) -> Option<Self> {
        let (encoded, signature) = token.trim().split_once('.')?;
        let signature = URL_SAFE_NO_PAD.decode(signature).ok()?;
        let key = ring::hmac::Key::new(ring::hmac::HMAC_SHA256, secret);
        ring::hmac::verify(&key, encoded.as_bytes(), &signature).ok()?;
        let claims: Self = serde_json::from_slice(&URL_SAFE_NO_PAD.decode(encoded).ok()?).ok()?;
        (claims.audience == "scenery"
            && !claims.principal.trim().is_empty()
            && !claims.conversation_digest.is_empty()
            && !claims.nonce.is_empty()
            && claims.expires_at > now - 30
            && claims.expires_at <= now + 330
            && claims.not_before <= now + 30)
            .then_some(claims)
    }
}

#[derive(Deserialize)]
struct Message {
    jsonrpc: String,
    method: String,
    id: Option<Value>,
    params: Option<Box<RawValue>>,
}

#[derive(Deserialize)]
struct ToolCall {
    name: String,
    arguments: Option<Box<RawValue>>,
}

struct Session {
    principal: String,
    conversation: String,
    last_used: Instant,
    initialized: bool,
}

#[derive(Default)]
pub(crate) struct Gateway {
    sessions: Mutex<HashMap<String, Session>>,
}

impl Gateway {
    pub(crate) async fn serve(
        &self,
        host: &Host,
        descriptor: &Descriptor,
        manifest: &Manifest,
        request: Request<Incoming>,
    ) -> Reply {
        let (parts, body) = request.into_parts();
        let headers = &parts.headers;
        if parts.uri.path() != "/" {
            return empty(StatusCode::NOT_FOUND);
        }
        if !loopback_request(headers) {
            return empty(StatusCode::FORBIDDEN);
        }
        let version = headers.get("mcp-protocol-version");
        let session = headers
            .get("mcp-session-id")
            .and_then(|value| value.to_str().ok());
        if version.is_some_and(|value| value != PROTOCOL) || session.is_some() && version.is_none()
        {
            return empty(StatusCode::BAD_REQUEST);
        }
        let Some(assertion) = headers
            .get("scenery-assistant-assertion")
            .and_then(|token| token.to_str().ok())
            .and_then(|token| {
                Assertion::signed(
                    token,
                    descriptor.mcp_bridge_secret.as_bytes(),
                    time::OffsetDateTime::now_utc().unix_timestamp(),
                )
            })
            .filter(|claims| claims.assistant_address == descriptor.assistant_address)
        else {
            return empty(StatusCode::UNAUTHORIZED);
        };
        if assertion.capability_revision != descriptor.capability_revision {
            return empty(StatusCode::CONFLICT);
        }
        if let Some(id) = session
            && let Err(status) = self.check_session(id, &assertion, parts.method == Method::DELETE)
        {
            return empty(status);
        }
        match parts.method {
            Method::DELETE if session.is_some() => return empty(StatusCode::NO_CONTENT),
            Method::GET if session.is_some() => {
                crate::http_timeout::request_body_finished(&parts.extensions);
                if !accepts(headers, "text/event-stream") {
                    return empty(StatusCode::NOT_ACCEPTABLE);
                }
                let mut response = empty(StatusCode::OK);
                header(response.headers_mut(), "content-type", "text/event-stream");
                header(response.headers_mut(), "cache-control", "no-store");
                let stop = host.stop.clone();
                let stream = futures_util::stream::once(async move {
                    Ok(Frame::data(Bytes::from_static(b": connected\n\n")))
                });
                let lifetime = futures_util::stream::once(async move {
                    stop.cancelled().await;
                    Ok(Frame::data(Bytes::new()))
                });
                use futures_util::StreamExt;
                *response.body_mut() = StreamBody::new(stream.chain(lifetime)).boxed_unsync();
                return response;
            }
            Method::GET | Method::DELETE => return empty(StatusCode::BAD_REQUEST),
            Method::POST => {}
            _ => return empty(StatusCode::METHOD_NOT_ALLOWED),
        }
        if !accepts(headers, "application/json") || !accepts(headers, "text/event-stream") {
            return empty(StatusCode::NOT_ACCEPTABLE);
        }
        if !headers
            .get("content-type")
            .and_then(|value| value.to_str().ok())
            .is_some_and(|value| value.split(';').next() == Some("application/json"))
        {
            return empty(StatusCode::UNSUPPORTED_MEDIA_TYPE);
        }
        let input = transport::read(body, MAX_BODY).await;
        crate::http_timeout::request_body_finished(&parts.extensions);
        let input = match input {
            Ok(input) => input,
            Err(_) => return empty(StatusCode::PAYLOAD_TOO_LARGE),
        };
        let Ok(message) = serde_json::from_slice::<Message>(&input) else {
            return rpc_error(Value::Null, -32700, "invalid JSON-RPC message");
        };
        let id = message.id.unwrap_or(Value::Null);
        if message.jsonrpc != "2.0" || !(id.is_null() || id.is_string() || id.is_number()) {
            return rpc_error(Value::Null, -32600, "invalid JSON-RPC request");
        }
        let params = message.params.as_deref().map_or("{}", RawValue::get);
        if message.method == "initialize" {
            if session.is_some()
                || id.is_null()
                || serde_json::from_str::<Value>(params)
                    .ok()
                    .and_then(|params| params["protocolVersion"].as_str().map(str::to_owned))
                    .as_deref()
                    != Some(PROTOCOL)
            {
                return empty(StatusCode::BAD_REQUEST);
            }
            let Some(session_id) = self.initialize_session(&assertion) else {
                return empty(StatusCode::SERVICE_UNAVAILABLE);
            };
            let mut reply = rpc_result(
                id,
                json!({
                    "protocolVersion": PROTOCOL,
                    "serverInfo": {"name": "application-tools", "version": "1"},
                    "capabilities": {"tools": {}},
                }),
            );
            header(reply.headers_mut(), "mcp-session-id", &session_id);
            return reply;
        }
        let Some(session_id) = session else {
            return empty(StatusCode::BAD_REQUEST);
        };
        if message.method == "notifications/initialized" && id.is_null() {
            if let Some(session) = self.sessions.lock().expect("sessions").get_mut(session_id) {
                session.initialized = true;
            }
            return empty(StatusCode::ACCEPTED);
        }
        if id.is_null() {
            return empty(StatusCode::ACCEPTED);
        }
        if !self
            .sessions
            .lock()
            .expect("sessions")
            .get(session_id)
            .is_some_and(|session| session.initialized)
        {
            return rpc_error(id, -32600, "MCP session is not initialized");
        }
        match message.method.as_str() {
            "ping" => rpc_result(id, json!({})),
            "tools/list" => rpc_result(
                id,
                json!({"tools": manifest.capabilities.iter().map(Capability::definition).collect::<Vec<_>>()}),
            ),
            "tools/call" => {
                let Ok(tool) = serde_json::from_str::<ToolCall>(params) else {
                    return rpc_error(id, -32602, "invalid tool parameters");
                };
                let Some(cap) = manifest
                    .capabilities
                    .iter()
                    .find(|cap| cap.name == tool.name)
                else {
                    return rpc_error(id, -32602, "unknown tool");
                };
                let input = tool.arguments.as_deref().map_or("{}", RawValue::get);
                match invoke(host, &assertion, cap, input).await {
                    Ok((result, trace, worker)) => {
                        let mut reply = rpc_result(id, result);
                        header(reply.headers_mut(), "x-trace-id", &trace);
                        if let Some((pid, revision)) = worker {
                            header(reply.headers_mut(), "x-app-worker-pid", &pid.to_string());
                            header(reply.headers_mut(), "x-app-worker-revision", &revision);
                        }
                        reply
                    }
                    Err(failure) => rpc_result(id, tool_failure(failure)),
                }
            }
            _ => rpc_error(id, -32601, "unknown method"),
        }
    }

    fn initialize_session(&self, assertion: &Assertion) -> Option<String> {
        let mut sessions = self.sessions.lock().expect("sessions");
        let now = Instant::now();
        sessions.retain(|_, session| now.duration_since(session.last_used) < SESSION_TTL);
        if sessions.len() >= SESSION_LIMIT {
            return None;
        }
        let id = process::random_hex::<16>().ok()?;
        sessions.insert(
            id.clone(),
            Session {
                principal: assertion.principal.clone(),
                conversation: assertion.conversation_digest.clone(),
                last_used: now,
                initialized: false,
            },
        );
        Some(id)
    }

    fn check_session(
        &self,
        id: &str,
        assertion: &Assertion,
        delete: bool,
    ) -> Result<(), StatusCode> {
        let mut sessions = self.sessions.lock().expect("sessions");
        let session = sessions.get_mut(id).ok_or(StatusCode::NOT_FOUND)?;
        if session.last_used.elapsed() >= SESSION_TTL {
            sessions.remove(id);
            return Err(StatusCode::NOT_FOUND);
        }
        if session.principal != assertion.principal
            || session.conversation != assertion.conversation_digest
        {
            return Err(StatusCode::FORBIDDEN);
        }
        session.last_used = Instant::now();
        if delete {
            sessions.remove(id);
        }
        Ok(())
    }
}

fn accepts(headers: &HeaderMap, media: &str) -> bool {
    headers
        .get_all("accept")
        .iter()
        .filter_map(|value| value.to_str().ok())
        .flat_map(|value| value.split(','))
        .any(|value| {
            value
                .split(';')
                .next()
                .is_some_and(|value| value.trim() == media)
        })
}

fn loopback_request(headers: &HeaderMap) -> bool {
    let allowed = |host: &str| {
        host.eq_ignore_ascii_case("localhost")
            || host
                .trim_matches(['[', ']'])
                .parse::<std::net::IpAddr>()
                .is_ok_and(|ip| ip.is_loopback())
    };
    headers
        .get("host")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse::<hyper::http::uri::Authority>().ok())
        .is_some_and(|host| allowed(host.host()))
        && headers.get("origin").is_none_or(|value| {
            value
                .to_str()
                .ok()
                .and_then(|value| url::Url::parse(value).ok())
                .is_some_and(|origin| origin.host_str().is_some_and(allowed))
        })
}

fn rpc_result(id: Value, result: Value) -> Reply {
    json_reply(
        StatusCode::OK,
        &json!({"jsonrpc":"2.0","id":id,"result":result}),
    )
}

fn rpc_error(id: Value, code: i32, message: &str) -> Reply {
    json_reply(
        StatusCode::OK,
        &json!({"jsonrpc":"2.0","id":id,"error":{"code":code,"message":message}}),
    )
}

fn tool_result(content: Value, is_error: bool) -> Value {
    let mut result = json!({
        "content": [{"type": "text", "text": values::encode(&content)}],
        "structuredContent": content,
    });
    if is_error {
        result["isError"] = json!(true);
    }
    result
}

fn tool_failure(fault: Fault) -> Value {
    let mut problem = fault.envelope();
    problem["code"] = json!(match fault.code {
        "transport.invalid_request" => "invalid_argument",
        "system.internal" => "internal_error",
        code => code,
    });
    tool_result(json!({"outcome":"failed","problem":problem}), true)
}

type ToolOutcome = (Value, String, Option<(u32, String)>);

async fn invoke(
    host: &Host,
    assertion: &Assertion,
    cap: &Capability,
    raw: &str,
) -> Result<ToolOutcome, Fault> {
    if raw.len() > cap.limits.max_input_bytes {
        return Err(Fault::new(
            429,
            "resource_exhausted",
            "tool input exceeds configured limit",
        ));
    }
    let input = values::parse(raw.as_bytes()).map_err(|_| Fault::invalid())?;
    if !input.is_object() {
        return Err(Fault::invalid());
    }
    let run_key = RunKey::new(
        &assertion.assistant_address,
        &assertion.principal,
        &assertion.conversation_digest,
        &assertion.run_id,
    );
    let run = host.assistants.run(&run_key).ok_or_else(Fault::forbidden)?;
    let (service, binding) = host
        .binding(&cap.origin.address)
        .ok_or_else(|| Fault::internal("missing tool binding"))?;
    let timeout = crate::gateway::timeout(&binding.policy);
    let mut headers = HeaderMap::new();
    if let Some(trace) = assertion
        .trace_context
        .get("traceparent")
        .or(run.traceparent.as_ref())
    {
        header(&mut headers, "traceparent", trace);
    }
    let mut call = Call::new(run.generation.clone(), &headers, "MCP", "/", timeout)
        .map_err(Fault::internal)?;
    call.principal = run.principal;
    call.deadline = Deadline::now() + timeout;
    if cap.approval == Approval::Always
        && !host
            .assistants
            .consume_approval(&run_key, &cap.name, &input)
    {
        return Err(Fault::forbidden());
    }
    host.telemetry.started(&call, service, &binding.address);
    let started = Deadline::now();
    let dispatched = host.dispatch(&call, service, binding, input).await;
    let error = match &dispatched {
        Ok(outcome) if outcome.kind == "error" => outcome.name.as_str(),
        Err(fault) => fault.code,
        _ => "",
    };
    host.telemetry
        .span(&call, service, &binding.address, started.elapsed(), error);
    let mut outcome = dispatched?;
    if outcome.events.is_some() {
        return Err(Fault::internal("MCP tool returned an event stream"));
    }
    if let Some(stream) = outcome.stream.take() {
        let limit = cap.limits.max_result_bytes;
        if stream.length() > limit as u64 {
            return Err(result_limit());
        }
        let bytes = Limited::new(stream, limit)
            .collect()
            .await
            .map_err(Fault::internal)?
            .to_bytes();
        let body = service
            .bindings
            .iter()
            .filter(|binding| binding.operation == cap.operation_address)
            .filter_map(|binding| binding.http.as_ref())
            .filter_map(|route| route.responses.get(&format!("result.{}", outcome.name)))
            .find(|response| response.stream && response.codec == "bytes")
            .ok_or_else(|| Fault::internal("tool has no byte stream projection"))?;
        let mut target = &mut outcome.payload;
        for field in &body.body.path {
            target = &mut target[field];
        }
        *target = json!(STANDARD.encode(&bytes));
    }
    let is_error = outcome.kind == "error";
    let key = if is_error { "problem" } else { "value" };
    let content = json!({"outcome":outcome.name,key:outcome.payload});
    if values::encode(&content).len() > cap.limits.max_result_bytes {
        return Err(result_limit());
    }
    Ok((
        tool_result(content, is_error),
        call.trace_id,
        outcome.worker,
    ))
}

fn result_limit() -> Fault {
    Fault::new(
        429,
        "resource_exhausted",
        "tool result exceeds configured limit",
    )
}
