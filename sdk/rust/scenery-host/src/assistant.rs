use std::collections::{BTreeMap, HashMap};
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use anyhow::{Result, ensure};
use hyper::body::Incoming;
use hyper::{Method, Request, StatusCode};
use serde::Deserialize;
use serde_json::{Value, json};
use tokio::net::TcpListener;
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;

use crate::assistant_token::{self as token, Approval, Claims, Conversation, Key};
use crate::auth;
use crate::host::{Call, Host, Reply, header, json_reply};
use crate::plan::Policy;
use crate::supervisor::Generation;

const CONTROL_PATH: &str = "/scenery/v1/control";
const CONTROL_HEADER: &str = "x-scenery-assistant-control-token";
const CONTROL_LIMIT: usize = 16 << 20;
const MAX_CONVERSATIONS: usize = 256;
const MAX_RUNS: usize = 4096;
const RUN_IDLE_EVICT: Duration = Duration::from_secs(30);
const RUN_SWEEP_TTL: Duration = Duration::from_secs(900);

#[derive(Deserialize)]
pub(crate) struct Plan {
    address: String,
    pub path: String,
    pub mcp_server: String,
    authentication: String,
    session_access: String,
    runtime_revision: String,
    capability_revision: String,
    pub policy: Policy,
}

impl Plan {
    pub(crate) fn verify(&self, servers: &BTreeMap<String, crate::mcp::Manifest>) -> Result<()> {
        ensure!(
            servers.contains_key(&self.mcp_server)
                && self.path.starts_with('/')
                && !self.path.ends_with('/')
                && !self.path.to_ascii_lowercase().contains("scenery")
                && self.session_access == "initiator"
                && !self.authentication.is_empty()
                && !self.runtime_revision.is_empty()
                && !self.capability_revision.is_empty(),
            "invalid assistant plan"
        );
        Ok(())
    }

    pub(crate) fn route<'a>(&self, method: &Method, path: &'a str) -> Option<Route<'a>> {
        let path = path
            .strip_prefix(&self.path)?
            .strip_prefix("/v1/conversations")?;
        if path.is_empty() && method == Method::POST {
            return Some(Route::Create);
        }
        let (conversation, action) = path.strip_prefix('/')?.split_once('/')?;
        match (method, action) {
            (&Method::POST, "turns") => Some(Route::Turn(conversation)),
            (&Method::GET, "events") => Some(Route::Events(conversation)),
            (&Method::POST, action) => {
                let (collection, rest) = action.split_once('/')?;
                match collection {
                    "approvals" if !rest.is_empty() && !rest.contains('/') => {
                        Some(Route::Approve(conversation, rest))
                    }
                    "runs" => {
                        let id = rest.strip_suffix("/cancel")?;
                        (!id.is_empty() && !id.contains('/'))
                            .then_some(Route::Cancel(conversation, id))
                    }
                    _ => None,
                }
            }
            _ => None,
        }
    }
}

pub(crate) enum Route<'a> {
    Create,
    Turn(&'a str),
    Events(&'a str),
    Approve(&'a str, &'a str),
    Cancel(&'a str, &'a str),
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Descriptor {
    pub assistant_address: String,
    control_address: String,
    control_token: String,
    #[serde(default, deserialize_with = "optional_address")]
    mcp_listen_address: Option<std::net::SocketAddr>,
    #[serde(default)]
    pub mcp_bridge_secret: String,
    runtime_revision: String,
    pub capability_revision: String,
    #[serde(default = "required")]
    required: bool,
}

fn optional_address<'de, D>(input: D) -> std::result::Result<Option<std::net::SocketAddr>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    let value = String::deserialize(input)?;
    if value.is_empty() {
        Ok(None)
    } else {
        value.parse().map(Some).map_err(serde::de::Error::custom)
    }
}

fn required() -> bool {
    true
}

impl Descriptor {
    fn control_url(&self) -> Option<url::Url> {
        let url = url::Url::parse(&self.control_address).ok()?;
        if url.scheme() != "http"
            || url.port_or_known_default().is_none()
            || url.query().is_some()
            || url.fragment().is_some()
            || url.path() != "/"
            || !url.username().is_empty()
            || url.password().is_some()
        {
            return None;
        }
        match url.host()? {
            url::Host::Ipv4(ip) if ip.is_loopback() => Some(url),
            url::Host::Ipv6(ip) if ip.is_loopback() => Some(url),
            // Use the loopback address explicitly so host name resolution is never needed.
            url::Host::Domain("localhost") => {
                let mut url = url;
                url.set_host(Some("127.0.0.1")).ok()?;
                Some(url)
            }
            _ => None,
        }
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RuntimeConfig {
    assistants: Vec<Descriptor>,
}

#[derive(Clone, Hash, PartialEq, Eq)]
pub(crate) struct RunKey {
    assistant: String,
    principal: String,
    conversation: String,
    run: String,
}

impl RunKey {
    pub(crate) fn new(assistant: &str, principal: &str, conversation: &str, run: &str) -> Self {
        Self {
            assistant: assistant.into(),
            principal: principal.into(),
            conversation: conversation.into(),
            run: run.into(),
        }
    }
}

#[derive(Clone)]
pub(crate) struct Run {
    pub generation: Arc<Generation>,
    pub principal: Option<auth::Claims>,
    pub traceparent: Option<String>,
    started: Instant,
    last_active: Instant,
}

#[derive(Default)]
struct ConversationState {
    continuation: String,
    updated: Option<Instant>,
}

#[derive(Clone)]
struct PendingApproval {
    token: String,
    capability: String,
    input: Option<Value>,
    local_tool: bool,
    decision: Decision,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Decision {
    Pending,
    Approved,
    Consumed,
    Denied,
}

#[derive(Default)]
struct State {
    runs: HashMap<RunKey, Run>,
    conversations: HashMap<String, ConversationState>,
    approvals: HashMap<(RunKey, String), PendingApproval>,
}

pub(crate) struct Gateway {
    pub key: Option<Key>,
    descriptors: BTreeMap<String, Descriptor>,
    client: reqwest::Client,
    state: Arc<Mutex<State>>,
}

impl Gateway {
    pub(crate) fn new(
        config_file: Option<&Path>,
        key_file: Option<&Path>,
        plans: &[Plan],
    ) -> Result<Self> {
        let mut descriptors = BTreeMap::new();
        if let Some(config) = config_file.filter(|_| !plans.is_empty()) {
            let raw = token::read_private(config, 256 << 10)
                .ok_or_else(|| anyhow::anyhow!("assistant descriptor file is unavailable"))?;
            let config: RuntimeConfig = crate::values::parse(raw.as_bytes())
                .ok()
                .and_then(|data| serde_json::from_value(data).ok())
                .ok_or_else(|| anyhow::anyhow!("invalid assistant runtime descriptors"))?;
            for descriptor in config.assistants {
                ensure!(
                    descriptor.control_url().is_some()
                        && !descriptor.control_token.is_empty()
                        && descriptor
                            .mcp_listen_address
                            .is_none_or(|addr| addr.ip().is_loopback() && addr.port() != 0)
                        && descriptor.mcp_listen_address.is_none()
                            == descriptor.mcp_bridge_secret.is_empty()
                        && plans
                            .iter()
                            .any(|p| p.address == descriptor.assistant_address
                                && p.capability_revision == descriptor.capability_revision
                                && p.runtime_revision == descriptor.runtime_revision),
                    "invalid assistant runtime descriptor",
                );
                ensure!(
                    descriptors
                        .insert(descriptor.assistant_address.clone(), descriptor)
                        .is_none(),
                    "duplicate assistant runtime descriptor"
                );
            }
        }
        Ok(Self {
            key: (!plans.is_empty()).then(|| Key::load(key_file)).flatten(),
            descriptors,
            client: reqwest::Client::builder()
                .no_proxy()
                .redirect(reqwest::redirect::Policy::none())
                .connect_timeout(Duration::from_secs(3))
                .build()?,
            state: Arc::new(Mutex::new(State::default())),
        })
    }

    pub(crate) async fn listeners(
        host: &Arc<Host>,
        stop: &CancellationToken,
        servers: &mut JoinSet<Result<()>>,
    ) -> Result<()> {
        for descriptor in host.assistants.descriptors.values() {
            let Some(address) = descriptor.mcp_listen_address else {
                continue;
            };
            let listener = match TcpListener::bind(address).await {
                Ok(listener) => listener,
                Err(error) if !descriptor.required => {
                    eprintln!("optional assistant tools unavailable: {error}");
                    continue;
                }
                Err(error) => return Err(error.into()),
            };
            let address = descriptor.assistant_address.clone();
            let app = host.clone();
            let stop = stop.clone();
            servers.spawn(serve_tools(app, address, listener, stop));
        }
        if !host.assistants.descriptors.is_empty() {
            let host = host.clone();
            let stop = stop.clone();
            servers.spawn(async move {
                let mut tick = tokio::time::interval(Duration::from_secs(10));
                loop {
                    tokio::select! {
                        () = stop.cancelled() => return Ok(()),
                        _ = tick.tick() => host.assistants.sweep(),
                    }
                }
            });
        }
        Ok(())
    }

    pub(crate) fn run(&self, key: &RunKey) -> Option<Run> {
        let mut state = self.state.lock().expect("assistant state");
        let State {
            runs, approvals, ..
        } = &mut *state;
        let run = runs.get_mut(key).filter(|run| {
            !run.generation.retired.is_cancelled()
                && run.started.elapsed() < Duration::from_secs(3600)
                && (has_active_approval(approvals, key)
                    || run.last_active.elapsed() < RUN_SWEEP_TTL)
        })?;
        run.last_active = Instant::now();
        Some(run.clone())
    }

    pub(crate) fn touch(&self, key: &RunKey) {
        if let Some(run) = self
            .state
            .lock()
            .expect("assistant state")
            .runs
            .get_mut(key)
        {
            run.last_active = Instant::now();
        }
    }

    fn finish(&self, key: &RunKey) {
        let mut state = self.state.lock().expect("assistant state");
        state.runs.remove(key);
        state.approvals.retain(|(run, _), _| run != key);
    }

    pub(crate) fn clear_runs(&self) {
        let mut state = self.state.lock().expect("assistant state");
        state.runs.clear();
        state.approvals.clear();
    }

    fn sweep(&self) {
        let mut state = self.state.lock().expect("assistant state");
        Self::sweep_locked(&mut state);
    }

    fn sweep_locked(state: &mut State) {
        let State {
            runs, approvals, ..
        } = state;
        runs.retain(|key, run| {
            !run.generation.retired.is_cancelled()
                && run.started.elapsed() < Duration::from_secs(3600)
                && (has_active_approval(approvals, key)
                    || run.last_active.elapsed() < RUN_SWEEP_TTL)
        });
        approvals.retain(|(key, _), _| runs.contains_key(key));
    }

    pub(crate) fn consume_approval(&self, key: &RunKey, capability: &str, input: &Value) -> bool {
        let mut state = self.state.lock().expect("assistant state");
        let Some(approval) = state.approvals.iter_mut().find_map(|((run, _), approval)| {
            (key == run
                && approval.decision == Decision::Approved
                && approval.capability == capability
                && approval.input.as_ref() == Some(input))
            .then_some(approval)
        }) else {
            return false;
        };
        approval.decision = Decision::Consumed;
        true
    }

    pub(crate) async fn serve(
        host: &Arc<Host>,
        plan: &Plan,
        route: Route<'_>,
        request: Request<Incoming>,
        mount: &str,
        generation: Arc<Generation>,
    ) -> Reply {
        if plan.policy.max_request_header_bytes > 0
            && request
                .headers()
                .iter()
                .map(|(name, value)| name.as_str().len() + value.as_bytes().len() + 4)
                .sum::<usize>()
                > plan.policy.max_request_header_bytes
        {
            return Error::HeaderTooLarge.reply();
        }
        let mut call = match Call::new(
            generation,
            request.headers(),
            request.method().as_str(),
            request.uri().path(),
            crate::gateway::timeout(&plan.policy),
        ) {
            Ok(call) => call,
            Err(_) => return Error::Internal.reply(),
        };
        if host
            .authenticate(&mut call, request.headers(), false)
            .await
            .is_err()
        {
            return Error::Unauthorized.reply();
        }
        if Host::authorize(&call, &plan.policy, &Value::Null).is_err() {
            return Error::Forbidden.reply();
        }
        host.assistants
            .handle(host, plan, route, request, mount, call)
            .await
            .unwrap_or_else(Error::reply)
    }

    async fn handle(
        &self,
        host: &Arc<Host>,
        plan: &Plan,
        route: Route<'_>,
        request: Request<Incoming>,
        mount: &str,
        call: Call,
    ) -> Result<Reply, Error> {
        if matches!(route, Route::Events(_) | Route::Cancel(_, _)) {
            crate::http_timeout::request_body_finished(request.extensions());
        }
        let principal = call.principal_id().ok_or(Error::Unauthorized)?;
        let owner = token::owner(principal);
        if matches!(route, Route::Create) {
            let message = read_message(request).await?;
            let (run, digest, response) = self.start(plan, &call, None, message).await?;
            let conversation = self
                .key
                .as_ref()
                .ok_or(Error::Unavailable)?
                .seal(
                    &plan.address,
                    &owner,
                    &digest,
                    Conversation {
                        private_session_id: response.private_session_id,
                        continuation_token: response.continuation_token,
                    },
                )
                .ok_or(Error::Internal)?;
            let url = format!(
                "{mount}{}/v1/conversations/{conversation}/events",
                plan.path
            );
            return Ok(public(
                json!({"conversation_id":conversation,"run_id":run,"events_url":url}),
            ));
        }
        let conversation_id = match route {
            Route::Turn(id) | Route::Events(id) | Route::Approve(id, _) | Route::Cancel(id, _) => {
                id
            }
            Route::Create => unreachable!(),
        };
        let mut claims = self
            .key
            .as_ref()
            .ok_or(Error::Unavailable)?
            .open::<Conversation>(conversation_id, &plan.address, &owner)
            .ok_or(Error::NotFound)?;
        if let Some(state) = self
            .state
            .lock()
            .expect("assistant state")
            .conversations
            .get(conversation_id)
        {
            claims
                .data
                .continuation_token
                .clone_from(&state.continuation);
        }
        match route {
            Route::Turn(_) => {
                let message = read_message(request).await?;
                let (run, _, response) = self.start(plan, &call, Some(&claims), message).await?;
                self.remember_continuation(conversation_id, response.continuation_token);
                Ok(public(json!({"run_id":run})))
            }
            Route::Events(_) => {
                let after =
                    url::form_urlencoded::parse(request.uri().query().unwrap_or("").as_bytes())
                        .find(|(key, _)| key == "after")
                        .map_or(Ok(0), |(_, value)| value.trim().parse::<u64>())
                        .map_err(|_| Error::Invalid)?;
                let sse = request
                    .headers()
                    .get("accept")
                    .and_then(|value| value.to_str().ok())
                    .is_some_and(|value| {
                        value
                            .split(',')
                            .any(|media| media.trim() == "text/event-stream")
                    });
                events::stream(host, plan, claims, conversation_id, principal, after, sse).await
            }
            Route::Approve(_, id) => {
                let approval = self
                    .key
                    .as_ref()
                    .ok_or(Error::Unavailable)?
                    .open::<Approval>(id, &plan.address, &owner)
                    .filter(|approval| {
                        approval.conversation_digest == token::conversation(conversation_id)
                    })
                    .ok_or(Error::NotFound)?;
                let input = read_request(request).await?;
                let allow = match input
                    .get("decision")
                    .and_then(Value::as_str)
                    .filter(|_| single_field(&input))
                {
                    Some("approve") => true,
                    Some("deny") => false,
                    _ => return Err(Error::Invalid),
                };
                self.approve(plan, &claims, principal, approval, allow)
                    .await?;
                Ok(public(
                    json!({"approval_id":id,"decision":if allow {"approve"} else {"deny"}}),
                ))
            }
            Route::Cancel(_, run_id) => {
                if !token::valid_run(run_id) {
                    return Err(Error::NotFound);
                }
                let run = RunKey::new(
                    &plan.address,
                    principal,
                    &claims.conversation_digest,
                    run_id,
                );
                let control =
                    self.control(plan, "run.cancel", principal, &claims.conversation_digest)?;
                let response = control
                    .session(&claims)
                    .field("run_id", json!(run_id))
                    .send("run.cancelled")
                    .await?;
                if response.run_id != run_id
                    || response.private_session_id != claims.data.private_session_id
                {
                    return Err(Error::Internal);
                }
                self.finish(&run);
                Ok(public(json!({"run_id":run_id,"state":"cancelled"})))
            }
            Route::Create => unreachable!(),
        }
    }

    async fn start(
        &self,
        plan: &Plan,
        call: &Call,
        claims: Option<&Claims<Conversation>>,
        message: String,
    ) -> Result<(String, String, ControlResponse), Error> {
        if self.key.is_none() {
            return Err(Error::Unavailable);
        }
        let principal = call.principal_id().ok_or(Error::Unauthorized)?;
        let run = format!(
            "run_{}",
            crate::process::random_hex::<16>().map_err(|_| Error::Internal)?
        );
        let digest = claims.map_or_else(
            || token::conversation(&run),
            |claims| claims.conversation_digest.clone(),
        );
        let (request, response) = if claims.is_none() {
            ("conversation.create", "conversation.created")
        } else {
            ("conversation.turn", "conversation.turn.accepted")
        };
        let mut control = self.control(plan, request, principal, &digest)?;
        if let Some(claims) = claims {
            control = control.session(claims);
        }
        let run_key = RunKey::new(&plan.address, principal, &digest, &run);
        {
            let mut state = self.state.lock().expect("assistant state");
            if state.runs.len() >= MAX_RUNS {
                Self::sweep_locked(&mut state);
                while state.runs.len() >= MAX_RUNS {
                    let State {
                        runs, approvals, ..
                    } = &mut *state;
                    let Some(evicted) = runs
                        .iter()
                        .filter(|(key, existing)| {
                            !has_active_approval(approvals, key)
                                && existing.last_active.elapsed() >= RUN_IDLE_EVICT
                        })
                        .min_by_key(|(_, existing)| existing.last_active)
                        .map(|(key, _)| key.clone())
                    else {
                        return Err(Error::Unavailable);
                    };
                    runs.remove(&evicted);
                    approvals.retain(|(key, _), _| key != &evicted);
                }
            }
            let now = Instant::now();
            state.runs.insert(
                run_key.clone(),
                Run {
                    generation: call.generation.clone(),
                    principal: call.principal.clone(),
                    traceparent: Some(format!("00-{}-{}-01", call.trace_id, call.span_id)),
                    started: now,
                    last_active: now,
                },
            );
        }
        struct PendingRun<'a> {
            gateway: &'a Gateway,
            key: Option<RunKey>,
        }
        impl Drop for PendingRun<'_> {
            fn drop(&mut self) {
                if let Some(key) = self.key.take() {
                    self.gateway.finish(&key);
                }
            }
        }
        let mut pending = PendingRun {
            gateway: self,
            key: Some(run_key),
        };
        let response = control
            .field("run_id", json!(run))
            .field("message", json!(message))
            .send(response)
            .await
            .and_then(|result| {
                if result.run_id != run
                    || result.private_session_id.is_empty()
                    || result.continuation_token.is_empty()
                    || claims.is_some_and(|claims| {
                        claims.data.private_session_id != result.private_session_id
                    })
                {
                    return Err(Error::Internal);
                }
                Ok(result)
            })?;
        pending.key = None;
        Ok((run, digest, response))
    }

    fn remember_continuation(&self, id: &str, continuation: String) {
        let mut state = self.state.lock().expect("assistant state");
        if !state.conversations.contains_key(id)
            && state.conversations.len() >= MAX_CONVERSATIONS
            && let Some(oldest) = state
                .conversations
                .iter()
                .min_by_key(|(_, state)| state.updated)
                .map(|(id, _)| id.clone())
        {
            state.conversations.remove(&oldest);
        }
        state.conversations.insert(
            id.into(),
            ConversationState {
                continuation,
                updated: Some(Instant::now()),
            },
        );
    }

    async fn approve(
        &self,
        plan: &Plan,
        claims: &Claims<Conversation>,
        principal: &str,
        approval: Claims<Approval>,
        allow: bool,
    ) -> Result<(), Error> {
        let run = RunKey::new(
            &plan.address,
            principal,
            &claims.conversation_digest,
            &approval.data.run_id,
        );
        let key = (run, approval.data.approval_id.clone());
        let control = self.control(
            plan,
            "approval.resolve",
            principal,
            &claims.conversation_digest,
        )?;
        let local_tool;
        {
            let mut state = self.state.lock().expect("assistant state");
            if let Some(active) = state.runs.get_mut(&key.0) {
                active.last_active = Instant::now();
            }
            let pending = state
                .approvals
                .get_mut(&key)
                .filter(|pending| pending.decision == Decision::Pending)
                .ok_or(Error::Conflict)?;
            local_tool = pending.local_tool;
            if allow && local_tool && pending.input.is_none() {
                return Err(Error::Conflict);
            }
            pending.decision = if allow {
                Decision::Approved
            } else {
                Decision::Denied
            };
        }
        struct ApprovalGuard<'a>(&'a Mutex<State>, Option<(RunKey, String)>);
        impl Drop for ApprovalGuard<'_> {
            fn drop(&mut self) {
                if let Some(key) = self.1.take()
                    && let Ok(mut state) = self.0.lock()
                    && let Some(pending) = state.approvals.get_mut(&key)
                    && pending.decision == Decision::Approved
                {
                    pending.decision = Decision::Denied;
                }
            }
        }
        let mut guard = ApprovalGuard(&self.state, allow.then(|| key.clone()));
        let decision = if allow { "allow" } else { "deny" };
        let response = control
            .session(claims)
            .field("run_id", json!(approval.data.run_id))
            .field("approval_id", json!(approval.data.approval_id))
            .field("decision", json!(decision))
            .send("approval.resolved")
            .await;
        match response {
            Ok(response)
                if response.run_id == approval.data.run_id
                    && response.approval_id == approval.data.approval_id
                    && response.private_session_id == claims.data.private_session_id
                    && response.decision == decision =>
            {
                if let Some(key) = guard.1.take()
                    && allow
                    && !local_tool
                {
                    let mut state = self.state.lock().expect("assistant state");
                    if let Some(pending) = state.approvals.get_mut(&key)
                        && pending.decision == Decision::Approved
                    {
                        pending.decision = Decision::Consumed;
                    }
                }
                Ok(())
            }
            result => Err(result.err().unwrap_or(Error::Internal)),
        }
    }

    fn control<'a>(
        &'a self,
        plan: &Plan,
        typ: &str,
        principal: &str,
        digest: &str,
    ) -> Result<Control<'a>, Error> {
        let descriptor = self
            .descriptors
            .get(&plan.address)
            .ok_or(Error::Unavailable)?;
        let request_id = crate::process::random_hex::<16>().map_err(|_| Error::Internal)?;
        Ok(Control {
            descriptor,
            client: &self.client,
            input: json!({
                "kind": "scenery.assistant.control.request",
                "schema_revision": "sha256:eb03bc81084232c8d046780dd51041a069f8bc8cc5fc979f5a6d7106d17dd953",
                "type": typ, "request_id":request_id, "principal":principal, "conversation_digest":digest,
                "assistant_address":plan.address, "runtime_revision":plan.runtime_revision, "capability_revision":plan.capability_revision
            }),
        })
    }
}

async fn serve_tools(
    host: Arc<Host>,
    address: String,
    listener: TcpListener,
    stop: CancellationToken,
) -> Result<()> {
    let gateway = Arc::new(crate::mcp::Gateway::default());
    let mut connections = JoinSet::new();
    loop {
        tokio::select! {
            () = stop.cancelled() => break,
            completed = connections.join_next(), if !connections.is_empty() => { let _ = completed; }
            result = listener.accept() => {
                let (socket, remote) = match result {
                    Ok(pair) => pair,
                    Err(error) => {
                        crate::transport::accept_failed(error).await?;
                        continue;
                    }
                };
                if !remote.ip().is_loopback() { continue; }
                let host = host.clone();
                let address = address.clone();
                let gateway = gateway.clone();
                connections.spawn(async move {
                    let service = hyper::service::service_fn(|request| async {
                        let descriptor = &host.assistants.descriptors[&address];
                        let plan = host.bundle.host.assistants.iter().find(|plan| plan.address == address).expect("verified assistant");
                        let manifest = &host.bundle.host.mcp_servers[&plan.mcp_server];
                        Ok::<_, std::convert::Infallible>(gateway.serve(&host, descriptor, manifest, request).await)
                    });
                    let _ = hyper::server::conn::http1::Builder::new()
                        .serve_connection(hyper_util::rt::TokioIo::new(socket), service).await;
                });
            }
        }
    }
    connections.shutdown().await;
    Ok(())
}

struct Control<'a> {
    descriptor: &'a Descriptor,
    client: &'a reqwest::Client,
    input: Value,
}

impl Control<'_> {
    fn session(mut self, claims: &Claims<Conversation>) -> Self {
        self.input["private_session_id"] = json!(claims.data.private_session_id);
        self.input["continuation_token"] = json!(claims.data.continuation_token);
        self
    }

    fn field(mut self, key: &str, value: Value) -> Self {
        self.input[key] = value;
        self
    }

    async fn send(self, expected: &str) -> Result<ControlResponse, Error> {
        let url = self
            .descriptor
            .control_url()
            .ok_or(Error::Unavailable)?
            .join(CONTROL_PATH)
            .map_err(|_| Error::Unavailable)?;
        let response = self
            .client
            .post(url)
            .timeout(Duration::from_secs(15))
            .header(CONTROL_HEADER, &self.descriptor.control_token)
            .json(&self.input)
            .send()
            .await
            .map_err(|_| Error::Unavailable)?;
        let status = response.status();
        let data = crate::transport::read_response(response, CONTROL_LIMIT)
            .await
            .map_err(|_| Error::Internal)?;
        let output: ControlResponse =
            serde_json::from_value(crate::values::parse(&data).map_err(|_| Error::Internal)?)
                .map_err(|_| Error::Internal)?;
        if output.kind != "scenery.assistant.control.response"
            || output.schema_revision
                != "sha256:8b0cefb8d74e4097e7c058205e55b26675c671e2922b9df8fcd8a86403390e4d"
            || self.input["request_id"] != output.request_id
            || output.assistant_address != self.descriptor.assistant_address
            || output.runtime_revision != self.descriptor.runtime_revision
            || output.capability_revision != self.descriptor.capability_revision
        {
            return Err(Error::Unavailable);
        }
        if output.typ == "error" {
            return Err(match output.error["code"].as_str() {
                Some(code) if code.ends_with("_not_found") => Error::NotFound,
                Some("cancelled") => Error::Cancelled,
                Some("conflict") => Error::Conflict,
                _ => Error::Unavailable,
            });
        }
        if !status.is_success() || expected != output.typ {
            return Err(Error::Internal);
        }
        Ok(output)
    }
}

#[derive(Deserialize)]
struct ControlResponse {
    kind: String,
    schema_revision: String,
    #[serde(rename = "type")]
    typ: String,
    request_id: String,
    assistant_address: String,
    runtime_revision: String,
    capability_revision: String,
    #[serde(default)]
    private_session_id: String,
    #[serde(default)]
    continuation_token: String,
    #[serde(default)]
    run_id: String,
    #[serde(default)]
    approval_id: String,
    #[serde(default)]
    decision: String,
    #[serde(default)]
    error: Value,
}

async fn read_request(request: Request<Incoming>) -> Result<Value, Error> {
    let bytes = crate::transport::read_request(request, 128 << 10)
        .await
        .map_err(|_| Error::Invalid)?;
    crate::values::parse(&bytes).map_err(|_| Error::Invalid)
}

fn single_field(input: &Value) -> bool {
    input.as_object().is_some_and(|object| object.len() == 1)
}

async fn read_message(request: Request<Incoming>) -> Result<String, Error> {
    let input = read_request(request).await?;
    let msg = input["message"]
        .as_object()
        .filter(|map| map.len() == 2)
        .ok_or(Error::Invalid)?;
    if !single_field(&input) || msg.get("role").and_then(Value::as_str) != Some("user") {
        return Err(Error::Invalid);
    }
    Ok(msg
        .get("content")
        .and_then(Value::as_str)
        .filter(|text| !text.trim().is_empty() && text.len() <= 64 << 10)
        .ok_or(Error::Invalid)?
        .to_owned())
}

fn public(value: Value) -> Reply {
    let mut reply = json_reply(StatusCode::OK, &value);
    header(reply.headers_mut(), "cache-control", "no-store");
    reply
}

fn has_active_approval(
    approvals: &HashMap<(RunKey, String), PendingApproval>,
    key: &RunKey,
) -> bool {
    approvals
        .iter()
        .any(|((run, _), pending)| run == key && pending.decision == Decision::Pending)
}

pub(crate) use events::Error;

#[path = "assistant_events.rs"]
mod events;
