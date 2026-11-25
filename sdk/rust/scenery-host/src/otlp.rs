use std::collections::BTreeMap;
use std::time::Duration;

use serde_json::{Value, json};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};
use tokio::sync::mpsc;
use url::Url;

type Queue = mpsc::Sender<(String, Value)>;

pub(crate) type Endpoints = BTreeMap<String, String>;

pub(crate) fn start(endpoints: Endpoints) -> anyhow::Result<Option<Queue>> {
    if endpoints.is_empty() {
        return Ok(None);
    }
    let endpoints = endpoints
        .into_iter()
        .map(|(signal, raw)| {
            let url = Url::parse(&raw)?;
            anyhow::ensure!(
                url.scheme() == "http"
                    && matches!(url.host_str(), Some("127.0.0.1" | "[::1]" | "localhost"))
                    && url.username().is_empty()
                    && url.password().is_none(),
                "invalid local telemetry endpoint"
            );
            Ok((signal, url))
        })
        .collect::<anyhow::Result<BTreeMap<_, _>>>()?;
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(2))
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let (send, mut receive) = mpsc::channel::<(String, Value)>(512);
    tokio::spawn(async move {
        let mut events = BTreeMap::<(String, String), Vec<Value>>::new();
        let mut batch = Vec::with_capacity(64);
        loop {
            if receive.recv_many(&mut batch, 64).await == 0 {
                break;
            }
            let mut payloads = BTreeMap::<&str, Vec<u8>>::new();
            for (kind, record) in batch.drain(..) {
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
            for (signal, body) in payloads {
                if let Some(endpoint) = endpoints.get(signal) {
                    let sent = client
                        .post(endpoint.clone())
                        .header("content-type", "application/x-protobuf")
                        .body(body)
                        .send()
                        .await
                        .and_then(reqwest::Response::error_for_status);
                    if let Err(error) = sent {
                        eprintln!("could not export {signal}: {}", error.without_url());
                    }
                }
            }
        }
    });
    Ok(Some(send))
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
}
