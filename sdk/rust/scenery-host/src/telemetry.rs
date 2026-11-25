use std::collections::{HashMap, VecDeque};
use std::env;
use std::os::unix::fs::{DirBuilderExt, OpenOptionsExt};
use std::path::PathBuf;
use std::sync::Mutex;
use std::time::Duration;

use anyhow::{Result, ensure};
use bytes::Bytes;
use http_body_util::Full;
use hyper::Request;
use hyper_util::rt::TokioIo;
use serde::Deserialize;
use serde_json::{Map, Value, json};
use time::OffsetDateTime;
use tokio::net::TcpStream;
use tokio::sync::mpsc;
use url::Url;

use crate::host::{Call, Scope, stamp};
use crate::plan::Service;
use crate::process::random_hex;

pub(crate) struct Sink {
    app: String,
    session: String,
    sender: Option<mpsc::Sender<Value>>,
    report_dir: PathBuf,
    recent: Option<Mutex<Recent>>,
}

#[derive(Default)]
struct Recent {
    sequence: u64,
    bytes: usize,
    reports: VecDeque<Stored>,
}

struct Stored {
    kind: String,
    record: Value,
    bytes: usize,
}

impl Sink {
    pub(crate) fn new(app: &str, development: bool) -> Result<Self> {
        let session = env::var("SCENERY_SESSION_ID").unwrap_or_default();
        let mut sender = None;
        if let (Ok(endpoint), Ok(token)) = (
            env::var("SCENERY_DEV_REPORT_URL"),
            env::var("SCENERY_DEV_REPORT_TOKEN"),
        ) {
            let endpoint = Url::parse(&endpoint)?;
            ensure!(
                endpoint.scheme() == "http"
                    && endpoint.host_str() == Some("127.0.0.1")
                    && endpoint.path() == "/report"
                    && endpoint.port().is_some(),
                "invalid development report receiver"
            );
            let (tx, rx) = mpsc::channel(512);
            tokio::spawn(send_reports(rx, endpoint, token));
            sender = Some(tx);
        }
        let report_dir = env::var_os("SCENERY_AGENT_HOME")
            .map(PathBuf::from)
            .or_else(|| env::var_os("HOME").map(|home| PathBuf::from(home).join(".scenery")))
            .unwrap_or_else(|| PathBuf::from("."))
            .join("reports");
        Ok(Self {
            app: app.into(),
            session,
            sender,
            report_dir,
            recent: development.then(|| Mutex::new(Recent::default())),
        })
    }

    fn retain(&self, kind: &str, mut value: Value) {
        if let Some(recent) = &self.recent
            && matches!(kind, "log" | "trace-summary" | "trace-event" | "metric")
        {
            let mut recent = recent.lock().expect("telemetry mutex");
            recent.sequence += 1;
            if kind == "log" {
                value["event_id"] = json!(recent.sequence);
            }
            let bytes = value.to_string().len();
            if bytes <= 64 << 10 {
                recent.bytes += bytes;
                recent.reports.push_back(Stored {
                    kind: kind.into(),
                    record: value,
                    bytes,
                });
                while recent.reports.len() > 2048 || recent.bytes > 2 << 20 {
                    if let Some(old) = recent.reports.pop_front() {
                        recent.bytes -= old.bytes;
                    }
                }
            }
        }
    }

    fn report(&self, kind: &str, key: &str, value: Value) {
        self.retain(kind, value.clone());
        if let Some(sender) = &self.sender {
            let envelope = json!({
                "type": kind, "app_id": self.app, "session_id": self.session,
                "reporter_pid": std::process::id(), key: value,
            });
            let _ = sender.try_send(envelope);
        }
    }

    pub(crate) fn ingest(&self, input: &[u8]) -> Result<()> {
        let batch = crate::values::parse(input)?;
        let reports = batch["reports"]
            .as_array()
            .ok_or_else(|| anyhow::anyhow!("invalid report batch"))?;
        ensure!(reports.len() <= 64, "too many reports");
        for report in reports {
            if report["app_id"] != self.app
                || report["session_id"] != self.session
                || report["reporter_pid"] == std::process::id()
            {
                continue;
            }
            let (kind, record) = match report["type"].as_str() {
                Some("log") => ("log", &report["log_event"]),
                Some("trace-summary") => ("trace-summary", &report["trace_summary"]),
                Some("trace-event") => ("trace-event", &report["trace_event"]),
                _ => continue,
            };
            if record.is_object() {
                self.retain(kind, record.clone());
            }
        }
        Ok(())
    }

    pub(crate) fn snapshot(&self, id: Option<&str>) -> Value {
        let mut logs = Vec::new();
        let mut spans = Vec::new();
        let mut metrics = Vec::new();
        let mut events = HashMap::<String, Vec<Value>>::new();
        if let Some(recent) = &self.recent {
            let recent = recent.lock().expect("telemetry mutex");
            for item in &recent.reports {
                let record = &item.record;
                if id.is_some_and(|id| record["trace_id"] != id) {
                    continue;
                }
                match item.kind.as_str() {
                    "log" => logs.push(record.clone()),
                    "trace-summary" => spans.push(record.clone()),
                    "metric" => metrics.push(record.clone()),
                    "trace-event" => {
                        if let Some(span) = record["span_id"].as_str() {
                            events.entry(span.to_owned()).or_default().push(json!({
                                "time": record["event_time"], "data": record["event"],
                            }));
                        }
                    }
                    _ => {}
                }
            }
        }
        for span in &mut spans {
            let events = span["span_id"]
                .as_str()
                .and_then(|id| events.remove(id))
                .unwrap_or_default();
            span["events"] = json!(events);
        }
        json!({"logs": logs, "spans": spans, "metrics": metrics})
    }

    pub(crate) fn failure(&self, cause: &str) -> Option<String> {
        let token = format!("rpt_{}", random_hex::<20>().ok()?);
        let scrub =
            regex::Regex::new(r"(?i)(postgres(?:ql)?|https?)://[^\s]+").expect("valid URL pattern");
        let cause = scrub.replace_all(cause, "[redacted URL]");
        let timestamp = stamp(OffsetDateTime::now_utc());
        let failure = json!({"report_token": token, "code": "SCN9000", "cause": cause, "timestamp": timestamp});
        if self.sender.is_some() {
            self.report("internal-failure", "internal_failure", failure);
        } else {
            let record = json!({
                "kind": "scenery.failure.report", "report_token": token, "code": "SCN9000",
                "cause": cause, "recorded_at": timestamp, "command": "runtime", "arguments": [],
                "origin": "runtime", "reporter_pid": std::process::id(), "app_id": self.app,
            });
            use std::io::Write;
            let saved = (|| -> Result<()> {
                std::fs::DirBuilder::new()
                    .mode(0o700)
                    .recursive(true)
                    .create(&self.report_dir)?;
                let mut file = std::fs::OpenOptions::new()
                    .create_new(true)
                    .write(true)
                    .mode(0o600)
                    .open(self.report_dir.join(format!("{token}.json")))?;
                file.write_all(&serde_json::to_vec(&record)?)?;
                Ok(())
            })();
            if let Err(error) = saved {
                eprintln!("could not write internal failure report: {error}");
            }
        }
        Some(token)
    }

    pub(crate) fn span(
        &self,
        call: &Call,
        service: &Service,
        operation: &str,
        duration: Duration,
        code: &str,
    ) {
        self.span_record(call, service, operation, duration, code, Map::new());
    }

    fn span_record(
        &self,
        call: &Call,
        service: &Service,
        operation: &str,
        duration: Duration,
        code: &str,
        attrs: Map<String, Value>,
    ) {
        if self.sender.is_none() && self.recent.is_none() {
            return;
        }
        let at = OffsetDateTime::now_utc();
        let parent = (!call.parent_span_id.is_empty()).then_some(&call.parent_span_id);
        if !attrs.is_empty() {
            self.report(
                "trace-event",
                "trace_event",
                json!({
                    "trace_id": call.trace_id, "span_id": call.span_id, "event_id": 1,
                    "event_time": stamp(at), "event": attrs, "session_id": self.session,
                }),
            );
        }
        let kind = if !call.stack.is_empty() {
            "WORKER"
        } else {
            match call.method.as_str() {
                "DURABLE" => "DURABLE",
                "SCHEDULE" => "SCHEDULE",
                _ => "REQUEST",
            }
        };
        self.report("trace-summary", "trace_summary", json!({
            "app_id": self.app, "session_id": self.session, "type": kind,
            "trace_id": call.trace_id, "span_id": call.span_id, "parent_span_id": parent,
            "invocation_id": call.invocation_id, "execution_id": call.execution_id,
            "module": service.module, "runtime": service.runtime, "generation": call.generation.number,
            "build_input_digest": call.generation.identity.build_input_digest,
            "service_name": service.name, "endpoint_name": operation,
            "is_root": kind != "WORKER", "started_at": stamp(at - duration),
            "duration_nanos": duration.as_nanos() as u64, "is_error": !code.is_empty(), "error_code": code,
        }));
    }

    pub(crate) fn started(&self, call: &Call, service: &Service, operation: &str) {
        if self.recent.is_none() && self.sender.is_none() {
            return;
        }
        self.report("log", "log_event", json!({
            "app_id": self.app, "session_id": self.session, "timestamp": stamp(call.started),
            "trace_id": call.trace_id, "span_id": call.span_id, "parent_span_id": call.parent_span_id,
            "invocation_id": call.invocation_id, "execution_id": call.execution_id, "service": service.name,
            "runtime": service.runtime, "module": service.module, "operation": operation,
            "generation": call.generation.number, "level": "info", "message": "starting request",
        }));
    }

    pub(crate) fn worker(&self, scope: &Scope, service: &Service, input: &[u8]) -> Result<usize> {
        let records: Vec<Record> = serde_json::from_slice(input)?;
        ensure!((1..=64).contains(&records.len()), "invalid telemetry batch");
        for record in &records {
            ensure!(
                matches!(record.kind.as_str(), "span" | "log" | "metric")
                    && !record.name.trim().is_empty()
                    && record.name.len() <= 128
                    && record.message.len() <= 2048
                    && record.attributes.len() <= 32
                    && record.elapsed_ns <= 3_600_000_000_000,
                "invalid telemetry record"
            );
        }
        let metric_name = |name: &str| {
            name.bytes().enumerate().all(|(index, byte)| {
                byte.is_ascii_alphabetic()
                    || b"_:".contains(&byte)
                    || index > 0 && byte.is_ascii_digit()
            })
        };
        for record in records.iter().filter(|record| record.kind == "metric") {
            ensure!(
                metric_name(&record.name)
                    && record
                        .attributes
                        .get("value")
                        .is_none_or(|value| value.as_f64().is_some_and(f64::is_finite))
                    && record
                        .attributes
                        .get("unit")
                        .is_none_or(|unit| unit.as_str().is_some_and(|value| value.len() <= 32)),
                "invalid metric sample"
            );
        }
        let count = records.len();
        for record in records {
            let call = &scope.call;
            match record.kind.as_str() {
                "span" => {
                    let mut child = call.clone();
                    child.parent_span_id.clone_from(&call.span_id);
                    child.stack.push(scope.operation.clone());
                    child.span_id = random_hex::<8>()?;
                    let code = record.attributes.get("error_code").and_then(Value::as_str).unwrap_or_default().to_owned();
                    self.span_record(&child, service, &record.name, Duration::from_nanos(record.elapsed_ns), &code, record.attributes);
                }
                "metric" => self.report("metric", "metric_event", json!({
                    "app_id": self.app, "session_id": self.session, "timestamp": stamp(OffsetDateTime::now_utc()),
                    "trace_id": call.trace_id, "span_id": call.span_id, "parent_span_id": call.parent_span_id,
                    "invocation_id": call.invocation_id, "execution_id": call.execution_id,
                    "service": service.name, "module": service.module, "runtime": service.runtime,
                    "operation": scope.operation, "generation": call.generation.number, "name": record.name,
                    "error_code": record.attributes.get("error_code").and_then(Value::as_str).unwrap_or_default(),
                    "duration_ms": record.elapsed_ns as f64 / 1e6,
                    "value": record.attributes.get("value").and_then(Value::as_f64).unwrap_or(record.elapsed_ns as f64 / 1e9),
                    "unit": record.attributes.get("unit").and_then(Value::as_str).unwrap_or(
                        if record.attributes.contains_key("value") { "" } else { "s" }),
                })),
                "log" => self.report("log", "log_event", json!({
                    "app_id": self.app, "session_id": self.session, "timestamp": stamp(OffsetDateTime::now_utc()),
                    "trace_id": call.trace_id, "span_id": call.span_id, "parent_span_id": call.parent_span_id,
                    "invocation_id": call.invocation_id, "execution_id": call.execution_id,
                    "service": service.name, "module": service.module, "runtime": service.runtime,
                    "operation": scope.operation, "generation": call.generation.number, "level": "info",
                    "message": record.message, "attrs": {"name": record.name, "kind": record.kind, "values": record.attributes},
                })),
                _ => unreachable!(),
            }
        }
        Ok(count)
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Record {
    kind: String,
    name: String,
    #[serde(default)]
    message: String,
    #[serde(default)]
    elapsed_ns: u64,
    #[serde(default)]
    attributes: Map<String, Value>,
}

async fn send_reports(mut receiver: mpsc::Receiver<Value>, endpoint: Url, token: String) {
    let address = format!("127.0.0.1:{}", endpoint.port().expect("validated endpoint"));
    while let Some(first) = receiver.recv().await {
        let mut batch = vec![first];
        while batch.len() < 32 {
            let Ok(next) = receiver.try_recv() else { break };
            batch.push(next);
        }
        let send = async {
            let stream = TcpStream::connect(&address).await?;
            let (mut sender, connection) =
                hyper::client::conn::http1::handshake(TokioIo::new(stream)).await?;
            tokio::spawn(connection);
            let data = serde_json::to_vec(&json!({"reports": batch}))?;
            let request = Request::post(endpoint.path())
                .header("host", &address)
                .header("content-type", "application/json")
                .header("authorization", format!("Bearer {token}"))
                .header("connection", "close")
                .body(Full::<Bytes>::new(data.into()))?;
            sender.send_request(request).await?;
            Ok::<_, anyhow::Error>(())
        };
        let _ = tokio::time::timeout(Duration::from_secs(2), send).await;
    }
}
