use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::abi::Outcome;
use crate::host::{Call, Fault};
use crate::plan::Service;
use crate::values;

const MAX_REPLAY_BYTES: usize = 256 << 10;

#[derive(Clone, Debug, Serialize, Deserialize)]
pub(crate) struct ReplayInvocation {
    pub module: String,
    pub runtime: String,
    pub service: String,
    pub operation: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub binding: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub target: Option<String>,
    pub mode: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub http_method: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub http_path: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub http_body: Option<Value>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub headers: BTreeMap<String, Vec<String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub principal: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tenant_id: Option<String>,
    pub contract_revision: String,
    pub generation: u64,
    pub trace_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub input: Option<Value>,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub input_truncated: bool,
}

impl ReplayInvocation {
    pub(crate) fn capture(
        call: &Call,
        service: &Service,
        operation: &str,
        binding: Option<&str>,
        mode: &str,
        input: &Value,
    ) -> Self {
        let mut http_method = None;
        let mut http_path = None;
        let mut headers = BTreeMap::new();
        let is_top_level_ingress = call.stack.len() <= 1;
        let http_body = if is_top_level_ingress {
            call.raw_body
                .as_deref()
                .filter(|raw| raw.len() <= MAX_REPLAY_BYTES)
                .and_then(|raw| values::parse(raw.as_bytes()).ok())
        } else {
            None
        };
        if is_top_level_ingress
            && (!call.has_http_body || http_body.is_some())
            && let Some(req) = call.request.as_deref()
        {
            http_method = req["method"].as_str().map(str::to_owned);
            http_path = call
                .raw_uri
                .clone()
                .or_else(|| req["path"].as_str().map(str::to_owned));
            if let Some(raw_headers) = req["headers"].as_object() {
                for (name, values) in raw_headers {
                    if sensitive_header(name) {
                        continue;
                    }
                    if let Some(arr) = values.as_array() {
                        let items: Vec<String> = arr
                            .iter()
                            .filter_map(Value::as_str)
                            .map(str::to_owned)
                            .collect();
                        if !items.is_empty() {
                            headers.insert(name.clone(), items);
                        }
                    }
                }
            }
        }
        let encoded_input = values::encode(input);
        let (captured_input, input_truncated) = if encoded_input.len() <= MAX_REPLAY_BYTES {
            (Some(input.clone()), false)
        } else {
            (None, true)
        };
        let target = if binding.is_none() {
            call.schedule_target.clone().or_else(|| {
                (matches!(call.method.as_str(), "DURABLE" | "SCHEDULE") && !call.path.is_empty())
                    .then(|| call.path.clone())
            })
        } else {
            None
        };
        Self {
            module: service.module.clone(),
            runtime: service.runtime.clone(),
            service: service.address.clone(),
            operation: operation.to_owned(),
            binding: binding.map(str::to_owned),
            target,
            mode: mode.to_owned(),
            http_method,
            http_path,
            http_body,
            headers,
            principal: call
                .principal_id()
                .filter(|id| !id.is_empty())
                .map(str::to_owned),
            tenant_id: call
                .principal
                .as_ref()
                .map(|p| p.tenant.as_str())
                .filter(|t| !t.is_empty())
                .map(str::to_owned),
            contract_revision: call.generation.identity.contract_revision.clone(),
            generation: call.generation.number,
            trace_id: call.trace_id.clone(),
            input: captured_input,
            input_truncated,
        }
    }
}

pub(crate) fn attach(
    result: Result<Outcome, Fault>,
    call: &Call,
    service: &Service,
    operation: &str,
    binding: Option<&str>,
    mode: &str,
    input: &Value,
) -> Result<Outcome, Fault> {
    result.map_err(|mut fault| {
        if fault.cause.is_some() && fault.replay.is_none() {
            fault.replay = Some(Box::new(ReplayInvocation::capture(
                call, service, operation, binding, mode, input,
            )));
        }
        fault
    })
}

fn sensitive_header(name: &str) -> bool {
    let lower = name.to_ascii_lowercase();
    matches!(
        lower.as_str(),
        "authorization" | "proxy-authorization" | "cookie" | "set-cookie" | "host"
    ) || lower.contains("secret")
        || lower.contains("token")
        || lower.contains("api-key")
        || lower.contains("apikey")
        || lower.starts_with("x-scenery-")
        || lower.starts_with("x-app-invocation-")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn scrubs_sensitive_headers_and_retains_safe_ones() {
        assert!(sensitive_header("Authorization"));
        assert!(sensitive_header("Cookie"));
        assert!(sensitive_header("X-API-Key"));
        assert!(sensitive_header("X-Webhook-Secret"));
        assert!(sensitive_header("X-Auth-Token"));
        assert!(!sensitive_header("content-type"));
        assert!(!sensitive_header("x-idempotency-id"));
    }
}
