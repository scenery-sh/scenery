use std::collections::HashMap;
use std::convert::Infallible;
use std::fmt::Display;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use anyhow::Result;
use bytes::Bytes;
use http_body_util::{BodyExt, Full, combinators::UnsyncBoxBody};
use hyper::body::Incoming;
use hyper::header::{HeaderName, HeaderValue};
use hyper::{HeaderMap, Method, Request, Response, StatusCode};
use scenery_wire::{
    BindingCall, Delivery, Invocation, InvocationContext, MAX_METADATA, SCOPE_HEADER,
};
use serde_json::{Value, json, value::RawValue};
use subtle::ConstantTimeEq;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;
use tokio::sync::watch;
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;

use crate::abi::{self, Outcome};
use crate::auth::{Authentication, Claims};
use crate::durable::Engine;
use crate::plan::{Binding, Bundle, Policy, Service, project_snapshot};
use crate::process::random_hex;
use crate::supervisor::Generation;
use crate::transport::read;
use crate::{gateway, policy, telemetry, values};

pub(crate) type Body = UnsyncBoxBody<Bytes, std::io::Error>;
pub(crate) type Reply = Response<Body>;
type Scopes = Arc<Mutex<HashMap<String, Arc<Scope>>>>;

pub(crate) fn body(value: impl Into<Bytes>) -> Body {
    Full::new(value.into())
        .map_err(|never: Infallible| match never {})
        .boxed_unsync()
}

pub(crate) fn empty(status: StatusCode) -> Reply {
    let mut reply = Response::new(body(Bytes::new()));
    *reply.status_mut() = status;
    reply
}

pub(crate) fn json_reply(status: StatusCode, value: &Value) -> Reply {
    let mut reply = empty(status);
    header(reply.headers_mut(), "content-type", "application/json");
    *reply.body_mut() = body(values::encode(value));
    reply
}

pub(crate) fn header(headers: &mut HeaderMap, name: &'static str, value: &str) {
    if let Ok(value) = HeaderValue::from_str(value) {
        headers.insert(HeaderName::from_static(name), value);
    }
}

pub(crate) struct Fault {
    pub status: StatusCode,
    pub code: &'static str,
    message: &'static str,
    details: Value,
    pub cause: Option<String>,
    pub replay: Option<Box<crate::replay::ReplayInvocation>>,
}

impl Fault {
    pub(crate) fn new(status: u16, code: &'static str, message: &'static str) -> Self {
        Self {
            status: StatusCode::from_u16(status).expect("valid HTTP status"),
            code,
            message,
            details: Value::Null,
            cause: None,
            replay: None,
        }
    }

    pub(crate) fn invalid() -> Self {
        Self::new(
            400,
            "transport.invalid_request",
            "request does not match the declared input",
        )
    }

    pub(crate) fn forbidden() -> Self {
        Self::new(
            403,
            "permission_denied",
            "authorization policy denied invocation",
        )
    }

    pub(crate) fn internal(error: impl Display) -> Self {
        let mut fault = Self::new(500, "system.internal", "contract implementation failure");
        fault.cause = Some(error.to_string());
        fault
    }

    pub(crate) fn unavailable(delivery: Delivery) -> Self {
        let mut fault = Self::new(503, "unavailable", "service is unavailable");
        fault.details = json!({"delivery": delivery});
        fault
    }

    pub(crate) fn deadline() -> Self {
        Self::new(504, "deadline_exceeded", "invocation deadline exceeded")
    }

    /// The outcome a binding with `delivery = "wait"` declares for an
    /// execution that did not finish within the invocation deadline.
    pub(crate) fn wait_timeout() -> Self {
        Self::new(
            504,
            "dispatch.wait_timeout",
            "timed out waiting for execution",
        )
    }

    pub(crate) fn envelope(&self) -> Value {
        let mut value = json!({"code": self.code, "message": self.message});
        if !self.details.is_null() {
            value["meta"] = self.details.clone();
        }
        value
    }

    /// The status and problem a binding answers this fault with. A binding
    /// declares a failed authentication or authorization as an admission
    /// outcome: its status mapping and the clients generated from it know
    /// that name, not the code the host uses inside.
    pub(crate) fn problem(&self, policy: Option<&Policy>) -> (StatusCode, Value) {
        let code = match (policy, self.code) {
            (Some(_), "unauthenticated") => "admission.unauthenticated",
            (Some(_), "permission_denied") => "admission.forbidden",
            (_, code) => code,
        };
        let status = policy
            .and_then(|policy| policy.transport_statuses.get(code))
            .and_then(|status| StatusCode::from_u16(*status).ok())
            .unwrap_or(self.status);
        let mut problem = self.envelope();
        problem["code"] = json!(code);
        (status, problem)
    }

    pub(crate) fn reply(self, host: &Host, policy: Option<&Policy>) -> Reply {
        let (status, problem) = self.problem(policy);
        let mut reply = json_reply(status, &problem);
        header(
            reply.headers_mut(),
            "content-type",
            "application/problem+json",
        );
        if let Some(cause) = self.cause
            && let Some(token) = host
                .telemetry
                .failure_with_context(&cause, self.replay.as_deref())
        {
            header(reply.headers_mut(), "x-report-token", &token);
        }
        reply
    }
}

#[derive(Clone)]
pub(crate) struct Call {
    pub generation: Arc<Generation>,
    pub principal: Option<Claims>,
    pub workload: Option<Value>,
    pub schedule_target: Option<String>,
    pub invocation_id: String,
    pub trace_id: String,
    pub span_id: String,
    pub parent_span_id: String,
    pub execution_id: Option<String>,
    pub caller: String,
    pub cron_idempotency_key: Option<String>,
    pub event_depth: u32,
    pub stack: Vec<String>,
    pub cancel: CancellationToken,
    pub deadline: Instant,
    pub started: OffsetDateTime,
    pub method: String,
    pub path: String,
    /// The HTTP request a worker observes: the invocation metadata members
    /// `method`, `path`, `path_params` and `headers`. Calls made on behalf of
    /// the request inherit it; durable and scheduled work has none.
    pub request: Option<Arc<Value>>,
    pub raw_uri: Option<String>,
    pub raw_body: Option<String>,
    pub has_http_body: bool,
}

impl Call {
    pub(crate) fn new(
        generation: Arc<Generation>,
        headers: &HeaderMap,
        method: &str,
        path: &str,
        timeout: Duration,
    ) -> Result<Self> {
        let parent = headers
            .get("traceparent")
            .and_then(|v| v.to_str().ok())
            .and_then(trace_parent);
        let (trace_id, parent_span_id) = match parent {
            Some((trace, span)) => (trace.into(), span.into()),
            None => (random_hex::<16>()?, String::new()),
        };
        Ok(Self {
            // A call ends with its caller, its deadline or the worker that
            // serves it, not with the generation it started in.
            cancel: CancellationToken::new(),
            generation,
            principal: None,
            workload: None,
            schedule_target: None,
            invocation_id: random_hex::<16>()?,
            trace_id,
            span_id: random_hex::<8>()?,
            parent_span_id,
            execution_id: None,
            caller: String::new(),
            cron_idempotency_key: None,
            event_depth: 0,
            stack: Vec::new(),
            deadline: Instant::now() + timeout,
            started: OffsetDateTime::now_utc(),
            method: method.into(),
            path: path.into(),
            request: None,
            raw_uri: None,
            raw_body: None,
            has_http_body: false,
        })
    }

    pub(crate) fn child_span(&self) -> Result<Self, Fault> {
        let mut child = self.clone();
        child.parent_span_id.clone_from(&self.span_id);
        child.span_id = random_hex::<8>().map_err(Fault::internal)?;
        child.cancel = self.cancel.child_token();
        Ok(child)
    }

    pub(crate) fn descend(&self, address: &str, policy: Option<&Policy>) -> Result<Self, Fault> {
        if self.stack.len() >= 32 || self.stack.iter().any(|ancestor| ancestor == address) {
            return Err(Fault::new(
                409,
                "failed_precondition",
                "binding invocation cycle or depth limit",
            ));
        }
        let mut child = self.clone();
        child.stack.push(address.into());
        child.parent_span_id.clone_from(&self.span_id);
        child.span_id = random_hex::<8>().map_err(Fault::internal)?;
        child.cancel = self.cancel.child_token();
        let timeout = policy.map_or(0, |policy| policy.total_invocation_timeout_nanos);
        if timeout > 0 {
            child.deadline = child
                .deadline
                .min(Instant::now() + Duration::from_nanos(timeout));
        }
        Ok(child)
    }

    pub(crate) fn principal_value(&self) -> Value {
        let uid = self.principal_id().unwrap_or_default();
        let mut principal = self.workload.clone().unwrap_or_else(|| {
            self.principal
                .as_ref()
                .map_or_else(|| json!({}), Claims::data)
        });
        principal["authenticated"] = json!(!uid.is_empty());
        principal["uid"] = json!(uid);
        principal["id"] = json!(uid);
        principal
    }

    pub(crate) fn principal_id(&self) -> Option<&str> {
        if self.workload.is_some() {
            return Some("workload:std.workload_identity.scheduler");
        }
        self.principal
            .as_ref()
            .map(|identity| identity.user.as_str())
    }

    pub(crate) fn wire_deadline(&self) -> String {
        let remaining = self.deadline.saturating_duration_since(Instant::now());
        stamp(OffsetDateTime::now_utc() + remaining)
    }
}

fn trace_parent(source: &str) -> Option<(&str, &str)> {
    let mut parts = source.split('-');
    let (version, trace, span, flags) =
        (parts.next()?, parts.next()?, parts.next()?, parts.next()?);
    let valid =
        |value: &str, length| value.len() == length && value.bytes().all(|c| c.is_ascii_hexdigit());
    (version == "00"
        && valid(trace, 32)
        && !trace.bytes().all(|c| c == b'0')
        && valid(span, 16)
        && !span.bytes().all(|c| c == b'0')
        && valid(flags, 2)
        && parts.next().is_none())
    .then_some((trace, span))
}

pub(crate) fn stamp(at: OffsetDateTime) -> String {
    at.format(&Rfc3339).expect("valid time")
}

pub(crate) struct Scope {
    pub call: Call,
    pub service: String,
    pub operation: String,
    pub endpoint: String,
    pub token: String,
    pub cancel: CancellationToken,
}

pub(crate) struct ScopeGuard {
    scopes: Scopes,
    id: String,
    pub scope: Arc<Scope>,
}

impl Drop for ScopeGuard {
    fn drop(&mut self) {
        self.scope.cancel.cancel();
        self.scopes.lock().expect("scope mutex").remove(&self.id);
    }
}

#[cfg(test)]
impl ScopeGuard {
    pub(crate) fn test() -> Self {
        let generation = Arc::new(Generation {
            number: 1,
            identity: crate::process::BuildIdentity {
                contract_revision: String::new(),
                implementation_revision: String::new(),
                build_input_digest: String::new(),
                go_target: String::new(),
            },
            workers: std::collections::BTreeMap::new(),
            retired: CancellationToken::new(),
        });
        let call = Call::new(
            generation,
            &HeaderMap::new(),
            "GET",
            "/",
            Duration::from_secs(30),
        )
        .expect("test call");
        Self {
            scopes: Arc::new(Mutex::new(HashMap::new())),
            id: "test".into(),
            scope: Arc::new(Scope {
                call,
                service: String::new(),
                operation: String::new(),
                endpoint: String::new(),
                token: String::new(),
                cancel: CancellationToken::new(),
            }),
        }
    }
}

pub(crate) enum Mode {
    Production,
    Development(Arc<crate::dev::Status>),
    Test,
}

pub(crate) struct Host {
    pub bundle: Bundle,
    pub auth: Authentication,
    pub assistants: crate::assistant::Gateway,
    pub development: bool,
    pub configuration: Value,
    pub telemetry: telemetry::Sink,
    pub engine: Engine,
    pub origins: gateway::Origins,
    pub sessions: crate::database::Pool,
    generation: watch::Receiver<Option<Arc<Generation>>>,
    capability_scopes: Scopes,
    control_token: String,
    dev_status: Option<Arc<crate::dev::Status>>,
    event_target: Option<crate::supervisor::EventTarget>,
    pub stop: CancellationToken,
    auth_schema: tokio::sync::OnceCell<()>,
}

impl Host {
    pub(crate) fn new(
        bundle: Bundle,
        configuration: Value,
        export: crate::otlp::Export,
        control_token: String,
        mode: Mode,
        lifetime: (CancellationToken, crate::assistant::Gateway),
        generation: watch::Receiver<Option<Arc<Generation>>>,
    ) -> Result<Self> {
        let (stop, assistants) = lifetime;
        let (development, dev_status) = match mode {
            Mode::Production => (false, None),
            Mode::Development(status) => (true, Some(status)),
            Mode::Test => (true, None),
        };
        let scope = project_snapshot(&configuration, "host", &bundle.host_config_keys);
        let auth = Authentication::new(bundle.auth.as_ref(), &scope, development)?;
        // Telemetry carries the runtime application identity its workers
        // report under, which a supervisor may set apart from the graph name.
        let app = std::env::var("SCENERY_APP_ID")
            .ok()
            .filter(|app| !app.trim().is_empty())
            .unwrap_or_else(|| bundle.host.name.clone());
        let telemetry = telemetry::Sink::new(
            &app,
            development,
            &bundle.host.observability,
            export,
            &bundle.reports,
        )?;
        Ok(Self {
            bundle,
            configuration,
            development,
            auth,
            assistants,
            generation,
            telemetry,
            engine: Engine::new(),
            sessions: crate::database::Pool::new(),
            origins: gateway::Origins::new(
                &std::env::var("SCENERY_CORS_ALLOW_ORIGINS").unwrap_or_default(),
                development,
            ),
            control_token,
            dev_status,
            event_target: None,
            stop,
            auth_schema: tokio::sync::OnceCell::new(),
            capability_scopes: Scopes::default(),
        })
    }

    pub(crate) fn with_event_target(
        mut self,
        target: Option<crate::supervisor::EventTarget>,
    ) -> Self {
        self.event_target = target;
        self
    }

    /// Records a sanitized runtime fault (such as a Postgres deadlock or lease
    /// error) to the local telemetry journal when running in development mode.
    pub(crate) async fn record_runtime_fault(
        &self,
        component: &str,
        target: &str,
        error_class: &str,
    ) {
        let Some(event_target) = &self.event_target else {
            return;
        };
        let generation = self.generation().map_or(0, |current| current.number);
        let at = time::OffsetDateTime::now_utc()
            .format(&time::format_description::well_known::Rfc3339)
            .unwrap_or_default();
        let event = json!({
            "at": at,
            "kind": "runtime_fault",
            "app": event_target.app,
            "generation": generation,
            "runtime_fault": {
                "component": component,
                "target": target,
                "error_class": error_class,
            }
        });
        let _ = crate::dev::journal(&event_target.path, &event).await;
    }

    pub(crate) fn classify_runtime_error(error: &anyhow::Error) -> &'static str {
        let message = format!("{error:#}").to_ascii_lowercase();
        if message.contains("deadlock") {
            "deadlock"
        } else if message.contains("timed out") || message.contains("timeout") {
            "timeout"
        } else if message.contains("connection closed") || message.contains("connection refused") {
            "connection_error"
        } else if message.contains("db error") || message.contains("postgres") {
            "db_error"
        } else {
            "runtime_error"
        }
    }

    pub(crate) fn generation(&self) -> Option<Arc<Generation>> {
        self.generation.borrow().clone()
    }

    pub(crate) fn service(&self, address: &str) -> Option<&Service> {
        self.bundle
            .host
            .workers
            .iter()
            .find(|service| service.address == address)
    }

    pub(crate) fn binding(&self, address: &str) -> Option<(&Service, &Binding)> {
        self.bundle.host.workers.iter().find_map(|service| {
            service
                .bindings
                .iter()
                .find(|binding| binding.address == address)
                .map(|binding| (service, binding))
        })
    }

    pub(crate) async fn authenticate(
        &self,
        call: &mut Call,
        headers: &HeaderMap,
        private: bool,
    ) -> Result<(), Fault> {
        let invalid = || Fault::new(401, "unauthenticated", "invalid or missing authentication");
        let claims = self
            .auth
            .credentials(headers, private)
            .map_err(|_| invalid())?;
        if !claims.session.is_empty() {
            let uuid = crate::connections::valid_uuid;
            if !uuid(&claims.session)
                || !uuid(&claims.user)
                || !uuid(&claims.tenant)
                || !claims.actor.is_empty() && !uuid(&claims.actor)
                || !claims.impersonation_id.is_empty() && !uuid(&claims.impersonation_id)
            {
                return Err(invalid());
            }
            self.ensure_auth_schema().await.map_err(Fault::internal)?;
            let connection = self.engine.connection().await.map_err(Fault::internal)?;
            if !claims
                .check_session(&connection)
                .await
                .map_err(Fault::internal)?
            {
                return Err(invalid());
            }
        }
        call.principal = Some(claims);
        Ok(())
    }

    pub(crate) async fn ensure_auth_schema(&self) -> Result<()> {
        let Some(plan) = self
            .bundle
            .auth
            .as_ref()
            .filter(|plan| !plan.bootstrap_sql.is_empty())
        else {
            return Ok(());
        };
        self.auth_schema.get_or_try_init(|| async {
            let (mut database, _driver) = crate::durable::connect().await?;
            let tx = database.transaction().await?;
            tx.execute("SELECT pg_advisory_xact_lock(hashtextextended('scenery.standard.auth', 0))", &[]).await?;
            let row = tx.query_one(
                "SELECT to_regclass('scenery.scenery_auth_connections') IS NOT NULL,
                        to_regclass('scenery.scenery_auth_connection_schema') IS NOT NULL", &[],
            ).await?;
            let had_connections: bool = row.get(0);
            let ledger: bool = row.get(1);
            anyhow::ensure!(!had_connections || ledger, "connection schema requires explicit migration");
            if ledger {
                let current: bool = tx.query_one(
                    "SELECT EXISTS(SELECT 1 FROM scenery.scenery_auth_connection_schema WHERE singleton AND version=1)", &[],
                ).await?.get(0);
                anyhow::ensure!(current, "connection schema requires explicit migration");
            }
            tx.batch_execute(&plan.bootstrap_sql).await?;
            tx.commit().await?;
            Ok::<_, anyhow::Error>(())
        }).await?;
        Ok(())
    }

    pub(crate) fn authorize(call: &Call, policy: &Policy, input: &Value) -> Result<(), Fault> {
        if policy.authorization_strategy == "public" {
            return Ok(());
        }
        let principal = call.principal_value();
        let mut context = json!({"caller_binding": policy.binding_address, "method": call.method, "path": call.path});
        if let Some(tenant) = principal
            .get("tenant_id")
            .or_else(|| principal.get("TenantID"))
            .filter(|v| v.as_str().is_none_or(|s| !s.is_empty()))
        {
            context["tenant_id"] = tenant.clone();
        }
        let variables = json!({"principal": principal, "context": context, "input": input});
        let mut allowed = Vec::new();
        for rule in &policy.authorization_rules {
            let matches =
                policy::evaluate(&rule.expression, &variables).map_err(|_| Fault::forbidden())?;
            if rule.effect == "deny" {
                if matches {
                    return Err(Fault::forbidden());
                }
            } else {
                allowed.push(matches);
            }
        }
        let authorized = !allowed.is_empty()
            && match policy.authorization_strategy.as_str() {
                "deny_unless_allowed" => allowed.into_iter().any(|value| value),
                "all_must_allow" => allowed.into_iter().all(|value| value),
                "first_applicable" => allowed[0],
                _ => false,
            };
        if authorized {
            Ok(())
        } else {
            Err(Fault::forbidden())
        }
    }

    pub(crate) async fn dispatch(
        &self,
        call: &Call,
        service: &Service,
        binding: &Binding,
        input: Value,
    ) -> Result<Outcome, Fault> {
        let operation = service
            .operations
            .get(&binding.operation)
            .ok_or_else(|| Fault::internal("operation is not declared"))?;
        let input = values::normalize(&self.bundle.types, &operation.input, input)
            .map_err(|_| Fault::invalid())?;
        Self::authorize(call, &binding.policy, &input)?;
        let mut child = call.descend(&binding.address, Some(&binding.policy))?;
        child.caller.clone_from(&binding.address);
        if binding.delivery == "enqueue"
            || binding.delivery == "wait"
            || (binding.mcp && service.durable.contains_key(&binding.execution))
        {
            let plan = service
                .durable
                .get(&binding.execution)
                .ok_or_else(|| Fault::internal("execution is not declared"))?;
            let delivery = if binding.delivery == "enqueue" {
                "enqueue"
            } else {
                "wait"
            };
            return self.engine.dispatch(&child, plan, delivery, &input).await;
        }
        self.invoke(
            &child,
            service,
            &binding.operation,
            Some(&binding.address),
            "direct",
            &input,
        )
        .await
    }

    pub(crate) async fn invoke(
        &self,
        call: &Call,
        service: &Service,
        address: &str,
        binding: Option<&str>,
        mode: &str,
        input: &Value,
    ) -> Result<Outcome, Fault> {
        if call.cancel.is_cancelled() || Instant::now() >= call.deadline {
            return Err(Fault::deadline());
        }
        let slot = call
            .generation
            .workers
            .get(&service.process)
            .ok_or_else(|| Fault::unavailable(Delivery::NotSent))?;
        let worker = tokio::select! {
            () = abi::cancelled(call.cancel.clone(), call.deadline) => {
                return Err(Fault::deadline());
            }
            () = call.generation.retired.cancelled() => {
                return Err(Fault::unavailable(Delivery::NotSent));
            }
            () = slot.retired.cancelled() => {
                return Err(Fault::unavailable(Delivery::NotSent));
            }
            result = slot.ensure() => {
                result.map_err(|_| Fault::unavailable(Delivery::NotSent))?
            }
        };
        if !matches!(worker.exited(), Ok(None)) {
            return Err(Fault::unavailable(Delivery::NotSent));
        }
        let scope_id = random_hex::<32>().map_err(Fault::internal)?;
        let scope = Arc::new(Scope {
            call: call.clone(),
            service: service.address.clone(),
            operation: address.into(),
            endpoint: binding.unwrap_or(address).into(),
            token: worker.launch.token.clone(),
            cancel: call.cancel.child_token(),
        });
        self.capability_scopes
            .lock()
            .expect("scope mutex")
            .insert(scope_id.clone(), scope.clone());
        let guard = ScopeGuard {
            scopes: self.capability_scopes.clone(),
            id: scope_id.clone(),
            scope: scope.clone(),
        };
        let metadata =
            worker_invocation_metadata(&self.telemetry, call, service, &scope.endpoint, mode);
        let invocation = Invocation {
            service: service.address.clone(),
            operation: address.into(),
            binding: binding.map(str::to_owned),
            mode: mode.into(),
            context: InvocationContext {
                invocation_id: call.invocation_id.clone(),
                scope: scope_id,
                generation: call.generation.number,
                contract_revision: worker.program.identity.contract_revision.clone(),
                principal: call.principal_id().map(str::to_owned),
                tenant_id: call
                    .principal
                    .as_ref()
                    .map(|principal| principal.tenant.clone()),
                traceparent: Some(format!("00-{}-{}-01", call.trace_id, call.span_id)),
                deadline: Some(call.wire_deadline()),
                locale: None,
                execution_id: call.execution_id.clone(),
                metadata: Some(
                    serde_json::value::to_raw_value(&metadata).map_err(Fault::internal)?,
                ),
            },
            input: RawValue::from_string(values::encode(input)).map_err(Fault::internal)?,
        };
        let started = Instant::now();
        let result = tokio::select! {
            () = abi::cancelled(call.cancel.clone(), call.deadline) => Err(Fault::deadline()),
            () = worker.retired.cancelled() => Err(Fault::deadline()),
            result = worker.invoke(invocation, guard) => result,
        };
        let code = match &result {
            Ok(outcome) if outcome.kind == "error" => outcome.name.as_str(),
            Err(fault) => fault.code,
            _ => "",
        };
        self.telemetry
            .span(call, service, address, started.elapsed(), code);
        let validated = result.and_then(|mut result| {
            let operation = service
                .operations
                .get(address)
                .ok_or_else(|| Fault::internal("unknown operation"))?;
            let ty = operation
                .outcomes
                .get(&format!("{}.{}", result.kind, result.name))
                .ok_or_else(|| Fault::internal("worker returned undeclared outcome"))?;
            if result.events.is_some() {
                if result.kind != "result" || !result.payload.is_null() {
                    return Err(Fault::internal(
                        "event stream outcome must be a result with null metadata payload",
                    ));
                }
            } else {
                result.payload = values::normalize(&self.bundle.types, ty, result.payload)
                    .map_err(Fault::internal)?;
            }
            Ok(result)
        });
        crate::replay::attach(validated, call, service, address, binding, mode, input)
    }

    pub(crate) async fn capability(&self, request: Request<Incoming>) -> Reply {
        let scope = request
            .headers()
            .get(SCOPE_HEADER)
            .and_then(|header| header.to_str().ok())
            .and_then(|id| {
                self.capability_scopes
                    .lock()
                    .expect("scope mutex")
                    .get(id)
                    .cloned()
            });
        let Some(scope) = scope else {
            return empty(StatusCode::FORBIDDEN);
        };
        if scope.cancel.is_cancelled()
            || !authorized(request.headers(), &format!("Bearer {}", scope.token))
        {
            return empty(StatusCode::FORBIDDEN);
        }
        let Some(service) = self.service(&scope.service) else {
            return empty(StatusCode::FORBIDDEN);
        };
        let head = request.method() == Method::HEAD;
        let storage = request.uri().path().starts_with("/host/v1/storage/");
        let result = tokio::select! {
            () = abi::cancelled(scope.cancel.clone(), scope.call.deadline) => Err(Fault::deadline()),
            reply = self.serve_capability(request, &scope, service) => reply,
        };
        let mut reply = match result {
            Ok(reply) => reply,
            Err(fault) if storage => crate::storage::failure(self, fault),
            Err(fault) => fault.reply(self, None),
        };
        if head {
            *reply.body_mut() = body(Bytes::new());
        } else if storage && reply.status().is_success() {
            let inner = std::mem::replace(reply.body_mut(), body(Bytes::new()));
            *reply.body_mut() = crate::transport::ScopedBody::boxed(
                inner,
                scope.cancel.clone(),
                scope.call.deadline,
            );
        }
        reply
    }

    async fn serve_capability(
        &self,
        request: Request<Incoming>,
        scope: &Scope,
        service: &Service,
    ) -> Result<Reply, Fault> {
        let path = request.uri().path();
        match (request.method().clone(), path) {
            (Method::POST, "/host/v1/events/emit") => {
                crate::events::emit(self, scope, service, request.into_body()).await
            }
            (Method::POST, "/host/v1/binding/invoke" | "/host/v1/durable/enqueue") => {
                let enqueue = path.ends_with("/enqueue");
                let input = read(request.into_body(), MAX_METADATA)
                    .await
                    .map_err(|_| Fault::invalid())?;
                let decoded: BindingCall<String> =
                    serde_json::from_slice(&input).map_err(|_| Fault::invalid())?;
                let client = service
                    .clients
                    .get(&decoded.client)
                    .filter(|client| client.enqueue == enqueue)
                    .ok_or_else(Fault::forbidden)?;
                let (owner, binding) =
                    self.binding(&client.binding).ok_or_else(Fault::forbidden)?;
                if binding.visibility == "package" && owner.module != service.module {
                    return Err(Fault::forbidden());
                }
                let input =
                    values::parse(decoded.input.get().as_bytes()).map_err(|_| Fault::invalid())?;
                let mut call = scope.call.clone();
                call.cancel = scope.cancel.child_token();
                match self.dispatch(&call, owner, binding, input).await {
                    Ok(outcome) if !outcome.is_streamed() => {
                        let output = if enqueue {
                            outcome.payload
                        } else {
                            outcome.json()
                        };
                        Ok(json_reply(StatusCode::OK, &json!({"output": output})))
                    }
                    Ok(_) => Err(Fault::internal("internal binding returned a stream")),
                    Err(fault) => Ok(json_reply(
                        StatusCode::OK,
                        &json!({"error": {
                            "kind": "error", "message": fault.message, "code": fault.code, "meta": fault.details,
                        }}),
                    )),
                }
            }
            (Method::GET, "/host/v1/config/snapshot") => {
                let keys = self
                    .bundle
                    .programs
                    .iter()
                    .find(|program| program.service == service.address)
                    .map(|program| program.config_keys.as_slice())
                    .unwrap_or_default();
                let snapshot = project_snapshot(&self.configuration, &service.address, keys);
                if snapshot.is_null() {
                    return Ok(empty(StatusCode::NOT_FOUND));
                }
                let mut response = json_reply(StatusCode::OK, &snapshot);
                header(response.headers_mut(), "cache-control", "no-store");
                Ok(response)
            }
            (Method::POST, "/host/v1/telemetry") => {
                let input = read(request.into_body(), 64 << 10)
                    .await
                    .map_err(|_| Fault::invalid())?;
                let accepted = self
                    .telemetry
                    .worker(scope, service, &input)
                    .map_err(|_| Fault::invalid())?;
                Ok(json_reply(StatusCode::OK, &json!({"accepted": accepted})))
            }
            (_, path) if path.starts_with("/host/v1/storage/") => {
                crate::storage::serve(self, request, scope, service).await
            }
            (Method::POST, "/host/v1/connections/token") => {
                let bad_request =
                    || Fault::new(400, "invalid_argument", "invalid connection request");
                let raw = read(request.into_body(), 8 << 10)
                    .await
                    .map_err(|_| bad_request())?;
                let input = serde_json::from_slice(&raw).map_err(|_| bad_request())?;
                let token = crate::connections::token(self, scope, input).await?;
                let mut reply = json_reply(StatusCode::OK, &json!({"token": token}));
                header(reply.headers_mut(), "cache-control", "no-store");
                Ok(reply)
            }
            _ => Ok(empty(StatusCode::NOT_FOUND)),
        }
    }
}

#[path = "control.rs"]
mod control;
#[cfg(test)]
use control::forward_invocation_authorization;

pub(crate) fn authorized(headers: &HeaderMap, expected: &str) -> bool {
    let mut supplied = headers.get_all("authorization").iter();
    supplied
        .next()
        .is_some_and(|value| bool::from(value.as_bytes().ct_eq(expected.as_bytes())))
        && supplied.next().is_none()
}

fn worker_invocation_metadata(
    telemetry: &telemetry::Sink,
    call: &Call,
    service: &Service,
    endpoint: &str,
    mode: &str,
) -> Value {
    let mut metadata = json!({
        "caller_binding": call.caller,
        "started": stamp(call.started),
        "parent_span_id": call.parent_span_id,
        "module": service.module,
        "service_name": service.name,
        "logs_enabled": telemetry.logs_enabled(call, service, endpoint),
        "request_type": if mode == "durable" { "durable-call" } else { "api-call" },
    });
    if let Some(workload) = &call.workload {
        metadata["auth_data_kind"] = json!("json");
        metadata["auth_data"] = workload.clone();
    } else if let Some(principal) = &call.principal {
        metadata["auth_data_kind"] = json!("scenery.auth.standard");
        metadata["auth_data"] = principal.data();
    }
    if let Some(request) = call.request.as_deref().and_then(Value::as_object) {
        for (member, value) in request {
            metadata[member.as_str()] = value.clone();
        }
    }
    if metadata.get("api").is_none() {
        metadata["api"] = json!({
            "exposed": false,
            "auth_required": call.principal_id().is_some(),
        });
    }
    if let Some(key) = call
        .cron_idempotency_key
        .as_deref()
        .filter(|value| !value.is_empty())
    {
        metadata["cron_idempotency_key"] = json!(key);
    }
    metadata
}

#[cfg(test)]
#[path = "host_test.rs"]
mod tests;
