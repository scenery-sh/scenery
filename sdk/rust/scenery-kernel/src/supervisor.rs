use std::collections::{BTreeMap, VecDeque};
use std::convert::Infallible;
use std::fs::{OpenOptions, symlink_metadata};
use std::io::Write;
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, Instant};

use anyhow::{Context, Result, ensure};
use hyper::body::Incoming;
use hyper::header::AUTHORIZATION;
use hyper::service::service_fn;
use hyper::{Method, Request, Response, StatusCode};
use hyper_util::rt::{TokioExecutor, TokioIo};
use scenery_wire::MAX_METADATA;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use tokio::net::UnixListener;
use tokio::sync::{Mutex, watch};
use tokio::task::JoinSet;
use tokio::time::interval;
use tokio_util::sync::CancellationToken;

use crate::process::{BuildIdentity, DRAIN_TIMEOUT, Program, Worker, hex, random_hex};
use crate::transport::{Body, read, response};

const CANDIDATE_TIMEOUT: Duration = Duration::from_secs(20);

pub(crate) type Workers = BTreeMap<String, Arc<WorkerSlot>>;

enum SlotState {
    Cold,
    Starting(watch::Receiver<Option<std::result::Result<Arc<Worker>, String>>>),
    Ready(Arc<Worker>),
}

pub(crate) struct WorkerSlot {
    pub(crate) program: Program,
    directory: PathBuf,
    state: std::sync::Mutex<SlotState>,
    /// Cancelled once no serving or draining generation references this slot.
    pub(crate) retired: CancellationToken,
}

impl WorkerSlot {
    pub(crate) fn warm(directory: PathBuf, worker: Arc<Worker>) -> Self {
        Self {
            program: worker.program.clone(),
            directory,
            state: std::sync::Mutex::new(SlotState::Ready(worker)),
            retired: CancellationToken::new(),
        }
    }

    pub(crate) fn cold(directory: PathBuf, program: Program) -> Self {
        Self {
            program,
            directory,
            state: std::sync::Mutex::new(SlotState::Cold),
            retired: CancellationToken::new(),
        }
    }

    pub(crate) fn running(&self) -> Option<Arc<Worker>> {
        match &*self.state.lock().expect("worker slot") {
            SlotState::Ready(worker) => Some(worker.clone()),
            SlotState::Cold | SlotState::Starting(_) => None,
        }
    }

    pub(crate) fn starting(&self) -> Option<watch::Receiver<Option<std::result::Result<Arc<Worker>, String>>>> {
        match &*self.state.lock().expect("worker slot") {
            SlotState::Starting(rx) => Some(rx.clone()),
            SlotState::Cold | SlotState::Ready(_) => None,
        }
    }

    pub(crate) fn is_started(&self) -> bool {
        !matches!(*self.state.lock().expect("worker slot"), SlotState::Cold)
    }

    pub(crate) fn still_matches(&self, program: &Program) -> bool {
        let same_program = self.program.digest == program.digest
            && self.program.identity == program.identity
            && self.program.service == program.service
            && self.program.runtime == program.runtime
            && self.program.environment == program.environment
            && self.program.startup.as_ref().map(|config| config.get())
                == program.startup.as_ref().map(|config| config.get());
        if !same_program {
            return false;
        }
        match &*self.state.lock().expect("worker slot") {
            SlotState::Cold | SlotState::Starting(_) => true,
            SlotState::Ready(worker) => matches!(worker.exited(), Ok(None)),
        }
    }

    /// Returns the running worker for this slot, spawning it on demand if it
    /// has not been started yet. Concurrent callers coalesce onto a single
    /// startup attempt, and cancelling one caller does not abort startup for
    /// others.
    pub(crate) async fn ensure(self: &Arc<Self>) -> std::result::Result<Arc<Worker>, String> {
        let mut rx = {
            let mut guard = self.state.lock().expect("worker slot");
            match &*guard {
                SlotState::Ready(worker) => return Ok(worker.clone()),
                SlotState::Starting(rx) => rx.clone(),
                SlotState::Cold => {
                    if self.retired.is_cancelled() {
                        return Err("worker generation retired".into());
                    }
                    let (tx, rx) = watch::channel(None);
                    *guard = SlotState::Starting(rx.clone());
                    let slot = Arc::clone(self);
                    tokio::spawn(async move {
                        let capabilities = slot.directory.join("capabilities.sock");
                        let outcome =
                            Worker::spawn(&slot.directory, &capabilities, slot.program.clone())
                                .await;
                        match outcome {
                            Ok(worker) => {
                                let worker = Arc::new(worker);
                                let retired = {
                                    let mut guard = slot.state.lock().expect("worker slot");
                                    if slot.retired.is_cancelled() {
                                        *guard = SlotState::Cold;
                                        true
                                    } else {
                                        *guard = SlotState::Ready(worker.clone());
                                        false
                                    }
                                };
                                if retired {
                                    worker.retired.cancel();
                                    let stopped = worker.clone();
                                    let _ = tokio::task::spawn_blocking(move || stopped.stop_process()).await;
                                    let _ = tx.send(Some(Err("worker generation retired".into())));
                                } else {
                                    let _ = tx.send(Some(Ok(worker)));
                                }
                            }
                            Err(error) => {
                                let message = format!("{error:#}");
                                eprintln!(
                                    "on-demand worker {} failed to start: {message}",
                                    slot.program.service
                                );
                                {
                                    let mut guard = slot.state.lock().expect("worker slot");
                                    *guard = SlotState::Cold;
                                }
                                let _ = tx.send(Some(Err(message)));
                            }
                        }
                    });
                    rx
                }
            }
        };
        let value = rx
            .wait_for(Option::is_some)
            .await
            .map_err(|_| "worker startup aborted".to_owned())?;
        value.as_ref().expect("watch predicate ensured Some").clone()
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct LaunchPlan {
    pub contract_revision: String,
    pub programs: Vec<Program>,
}

pub(crate) struct Generation {
    pub number: u64,
    pub identity: BuildIdentity,
    pub workers: Workers,
    pub retired: CancellationToken,
}

struct Candidate {
    generation: Arc<Generation>,
    prepared: Instant,
    order: Vec<String>,
}

struct Retired {
    generation: Arc<Generation>,
    deadline: Instant,
}

#[derive(Serialize)]
struct Status {
    current: u64,
    pending: Option<u64>,
    processes: BTreeMap<String, WorkerStatus>,
    faults: VecDeque<WorkerFault>,
}

#[derive(Serialize)]
struct WorkerStatus {
    pid: u32,
    digest: String,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    lazy: bool,
}

#[derive(Clone, Serialize)]
struct WorkerFault {
    sequence: u64,
    generation: u64,
    service: String,
    runtime: String,
    pid: u32,
    exit_code: Option<i32>,
    signal: Option<i32>,
}

struct Supervisor {
    directory: PathBuf,
    publication: watch::Sender<Option<Arc<Generation>>>,
    revision: String,
    expected: BTreeMap<String, (String, String)>,
    sequence: u64,
    order: Vec<String>,
    pending: Option<Candidate>,
    retired: BTreeMap<u64, Retired>,
    faults: VecDeque<WorkerFault>,
    fault_sequence: u64,
    lazy: bool,
}

impl Supervisor {
    async fn prepare(&mut self, plan: LaunchPlan) -> Result<u64> {
        ensure!(self.pending.is_none(), "a candidate is already pending");
        ensure!(
            !plan.contract_revision.is_empty(),
            "missing contract revision"
        );
        ensure!(
            self.revision == plan.contract_revision,
            "application contract changed",
        );
        ensure!(
            plan.programs.len() == self.expected.len(),
            "worker plan is incomplete"
        );
        let mut names = std::collections::BTreeSet::new();
        for program in &plan.programs {
            ensure!(
                self.expected
                    .get(&program.process)
                    .is_some_and(|(service, runtime)| {
                        service == &program.service && runtime == &program.runtime
                    })
                    && program.binary.is_absolute()
                    && names.insert(&program.process),
                "invalid or duplicate worker process",
            );
        }
        let active = self.publication.borrow().clone();
        let order = plan
            .programs
            .iter()
            .map(|program| program.process.clone())
            .collect();
        let mut workers = Workers::new();
        let mut started = JoinSet::new();
        let mut hash = Sha256::new();
        for program in plan.programs {
            hash.update(program.digest.as_bytes());
            if let Some(slot) = active
                .as_ref()
                .and_then(|generation| generation.workers.get(&program.process))
            {
                if slot.still_matches(&program) {
                    workers.insert(program.process, slot.clone());
                    continue;
                }
                if self.lazy && !slot.is_started() {
                    workers.insert(
                        program.process.clone(),
                        Arc::new(WorkerSlot::cold(self.directory.clone(), program)),
                    );
                    continue;
                }
            } else if self.lazy {
                workers.insert(
                    program.process.clone(),
                    Arc::new(WorkerSlot::cold(self.directory.clone(), program)),
                );
                continue;
            }
            let directory = self.directory.clone();
            started.spawn(async move {
                Worker::spawn(&directory, &directory.join("capabilities.sock"), program).await
            });
        }
        let mut failure = None;
        while let Some(result) = started.join_next().await {
            match result
                .map_err(anyhow::Error::from)
                .and_then(std::convert::identity)
            {
                Ok(worker) => {
                    workers.insert(
                        worker.program.process.clone(),
                        Arc::new(WorkerSlot::warm(self.directory.clone(), Arc::new(worker))),
                    );
                }
                Err(error) => {
                    failure.get_or_insert(error);
                }
            }
        }
        if let Some(error) = failure {
            tokio::task::spawn_blocking(move || drop(workers));
            return Err(error);
        }
        if let Some(active) = active
            && workers.len() == active.workers.len()
            && workers.iter().all(|(name, worker)| {
                active
                    .workers
                    .get(name)
                    .is_some_and(|active| Arc::ptr_eq(worker, active))
            })
        {
            return Ok(active.number);
        }
        self.sequence += 1;
        let digest = format!("sha256:{}", hex(&hash.finalize()));
        self.pending = Some(Candidate {
            generation: Arc::new(Generation {
                number: self.sequence,
                identity: BuildIdentity {
                    contract_revision: plan.contract_revision,
                    implementation_revision: digest.clone(),
                    build_input_digest: digest,
                    go_target: "worker".into(),
                },
                workers,
                retired: CancellationToken::new(),
            }),
            prepared: Instant::now(),
            order,
        });
        Ok(self.sequence)
    }

    fn commit(&mut self, generation: u64) -> Result<()> {
        if self
            .publication
            .borrow()
            .as_ref()
            .is_some_and(|active| active.number == generation)
        {
            return Ok(());
        }
        let candidate = self.pending.as_ref().context("no prepared generation")?;
        ensure!(
            candidate.generation.number == generation,
            "generation is not prepared"
        );
        for slot in candidate.generation.workers.values() {
            if let Some(worker) = slot.running() {
                ensure!(
                    worker.exited()?.is_none(),
                    "worker exited before publication"
                );
            }
        }
        let candidate = self.pending.take().expect("candidate already validated");
        self.order = candidate.order;
        if let Some(previous) = self.publication.send_replace(Some(candidate.generation)) {
            self.retired.insert(
                previous.number,
                Retired {
                    generation: previous,
                    deadline: Instant::now() + DRAIN_TIMEOUT,
                },
            );
        }
        Ok(())
    }

    fn discard(&mut self) {
        if let Some(candidate) = self.pending.take() {
            tokio::task::spawn_blocking(move || drop(candidate));
        }
    }

    /// Discards the pending candidate only if it is `generation`: a prepare
    /// that reused the active generation left no candidate of its own.
    fn discard_candidate(&mut self, generation: u64) {
        if self
            .pending
            .as_ref()
            .is_some_and(|pending| pending.generation.number == generation)
        {
            self.discard();
        }
    }

    fn maintain(&mut self) {
        if self
            .pending
            .as_ref()
            .is_some_and(|p| p.prepared.elapsed() >= CANDIDATE_TIMEOUT)
        {
            self.discard();
        }
        let now = Instant::now();
        let active = self.publication.borrow().clone();
        let stopped_workers = retire_workers(
            self.retired
                .values()
                .map(|retired| (&*retired.generation, now >= retired.deadline)),
            active
                .iter()
                .chain(self.pending.iter().map(|pending| &pending.generation))
                .map(|generation| &**generation),
        );
        let expired_keys: Vec<u64> = self
            .retired
            .iter()
            .filter_map(|(&number, retired)| {
                (Arc::strong_count(&retired.generation) == 1).then_some(number)
            })
            .collect();
        let mut expired_generations = Vec::with_capacity(expired_keys.len());
        for key in expired_keys {
            if let Some(retired) = self.retired.remove(&key) {
                expired_generations.push(retired.generation);
            }
        }
        if !stopped_workers.is_empty() || !expired_generations.is_empty() {
            tokio::task::spawn_blocking(move || {
                for worker in stopped_workers {
                    worker.stop_process();
                }
                drop(expired_generations);
            });
        }
    }

    fn status(&self) -> Status {
        let active = self.publication.borrow();
        Status {
            current: active.as_ref().map_or(0, |a| a.number),
            pending: self.pending.as_ref().map(|p| p.generation.number),
            processes: active
                .as_ref()
                .into_iter()
                .flat_map(|a| a.workers.iter())
                .map(|(name, slot)| {
                    let status = match slot.running() {
                        Some(worker) => WorkerStatus {
                            pid: worker.pid(),
                            digest: worker.program.digest.clone(),
                            lazy: false,
                        },
                        None => WorkerStatus {
                            pid: 0,
                            digest: slot.program.digest.clone(),
                            lazy: true,
                        },
                    };
                    (name.clone(), status)
                })
                .collect(),
            faults: self.faults.clone(),
        }
    }

    async fn restart(&mut self) -> Result<()> {
        let Some(active) = self.publication.borrow().clone() else {
            return Ok(());
        };
        let mut stopped = false;
        for slot in active.workers.values() {
            let Some(worker) = slot.running() else {
                continue;
            };
            let Some(status) = worker.exited()? else {
                continue;
            };
            stopped = true;
            if !worker.record_fault() {
                continue;
            }
            self.fault_sequence += 1;
            let fault = WorkerFault {
                sequence: self.fault_sequence,
                generation: active.number,
                service: worker.program.service.clone(),
                runtime: worker.program.runtime.clone(),
                pid: worker.pid(),
                exit_code: status.code,
                signal: status.signal,
            };
            eprintln!("worker_fault {}", serde_json::to_string(&fault)?);
            if self.faults.len() == 64 {
                self.faults.pop_front();
            }
            self.faults.push_back(fault);
        }
        if stopped && self.pending.is_none() {
            let revision = self.revision.clone();
            let programs = self
                .order
                .iter()
                .map(|name| active.workers[name].program.clone())
                .collect();
            let generation = self
                .prepare(LaunchPlan {
                    contract_revision: revision,
                    programs,
                })
                .await?;
            self.commit(generation)?;
        }
        Ok(())
    }
}

/// Ends the calls of processes that left service. `retired` pairs each
/// replaced generation with whether its drain window is over; `serving` are
/// the active and the pending generation. A process still shared with one of
/// those, or with a generation that is still draining, keeps its calls: only
/// the processes a swap really replaced stop serving.
fn retire_workers<'a>(
    retired: impl Iterator<Item = (&'a Generation, bool)> + Clone,
    serving: impl Iterator<Item = &'a Generation>,
) -> Vec<Arc<Worker>> {
    let active_generations: Vec<&'a Generation> = serving
        .chain(
            retired
                .clone()
                .filter(|(_, drained)| !drained)
                .map(|(generation, _)| generation),
        )
        .collect();
    let in_use_slots: Vec<*const WorkerSlot> = active_generations
        .iter()
        .flat_map(|generation| generation.workers.values().map(Arc::as_ptr))
        .collect();
    let in_use_workers: Vec<*const Worker> = active_generations
        .iter()
        .flat_map(|generation| {
            generation
                .workers
                .values()
                .filter_map(|slot| slot.running().map(|worker| Arc::as_ptr(&worker)))
        })
        .collect();
    let mut stopped = Vec::new();
    for (generation, _) in retired.filter(|(_, drained)| *drained) {
        generation.retired.cancel();
        for slot in generation.workers.values() {
            if !in_use_slots.contains(&Arc::as_ptr(slot)) {
                slot.retired.cancel();
            }
            if let Some(worker) = slot.running()
                && !in_use_workers.contains(&Arc::as_ptr(&worker))
            {
                worker.retired.cancel();
                if !worker.is_stopped() && !stopped.iter().any(|item| Arc::ptr_eq(item, &worker)) {
                    stopped.push(worker);
                }
            }
        }
    }
    stopped
}

#[derive(Clone)]
pub(crate) struct Publisher {
    state: Arc<Mutex<Supervisor>>,
    publication: watch::Receiver<Option<Arc<Generation>>>,
}

impl Publisher {
    pub(crate) fn generation(&self) -> u64 {
        self.publication
            .borrow()
            .as_ref()
            .map_or(0, |active| active.number)
    }

    /// Starts the workers of `plan` and publishes them if `verify` still
    /// holds once they are ready. The check runs without the supervisor
    /// lock: it may read the source tree, and worker restarts, status
    /// requests and every request the host serves must not wait for it.
    pub(crate) async fn replace<Check>(
        &self,
        plan: LaunchPlan,
        verify: impl FnOnce() -> Check,
    ) -> Result<Option<u64>>
    where
        Check: Future<Output = Result<bool>>,
    {
        let candidate = self.state.lock().await.prepare(plan).await?;
        let verified = verify().await;
        let mut supervisor = self.state.lock().await;
        let published = match verified {
            Ok(true) => supervisor.commit(candidate).map(|()| Some(candidate)),
            checked => checked.map(|_| None),
        };
        if !matches!(published, Ok(Some(_))) {
            supervisor.discard_candidate(candidate);
        }
        published
    }
}

#[derive(Clone)]
pub(crate) struct EventTarget {
    pub path: PathBuf,
    pub app: serde_json::Value,
}

pub(crate) struct Control {
    events: Option<EventTarget>,
    listener: UnixListener,
    token: String,
    state: Arc<Mutex<Supervisor>>,
}

impl Control {
    pub(crate) fn bind(
        directory: &Path,
        revision: String,
        token: Option<String>,
        expected: BTreeMap<String, (String, String)>,
        events: Option<EventTarget>,
    ) -> Result<Self> {
        let metadata = symlink_metadata(directory)?;
        ensure!(
            metadata.is_dir()
                && metadata.uid() == unsafe { libc::geteuid() }
                && metadata.mode() & 0o077 == 0,
            "supervisor requires a private runtime directory",
        );
        let token = token.map_or_else(random_hex::<32>, Ok)?;
        ensure!(token.len() >= 32, "control token is too short");
        OpenOptions::new()
            .create_new(true)
            .write(true)
            .mode(0o600)
            .open(directory.join("supervisor.token"))?
            .write_all(token.as_bytes())?;
        let (publication, _) = watch::channel(None);
        let state = Supervisor {
            directory: directory.to_owned(),
            publication,
            revision,
            expected,
            sequence: 0,
            order: Vec::new(),
            pending: None,
            retired: BTreeMap::new(),
            faults: VecDeque::new(),
            fault_sequence: 0,
            lazy: false,
        };
        Ok(Self {
            listener: UnixListener::bind(directory.join("supervisor.sock"))?,
            token: format!("Bearer {token}"),
            events,
            state: Arc::new(Mutex::new(state)),
        })
    }

    pub(crate) fn with_lazy(self, lazy: bool) -> Self {
        self.state.try_lock().expect("uncontended supervisor").lazy = lazy;
        self
    }

    pub(crate) fn token(&self) -> &str {
        &self.token
    }

    pub(crate) async fn publication(&self) -> watch::Receiver<Option<Arc<Generation>>> {
        self.state.lock().await.publication.subscribe()
    }

    pub(crate) fn publisher(
        &self,
        publication: watch::Receiver<Option<Arc<Generation>>>,
    ) -> Publisher {
        Publisher {
            state: self.state.clone(),
            publication,
        }
    }

    pub(crate) async fn publish_initial(&self, plan: LaunchPlan) -> Result<()> {
        let mut state = self.state.lock().await;
        let number = state.prepare(plan).await?;
        state.commit(number)
    }

    pub(crate) async fn serve(self, stop: impl Future<Output = ()>) -> Result<()> {
        tokio::pin!(stop);
        let mut connections = JoinSet::new();
        let mut maintenance = interval(Duration::from_millis(200));
        maintenance.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let state = self.state.clone();
        let events = self.events;
        let monitor = tokio::spawn(async move {
            let mut observed = 0;
            loop {
                maintenance.tick().await;
                let (restart, faults) = {
                    let mut inner = state.lock().await;
                    inner.maintain();
                    let restart = inner.restart().await;
                    let faults = if events.is_some() {
                        inner
                            .faults
                            .iter()
                            .filter(|fault| fault.sequence > observed)
                            .cloned()
                            .collect::<Vec<_>>()
                    } else {
                        Vec::new()
                    };
                    (restart, faults)
                };
                if let Some(target) = &events {
                    for fault in faults {
                        observed = fault.sequence;
                        let event = serde_json::json!({
                            "at": crate::host::stamp(time::OffsetDateTime::now_utc()),
                            "kind":"worker_fault","app":target.app,"generation":fault.generation,
                            "worker_fault":{
                                "module":fault.service.split("/service/").next().unwrap_or(""),
                                "runtime":fault.runtime,"exit_code":fault.exit_code.unwrap_or(-1),
                                "signal":fault.signal.map(|signal| signal.to_string()).unwrap_or_default(),
                            }
                        });
                        if let Err(error) = crate::dev::journal(&target.path, &event).await {
                            eprintln!("could not record worker fault: {error:#}");
                        }
                    }
                }
                if let Err(error) = restart {
                    eprintln!("worker restart failed: {error:#}");
                    tokio::time::sleep(Duration::from_secs(1)).await;
                }
            }
        });
        let result = loop {
            tokio::select! {
                () = &mut stop => break Ok(()),
                socket = self.listener.accept() => {
                    let (socket, _) = match socket {
                        Ok(pair) => pair,
                        Err(error) => match crate::transport::accept_failed(error).await {
                            Ok(()) => continue,
                            Err(error) => break Err(error.into()),
                        },
                    };
                    let token = self.token.clone();
                    let state = self.state.clone();
                    let service = service_fn(move |request| {
                        let state = state.clone();
                        let token = token.clone();
                        async move { Ok::<_, Infallible>(handle(state, token, request).await) }
                    });
                    connections.spawn(async move {
                        let _ = hyper::server::conn::http2::Builder::new(TokioExecutor::new())
                            .serve_connection(TokioIo::new(socket), service)
                            .await;
                    });
                }
                _ = connections.join_next(), if !connections.is_empty() => {}
            }
        };
        monitor.abort();
        let _ = monitor.await;
        connections.shutdown().await;
        let mut state = self.state.lock().await;
        let active = state.publication.send_replace(None);
        let retired = std::mem::take(&mut state.retired);
        let pending = state.pending.take();
        drop(state);
        let mut workers = BTreeMap::new();
        let mut starting = Vec::new();
        for generation in active
            .into_iter()
            .chain(retired.into_values().map(|r| r.generation))
            .chain(pending.into_iter().map(|p| p.generation))
        {
            generation.retired.cancel();
            for slot in generation.workers.values() {
                slot.retired.cancel();
                if let Some(worker) = slot.running() {
                    worker.retired.cancel();
                    workers.entry(worker.pid()).or_insert(worker);
                } else if let Some(rx) = slot.starting() {
                    starting.push(rx);
                }
            }
        }
        for mut rx in starting {
            let _ = tokio::time::timeout(DRAIN_TIMEOUT, rx.wait_for(Option::is_some)).await;
        }
        // Give every owned group the same drain window, even if a request
        // still holds its generation or several workers have slow shutdowns.
        for worker in workers.values() {
            worker.signal_shutdown(false);
        }
        let deadline = Instant::now() + DRAIN_TIMEOUT;
        while Instant::now() < deadline
            && workers
                .values()
                .any(|worker| matches!(worker.exited(), Ok(None)))
        {
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
        for worker in workers.into_values() {
            worker.signal_shutdown(true);
            connections.spawn_blocking(move || drop(worker));
        }
        while connections.join_next().await.is_some() {}
        result
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Selection {
    generation: u64,
}

async fn handle(
    state: Arc<Mutex<Supervisor>>,
    token: String,
    request: Request<Incoming>,
) -> Response<Body> {
    let authorization = request.headers().get_all(AUTHORIZATION);
    let mut values = authorization.iter();
    if !values
        .next()
        .is_some_and(|value| bool::from(value.as_bytes().ct_eq(token.as_bytes())))
        || values.next().is_some()
    {
        return response(StatusCode::UNAUTHORIZED, "");
    }
    let kind = (request.method().clone(), request.uri().path().to_owned());
    let body = match read(request.into_body(), MAX_METADATA).await {
        Ok(body) => body,
        Err(_) => return response(StatusCode::BAD_REQUEST, "invalid control body"),
    };
    let mut supervisor = state.lock().await;
    let result = match (&kind.0, kind.1.as_str()) {
        (&Method::GET, "/status") => serde_json::to_vec(&supervisor.status()).map_err(Into::into),
        (&Method::PUT, "/candidate") => match serde_json::from_slice(&body) {
            Ok(plan) => supervisor.prepare(plan).await.and_then(|generation| {
                Ok(serde_json::to_vec(
                    &serde_json::json!({ "generation": generation }),
                )?)
            }),
            Err(_) => return response(StatusCode::BAD_REQUEST, "invalid launch plan"),
        },
        (&Method::PUT | &Method::DELETE, "/candidate/selection") => {
            let Ok(Selection { generation }) = serde_json::from_slice(&body) else {
                return response(StatusCode::BAD_REQUEST, "invalid candidate selection");
            };
            if kind.0 == Method::PUT {
                supervisor.commit(generation).map(|()| vec![])
            } else {
                if supervisor
                    .pending
                    .as_ref()
                    .is_some_and(|p| p.generation.number == generation)
                {
                    supervisor.discard();
                }
                Ok(vec![])
            }
        }
        _ => return response(StatusCode::NOT_FOUND, ""),
    };
    match result {
        Ok(body) => response(StatusCode::OK, body),
        Err(error) => response(StatusCode::CONFLICT, format!("{error:#}")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn generation(number: u64, workers: &[(&str, &Arc<Worker>)]) -> Generation {
        Generation {
            number,
            identity: BuildIdentity {
                contract_revision: "contract".into(),
                implementation_revision: String::new(),
                build_input_digest: String::new(),
                go_target: "worker".into(),
            },
            workers: workers
                .iter()
                .map(|(name, worker)| {
                    (
                        (*name).to_owned(),
                        Arc::new(WorkerSlot::warm(PathBuf::new(), (*worker).clone())),
                    )
                })
                .collect(),
            retired: CancellationToken::new(),
        }
    }

    #[tokio::test]
    async fn the_supervisor_answers_while_a_publication_is_verified() -> Result<()> {
        use std::os::unix::fs::PermissionsExt;
        let directory = tempfile::Builder::new()
            .prefix("sock-")
            .permissions(std::fs::Permissions::from_mode(0o700))
            .tempdir_in("/tmp")?;
        let control = Control::bind(
            directory.path(),
            "contract".into(),
            None,
            BTreeMap::new(),
            None,
        )?;
        let publisher = control.publisher(control.publication().await);
        let (entered, checking) = tokio::sync::oneshot::channel();
        let (finish, finished) = tokio::sync::oneshot::channel::<()>();
        let replacing = tokio::spawn({
            let publisher = publisher.clone();
            async move {
                let plan = LaunchPlan {
                    contract_revision: "contract".into(),
                    programs: vec![],
                };
                publisher
                    .replace(plan, || async move {
                        let _ = entered.send(());
                        let _ = finished.await;
                        Ok(true)
                    })
                    .await
            }
        });
        checking.await?;
        // The source check is still running, yet the supervisor lock is
        // free: this used to wait until the whole tree had been hashed.
        tokio::time::timeout(Duration::from_secs(1), control.publication()).await?;
        assert_eq!(publisher.generation(), 0);
        let _ = finish.send(());
        assert_eq!(replacing.await??, Some(1));
        assert_eq!(publisher.generation(), 1);

        // A check that fails or reports a newer tree publishes nothing.
        let stale = LaunchPlan {
            contract_revision: "contract".into(),
            programs: vec![],
        };
        assert_eq!(
            publisher.replace(stale, || async { Ok(false) }).await?,
            None
        );
        assert_eq!(publisher.generation(), 1);
        Ok(())
    }

    #[test]
    fn a_swap_ends_only_the_calls_of_replaced_workers() -> Result<()> {
        let directory = tempfile::Builder::new()
            .prefix("sock-")
            .tempdir_in("/tmp")?;
        let worker = || Worker::test_worker(directory.path()).map(Arc::new);
        let (orders, billing, rebuilt) = (worker()?, worker()?, worker()?);
        // Generation 2 rebuilt `orders` and carried `billing` over.
        let first = generation(1, &[("orders", &orders), ("billing", &billing)]);
        let second = generation(2, &[("orders", &rebuilt), ("billing", &billing)]);

        retire_workers([(&first, false)].into_iter(), [&second].into_iter());
        assert!(!orders.retired.is_cancelled(), "the drain window is open");

        retire_workers([(&first, true)].into_iter(), [&second].into_iter());
        assert!(first.retired.is_cancelled());
        assert!(orders.retired.is_cancelled(), "the replaced process");
        assert!(
            !billing.retired.is_cancelled() && !rebuilt.retired.is_cancelled(),
            "a healthy carried-over worker keeps its calls"
        );

        // Generation 3 replaces `billing`. Generation 2 is still draining,
        // so the long-drained generation 1 must not cut that window short.
        let replaced = worker()?;
        let third = generation(3, &[("orders", &rebuilt), ("billing", &replaced)]);
        retire_workers(
            [(&first, true), (&second, false)].into_iter(),
            [&third].into_iter(),
        );
        assert!(!billing.retired.is_cancelled());
        retire_workers(
            [(&first, true), (&second, true)].into_iter(),
            [&third].into_iter(),
        );
        assert!(billing.retired.is_cancelled());
        assert!(!rebuilt.retired.is_cancelled() && !replaced.retired.is_cancelled());
        Ok(())
    }

    #[tokio::test]
    async fn maintaining_retired_generations_stops_replaced_workers_while_an_old_generation_stays_held()
    -> Result<()> {
        let directory = tempfile::Builder::new()
            .prefix("sock-")
            .tempdir_in("/tmp")?;
        let worker = || Worker::test_worker(directory.path()).map(Arc::new);
        let (orders, billing, rebuilt) = (worker()?, worker()?, worker()?);
        let first = Arc::new(generation(1, &[("orders", &orders), ("billing", &billing)]));
        let second = Arc::new(generation(2, &[("orders", &orders), ("billing", &rebuilt)]));
        let held_generation = first.clone();

        let (publication, _) = watch::channel(Some(second));
        let mut supervisor = Supervisor {
            directory: directory.path().to_path_buf(),
            publication,
            revision: "contract".into(),
            expected: BTreeMap::new(),
            sequence: 2,
            order: vec!["orders".into(), "billing".into()],
            pending: None,
            retired: BTreeMap::from([(
                1,
                Retired {
                    generation: first,
                    deadline: Instant::now() - Duration::from_secs(1),
                },
            )]),
            faults: VecDeque::new(),
            fault_sequence: 0,
            lazy: false,
        };

        supervisor.maintain();
        tokio::time::timeout(Duration::from_secs(2), async {
            while !billing.is_stopped() {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await?;

        assert!(billing.retired.is_cancelled());
        assert!(billing.exited()?.is_some());
        assert!(!orders.is_stopped() && !rebuilt.is_stopped());
        assert!(!orders.retired.is_cancelled() && orders.exited()?.is_none());
        assert!(supervisor.retired.contains_key(&1));

        drop(held_generation);
        supervisor.maintain();
        assert!(supervisor.retired.is_empty());

        orders.stop_process();
        rebuilt.stop_process();
        Ok(())
    }

    #[tokio::test]
    async fn lazy_mode_keeps_workers_cold_until_needed_and_recovers_after_failed_spawn() -> Result<()> {
        use std::os::unix::fs::PermissionsExt;
        let directory = tempfile::Builder::new()
            .prefix("lazy-")
            .permissions(std::fs::Permissions::from_mode(0o700))
            .tempdir_in("/tmp")?;
        let bad_bin = directory.path().join("fail-worker");
        std::fs::write(&bad_bin, "#!/bin/sh\nexit 1\n")?;
        std::fs::set_permissions(&bad_bin, std::fs::Permissions::from_mode(0o700))?;

        let expected = BTreeMap::from([(
            "orders".to_owned(),
            ("orders/service/orders".to_owned(), "go".to_owned()),
        )]);
        let control = Control::bind(
            directory.path(),
            "contract".into(),
            None,
            expected,
            None,
        )?
        .with_lazy(true);
        let program = Program {
            process: "orders".into(),
            service: "orders/service/orders".into(),
            runtime: "go".into(),
            binary: bad_bin.clone(),
            digest: "sha256:v1".into(),
            identity: BuildIdentity {
                contract_revision: "contract".into(),
                implementation_revision: "sha256:v1".into(),
                build_input_digest: "sha256:v1".into(),
                go_target: "worker".into(),
            },
            environment: Vec::new(),
            startup: None,
            artifact: None,
        };
        control
            .publish_initial(LaunchPlan {
                contract_revision: "contract".into(),
                programs: vec![program.clone()],
            })
            .await?;

        // Initial publication in lazy mode spawns nothing and reports lazy: true, pid: 0.
        let status = control.state.lock().await.status();
        let worker_status = status.processes.get("orders").expect("orders status");
        assert_eq!(worker_status.pid, 0);
        assert!(worker_status.lazy);

        // Hot reload of a cold worker keeps it cold with the new digest.
        let mut updated = program.clone();
        updated.digest = "sha256:v2".into();
        let publisher = control.publisher(control.publication().await);
        let gen2 = publisher
            .replace(
                LaunchPlan {
                    contract_revision: "contract".into(),
                    programs: vec![updated],
                },
                || async { Ok(true) },
            )
            .await?;
        assert_eq!(gen2, Some(2));
        let status2 = control.state.lock().await.status();
        let worker_status2 = status2.processes.get("orders").expect("orders status gen2");
        assert_eq!(worker_status2.pid, 0);
        assert!(worker_status2.lazy);
        assert_eq!(worker_status2.digest, "sha256:v2");

        // Triggering ensure() on a failing binary returns Err and resets slot to Cold.
        let active = control.publication().await.borrow().clone().expect("active gen");
        let slot = active.workers.get("orders").expect("orders slot").clone();
        assert!(slot.ensure().await.is_err());
        assert!(!slot.is_started(), "failed spawn must reset slot to Cold for retry");
        Ok(())
    }
}
