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
}
