use std::collections::VecDeque;
use std::io;
use std::sync::LazyLock;

use bytes::Bytes;
use futures_util::TryStreamExt;
use http_body_util::{BodyExt, StreamBody};
use hyper::body::Frame;
use regex::Regex;
use tokio::io::{AsyncBufReadExt, AsyncReadExt, BufReader};
use tokio_util::io::StreamReader;
use unicode_normalization::UnicodeNormalization;

use super::*;

#[derive(Debug)]
pub(crate) enum Error {
    Invalid,
    HeaderTooLarge,
    Unauthorized,
    Forbidden,
    NotFound,
    Conflict,
    Cancelled,
    Unavailable,
    Internal,
}

impl Error {
    pub(crate) fn reply(self) -> Reply {
        let (status, code, message) = match self {
            Self::Invalid => (
                StatusCode::BAD_REQUEST,
                "invalid_request",
                "invalid assistant request",
            ),
            Self::HeaderTooLarge => (
                StatusCode::REQUEST_HEADER_FIELDS_TOO_LARGE,
                "request_header_too_large",
                "request headers exceed the declared limit",
            ),
            Self::Unauthorized => (
                StatusCode::UNAUTHORIZED,
                "unauthorized",
                "assistant authentication required",
            ),
            Self::Forbidden => (
                StatusCode::FORBIDDEN,
                "forbidden",
                "assistant request forbidden",
            ),
            Self::NotFound => (
                StatusCode::NOT_FOUND,
                "not_found",
                "assistant resource not found",
            ),
            Self::Conflict => (
                StatusCode::CONFLICT,
                "conflict",
                "assistant approval is no longer pending",
            ),
            Self::Cancelled => (StatusCode::CONFLICT, "cancelled", "assistant run cancelled"),
            Self::Unavailable => (
                StatusCode::SERVICE_UNAVAILABLE,
                "unavailable",
                "assistant runtime unavailable",
            ),
            Self::Internal => (
                StatusCode::INTERNAL_SERVER_ERROR,
                "internal",
                "assistant request failed",
            ),
        };
        let mut reply = json_reply(status, &json!({"code":code,"message":message}));
        header(reply.headers_mut(), "cache-control", "no-store");
        reply
    }
}

const MAX_EVENT: usize = 1 << 20;

pub(super) async fn stream(
    host: &Arc<Host>,
    plan: &Plan,
    claims: Claims<Conversation>,
    conversation_id: &str,
    principal: &str,
    after: u64,
    sse: bool,
) -> Result<Reply, Error> {
    let descriptor = host
        .assistants
        .descriptors
        .get(&plan.address)
        .ok_or(Error::Unavailable)?;
    let mut url = descriptor.control_url().ok_or(Error::Unavailable)?;
    url.path_segments_mut()
        .map_err(|_| Error::Unavailable)?
        .clear()
        .extend([
            "scenery",
            "v1",
            "control",
            "sessions",
            &claims.data.private_session_id,
            "events",
        ]);
    // Replay from the beginning to derive exactly the same redacted deltas
    // on reconnect, regardless of where the caller resumes.
    url.query_pairs_mut().append_pair("after", "0");
    let response = host
        .assistants
        .client
        .get(url)
        .timeout(Duration::from_secs(300))
        .header(CONTROL_HEADER, &descriptor.control_token)
        .header("accept", "application/x-ndjson")
        .send()
        .await
        .map_err(|_| Error::Unavailable)?;
    if response.status() != StatusCode::OK
        || !response
            .headers()
            .get("content-type")
            .and_then(|value| value.to_str().ok())
            .is_some_and(|value| value.split(';').next() == Some("application/x-ndjson"))
    {
        return Err(Error::Unavailable);
    }
    // Each line is bounded below; the replay as a whole is not. It restarts
    // from the first event on every connection, so a cap on its total size
    // would end a long conversation for good once its history outgrew it.
    let reader = BufReader::new(StreamReader::new(
        response.bytes_stream().map_err(io::Error::other),
    ));
    let events = EventStream {
        host: host.clone(),
        claims,
        conversation_id: conversation_id.into(),
        principal: principal.into(),
        capability_revision: plan.capability_revision.clone(),
        runtime_revision: plan.runtime_revision.clone(),
        name: plan.address.rsplit('/').next().unwrap_or_default().into(),
        server: plan.mcp_server.clone(),
        cursor: 0,
        after,
        redactor: Redactor::default(),
        delta: None,
        ready: VecDeque::new(),
        done: false,
        sse,
    };
    let stream =
        futures_util::stream::unfold((reader, events), |(mut reader, mut events)| async move {
            loop {
                if let Some(event) = events.ready.pop_front() {
                    if event["sequence"]
                        .as_u64()
                        .is_some_and(|seq| seq > events.after)
                    {
                        let encoded = crate::values::encode(&event);
                        let frame = if events.sse {
                            format!("data: {encoded}\n\n")
                        } else {
                            format!("{encoded}\n")
                        };
                        return Some((
                            Ok::<_, io::Error>(Frame::data(Bytes::from(frame))),
                            (reader, events),
                        ));
                    }
                    continue;
                }
                if events.done {
                    return None;
                }
                let mut data = Vec::new();
                let mut line = (&mut reader).take((MAX_EVENT + 1) as u64);
                let read = tokio::select! {
                    () = events.host.stop.cancelled() => return None,
                    read = line.read_until(b'\n', &mut data) => read,
                };
                match read {
                    Ok(0) => {
                        events.flush_delta();
                        events.done = true;
                    }
                    Ok(n) if n <= MAX_EVENT && data.last() == Some(&b'\n') => {
                        let event = crate::values::parse(&data)
                            .ok()
                            .and_then(|value| serde_json::from_value(value).ok());
                        if event.is_none_or(|event| events.accept(event).is_err()) {
                            return Some((
                                Err(io::Error::other("invalid assistant event")),
                                (reader, events),
                            ));
                        }
                    }
                    _ => {
                        return Some((
                            Err(io::Error::other("incomplete assistant event")),
                            (reader, events),
                        ));
                    }
                }
            }
        });
    let mut reply = crate::host::empty(StatusCode::OK);
    header(
        reply.headers_mut(),
        "content-type",
        if sse {
            "text/event-stream; charset=utf-8"
        } else {
            "application/x-ndjson; charset=utf-8"
        },
    );
    header(reply.headers_mut(), "cache-control", "no-store");
    header(reply.headers_mut(), "x-accel-buffering", "no");
    header(reply.headers_mut(), "x-contract-compression", "identity");
    *reply.body_mut() = StreamBody::new(stream).boxed_unsync();
    Ok(reply)
}

#[derive(Deserialize)]
struct PrivateEvent {
    kind: String,
    schema_revision: String,
    #[serde(rename = "type")]
    typ: String,
    assistant_address: String,
    runtime_revision: String,
    capability_revision: String,
    private_session_id: String,
    continuation_token: String,
    run_id: String,
    sequence: u64,
    occurred_at: String,
    #[serde(default)]
    capability_name: String,
    #[serde(default)]
    approval_id: String,
    #[serde(default)]
    capability_proposal: Value,
    #[serde(default)]
    approval_wait: Value,
    data: Value,
}

struct EventStream {
    host: Arc<Host>,
    claims: Claims<Conversation>,
    conversation_id: String,
    principal: String,
    capability_revision: String,
    runtime_revision: String,
    name: String,
    server: String,
    cursor: u64,
    after: u64,
    redactor: Redactor,
    delta: Option<Value>,
    ready: VecDeque<Value>,
    done: bool,
    sse: bool,
}

impl EventStream {
    fn accept(&mut self, event: PrivateEvent) -> Result<(), Error> {
        if event.kind != "scenery.assistant.control.event"
            || event.schema_revision
                != "sha256:0a6e5fc603f3bb2ea15292b5910af273887003be5a7ae604745501b831a65df8"
            || event.assistant_address != self.claims.assistant_address
            || event.runtime_revision != self.runtime_revision
            || event.capability_revision != self.capability_revision
            || event.private_session_id != self.claims.data.private_session_id
            || event.continuation_token != self.claims.data.continuation_token
            || event.sequence <= self.cursor
            || !token::valid_run(&event.run_id)
            || !token::valid_time(&event.occurred_at)
        {
            return Err(Error::Internal);
        }
        self.cursor = event.sequence;
        let run = RunKey::new(
            &event.assistant_address,
            &self.principal,
            &self.claims.conversation_digest,
            &event.run_id,
        );
        if !matches!(
            event.typ.as_str(),
            "run.completed" | "run.cancelled" | "run.failed"
        ) {
            self.host.assistants.touch(&run);
        }
        let capability = &event.capability_name;
        let (typ, data) = match event.typ.as_str() {
            "run.started" => ("run.started", json!({"state":"started"})),
            "text.delta" => {
                let text = event.data["text"].as_str().ok_or(Error::Internal)?;
                let replacement = self.redactor.write(text);
                ("message.delta", json!({"text":replacement}))
            }
            "message.completed" => (
                "message.completed",
                json!({"text": redact(event.data["text"].as_str().unwrap_or_default())}),
            ),
            "capability.proposal" => {
                let proposal = event.capability_proposal;
                let cap = proposal["capability_name"].as_str().unwrap_or(capability);
                let id = proposal["approval_id"]
                    .as_str()
                    .unwrap_or(&event.approval_id);
                let input = proposal.get("input").cloned().unwrap_or(event.data);
                let approval = self.public_approval(&run, cap, id, None)?;
                (
                    "capability.proposed",
                    json!({"capability":redact(cap),"approval_id":approval,"input":redact_json(input)}),
                )
            }
            "approval.wait" => {
                let id = event.approval_wait["approval_id"]
                    .as_str()
                    .unwrap_or(&event.approval_id);
                let approval =
                    self.public_approval(&run, capability, id, event.data.get("input").cloned())?;
                (
                    "approval.required",
                    json!({"capability":redact(capability),"approval_id":approval}),
                )
            }
            "capability.started" => (
                "capability.started",
                json!({"capability":redact(capability)}),
            ),
            "capability.completed" => (
                "capability.completed",
                json!({"capability":redact(capability),"state":"completed"}),
            ),
            "run.completed" | "run.cancelled" => {
                self.host.assistants.finish(&run);
                let state = if event.typ == "run.completed" {
                    "completed"
                } else {
                    "cancelled"
                };
                (event.typ.as_str(), json!({"state":state}))
            }
            "run.failed" => {
                self.host.assistants.finish(&run);
                (
                    "run.failed",
                    json!({"code":"assistant_failed","message":"assistant run failed"}),
                )
            }
            "runtime.crashed" | "runtime.restarting" => {
                ("runtime.restarting", json!({"state":"restarting"}))
            }
            _ => return Err(Error::Internal),
        };
        let public = json!({
            "type": format!("assistant.{typ}"), "assistant":self.name, "conversation_id":self.conversation_id,
            "run_id":event.run_id, "sequence":event.sequence, "occurred_at":event.occurred_at, "data":data,
        });
        if typ == "message.delta" {
            if let Some(previous) = self.delta.replace(public) {
                self.ready.push_back(previous);
            }
        } else {
            self.flush_delta();
            self.ready.push_back(public);
        }
        Ok(())
    }

    fn flush_delta(&mut self) {
        if let Some(mut previous) = self.delta.take() {
            let mut text = previous["data"]["text"]
                .as_str()
                .unwrap_or_default()
                .to_owned();
            text.push_str(&self.redactor.flush());
            previous["data"]["text"] = json!(text);
            self.ready.push_back(previous);
        }
    }

    fn public_approval(
        &self,
        run: &RunKey,
        capability: &str,
        private_id: &str,
        input: Option<Value>,
    ) -> Result<String, Error> {
        let manifest = &self.host.bundle.host.mcp_servers[&self.server];
        // The adapter may prefix imported connection tools with the connection name.
        let local = capability.strip_prefix("scenery__").unwrap_or(capability);
        let approved_tool = manifest
            .capabilities
            .iter()
            .any(|cap| cap.name == local && cap.approval == crate::mcp::Approval::Always);
        let index = (run.clone(), private_id.into());
        let mut state = self.host.assistants.state.lock().expect("assistant state");
        if let Some(pending) = state.approvals.get_mut(&index) {
            if approved_tool
                && pending.capability == local
                && pending.input.is_none()
                && input.is_some()
            {
                pending.input = input;
            }
            return Ok(pending.token.clone());
        }
        let token = self
            .host
            .assistants
            .key
            .as_ref()
            .ok_or(Error::Unavailable)?
            .seal(
                &run.assistant,
                &self.claims.owner_digest,
                &token::conversation(&self.conversation_id),
                Approval {
                    run_id: run.run.clone(),
                    approval_id: private_id.into(),
                    decision_context: format!("capability:{}", redact(capability)),
                },
            )
            .ok_or(Error::Internal)?;
        if state.runs.contains_key(run) {
            if state.approvals.len() >= MAX_RUNS {
                return Err(Error::Unavailable);
            }
            state.approvals.insert(
                index,
                PendingApproval {
                    token: token.clone(),
                    capability: local.into(),
                    input: if approved_tool { input } else { None },
                    local_tool: approved_tool,
                    decision: Decision::Pending,
                },
            );
        }
        Ok(token)
    }
}

static SIGNATURES: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(
    r#"(?i)@vercel/connect/eve|node_modules/eve|from "eve[/"]|/eve/v1|/eve/|eve_|eve-|eve|scenery"#
).expect("redaction patterns")
});
static WORD: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^[\pL\pN\pM_]$").expect("word rune"));

fn word(c: char) -> bool {
    WORD.is_match(c.encode_utf8(&mut [0; 4]))
}

#[derive(Default)]
struct Redactor {
    pending: String,
    previous: Option<char>,
}

impl Redactor {
    fn write(&mut self, text: &str) -> String {
        self.pending.push_str(text);
        self.pending = self.pending.nfkc().collect();
        let safe = self
            .pending
            .char_indices()
            .rev()
            .nth(23)
            .map_or(0, |(offset, _)| offset);
        self.prefix(safe)
    }

    fn flush(&mut self) -> String {
        let text = self.prefix(self.pending.len());
        self.previous = None;
        text
    }

    fn prefix(&mut self, mut limit: usize) -> String {
        let mut output = String::new();
        let mut position = 0;
        for matched in SIGNATURES.find_iter(&self.pending) {
            if matched.start() >= limit {
                break;
            }
            let literal = matched.as_str();
            let before = self.pending[..matched.start()]
                .chars()
                .next_back()
                .or(self.previous)
                .is_some_and(word);
            let after = self.pending[matched.end()..]
                .chars()
                .next()
                .is_some_and(word);
            if literal.eq_ignore_ascii_case("eve") && (before || after)
                || (literal.ends_with('_') || literal.ends_with('-')) && before
            {
                continue;
            }
            if matched.end() > limit {
                limit = matched.start();
                break;
            }
            output.push_str(&self.pending[position..matched.start()]);
            output.push_str("assistant runtime");
            position = matched.end();
        }
        output.push_str(&self.pending[position..limit]);
        self.previous = self.pending[..limit].chars().next_back().or(self.previous);
        self.pending.drain(..limit);
        output
    }
}

fn redact(text: &str) -> String {
    let mut redactor = Redactor {
        pending: text.nfkc().collect(),
        previous: None,
    };
    redactor.flush()
}

fn redact_json(value: Value) -> Value {
    match value {
        Value::String(value) => json!(redact(&value)),
        Value::Array(values) => Value::Array(values.into_iter().map(redact_json).collect()),
        Value::Object(fields) => Value::Object(
            fields
                .into_iter()
                .filter(|(key, _)| {
                    let key = key.to_ascii_lowercase();
                    !["provider", "private", "continuation", "session"]
                        .iter()
                        .any(|word| key.contains(word))
                })
                .map(|(key, value)| (redact(&key), redact_json(value)))
                .collect(),
        ),
        other => other,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs::Permissions;
    use std::os::unix::fs::PermissionsExt;

    #[test]
    fn redacts_signatures_across_events_without_removing_word_fragments() {
        let input = "Some long text. The previous evening with EVE and /eve/v1 and SceNery.";
        let expected = "Some long text. The previous evening with assistant runtime and assistant runtime and assistant runtime.";
        for (i, _) in input.char_indices() {
            let mut state = Redactor::default();
            let output = state.write(&input[..i]) + &state.write(&input[i..]) + &state.flush();
            assert_eq!(output, expected, "split at {i}");
        }
    }

    fn test_plan() -> Plan {
        serde_json::from_value(json!({
            "address": "app/assistant/tools",
            "path": "/assistants/tools",
            "mcp_server": "app/mcp_server/tools",
            "authentication": "auth",
            "session_access": "initiator",
            "runtime_revision": "runtime",
            "capability_revision": "capabilities",
            "policy": {
                "BindingAddress": "app/binding/tool",
                "AuthorizationStrategy": "public",
                "AuthorizationRules": [],
                "TotalInvocationTimeoutNanos": 0,
                "MaxRequestHeaderBytes": 0,
                "CORS": "none",
                "AllowedOrigins": [],
                "TransportStatuses": {}
            }
        }))
        .unwrap()
    }

    fn test_gateway(dir: &Path, control_address: &str, plan: &Plan) -> anyhow::Result<Gateway> {
        let runtime = dir.join("descriptors.json");
        let key = dir.join("key");
        let descriptor = json!({"assistants":[{
            "assistant_address": plan.address,
            "control_address": control_address,
            "control_token": "private",
            "runtime_revision": "runtime",
            "capability_revision": "capabilities"
        }]});
        std::fs::write(&runtime, descriptor.to_string())?;
        std::fs::set_permissions(&runtime, Permissions::from_mode(0o600))?;
        std::fs::write(&key, "0123456789abcdef0123456789abcdef")?;
        std::fs::set_permissions(&key, Permissions::from_mode(0o600))?;
        Gateway::new(Some(&runtime), Some(&key), std::slice::from_ref(plan))
    }

    fn test_call(generation: Arc<Generation>) -> anyhow::Result<Call> {
        let mut call = Call::new(
            generation,
            &hyper::HeaderMap::new(),
            "POST",
            "/assistants/tools/v1/conversations",
            Duration::from_secs(5),
        )?;
        call.principal = Some(auth::Claims {
            user: "user-one".into(),
            ..Default::default()
        });
        Ok(call)
    }

    #[tokio::test]
    async fn pending_run_guard_and_idle_eviction_prevent_run_table_leaks() -> anyhow::Result<()> {
        let dir = tempfile::tempdir()?;
        let listener = TcpListener::bind("127.0.0.1:0").await?;
        let address = listener.local_addr()?;
        let respond_immediately = Arc::new(std::sync::atomic::AtomicBool::new(false));
        let server = tokio::spawn({
            let respond_immediately = respond_immediately.clone();
            async move {
                loop {
                    let Ok((socket, _)) = listener.accept().await else {
                        break;
                    };
                    let respond_immediately = respond_immediately.clone();
                    tokio::spawn(async move {
                        let service = hyper::service::service_fn(move |request| {
                            let respond_immediately = respond_immediately.clone();
                            async move {
                                if !respond_immediately.load(std::sync::atomic::Ordering::Relaxed) {
                                    std::future::pending::<()>().await;
                                }
                                let body = crate::transport::read(request.into_body(), 16 << 10)
                                    .await
                                    .unwrap();
                                let input: Value = serde_json::from_slice(&body).unwrap();
                                Ok::<_, std::convert::Infallible>(crate::transport::response(
                                    StatusCode::OK,
                                    json!({
                                        "kind": "scenery.assistant.control.response",
                                        "schema_revision": "sha256:8b0cefb8d74e4097e7c058205e55b26675c671e2922b9df8fcd8a86403390e4d",
                                        "type": "conversation.created",
                                        "request_id": input["request_id"],
                                        "assistant_address": "app/assistant/tools",
                                        "runtime_revision": "runtime",
                                        "capability_revision": "capabilities",
                                        "private_session_id": "private-session",
                                        "continuation_token": "private-continuation",
                                        "run_id": input["run_id"]
                                    })
                                    .to_string(),
                                ))
                            }
                        });
                        let _ = hyper::server::conn::http1::Builder::new()
                            .serve_connection(hyper_util::rt::TokioIo::new(socket), service)
                            .await;
                    });
                }
            }
        });

        let plan = test_plan();
        let gateway = test_gateway(dir.path(), &format!("http://{address}"), &plan)?;
        let generation = Arc::new(Generation {
            number: 1,
            identity: crate::process::BuildIdentity {
                contract_revision: "contract".into(),
                implementation_revision: String::new(),
                build_input_digest: String::new(),
                go_target: "worker".into(),
            },
            workers: BTreeMap::new(),
            retired: CancellationToken::new(),
        });
        let call = test_call(generation.clone())?;

        // Dropping an in-flight `start` future must remove the pre-inserted run.
        let timed_out = tokio::time::timeout(
            Duration::from_millis(25),
            gateway.start(&plan, &call, None, "hello".into()),
        )
        .await;
        assert!(timed_out.is_err());
        assert!(gateway.state.lock().unwrap().runs.is_empty());

        // When MAX_RUNS are all active (< 30s), new starts return 503 Unavailable.
        respond_immediately.store(true, std::sync::atomic::Ordering::Relaxed);
        let now = Instant::now();
        {
            let mut state = gateway.state.lock().unwrap();
            for index in 0..MAX_RUNS {
                state.runs.insert(
                    RunKey::new(
                        &plan.address,
                        "user-one",
                        "conv",
                        &format!("run_{index:04}"),
                    ),
                    Run {
                        generation: generation.clone(),
                        principal: call.principal.clone(),
                        traceparent: None,
                        started: now,
                        last_active: now,
                    },
                );
            }
        }
        assert!(matches!(
            gateway.start(&plan, &call, None, "hello".into()).await,
            Err(Error::Unavailable)
        ));

        // An active pending approval shields an idle run from 30s eviction until resolved.
        let oldest = RunKey::new(&plan.address, "user-one", "conv", "run_0000");
        {
            let mut state = gateway.state.lock().unwrap();
            state.runs.get_mut(&oldest).unwrap().last_active = now - Duration::from_secs(35);
            state.approvals.insert(
                (oldest.clone(), "apr_1".into()),
                PendingApproval {
                    token: "tok".into(),
                    capability: "write".into(),
                    input: None,
                    local_tool: false,
                    decision: Decision::Pending,
                },
            );
        }
        assert!(matches!(
            gateway.start(&plan, &call, None, "hello".into()).await,
            Err(Error::Unavailable)
        ));
        {
            let mut state = gateway.state.lock().unwrap();
            state
                .approvals
                .get_mut(&(oldest.clone(), "apr_1".into()))
                .unwrap()
                .decision = Decision::Denied;
        }
        assert!(
            gateway
                .start(&plan, &call, None, "hello".into())
                .await
                .is_ok()
        );
        {
            let state = gateway.state.lock().unwrap();
            assert_eq!(state.runs.len(), MAX_RUNS);
            assert!(!state.runs.contains_key(&oldest));
            assert!(state.approvals.is_empty());
        }

        // Making one entry exceed 900s sweeps it during `start` without needing eviction.
        let expired = RunKey::new(&plan.address, "user-one", "conv", "run_0001");
        gateway
            .state
            .lock()
            .unwrap()
            .runs
            .get_mut(&expired)
            .unwrap()
            .last_active = now - Duration::from_secs(905);
        assert!(
            gateway
                .start(&plan, &call, None, "hello".into())
                .await
                .is_ok()
        );
        assert!(!gateway.state.lock().unwrap().runs.contains_key(&expired));

        // A run with a pending approval must survive the 900s idle sweep and eviction.
        let pending_key = RunKey::new(&plan.address, "user-one", "conv", "run_pending");
        {
            let mut state = gateway.state.lock().unwrap();
            state.runs.insert(
                pending_key.clone(),
                Run {
                    generation: generation.clone(),
                    principal: call.principal.clone(),
                    traceparent: None,
                    started: now,
                    last_active: now - Duration::from_secs(1200),
                },
            );
            state.approvals.insert(
                (pending_key.clone(), "apr_pending".into()),
                PendingApproval {
                    token: "tok".into(),
                    capability: "write".into(),
                    input: None,
                    local_tool: false,
                    decision: Decision::Pending,
                },
            );
        }
        gateway.sweep();
        assert!(gateway.run(&pending_key).is_some());

        // Transitioning to Approved must not keep a stale run past the 900s idle sweep.
        {
            let mut state = gateway.state.lock().unwrap();
            state.runs.get_mut(&pending_key).unwrap().last_active = now - Duration::from_secs(1200);
            state
                .approvals
                .get_mut(&(pending_key.clone(), "apr_pending".into()))
                .unwrap()
                .decision = Decision::Approved;
        }
        gateway.sweep();
        assert!(gateway.run(&pending_key).is_none());

        server.abort();
        Ok(())
    }

    #[tokio::test]
    async fn cancelling_in_flight_approve_revokes_approved_decision() -> anyhow::Result<()> {
        let dir = tempfile::Builder::new()
            .prefix("asst-cancel-")
            .tempdir_in("/tmp")?;
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await?;
        let address = listener.local_addr()?;
        let stalled_server = tokio::spawn(async move {
            let Ok((socket, _)) = listener.accept().await else {
                return;
            };
            let service = hyper::service::service_fn(|_request| async {
                std::future::pending::<()>().await;
                #[allow(unreachable_code)]
                Ok::<_, std::convert::Infallible>(crate::transport::response(
                    StatusCode::OK,
                    String::new(),
                ))
            });
            let _ = hyper::server::conn::http1::Builder::new()
                .serve_connection(hyper_util::rt::TokioIo::new(socket), service)
                .await;
        });

        let plan = test_plan();
        let gateway = test_gateway(dir.path(), &format!("http://{address}"), &plan)?;
        let generation = Arc::new(Generation {
            number: 1,
            identity: crate::process::BuildIdentity {
                contract_revision: "contract".into(),
                implementation_revision: String::new(),
                build_input_digest: String::new(),
                go_target: "worker".into(),
            },
            workers: BTreeMap::new(),
            retired: CancellationToken::new(),
        });
        let owner = token::owner("user-one");
        let conv_digest = token::conversation("conv-1");
        let sealing_key = gateway.key.as_ref().unwrap();
        let conv_tok = sealing_key
            .seal(
                &plan.address,
                &owner,
                &conv_digest,
                token::Conversation {
                    private_session_id: "private-session".into(),
                    continuation_token: "cont".into(),
                },
            )
            .unwrap();
        let apr_tok = sealing_key
            .seal(
                &plan.address,
                &owner,
                &conv_digest,
                token::Approval {
                    run_id: "run_1".into(),
                    approval_id: "apr_1".into(),
                    decision_context: "ctx".into(),
                },
            )
            .unwrap();
        let conv_claims = sealing_key
            .open::<token::Conversation>(&conv_tok, &plan.address, &owner)
            .unwrap();
        let apr_claims = sealing_key
            .open::<token::Approval>(&apr_tok, &plan.address, &owner)
            .unwrap();
        let run_key = RunKey::new(&plan.address, "user-one", &conv_digest, "run_1");
        let now = Instant::now();
        {
            let mut state = gateway.state.lock().unwrap();
            state.runs.insert(
                run_key.clone(),
                Run {
                    generation,
                    principal: None,
                    traceparent: None,
                    started: now,
                    last_active: now,
                },
            );
            state.approvals.insert(
                (run_key.clone(), "apr_1".into()),
                PendingApproval {
                    token: apr_tok,
                    capability: "write".into(),
                    input: Some(json!({})),
                    local_tool: true,
                    decision: Decision::Pending,
                },
            );
        }

        let outcome = tokio::time::timeout(
            Duration::from_millis(40),
            gateway.approve(&plan, &conv_claims, "user-one", apr_claims, true),
        )
        .await;
        assert!(outcome.is_err());
        assert!(
            gateway.state.lock().unwrap().approvals[&(run_key, "apr_1".into())].decision
                == Decision::Denied
        );
        stalled_server.abort();
        Ok(())
    }
}
