use std::collections::BTreeMap;
use std::path::{Component, Path, PathBuf};

use anyhow::{Context, Result, ensure};
use serde::Deserialize;
use serde_json::{Value, json, value::to_raw_value};

use crate::process::{BuildIdentity, Program};
use crate::values::Types;

#[derive(Deserialize)]
pub(crate) struct Bundle {
    pub version: u32,
    pub contract_revision: String,
    pub host: HostPlan,
    pub types: Types,
    pub programs: Vec<Artifact>,
    pub frontend: Option<Frontend>,
    pub auth: Option<Auth>,
    #[serde(default)]
    pub host_config_keys: Vec<String>,
    pub reports: ReportIdentity,
}

/// The identity the CLI expects on the failure reports the Host records.
#[derive(Clone, Deserialize)]
pub(crate) struct ReportIdentity {
    pub schema_revision: String,
    pub producer_version: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct HostPlan {
    pub name: String,
    pub workers: Vec<Service>,
    #[serde(default)]
    pub observability: Observability,
    #[serde(rename = "MCPServers", default)]
    pub mcp_servers: BTreeMap<String, crate::mcp::Manifest>,
    #[serde(default)]
    pub assistants: Vec<crate::assistant::Plan>,
}

#[derive(Default, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Observability {
    pub logs: EndpointFilter,
    pub tracing: EndpointFilter,
}

#[derive(Default, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct EndpointFilter {
    #[serde(default, deserialize_with = "nullable")]
    pub include_endpoints: Vec<String>,
    #[serde(default, deserialize_with = "nullable")]
    pub exclude_endpoints: Vec<String>,
}

#[derive(Deserialize)]
pub(crate) struct Frontend {
    pub root: PathBuf,
    pub reserved: Vec<String>,
    pub upstream: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Auth {
    #[serde(rename = "bootstrap_sql", default)]
    pub bootstrap_sql: String,
    pub enabled: bool,
    #[serde(rename = "GoogleOAuth")]
    pub google_o_auth: GoogleOAuth,
    pub dev_bootstrap: Bootstrap,
    #[serde(deserialize_with = "nullable")]
    pub connections: Vec<crate::connections::Provider>,
}

#[derive(Deserialize)]
pub(crate) struct GoogleOAuth {
    #[serde(rename = "Enabled")]
    pub enabled: bool,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Bootstrap {
    pub enabled: bool,
    #[serde(default)]
    pub default_user_email: String,
    #[serde(rename = "DefaultTenantID")]
    pub default_tenant_id: String,
}

#[derive(Deserialize)]
pub(crate) struct Artifact {
    pub service: String,
    pub process: String,
    pub binary: PathBuf,
    pub digest: String,
    pub runtime: String,
    pub identity: BuildIdentity,
    #[serde(default)]
    pub databases: BTreeMap<String, String>,
    #[serde(default)]
    pub config_keys: Vec<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Service {
    pub address: String,
    pub module: String,
    pub name: String,
    pub process: String,
    pub runtime: String,
    pub operations: BTreeMap<String, Operation>,
    pub clients: BTreeMap<String, Client>,
    #[serde(deserialize_with = "nullable")]
    pub bindings: Vec<Binding>,
    pub durable: BTreeMap<String, Durable>,
    #[serde(deserialize_with = "nullable")]
    pub schedules: Vec<Schedule>,
    pub stores: BTreeMap<String, String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Operation {
    pub input: String,
    pub outcomes: BTreeMap<String, String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Client {
    pub binding: String,
    pub enqueue: bool,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Binding {
    pub address: String,
    pub operation: String,
    pub visibility: String,
    pub delivery: String,
    pub execution: String,
    #[serde(rename = "HTTP")]
    pub http: Option<Route>,
    #[serde(rename = "MCP", default)]
    pub mcp: bool,

    pub policy: Policy,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Durable {
    pub operation: String,
    pub registration: Registration,
    #[serde(deserialize_with = "nullable")]
    pub dedupe: Vec<Key>,
    #[serde(deserialize_with = "nullable")]
    pub concurrency: Vec<Key>,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Registration {
    pub address: String,
    pub engine_address: String,
    pub external_name: String,
    pub service: String,
    pub revision: u32,
    pub default_timeout: u64,
    pub default_lease: u64,
    pub max_attempts: i32,
    pub retry_initial: i64,
    pub retry_max: i64,
    pub retry_backoff: f32,
    pub retry_jitter: f32,
    pub success_retention: i64,
    pub failure_retention: i64,
    pub max_concurrency: i32,
    pub deduplication_retention: i64,
    pub deduplication_conflict: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Schedule {
    pub address: String,
    pub operation: String,
    pub execution: String,
    pub authorization: String,
    pub pipeline: String,
    pub policy: Policy,
    pub input: Value,
    pub timeout: u64,
    pub every: u64,
    pub overlap: String,
    pub catchup_maximum_age: u64,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Key {
    pub name: String,
    #[serde(rename = "Type")]
    pub ty: String,
}

#[derive(Clone, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Policy {
    pub binding_address: String,
    pub authorization_strategy: String,
    #[serde(deserialize_with = "nullable")]
    pub authorization_rules: Vec<Rule>,
    pub total_invocation_timeout_nanos: u64,
    #[serde(default)]
    pub read_timeout_nanos: u64,
    #[serde(default)]
    pub write_timeout_nanos: u64,
    #[serde(default)]
    pub idle_timeout_nanos: u64,
    pub max_request_header_bytes: usize,
    #[serde(rename = "CORS")]
    pub cors: String,
    #[serde(deserialize_with = "nullable")]
    pub allowed_origins: Vec<String>,
    #[serde(deserialize_with = "nullable")]
    pub transport_statuses: BTreeMap<String, u16>,
}

impl Policy {
    pub(crate) fn read_timeout(&self) -> std::time::Duration {
        if self.read_timeout_nanos > 0 {
            std::time::Duration::from_nanos(self.read_timeout_nanos)
        } else {
            crate::http_timeout::DEFAULT_HTTP_READ_TIMEOUT
        }
    }

    pub(crate) fn write_timeout(&self) -> std::time::Duration {
        if self.write_timeout_nanos > 0 {
            std::time::Duration::from_nanos(self.write_timeout_nanos)
        } else {
            crate::http_timeout::DEFAULT_HTTP_WRITE_TIMEOUT
        }
    }
}

impl HostPlan {
    pub(crate) fn http_timeouts(&self) -> crate::http_timeout::HttpTimeouts {
        use std::time::Duration;
        let mut max_read = Duration::ZERO;
        let mut max_write = Duration::ZERO;
        let mut max_idle = Duration::ZERO;
        let policies = self
            .workers
            .iter()
            .flat_map(|service| service.bindings.iter())
            .filter(|binding| binding.http.is_some())
            .map(|binding| &binding.policy)
            .chain(self.assistants.iter().map(|assistant| &assistant.policy));
        for policy in policies {
            if policy.read_timeout_nanos > 0 {
                max_read = max_read.max(Duration::from_nanos(policy.read_timeout_nanos));
            }
            if policy.write_timeout_nanos > 0 {
                max_write = max_write.max(Duration::from_nanos(policy.write_timeout_nanos));
            }
            if policy.idle_timeout_nanos > 0 {
                max_idle = max_idle.max(Duration::from_nanos(policy.idle_timeout_nanos));
            }
        }
        let read = if max_read.is_zero() {
            crate::http_timeout::DEFAULT_HTTP_READ_TIMEOUT
        } else {
            max_read.min(crate::http_timeout::DEFAULT_HTTP_READ_TIMEOUT)
        };
        let write = if max_write.is_zero() {
            crate::http_timeout::DEFAULT_HTTP_WRITE_TIMEOUT
        } else {
            max_write.min(crate::http_timeout::DEFAULT_HTTP_WRITE_TIMEOUT)
        };
        let idle = if max_idle.is_zero() {
            crate::http_timeout::DEFAULT_HTTP_IDLE_TIMEOUT
        } else {
            max_idle
        };
        crate::http_timeout::HttpTimeouts {
            header_read: read.min(crate::http_timeout::MAX_HEADER_READ_TIMEOUT),
            read,
            write,
            idle,
        }
    }
}

#[derive(Clone, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Rule {
    pub effect: String,
    pub expression: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Route {
    pub path: String,
    pub method: String,
    pub access: String,
    pub path_tail: Option<PathTail>,
    pub request: RequestPlan,
    pub responses: BTreeMap<String, ResponsePlan>,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct PathTail {
    pub minimum_segments: usize,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct RequestPlan {
    #[serde(deserialize_with = "nullable")]
    pub mappings: Vec<Mapping>,
    #[serde(deserialize_with = "nullable")]
    pub context_mappings: Vec<ContextMapping>,
    pub body: Option<BodyPlan>,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Mapping {
    pub source: String,
    pub name: String,
    pub target: String,
    #[serde(rename = "Type")]
    pub ty: String,
    pub encoding: String,
    pub optional: bool,
    #[serde(deserialize_with = "nullable")]
    pub enum_values: Vec<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct ContextMapping {
    pub source: String,
    pub target: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct BodyPlan {
    pub codec: String,
    pub target: String,
    #[serde(deserialize_with = "nullable")]
    pub include: Vec<String>,
    #[serde(deserialize_with = "nullable")]
    pub except: Vec<String>,
    #[serde(deserialize_with = "nullable")]
    pub accepted_media_types: Vec<String>,
    #[serde(deserialize_with = "nullable")]
    pub supported_content_encoding: Vec<String>,
    pub max_compressed_bytes: usize,
    pub max_decompressed_bytes: usize,
    #[serde(deserialize_with = "nullable")]
    pub fields: Vec<Mapping>,
    #[serde(deserialize_with = "nullable")]
    pub multipart_parts: Vec<MultipartPart>,
    pub max_multipart_parts: usize,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct MultipartPart {
    pub name: String,
    pub target: String,
    #[serde(rename = "Type")]
    pub ty: String,
    pub kind: String,
    #[serde(deserialize_with = "nullable")]
    pub media_types: Vec<String>,
    pub max_bytes: usize,
    pub multiple: bool,
    pub optional: bool,
    pub retain_filename: bool,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct ResponsePlan {
    pub status: u16,
    pub codec: String,
    #[serde(deserialize_with = "nullable")]
    pub media: Vec<String>,
    pub body: Selection,
    pub stream: bool,
    pub options: ResponseOptions,
    #[serde(deserialize_with = "nullable")]
    pub headers: Vec<ResponseHeader>,
    #[serde(deserialize_with = "nullable")]
    pub cookies: Vec<ResponseCookie>,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct ResponseOptions {
    pub max_bytes: u64,
    #[serde(deserialize_with = "nullable")]
    pub compression_algorithms: Vec<String>,
    pub compression_threshold: usize,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct ResponseHeader {
    pub name: String,
    pub encoding: String,
    pub value: Selection,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct ResponseCookie {
    pub cookie: Cookie,
    pub value: Selection,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Cookie {
    pub name: String,
    pub path: String,
    pub domain: String,
    pub max_age: i64,
    pub expires: String,
    pub secure: bool,
    #[serde(rename = "HTTPOnly")]
    pub http_only: bool,
    pub same_site: u8,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Selection {
    #[serde(deserialize_with = "nullable")]
    pub path: Vec<String>,
    pub optional: bool,
    pub default_empty: bool,
}

pub(crate) fn nullable<'de, D, T>(d: D) -> Result<T, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Deserialize<'de> + Default,
{
    Ok(Option::deserialize(d)?.unwrap_or_default())
}

impl Bundle {
    pub fn verify(&self) -> Result<()> {
        ensure!(
            self.version == 1 && !self.contract_revision.is_empty() && !self.programs.is_empty(),
            "invalid worker bundle"
        );
        ensure!(
            self.host.workers.len() == self.programs.len(),
            "bundle does not cover every service"
        );
        for manifest in self.host.mcp_servers.values() {
            manifest.verify(&self.host.workers)?;
        }
        for assistant in &self.host.assistants {
            assistant.verify(&self.host.mcp_servers)?;
        }

        let mut names = std::collections::BTreeSet::new();
        for program in &self.programs {
            ensure!(
                names.insert(&program.process)
                    && self
                        .host
                        .workers
                        .iter()
                        .any(|service| service.address == program.service
                            && service.process == program.process
                            && service.runtime == program.runtime),
                "worker artifact does not match a service declaration"
            );
        }
        Ok(())
    }
}

pub(crate) fn project_snapshot(snapshot: &Value, consumer: &str, keys: &[String]) -> Value {
    if snapshot.is_null() {
        return Value::Null;
    }
    let mut scope = snapshot.clone();
    scope["consumer"] = json!(consumer);
    for section in ["secrets", "values"] {
        if let Some(fields) = scope[section].as_object_mut() {
            fields.retain(|key, _| keys.contains(key));
        }
    }
    if let Some(fields) = scope["public"].as_array_mut() {
        fields.retain(|key| {
            key.as_str()
                .is_some_and(|key| keys.iter().any(|entry| entry == key))
        });
    }
    scope
}

pub(crate) fn bundle_path(root: &Path, relative: &Path) -> Result<PathBuf> {
    ensure!(
        relative
            .components()
            .all(|c| matches!(c, Component::Normal(_) | Component::CurDir)),
        "artifact path is outside the bundle"
    );
    let path = root.join(relative).canonicalize()?;
    ensure!(
        path.starts_with(root),
        "artifact path is outside the bundle"
    );
    Ok(path)
}

impl Artifact {
    pub fn program(&self, root: &Path, snapshot: &Value) -> Result<Program> {
        let mut environment: Vec<_> = std::env::vars()
            .filter(|(key, _)| production_worker_env(key, &self.runtime))
            .map(|(key, value)| format!("{key}={value}"))
            .collect();
        environment.sort_unstable();
        let configuration = project_snapshot(snapshot, &self.service, &self.config_keys);
        let startup = match self.runtime.as_str() {
            "go" => (!configuration.is_null())
                .then(|| to_raw_value(&configuration))
                .transpose()?,
            "ts" | "js" => {
                let databases = self
                    .databases
                    .iter()
                    .map(|(name, schema)| Ok((name, service_database(schema)?)))
                    .collect::<Result<BTreeMap<_, _>>>()?;
                Some(to_raw_value(
                    &json!({"config": configuration, "databases": databases}),
                )?)
            }
            "rust" => None,
            _ => anyhow::bail!("unsupported worker runtime"),
        };
        Ok(Program {
            service: self.service.clone(),
            process: self.process.clone(),
            binary: bundle_path(root, &self.binary)?,
            digest: self.digest.clone(),
            runtime: self.runtime.clone(),
            identity: self.identity.clone(),
            environment,
            startup,
            artifact: None,
        })
    }
}

fn service_database(schema: &str) -> Result<String> {
    let schema_key = schema.to_ascii_uppercase();
    let key = format!("{schema_key}_DATABASE_URL");
    if let Ok(value) = std::env::var(key)
        && !value.is_empty()
    {
        return Ok(value);
    }
    let base = std::env::var("DATABASE_URL").context("worker database is unavailable")?;
    let mut url = url::Url::parse(&base).context("invalid worker database URL")?;
    let query: Vec<_> = url
        .query_pairs()
        .filter(|(key, _)| key != "search_path")
        .map(|(key, value)| (key.into_owned(), value.into_owned()))
        .collect();
    let path = if schema == "scenery" {
        schema.to_owned()
    } else {
        format!("{schema},scenery")
    };
    url.query_pairs_mut()
        .clear()
        .extend_pairs(query)
        .append_pair("search_path", &path);
    Ok(url.into())
}

fn production_worker_env(key: &str, runtime: &str) -> bool {
    match key {
        "SCENERY_LISTEN_NETWORK"
        | "SCENERY_LISTEN_ADDR"
        | "SCENERY_PROCESS_LINK"
        | "SCENERY_CONFIG_SNAPSHOT_FD"
        | "SCENERY_STORAGE_CONFIG" => false,
        "DATABASE_URL" | "SCENERY_DATABASE_JSON" => runtime == "go",
        "PATH"
        | "HOME"
        | "USER"
        | "LOGNAME"
        | "SHELL"
        | "TMPDIR"
        | "TMP"
        | "TEMP"
        | "TZ"
        | "LANG"
        | "LANGUAGE"
        | "TERM"
        | "COLORTERM"
        | "NO_COLOR"
        | "CLICOLOR"
        | "CLICOLOR_FORCE"
        | "SSL_CERT_FILE"
        | "SSL_CERT_DIR"
        | "HTTP_PROXY"
        | "HTTPS_PROXY"
        | "NO_PROXY"
        | "http_proxy"
        | "https_proxy"
        | "no_proxy"
        | "XDG_RUNTIME_DIR"
        | "XDG_CACHE_HOME"
        | "XDG_CONFIG_HOME"
        | "XDG_DATA_HOME"
        | "LD_LIBRARY_PATH"
        | "DYLD_LIBRARY_PATH"
        | "DYLD_FALLBACK_LIBRARY_PATH"
        | "GODEBUG"
        | "GOMAXPROCS"
        | "GOMEMLIMIT"
        | "GOGC"
        | "GOTRACEBACK"
        | "GOCOVERDIR" => true,
        _ => key.starts_with("LC_") || key.starts_with("SCENERY_"),
    }
}
