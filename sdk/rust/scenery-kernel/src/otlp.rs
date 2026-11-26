use std::collections::BTreeMap;
use std::sync::Mutex;
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use hyper::header::{HeaderMap, HeaderName, HeaderValue};
use percent_encoding::percent_decode_str;
use serde_json::{Value, json};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};
use tokio::sync::mpsc;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;
use url::Url;

use crate::config::Lookup;

type Queue = mpsc::Sender<(String, Value)>;

/// A development worktree's Victoria endpoints by signal name.
pub(crate) type Endpoints = BTreeMap<String, String>;

/// The OTLP signals this host exports and the variable overriding each.
const SIGNALS: [(&str, &str); 3] = [
    ("traces", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
    ("logs", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"),
    ("metrics", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"),
];
const ENDPOINT: &str = "OTEL_EXPORTER_OTLP_ENDPOINT";
const HEADERS: &str = "OTEL_EXPORTER_OTLP_HEADERS";
/// Records waiting for export. A full queue drops new records instead of
/// slowing the requests that produce them.
const QUEUE: usize = 2048;

/// Where signals go and the headers every export request carries.
#[derive(Default)]
pub(crate) struct Export {
    endpoints: BTreeMap<&'static str, Url>,
    headers: HeaderMap,
    timeout: Duration,
}

impl Export {
    /// A development worktree's Victoria endpoints, which stay on loopback.
    pub(crate) fn local(endpoints: Endpoints) -> Result<Self> {
        let mut export = Self {
            timeout: Duration::from_secs(2),
            ..Self::default()
        };
        for (signal, raw) in endpoints {
            let signal = SIGNALS
                .iter()
                .map(|(name, _)| *name)
                .find(|name| *name == signal)
                .context("invalid local telemetry signal")?;
            let url = Url::parse(&raw)?;
            ensure!(
                url.scheme() == "http"
                    && matches!(url.host_str(), Some("127.0.0.1" | "[::1]" | "localhost"))
                    && url.username().is_empty()
                    && url.password().is_none(),
                "invalid local telemetry endpoint"
            );
            export.endpoints.insert(signal, url);
        }
        Ok(export)
    }

    /// The standard OTLP/HTTP exporter configuration of a production host:
    /// `OTEL_EXPORTER_OTLP_ENDPOINT` with `/v1/<signal>` appended, a
    /// per-signal `OTEL_EXPORTER_OTLP_<SIGNAL>_ENDPOINT` used as given, and
    /// the comma-separated, URL-encoded `key=value` pairs of
    /// `OTEL_EXPORTER_OTLP_HEADERS`. Without an endpoint nothing is exported.
    pub(crate) fn from_env(lookup: Lookup) -> Result<Self> {
        let read = |name: &str| -> Result<Option<String>> {
            lookup(name)
                .map(|value| {
                    value
                        .into_string()
                        .map_err(|_| anyhow::anyhow!("{name} is not valid UTF-8"))
                })
                .transpose()
                .map(|value| value.filter(|value| !value.trim().is_empty()))
        };
        let mut export = Self {
            timeout: Duration::from_secs(10),
            ..Self::default()
        };
        let base = read(ENDPOINT)?;
        for (signal, variable) in SIGNALS {
            let url = match (read(variable)?, &base) {
                (Some(endpoint), _) => remote_endpoint(variable, endpoint.trim(), None)?,
                (None, Some(base)) => remote_endpoint(ENDPOINT, base.trim(), Some(signal))?,
                (None, None) => continue,
            };
            export.endpoints.insert(signal, url);
        }
        if let Some(headers) = read(HEADERS)? {
            export.headers = parse_headers(&headers)?;
        }
        Ok(export)
    }

    pub(crate) fn is_empty(&self) -> bool {
        self.endpoints.is_empty()
    }
}

/// Validates an export URL. Credentials belong in `OTEL_EXPORTER_OTLP_HEADERS`,
/// never in a URL that a log line could print.
fn remote_endpoint(variable: &str, raw: &str, signal: Option<&str>) -> Result<Url> {
    let mut url = Url::parse(raw).with_context(|| format!("{variable} is not a valid URL"))?;
    ensure!(
        matches!(url.scheme(), "http" | "https") && url.host_str().is_some(),
        "{variable} must be an http or https URL"
    );
    ensure!(
        url.username().is_empty() && url.password().is_none(),
        "{variable} must not contain credentials; use {HEADERS}"
    );
    ensure!(
        url.fragment().is_none(),
        "{variable} must not contain a fragment"
    );
    if let Some(signal) = signal {
        let path = format!("{}/v1/{signal}", url.path().trim_end_matches('/'));
        url.set_path(&path);
    }
    Ok(url)
}

/// Parses `OTEL_EXPORTER_OTLP_HEADERS`. Errors name the offending entry by
/// position only, so a malformed credential never reaches a log.
fn parse_headers(raw: &str) -> Result<HeaderMap> {
    let mut headers = HeaderMap::new();
    for (index, entry) in raw.split(',').enumerate() {
        if entry.trim().is_empty() {
            continue;
        }
        let position = index + 1;
        let (name, value) = entry
            .split_once('=')
            .with_context(|| format!("{HEADERS} entry {position} is not key=value"))?;
        let decode = |text: &str| {
            percent_decode_str(text.trim())
                .decode_utf8()
                .map(|text| text.into_owned())
        };
        let name = decode(name)
            .ok()
            .and_then(|name| HeaderName::from_bytes(name.as_bytes()).ok())
            .with_context(|| format!("{HEADERS} entry {position} has an invalid name"))?;
        let mut value = decode(value)
            .ok()
            .and_then(|value| HeaderValue::from_str(&value).ok())
            .with_context(|| format!("{HEADERS} entry {position} has an invalid value"))?;
        value.set_sensitive(true);
        headers.append(name, value);
    }
    Ok(headers)
}

/// Ends the exporter: it sends what is queued and stops. Records reported
/// afterwards are dropped.
pub(crate) struct Flush {
    closing: CancellationToken,
    task: Mutex<Option<JoinHandle<()>>>,
}

impl Flush {
    /// Waits at most `limit` for the queued records to be delivered, so a
    /// stopping host never hangs on an unreachable collector.
    pub(crate) async fn finish(&self, limit: Duration) {
        self.closing.cancel();
        let Some(mut task) = self.task.lock().expect("exporter task").take() else {
            return;
        };
        if tokio::time::timeout(limit, &mut task).await.is_err() {
            task.abort();
            eprintln!("telemetry export did not finish within {limit:?}; dropped the rest");
        }
    }
}

/// Starts delivering records in the background. Each batch is encoded as
/// OTLP protobuf per signal and posted to that signal's endpoint.
pub(crate) fn start(export: Export) -> Result<Option<(Queue, Flush)>> {
    if export.is_empty() {
        return Ok(None);
    }
    let Export {
        endpoints,
        headers,
        timeout,
    } = export;
    let client = reqwest::Client::builder()
        .timeout(timeout)
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let (send, mut receive) = mpsc::channel::<(String, Value)>(QUEUE);
    let closing = CancellationToken::new();
    let stopping = closing.clone();
    let task = tokio::spawn(async move {
        let mut events = BTreeMap::<(String, String), Vec<Value>>::new();
        let mut batch = Vec::with_capacity(256);
        loop {
            let last = tokio::select! {
                count = receive.recv_many(&mut batch, 256) => {
                    if count == 0 {
                        break;
                    }
                    false
                }
                () = stopping.cancelled() => true,
            };
            if last {
                while let Ok(record) = receive.try_recv() {
                    batch.push(record);
                }
            }
            let payloads = encode(&mut events, batch.drain(..));
            let deliveries = payloads.into_iter().filter_map(|(signal, body)| {
                let endpoint = endpoints.get(signal)?.clone();
                let client = client.clone();
                let headers = headers.clone();
                Some(async move {
                    let sent = client
                        .post(endpoint)
                        .headers(headers)
                        .header("content-type", "application/x-protobuf")
                        .body(body)
                        .send()
                        .await
                        .and_then(reqwest::Response::error_for_status);
                    if let Err(error) = sent {
                        eprintln!("could not export {signal}: {}", error.without_url());
                    }
                })
            });
            futures_util::future::join_all(deliveries).await;
            if last {
                break;
            }
        }
    });
    Ok(Some((
        send,
        Flush {
            closing,
            task: Mutex::new(Some(task)),
        },
    )))
}

/// Encodes one batch into a protobuf payload per signal. A span's events
/// wait in `events` until the summary that completes the span arrives.
fn encode(
    events: &mut BTreeMap<(String, String), Vec<Value>>,
    batch: impl Iterator<Item = (String, Value)>,
) -> BTreeMap<&'static str, Vec<u8>> {
    let mut payloads = BTreeMap::<&str, Vec<u8>>::new();
    for (kind, record) in batch {
        let key = || {
            (
                text(&record, "trace_id").into(),
                text(&record, "span_id").into(),
            )
        };
        match kind.as_str() {
            "trace-event" => {
                if events.len() >= 512 && !events.contains_key(&key()) {
                    events.pop_first();
                }
                let span = events.entry(key()).or_default();
                if span.len() < 64 {
                    span.push(record);
                }
            }
            "trace-summary" => {
                let events = events.remove(&key()).unwrap_or_default();
                payloads
                    .entry("traces")
                    .or_default()
                    .extend(trace(&record, &events).0);
                let duration = record["duration_nanos"].as_u64().unwrap_or_default();
                let end = timestamp(&record, "started_at").saturating_add(duration);
                payloads.entry("metrics").or_default().extend(
                    metric(
                        &record,
                        end,
                        duration as f64 / 1e9,
                        "scenery_request_duration_seconds",
                        "s",
                        true,
                    )
                    .0,
                );
            }
            "log" => payloads.entry("logs").or_default().extend(log(&record).0),
            "metric" => {
                let value = record["value"].as_f64().unwrap_or_default();
                payloads.entry("metrics").or_default().extend(
                    metric(
                        &record,
                        timestamp(&record, "timestamp"),
                        value,
                        text(&record, "name"),
                        text(&record, "unit"),
                        false,
                    )
                    .0,
                );
            }
            _ => {}
        }
    }
    payloads
}

fn trace(record: &Value, events: &[Value]) -> Message {
    let mut span = Message::default();
    span.identifier(1, record, "trace_id", 16);
    span.identifier(2, record, "span_id", 8);
    span.identifier(4, record, "parent_span_id", 8);
    let name = text(record, "service_name");
    let endpoint = text(record, "endpoint_name");
    span.string(
        5,
        &match (name, endpoint) {
            ("", "") => "scenery.request".into(),
            (name, "") | ("", name) => name.into(),
            _ => format!("{name}.{endpoint}"),
        },
    );
    span.number(
        6,
        match text(record, "type") {
            "HTTP" | "DB" | "STORAGE" => 3,
            "PUBLISH" => 4,
            "EVENT" | "DURABLE" => 5,
            _ => 2,
        },
    );
    let start = timestamp(record, "started_at");
    span.fixed(7, start);
    span.fixed(
        8,
        start.saturating_add(record["duration_nanos"].as_u64().unwrap_or_default()),
    );
    span.attributes(
        9,
        record,
        &[
            ("app_id", "scenery.application_id"),
            ("session_id", "scenery.session_id"),
            ("app_root_hash", "scenery.app_root_hash"),
            ("type", "scenery.trace.type"),
            ("is_root", "scenery.is_root"),
            ("is_error", "scenery.is_error"),
            ("service_name", "scenery.service"),
            ("endpoint_name", "scenery.endpoint"),
            ("build_input_digest", "scenery.build_input_digest"),
        ],
    );
    correlation(&mut span, 9, record);
    for event in events {
        let mut payload = Message::default();
        payload.fixed(1, timestamp(event, "event_time"));
        payload.string(2, "scenery.event");
        payload.attribute(3, "scenery.event", &event["event"]);
        span.bytes(11, &payload.0);
    }
    if record["is_error"] == true {
        let mut status = Message::default();
        status.number(3, 2);
        span.bytes(15, &status.0);
    }
    wrap(record, span)
}

fn log(record: &Value) -> Message {
    let mut log = Message::default();
    let level = text(record, "level");
    log.fixed(1, timestamp(record, "timestamp"));
    log.number(
        2,
        match level {
            "trace" => 1,
            "debug" => 5,
            "info" => 9,
            "warn" | "warning" => 13,
            "error" => 17,
            "fatal" => 21,
            _ => 0,
        },
    );
    log.string(3, &level.to_uppercase());
    log.bytes(5, &scalar(&record["message"]).0);
    log.attributes(
        6,
        record,
        &[
            ("app_id", "scenery.application_id"),
            ("session_id", "scenery.session_id"),
            ("app_root_hash", "scenery.app_root_hash"),
        ],
    );
    correlation(&mut log, 6, record);
    if let Some(attributes) = record["attrs"].as_object() {
        for (key, value) in attributes {
            log.attribute(6, &format!("scenery.log.{key}"), value);
        }
    }
    log.identifier(9, record, "trace_id", 16);
    log.identifier(10, record, "span_id", 8);
    wrap(record, log)
}

fn metric(
    record: &Value,
    time: u64,
    value: f64,
    name: &str,
    unit: &str,
    request_duration: bool,
) -> Message {
    let mut point = Message::default();
    point.fixed(3, time);
    point.fixed(4, value.to_bits());
    point.attributes(
        7,
        record,
        &[
            ("app_id", "scenery_app"),
            ("session_id", "scenery_session_id"),
            ("app_root_hash", "scenery_app_root_hash"),
        ],
    );
    if request_duration {
        point.attributes(
            7,
            record,
            &[
                ("type", "scenery_trace_type"),
                ("is_root", "scenery_is_root"),
                ("is_error", "scenery_is_error"),
                ("service_name", "scenery_service"),
                ("endpoint_name", "scenery_endpoint"),
            ],
        );
    } else {
        correlation(&mut point, 7, record);
        point.attributes(
            7,
            record,
            &[
                ("trace_id", "trace_id"),
                ("span_id", "span_id"),
                ("duration_ms", "duration_ms"),
            ],
        );
    }
    let mut gauge = Message::default();
    gauge.bytes(1, &point.0);
    let mut metric = Message::default();
    metric.string(1, name);
    metric.string(3, unit);
    metric.bytes(5, &gauge.0);
    wrap(record, metric)
}

fn correlation(message: &mut Message, field: u64, record: &Value) {
    for key in [
        "invocation_id",
        "execution_id",
        "parent_span_id",
        "module",
        "runtime",
        "service",
        "operation",
        "error_code",
    ] {
        message.attribute(field, key, &record[key]);
    }
    if let Some(generation) = record["generation"].as_u64() {
        message.attribute(field, "generation", &json!(generation.to_string()));
    }
}

fn wrap(record: &Value, payload: Message) -> Message {
    let app = &record["app_id"];
    let mut scope = Message::default();
    let mut identity = Message::default();
    identity.string(1, "scenery");
    scope.bytes(1, &identity.0);
    scope.bytes(2, &payload.0);
    let mut resource = Message::default();
    resource.attribute(1, "service.name", app);
    resource.attribute(1, "telemetry.sdk.name", &json!("scenery"));
    resource.attribute(1, "telemetry.sdk.language", &json!("rust"));
    let mut group = Message::default();
    group.bytes(1, &resource.0);
    group.bytes(2, &scope.0);
    let mut envelope = Message::default();
    envelope.bytes(1, &group.0);
    envelope
}

fn timestamp(record: &Value, field: &str) -> u64 {
    OffsetDateTime::parse(text(record, field), &Rfc3339)
        .ok()
        .and_then(|at| at.unix_timestamp_nanos().try_into().ok())
        .unwrap_or_default()
}

fn text<'a>(record: &'a Value, field: &str) -> &'a str {
    record[field].as_str().unwrap_or_default()
}

fn scalar(value: &Value) -> Message {
    let mut output = Message::default();
    match value {
        Value::Bool(value) => output.number(2, u64::from(*value)),
        Value::Number(value) if value.is_i64() => {
            output.number(3, value.as_i64().unwrap_or_default() as u64)
        }
        Value::Number(value) => output.fixed(4, value.as_f64().unwrap_or_default().to_bits()),
        Value::String(value) => output.string(1, value),
        _ => output.string(1, &value.to_string()),
    }
    output
}

#[derive(Default)]
struct Message(Vec<u8>);
impl Message {
    fn varint(&mut self, mut value: u64) {
        while value >= 0x80 {
            self.0.push(value as u8 | 0x80);
            value >>= 7;
        }
        self.0.push(value as u8);
    }
    fn number(&mut self, field: u64, value: u64) {
        self.varint(field << 3);
        self.varint(value);
    }
    fn bytes(&mut self, field: u64, value: &[u8]) {
        self.varint(field << 3 | 2);
        self.varint(value.len() as u64);
        self.0.extend_from_slice(value);
    }
    fn string(&mut self, field: u64, value: &str) {
        self.bytes(field, value.as_bytes());
    }
    fn fixed(&mut self, field: u64, value: u64) {
        self.varint(field << 3 | 1);
        self.0.extend_from_slice(&value.to_le_bytes());
    }
    fn identifier(&mut self, field: u64, record: &Value, key: &str, length: usize) {
        let encoded = text(record, key).as_bytes();
        if encoded.len() == length * 2 {
            let decoded: Option<Vec<_>> = encoded
                .as_chunks::<2>()
                .0
                .iter()
                .map(|pair| {
                    std::str::from_utf8(pair)
                        .ok()
                        .and_then(|s| u8::from_str_radix(s, 16).ok())
                })
                .collect();
            if let Some(decoded) = decoded.filter(|bytes| bytes.iter().any(|&byte| byte != 0)) {
                self.bytes(field, &decoded);
            }
        }
    }
    fn attributes(&mut self, field: u64, record: &Value, keys: &[(&str, &str)]) {
        for &(key, name) in keys {
            self.attribute(field, name, &record[key]);
        }
    }
    fn attribute(&mut self, field: u64, key: &str, value: &Value) {
        if value.is_null() || value.as_str() == Some("") {
            return;
        }
        let mut attr = Message::default();
        attr.string(1, key);
        attr.bytes(2, &scalar(value).0);
        self.bytes(field, &attr.0);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn wraps_signal_in_an_otlp_resource_scope() {
        let data = json!({
            "app_id": "fixture",
            "trace_id": "00112233445566778899aabbccddeeff",
            "span_id": "aabbccddeeff0011",
            "timestamp": "2026-11-25T00:00:00Z",
            "level": "info",
            "message": "fixture message",
        });
        let payload = log(&data).0;
        assert_eq!(payload[0], 0x0a);
        assert!(payload.windows(15).any(|part| part == b"fixture message"));
        assert!(payload.windows(18).any(|part| part
            == [
                0x4a, 0x10, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb,
                0xcc, 0xdd, 0xee, 0xff
            ]));
    }

    type Seen = std::sync::Arc<Mutex<Vec<(String, HeaderMap, bytes::Bytes)>>>;

    /// A collector that records every request it receives and answers 200.
    async fn collector() -> (String, Seen) {
        use http_body_util::{BodyExt, Full};
        use hyper::service::service_fn;
        use hyper_util::rt::TokioIo;

        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = format!("http://{}", listener.local_addr().unwrap());
        let seen = Seen::default();
        let requests = seen.clone();
        tokio::spawn(async move {
            while let Ok((socket, _)) = listener.accept().await {
                let requests = requests.clone();
                tokio::spawn(async move {
                    let service =
                        service_fn(move |request: hyper::Request<hyper::body::Incoming>| {
                            let requests = requests.clone();
                            async move {
                                let (parts, body) = request.into_parts();
                                let body = body.collect().await?.to_bytes();
                                requests.lock().unwrap().push((
                                    parts.uri.path().into(),
                                    parts.headers,
                                    body,
                                ));
                                Ok::<_, hyper::Error>(hyper::Response::new(Full::new(
                                    bytes::Bytes::new(),
                                )))
                            }
                        });
                    let _ = hyper::server::conn::http1::Builder::new()
                        .serve_connection(TokioIo::new(socket), service)
                        .await;
                });
            }
        });
        (address, seen)
    }

    fn lookup(pairs: &[(&str, &str)]) -> impl Fn(&str) -> Option<std::ffi::OsString> + use<> {
        let pairs: BTreeMap<String, String> = pairs
            .iter()
            .map(|(key, value)| ((*key).into(), (*value).into()))
            .collect();
        move |name| pairs.get(name).map(Into::into)
    }

    fn contains(haystack: &[u8], needle: &[u8]) -> bool {
        haystack.windows(needle.len()).any(|part| part == needle)
    }

    #[tokio::test]
    async fn production_export_delivers_each_signal_with_headers_and_flushes_at_shutdown() {
        let (address, seen) = collector().await;
        let environment = lookup(&[
            (ENDPOINT, &format!("{address}/otlp/")),
            (
                "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
                &format!("{address}/custom/traces"),
            ),
            (HEADERS, "Authorization=Bearer%20s3cret, X-Tenant = acme"),
        ]);
        let (queue, flush) = start(Export::from_env(&environment).unwrap())
            .unwrap()
            .unwrap();
        let span = json!({
            "app_id": "shop", "trace_id": "00112233445566778899aabbccddeeff",
            "span_id": "aabbccddeeff0011", "service_name": "orders", "endpoint_name": "checkout",
            "type": "HTTP", "started_at": "2026-11-25T00:00:00Z", "duration_nanos": 1_500_000,
        });
        queue.try_send(("trace-summary".into(), span)).unwrap();
        let entry = json!({"app_id": "shop", "timestamp": "2026-11-25T00:00:01Z",
            "level": "warn", "message": "stock is low"});
        queue.try_send(("log".into(), entry)).unwrap();
        let gauge = json!({"app_id": "shop", "timestamp": "2026-11-25T00:00:02Z",
            "name": "orders_open", "unit": "1", "value": 7});
        queue.try_send(("metric".into(), gauge)).unwrap();
        // Shutdown delivers what is queued without any further wait.
        flush.finish(Duration::from_secs(5)).await;

        let requests = seen.lock().unwrap().clone();
        let body = |path: &str| -> Vec<u8> {
            requests
                .iter()
                .filter(|(seen, _, _)| seen == path)
                .flat_map(|(_, _, body)| body.to_vec())
                .collect()
        };
        assert!(contains(&body("/custom/traces"), b"orders.checkout"));
        assert!(contains(&body("/otlp/v1/logs"), b"stock is low"));
        let metrics = body("/otlp/v1/metrics");
        assert!(contains(&metrics, b"orders_open"));
        assert!(contains(&metrics, b"scenery_request_duration_seconds"));
        assert!(!requests.is_empty());
        for (path, headers, _) in &requests {
            assert_eq!(headers["authorization"], "Bearer s3cret", "{path}");
            assert_eq!(headers["x-tenant"], "acme", "{path}");
            assert_eq!(headers["content-type"], "application/x-protobuf", "{path}");
        }
        // Nothing is delivered once the exporter has finished.
        assert!(queue.try_send(("log".into(), json!({}))).is_err());
    }

    #[tokio::test]
    async fn shutdown_flush_is_bounded_by_an_unresponsive_collector() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = format!("http://{}", listener.local_addr().unwrap());
        let held = tokio::spawn(async move {
            let mut sockets = Vec::new();
            while let Ok((socket, _)) = listener.accept().await {
                sockets.push(socket);
            }
        });
        let environment = lookup(&[(ENDPOINT, &address)]);
        let (queue, flush) = start(Export::from_env(&environment).unwrap())
            .unwrap()
            .unwrap();
        queue
            .try_send(("log".into(), json!({"message": "lost"})))
            .unwrap();
        let started = std::time::Instant::now();
        flush.finish(Duration::from_millis(200)).await;
        assert!(started.elapsed() < Duration::from_secs(2));
        held.abort();
    }

    #[test]
    fn standard_variables_select_endpoints_and_reject_unsafe_values() {
        assert!(Export::from_env(&lookup(&[])).unwrap().is_empty());
        let export = Export::from_env(&lookup(&[
            (ENDPOINT, "https://collector.example.com/prefix/"),
            (
                "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
                "http://logs.internal:4318/ingest",
            ),
        ]))
        .unwrap();
        let endpoint = |signal| export.endpoints[signal].as_str().to_owned();
        assert_eq!(
            endpoint("traces"),
            "https://collector.example.com/prefix/v1/traces"
        );
        assert_eq!(
            endpoint("metrics"),
            "https://collector.example.com/prefix/v1/metrics"
        );
        assert_eq!(endpoint("logs"), "http://logs.internal:4318/ingest");
        let only_traces = Export::from_env(&lookup(&[(
            "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
            "https://collector.example.com",
        )]))
        .unwrap();
        assert_eq!(
            only_traces.endpoints.keys().collect::<Vec<_>>(),
            [&"traces"]
        );

        let headers = parse_headers("a=b%20c, x-y = z ,,api-key=k%3Dv").unwrap();
        assert_eq!(headers["a"], "b c");
        assert_eq!(headers["x-y"], "z");
        assert_eq!(headers["api-key"], "k=v");
        assert!(headers["a"].is_sensitive());

        for (variable, value, expected) in [
            (
                ENDPOINT,
                "https://user:hunter2@collector.example.com",
                "must not contain credentials",
            ),
            (ENDPOINT, "ftp://collector.example.com", "http or https"),
            (ENDPOINT, "collector:4318", "http or https"),
            (
                "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
                "not a url",
                "not a valid URL",
            ),
            (HEADERS, "Authorization hunter2", "entry 1 is not key=value"),
            (
                HEADERS,
                "ok=1,Authorization=Bearer%0Ahunter2",
                "entry 2 has an invalid value",
            ),
            (HEADERS, "bad name=hunter2", "entry 1 has an invalid name"),
        ] {
            let mut pairs = vec![(variable, value)];
            if variable == HEADERS {
                pairs.push((ENDPOINT, "https://collector.example.com"));
            }
            let error = format!("{:#}", Export::from_env(&lookup(&pairs)).err().unwrap());
            assert!(error.contains(expected), "{value}: {error}");
            assert!(!error.contains("hunter2"), "{error}");
        }
    }

    #[test]
    fn development_endpoints_stay_on_loopback() {
        let local = |url: &str| Export::local(Endpoints::from([("traces".into(), url.into())]));
        assert!(local("http://127.0.0.1:9428/insert/opentelemetry/v1/traces").is_ok());
        assert!(local("http://10.0.0.5:4318/v1/traces").is_err());
        assert!(local("https://127.0.0.1:4318/v1/traces").is_err());
        assert!(
            Export::local(Endpoints::from([(
                "profiles".into(),
                "http://127.0.0.1:1".into()
            )]))
            .is_err()
        );
    }
}
