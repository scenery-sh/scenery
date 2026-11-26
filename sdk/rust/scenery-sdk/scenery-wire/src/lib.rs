//! Shared types and codecs for Worker ABI v1.

use std::collections::BTreeMap;
use std::path::PathBuf;

use serde::{Deserialize, Serialize};
use serde_json::value::RawValue;

mod scalars;
pub use scalars::*;

pub use serde;
pub use serde_json;

pub type Error = Box<dyn std::error::Error + Send + Sync>;
pub type Result<T> = std::result::Result<T, Error>;

pub const VERSION: u32 = 1;
pub const MAX_METADATA: usize = 32 << 20;
pub const READY_PATH: &str = "/worker/v1/ready";
pub const INVOKE_PATH: &str = "/worker/v1/invoke";
pub const CAPABILITY_PREFIX: &str = "/host/v1/";
pub const CONTRACT_HEADER: &str = "x-worker-contract";
pub const IMPLEMENTATION_HEADER: &str = "x-worker-implementation";
pub const SCOPE_HEADER: &str = "x-worker-scope";
pub const STREAM_SIZE_HEADER: &str = "x-worker-stream-size";
pub const STREAM_MEDIA_TYPE: &str = "application/vnd.worker.stream";
pub const EVENTS_MEDIA_TYPE: &str = "application/vnd.worker.events";

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct RawEvent {
    pub data: Box<RawValue>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub event: Option<String>,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Delivery {
    NotSent,
    Unknown,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Identity {
    pub abi: u32,
    pub service: String,
    pub contract_revision: String,
    pub implementation_revision: String,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Launch {
    pub worker_socket: PathBuf,
    pub host_socket: PathBuf,
    pub token: String,
    pub identity: Identity,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub parent_death_fd: Option<i32>,
}

impl Launch {
    pub fn from_args() -> Result<Self> {
        let mut args = std::env::args_os().skip(1);
        let arg = args.next().ok_or("expected a launch file")?;
        if args.next().is_some() {
            return Err("expected one launch file".into());
        }
        let launch: Self = serde_json::from_slice(&std::fs::read(arg)?)?;
        if launch.identity.abi != VERSION
            || launch.token.len() < 32
            || launch.worker_socket.as_os_str().is_empty()
            || launch.host_socket.as_os_str().is_empty()
        {
            return Err("invalid worker identity or token".into());
        }
        Ok(launch)
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct InvocationContext {
    pub invocation_id: String,
    pub scope: String,
    pub generation: u64,
    pub contract_revision: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub principal: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tenant_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub traceparent: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub deadline: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub locale: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub execution_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub metadata: Option<Box<RawValue>>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Invocation {
    pub service: String,
    pub operation: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub binding: Option<String>,
    pub mode: String,
    pub context: InvocationContext,
    pub input: Box<RawValue>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct BindingFailure {
    pub code: Option<String>,
    pub message: String,
    pub meta: Option<serde_json::Value>,
}

impl std::fmt::Display for BindingFailure {
    fn fmt(&self, output: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        output.write_str(&self.message)
    }
}

impl std::error::Error for BindingFailure {}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct DispatchReceipt {
    pub durable_identity: String,
    pub execution_id: String,
    pub accepted_revision: String,
}

#[derive(Deserialize, Serialize)]
pub struct ConfigSnapshot {
    pub revision: String,
    pub values: BTreeMap<String, Box<RawValue>>,
    pub secrets: BTreeMap<String, String>,
}

#[derive(Serialize)]
pub struct TelemetryRecord<'a> {
    pub kind: &'a str,
    pub name: &'a str,
    pub message: &'a str,
    pub elapsed_ns: u64,
    pub attributes: BTreeMap<String, serde_json::Value>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct InvocationOutcome {
    pub outcome: Box<RawValue>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct BindingCall<T> {
    pub client: T,
    pub input: Box<RawValue>,
}

#[derive(Serialize)]
pub struct BindingCallRef<'a, T: ?Sized> {
    pub client: &'a str,
    pub input: &'a T,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct EventEmitCall<T> {
    pub publisher: T,
    pub payload: Box<RawValue>,
}

#[derive(Serialize)]
pub struct EventEmitCallRef<'a, T: ?Sized> {
    pub publisher: &'a str,
    pub payload: &'a T,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct EventReceipt {
    pub event_id: String,
    pub event: String,
    pub deliveries: u32,
    pub deduplicated: bool,
}

#[derive(Deserialize)]
pub struct BindingReply {
    pub output: Option<Box<RawValue>>,
    pub error: Option<BindingFailure>,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ConnectionTokenRequest<T> {
    pub provider: T,
    pub scopes: Vec<T>,
}

#[derive(Deserialize, Serialize)]
pub struct ConnectionTokenReply {
    pub token: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Unit {}

pub fn encode_outcome(value: &impl Serialize, kind: &str, name: &str) -> Result<Box<RawValue>> {
    #[derive(Serialize)]
    struct Envelope<'a, T> {
        kind: &'a str,
        name: &'a str,
        #[serde(skip_serializing_if = "Option::is_none")]
        value: Option<&'a T>,
        #[serde(skip_serializing_if = "Option::is_none")]
        problem: Option<&'a T>,
    }
    let (value, problem) = match kind {
        "result" => (Some(value), None),
        "error" => (None, Some(value)),
        _ => return Err("invalid outcome kind".into()),
    };
    Ok(serde_json::value::to_raw_value(&Envelope {
        kind,
        name,
        value,
        problem,
    })?)
}

pub fn decode_outcome(raw: &RawValue) -> Result<(String, String, Box<RawValue>)> {
    #[derive(Deserialize)]
    #[serde(deny_unknown_fields)]
    struct Envelope {
        kind: String,
        name: String,
        #[serde(default, deserialize_with = "present")]
        value: Option<Box<RawValue>>,
        #[serde(default, deserialize_with = "present")]
        problem: Option<Box<RawValue>>,
    }
    let envelope: Envelope = serde_json::from_str(raw.get())?;
    let payload = match (envelope.kind.as_str(), envelope.value, envelope.problem) {
        ("result", Some(value), None) | ("error", None, Some(value)) => value,
        _ => return Err("invalid outcome envelope".into()),
    };
    Ok((envelope.kind, envelope.name, payload))
}
