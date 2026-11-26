use std::collections::{HashMap, VecDeque};
use std::env;
use std::os::unix::fs::{DirBuilderExt, OpenOptionsExt};
use std::path::PathBuf;
use std::sync::{LazyLock, Mutex};
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
use crate::plan::{EndpointFilter, Observability, ReportIdentity, Service};
use crate::process::random_hex;

const MAX_FAILURE_REPORTS: usize = 200;

/// Connection strings and links never reach a stored failure cause.
static URLS: LazyLock<regex::Regex> = LazyLock::new(|| {
    regex::Regex::new(r"(?i)(postgres(?:ql)?|https?)://[^\s]+").expect("valid URL pattern")
});

pub(crate) struct Sink {
    app: String,
    development: bool,
    logs: Filter,
    tracing: Filter,
    session: String,
    /// root_hash names the development worktree the exported signals belong to.
    root_hash: String,
    sender: Option<mpsc::Sender<Value>>,
    victoria: Option<mpsc::Sender<(String, Value)>>,
    exporter: Option<crate::otlp::Flush>,
    report_dir: PathBuf,
    reports: ReportIdentity,
    working_directory: String,
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
    pub(crate) fn new(
        app: &str,
        development: bool,
        filters: &Observability,
        export: crate::otlp::Export,
        reports: &ReportIdentity,
    ) -> Result<Self> {
        let session = env::var("SCENERY_SESSION_ID").unwrap_or_default();
        let root_hash = env::var("SCENERY_APP_ROOT_HASH").unwrap_or_default();
        let mut sender = None;
        if !development
            && let (Ok(endpoint), Ok(token)) = (
                env::var("SCENERY_DEV_REPORT_URL"),
                env::var("SCENERY_DEV_REPORT_TOKEN"),
            )
        {
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
        let (victoria, exporter) = crate::otlp::start(export)?.unzip();
        Ok(Self {
            app: app.into(),
            development,
            logs: Filter::new(&filters.logs),
            tracing: Filter::new(&filters.tracing),
            session,
            root_hash,
            sender,
            victoria,
            exporter,
            report_dir,
            reports: reports.clone(),
            working_directory: env::current_dir()
                .map(|path| path.to_string_lossy().into_owned())
                .unwrap_or_default(),
            recent: development.then(|| Mutex::new(Recent::default())),
        })
    }

    /// Whether any consumer takes request records: the development report
    /// receiver, the development history, or an OTLP exporter.
    fn records(&self) -> bool {
        self.sender.is_some() || self.recent.is_some() || self.victoria.is_some()
    }

    /// Delivers the records still queued for export, waiting at most `limit`.
    pub(crate) async fn flush(&self, limit: Duration) {
        if let Some(exporter) = &self.exporter {
            exporter.finish(limit).await;
        }
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
        self.export(kind, &value);
        if kind == "log" && !self.development && self.sender.is_none() {
            emit_production_stderr(&value);
        }
        if let Some(sender) = &self.sender {
            let envelope = json!({
                "type": kind, "app_id": self.app, "session_id": self.session,
                "reporter_pid": std::process::id(), key: value,
            });
            let _ = sender.try_send(envelope);
        }
    }

    fn export(&self, kind: &str, value: &Value) {
        if let Some(sender) = &self.victoria {
            let mut value = value.clone();
            if !self.root_hash.is_empty()
                && value["app_root_hash"].as_str().is_none_or(str::is_empty)
            {
                value["app_root_hash"] = json!(self.root_hash);
            }
            let _ = sender.try_send((kind.into(), value));
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
                Some("internal-failure") => {
                    let _ = self.save_failure(
                        &report["internal_failure"],
                        report["reporter_pid"].as_u64().unwrap_or_default(),
                    );
                    continue;
                }
                _ => continue,
            };
            if record.is_object() {
                // A worker states its application and session once per
                // report; exported records carry them like the host's own.
                let mut record = record.clone();
                for key in ["app_id", "session_id"] {
                    if record[key].as_str().is_none_or(str::is_empty) {
                        record[key] = report[key].clone();
                    }
                }
                self.retain(kind, record.clone());
                self.export(kind, &record);
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
        let cause = URLS.replace_all(cause, "[redacted URL]");
        let timestamp = stamp(OffsetDateTime::now_utc());
        let failure = json!({"report_token": token, "code": "SCN9000", "cause": cause, "timestamp": timestamp});
        if self.development {
            let _ = self.save_failure(&failure, std::process::id().into());
        } else if let Some(sender) = &self.sender {
            let envelope = json!({
                "type": "internal-failure",
                "app_id": self.app,
                "session_id": self.session,
                "reporter_pid": std::process::id(),
                "internal_failure": failure,
            });
            let _ = sender.try_send(envelope);
        } else {
            let record = json!({
                "app_id": self.app,
                "session_id": self.session,
                "timestamp": timestamp,
                "level": "error",
                "message": format!("internal failure ({token}): {cause}"),
                "attrs": {
                    "code": "SCN9000",
                    "report_token": token,
                    "origin": "runtime",
                },
            });
            self.export("log", &record);
            emit_production_stderr(&record);
        }
        Some(token)
    }

    fn save_failure(&self, failure: &Value, pid: u64) -> Result<()> {
        use std::io::Write;
        let token = failure["report_token"].as_str().unwrap_or_default();
        ensure!(
            token
                .strip_prefix("rpt_")
                .is_some_and(|tail| (16..=80).contains(&tail.len())
                    && tail
                        .bytes()
                        .all(|ch| ch.is_ascii_lowercase() || ch.is_ascii_digit())),
            "invalid report token"
        );
        let raw = failure["cause"].as_str().unwrap_or_default();
        let cause = URLS.replace_all(raw, "[redacted URL]");
        // The CLI reads this record as `scenery.failure.report`, so it
        // carries the identity and every field that schema requires.
        let record = json!({
            "kind": "scenery.failure.report", "schema_revision": self.reports.schema_revision,
            "report_token": token, "code": "SCN9000", "cause": cause,
            "recorded_at": failure["timestamp"], "command": "runtime", "arguments": [],
            "working_directory": self.working_directory,
            "producer_version": self.reports.producer_version,
            "origin": "runtime", "reporter_pid": pid, "app_id": self.app, "session_id": self.session,
        });
        std::fs::DirBuilder::new()
            .mode(0o700)
            .recursive(true)
            .create(&self.report_dir)?;
        let final_path = self.report_dir.join(format!("{token}.json"));
        let temporary_path = self.report_dir.join(format!("{token}.json.tmp"));
        let write_result = (|| -> Result<()> {
            let mut file = std::fs::OpenOptions::new()
                .create_new(true)
                .write(true)
                .mode(0o600)
                .open(&temporary_path)?;
            file.write_all(&serde_json::to_vec(&record)?)?;
            file.sync_all()?;
            std::fs::rename(&temporary_path, &final_path)?;
            Ok(())
        })();
        if let Err(error) = write_result {
            let _ = std::fs::remove_file(&temporary_path);
            return Err(error);
        }
        let _ = prune_failure_reports(&self.report_dir);
        Ok(())
    }

    pub(crate) fn span(
        &self,
        call: &Call,
        service: &Service,
        operation: &str,
        duration: Duration,
        code: &str,
    ) {
        if self.tracing.allows(&service.name, operation, &call.path) {
            self.span_record(call, service, operation, duration, code, Map::new());
        }
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
        if !self.records() {
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
        if !self.logs_enabled(call, service, operation) || !self.records() {
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

    pub(crate) fn logs_enabled(&self, call: &Call, service: &Service, endpoint: &str) -> bool {
        self.logs.allows(&service.name, endpoint, &call.path)
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
                "span" if self.tracing.allows(&service.name, &scope.endpoint, &call.path) => {
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
                "log" if self.logs_enabled(call, service, &scope.endpoint) => self.report("log", "log_event", json!({
                    "app_id": self.app, "session_id": self.session, "timestamp": stamp(OffsetDateTime::now_utc()),
                    "trace_id": call.trace_id, "span_id": call.span_id, "parent_span_id": call.parent_span_id,
                    "invocation_id": call.invocation_id, "execution_id": call.execution_id,
                    "service": service.name, "module": service.module, "runtime": service.runtime,
                    "operation": scope.operation, "generation": call.generation.number, "level": "info",
                    "message": record.message, "attrs": {"name": record.name, "kind": record.kind, "values": record.attributes},
                })),
                _ => {},
            }
        }
        Ok(count)
    }
}

struct Filter {
    included: Vec<(String, Option<glob::Pattern>)>,
    excluded: Vec<(String, Option<glob::Pattern>)>,
}

impl Filter {
    fn new(config: &EndpointFilter) -> Self {
        let parse = |patterns: &[String]| {
            patterns
                .iter()
                .map(|pattern| {
                    let pattern = pattern.trim();
                    (pattern.to_owned(), glob::Pattern::new(pattern).ok())
                })
                .collect()
        };
        Self {
            included: parse(&config.include_endpoints),
            excluded: parse(&config.exclude_endpoints),
        }
    }

    fn allows(&self, service: &str, operation: &str, path: &str) -> bool {
        if self.included.is_empty() && self.excluded.is_empty() {
            return true;
        }
        // Go registers generated HTTP bindings under their exported name.
        let name = operation.rsplit('/').next().unwrap_or(operation);
        let endpoint: String = name
            .split(['_', '-'])
            .filter(|part| !part.is_empty())
            .flat_map(|part| {
                let mut characters = part.chars();
                characters
                    .next()
                    .into_iter()
                    .flat_map(char::to_uppercase)
                    .chain(characters)
            })
            .collect();
        let qualified = format!("{service}.{endpoint}");
        let candidates = [qualified.as_str(), service, &endpoint, path];
        let matches = |patterns: &[(String, Option<glob::Pattern>)]| {
            patterns.iter().any(|(text, pattern)| {
                candidates
                    .iter()
                    .filter(|candidate| !candidate.is_empty())
                    .any(|candidate| {
                        text == candidate
                            || pattern.as_ref().is_some_and(|pattern| {
                                pattern.matches_with(
                                    candidate,
                                    glob::MatchOptions {
                                        require_literal_separator: true,
                                        ..glob::MatchOptions::new()
                                    },
                                )
                            })
                    })
            })
        };
        (self.included.is_empty() || matches(&self.included)) && !matches(&self.excluded)
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

fn emit_production_stderr(value: &Value) {
    let mut line = crate::values::encode(value);
    line.push('\n');
    let _ = std::io::Write::write_all(&mut std::io::stderr().lock(), line.as_bytes());
}

fn prune_failure_reports(directory: &std::path::Path) -> Result<()> {
    let mut retained = Vec::new();
    for entry in std::fs::read_dir(directory)? {
        let Ok(entry) = entry else { continue };
        let path = entry.path();
        if path.extension().and_then(|ext| ext.to_str()) != Some("json") {
            continue;
        }
        let Ok(metadata) = std::fs::symlink_metadata(&path) else {
            continue;
        };
        if !metadata.file_type().is_file() {
            continue;
        }
        let modified = metadata
            .modified()
            .unwrap_or(std::time::SystemTime::UNIX_EPOCH);
        retained.push((modified, path));
    }
    retained.sort_by(|a, b| b.0.cmp(&a.0).then_with(|| b.1.cmp(&a.1)));
    for (_, path) in retained.into_iter().skip(MAX_FAILURE_REPORTS) {
        let _ = std::fs::remove_file(path);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_sink(
        directory: &std::path::Path,
        development: bool,
        sender: Option<mpsc::Sender<Value>>,
    ) -> Sink {
        Sink {
            app: "inbox".into(),
            development,
            logs: Filter::new(&EndpointFilter::default()),
            tracing: Filter::new(&EndpointFilter::default()),
            session: "session".into(),
            root_hash: "root".into(),
            sender,
            victoria: None,
            exporter: None,
            report_dir: directory.join("reports"),
            reports: ReportIdentity {
                schema_revision: "sha256:fixture".into(),
                producer_version: "v1.2.3".into(),
            },
            working_directory: "/srv/app".into(),
            recent: None,
        }
    }

    #[test]
    fn filters_use_the_go_binding_name_and_request_path() {
        let filter = Filter::new(&EndpointFilter {
            include_endpoints: vec!["tenants.GetConfig".into(), "/api/items/*".into()],
            exclude_endpoints: vec!["jobs.*".into(), "/api/items/private".into()],
        });
        assert!(filter.allows("tenants", "app/binding/get_config", "/tenants/config"));
        assert!(filter.allows("items", "app/binding/get_item", "/api/items/public"));
        assert!(!filter.allows("items", "app/binding/get_item", "/api/items/private"));
        assert!(!filter.allows("jobs", "app/binding/get_item", "/api/items/public"));
        assert!(!filter.allows("items", "app/binding/get_item", "/api/items/public/nested"));
    }

    #[test]
    fn failure_reports_match_the_cli_report_schema() -> Result<()> {
        let directory = tempfile::tempdir()?;
        let sink = test_sink(directory.path(), true, None);
        let token = sink
            .failure("connect postgres://user:secret@db/app failed")
            .unwrap();
        let path = directory
            .path()
            .join("reports")
            .join(format!("{token}.json"));
        let report: Value = serde_json::from_slice(&std::fs::read(path)?)?;
        let schema: Value = serde_json::from_str(include_str!(
            "../../../../docs/schemas/scenery.failure.report.schema.json"
        ))?;
        // Every required field is present and nothing undeclared is added:
        // the schema forbids additional properties.
        for field in schema["required"].as_array().unwrap() {
            let field = field.as_str().unwrap();
            assert!(report.get(field).is_some(), "report lacks {field}");
        }
        let declared = schema["properties"].as_object().unwrap();
        for field in report.as_object().unwrap().keys() {
            assert!(declared.contains_key(field), "report adds {field}");
        }
        assert_eq!(report["kind"], schema["properties"]["kind"]["const"]);
        assert_eq!(report["schema_revision"], "sha256:fixture");
        assert_eq!(report["producer_version"], "v1.2.3");
        assert_eq!(report["working_directory"], "/srv/app");
        assert_eq!(report["origin"], "runtime");
        assert_eq!(report["cause"], "connect [redacted URL] failed");
        assert!(report["arguments"].as_array().is_some_and(Vec::is_empty));
        assert!(report["reporter_pid"].is_u64() && report["recorded_at"].is_string());
        Ok(())
    }

    #[test]
    fn failure_reports_avoid_disk_in_production_forward_to_dev_sender_and_prune_in_dev()
    -> Result<()> {
        let prod_dir = tempfile::tempdir()?;
        let prod = test_sink(prod_dir.path(), false, None);
        let prod_token = prod
            .failure("postgres://user:pw@db/app unreachable")
            .unwrap();
        assert!(prod_token.starts_with("rpt_"));
        assert!(!prod_dir.path().join("reports").exists());

        let forwarded_dir = tempfile::tempdir()?;
        let (tx, mut rx) = mpsc::channel(4);
        let child = test_sink(forwarded_dir.path(), false, Some(tx));
        let forwarded_token = child.failure("boom").unwrap();
        assert!(!forwarded_dir.path().join("reports").exists());
        let envelope = rx.try_recv()?;
        assert_eq!(envelope["type"], "internal-failure");
        assert_eq!(
            envelope["internal_failure"]["report_token"],
            forwarded_token
        );

        let dev_dir = tempfile::tempdir()?;
        let reports_dir = dev_dir.path().join("reports");
        std::fs::create_dir_all(&reports_dir)?;
        for index in 0..205u64 {
            let file_path = reports_dir.join(format!("rpt_{index:016}.json"));
            let file = std::fs::File::create(&file_path)?;
            let timestamp = std::time::SystemTime::UNIX_EPOCH + Duration::from_secs(index + 1);
            file.set_modified(timestamp)?;
        }
        let dev = test_sink(dev_dir.path(), true, None);
        let dev_token = dev.failure("dev panic").unwrap();
        let entries: Vec<String> = std::fs::read_dir(&reports_dir)?
            .filter_map(|entry| entry.ok())
            .map(|entry| entry.file_name().to_string_lossy().into_owned())
            .collect();
        assert_eq!(entries.len(), MAX_FAILURE_REPORTS);
        assert!(entries.contains(&format!("{dev_token}.json")));
        assert!(!entries.contains(&"rpt_0000000000000000.json".to_owned()));
        assert!(!entries.contains(&"rpt_0000000000000005.json".to_owned()));
        assert!(entries.contains(&"rpt_0000000000000006.json".to_owned()));
        Ok(())
    }

    #[test]
    fn worker_spans_are_exported_under_their_reported_application() -> Result<()> {
        let (sender, mut receiver) = mpsc::channel(4);
        let mut sink = test_sink(std::path::Path::new(""), false, None);
        sink.victoria = Some(sender);
        sink.report_dir = PathBuf::new();
        sink.working_directory = String::new();
        sink.ingest(
            json!({"reports":[{"type":"trace-summary","app_id":"inbox","session_id":"session",
                "trace_summary":{"trace_id":"t","span_id":"s","type":"HTTP"}}]})
            .to_string()
            .as_bytes(),
        )?;
        let (kind, record) = receiver.try_recv()?;
        assert_eq!(kind, "trace-summary");
        assert_eq!(
            (record["app_id"].as_str(), record["session_id"].as_str()),
            (Some("inbox"), Some("session"))
        );
        Ok(())
    }
}
