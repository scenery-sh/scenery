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

pub(crate) type Workers = BTreeMap<String, Arc<Worker>>;

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
            if let Some(worker) = active
                .as_ref()
                .and_then(|generation| generation.workers.get(&program.process))
                .filter(|worker| worker.still_matches(&program))
            {
                workers.insert(program.process, worker.clone());
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
                    workers.insert(worker.program.process.clone(), Arc::new(worker));
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
        for worker in candidate.generation.workers.values() {
            ensure!(
                worker.exited()?.is_none(),
                "worker exited before publication"
            );
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

    fn maintain(&mut self) {
        if self
            .pending
            .as_ref()
            .is_some_and(|p| p.prepared.elapsed() >= CANDIDATE_TIMEOUT)
        {
            self.discard();
        }
        self.retired.retain(|_, retired| {
            if Instant::now() >= retired.deadline {
                retired.generation.retired.cancel();
            }
            if Arc::strong_count(&retired.generation) == 1 {
                let generation = retired.generation.clone();
                tokio::task::spawn_blocking(move || drop(generation));
                return false;
            }
            true
        });
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
                .map(|(name, worker)| {
                    (
                        name.clone(),
                        WorkerStatus {
                            pid: worker.pid(),
                            digest: worker.program.digest.clone(),
                        },
                    )
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
        for worker in active.workers.values() {
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

    pub(crate) async fn replace(
        &self,
        plan: LaunchPlan,
        verify: impl FnOnce() -> Result<bool>,
    ) -> Result<Option<u64>> {
        let mut supervisor = self.state.lock().await;
        let candidate = supervisor.prepare(plan).await?;
        match verify() {
            Ok(true) => {
                if let Err(error) = supervisor.commit(candidate) {
                    supervisor.discard();
                    return Err(error);
                }
                Ok(Some(candidate))
            }
            checked => {
                supervisor.discard();
                checked.map(|_| None)
            }
        }
    }
}

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
        };
        Ok(Self {
            listener: UnixListener::bind(directory.join("supervisor.sock"))?,
            token: format!("Bearer {token}"),
            events,
            state: Arc::new(Mutex::new(state)),
        })
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
                        Err(error) => break Err(error.into()),
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
        for generation in active
            .into_iter()
            .chain(retired.into_values().map(|r| r.generation))
            .chain(pending.into_iter().map(|p| p.generation))
        {
            generation.retired.cancel();
            connections.spawn_blocking(move || drop(generation));
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
