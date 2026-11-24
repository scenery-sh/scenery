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
use tokio::sync::Mutex;
use tokio::task::JoinSet;
use tokio::time::{interval, timeout};

use crate::process::{BuildIdentity, DRAIN_TIMEOUT, Instance, Program, Worker, hex, random_hex};
use crate::transport::{Body, Client, read, response};

const GATEWAY_GENERATIONS: &str = "/__scenery/process/v1/generations";
const CONTROL_TIMEOUT: Duration = Duration::from_secs(3);
const CANDIDATE_TIMEOUT: Duration = Duration::from_secs(20);

type Workers = BTreeMap<String, Arc<Worker>>;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct LaunchPlan {
    contract_revision: String,
    programs: Vec<Program>,
}

#[derive(Serialize)]
struct Publication {
    generation: u64,
    contract_revision: String,
    identity: BuildIdentity,
    processes: BTreeMap<String, Instance>,
}

struct Candidate {
    workers: Workers,
    publication: Publication,
    prepared: Instant,
    attempted: bool,
    order: Vec<String>,
}

struct Retired {
    workers: Workers,
    deadline: Instant,
}

#[derive(Serialize)]
struct Status<'a> {
    current: u64,
    pending: Option<u64>,
    processes: BTreeMap<&'a str, WorkerStatus<'a>>,
    faults: &'a VecDeque<WorkerFault>,
}

#[derive(Serialize)]
struct WorkerStatus<'a> {
    pid: u32,
    digest: &'a str,
}

#[derive(Serialize)]
struct WorkerFault {
    sequence: u64,
    generation: u64,
    service: String,
    runtime: String,
    pid: u32,
    exit_code: Option<i32>,
    signal: Option<i32>,
}

#[derive(Deserialize)]
struct GatewayStatus {
    current: u64,
}

struct Supervisor {
    directory: PathBuf,
    gateway: Client,
    revision: Option<String>,
    current: u64,
    sequence: u64,
    active: Workers,
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
            self.revision
                .as_ref()
                .is_none_or(|rev| rev == &plan.contract_revision),
            "application contract changed",
        );
        ensure!(!plan.programs.is_empty(), "worker plan is empty");
        let mut names = std::collections::BTreeSet::new();
        for program in &plan.programs {
            ensure!(
                !program.process.is_empty()
                    && !program.service.is_empty()
                    && program.binary.is_absolute()
                    && names.insert(&program.process),
                "invalid or duplicate worker process",
            );
        }
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
            if let Some(worker) = self
                .active
                .get(&program.process)
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
        while let Some(worker) = started.join_next().await {
            let worker = worker??;
            workers.insert(worker.program.process.clone(), Arc::new(worker));
        }
        if workers.len() == self.active.len()
            && workers.iter().all(|(name, worker)| {
                self.active
                    .get(name)
                    .is_some_and(|active| Arc::ptr_eq(worker, active))
            })
        {
            return Ok(self.current);
        }
        self.sequence += 1;
        let digest = format!("sha256:{}", hex(&hash.finalize()));
        self.pending = Some(Candidate {
            publication: Publication {
                generation: self.sequence,
                identity: BuildIdentity {
                    contract_revision: plan.contract_revision.clone(),
                    implementation_revision: digest.clone(),
                    build_input_digest: digest,
                    go_target: "worker".into(),
                },
                contract_revision: plan.contract_revision,
                processes: workers
                    .iter()
                    .map(|(name, worker)| (name.clone(), worker.instance()))
                    .collect(),
            },
            workers,
            prepared: Instant::now(),
            attempted: false,
            order,
        });
        Ok(self.sequence)
    }

    async fn commit(&mut self, generation: u64) -> Result<()> {
        if self.current == generation {
            return Ok(());
        }
        let candidate = self.pending.as_mut().context("no prepared generation")?;
        ensure!(
            candidate.publication.generation == generation,
            "generation is not prepared"
        );
        if !candidate.attempted {
            for worker in candidate.workers.values() {
                if worker.exited()?.is_some() {
                    self.discard();
                    anyhow::bail!("worker exited before publication");
                }
            }
            candidate.attempted = true;
        }
        self.reconcile_publication().await
    }

    async fn reconcile_publication(&mut self) -> Result<()> {
        let candidate = self.pending.as_ref().context("no pending publication")?;
        let generation = candidate.publication.generation;
        let data = serde_json::to_vec(&candidate.publication)?;
        let published = timeout(
            CONTROL_TIMEOUT,
            self.gateway.request(Method::PUT, GATEWAY_GENERATIONS, data),
        )
        .await;
        let rejection = match published {
            Ok(Ok(reply)) if reply.status() == StatusCode::NO_CONTENT => {
                self.activate();
                return Ok(());
            }
            Ok(Ok(reply)) => {
                let status = reply.status();
                let bytes = timeout(CONTROL_TIMEOUT, read(reply.into_body(), 16 << 10)).await??;
                Some((status, bytes))
            }
            _ => None,
        };
        let status = timeout(CONTROL_TIMEOUT, async {
            let reply = self
                .gateway
                .request(Method::GET, GATEWAY_GENERATIONS, vec![])
                .await?;
            ensure!(
                reply.status() == StatusCode::OK,
                "gateway status unavailable"
            );
            let bytes = read(reply.into_body(), MAX_METADATA).await?;
            Ok::<_, anyhow::Error>(serde_json::from_slice::<GatewayStatus>(&bytes)?)
        })
        .await??;
        if status.current == generation {
            self.activate();
            return Ok(());
        }
        ensure!(
            status.current == self.current,
            "gateway published an unexpected generation"
        );
        if let Some((status, bytes)) = rejection.filter(|(status, _)| status.is_client_error()) {
            self.discard();
            anyhow::bail!(
                "gateway rejected generation: {status}: {}",
                String::from_utf8_lossy(&bytes).trim()
            );
        }
        // A timeout or transport error does not establish whether the gateway
        // received the publication. Keep its workers alive until reconciled.
        anyhow::bail!("gateway publication is awaiting confirmation");
    }

    fn activate(&mut self) {
        let candidate = self.pending.take().expect("candidate already validated");
        let previous = self.current;
        self.current = candidate.publication.generation;
        self.revision = Some(candidate.publication.contract_revision);
        self.order = candidate.order;
        let workers = std::mem::replace(&mut self.active, candidate.workers);
        if previous > 0 {
            self.retired.insert(
                previous,
                Retired {
                    workers,
                    deadline: Instant::now() + DRAIN_TIMEOUT,
                },
            );
        }
    }

    fn discard(&mut self) {
        if let Some(candidate) = self.pending.take() {
            tokio::task::spawn_blocking(move || drop(candidate));
        }
    }

    async fn maintain(&mut self) -> Result<()> {
        if let Some(candidate) = &self.pending {
            if candidate.attempted {
                self.reconcile_publication().await?;
            } else if candidate.prepared.elapsed() >= CANDIDATE_TIMEOUT {
                self.discard();
            }
        }

        for (generation, retired) in &self.retired {
            let force = if Instant::now() >= retired.deadline {
                "?force=true"
            } else {
                ""
            };
            let path = format!("{GATEWAY_GENERATIONS}/{generation}{force}");
            let result = timeout(
                CONTROL_TIMEOUT,
                self.gateway.request(Method::DELETE, &path, vec![]),
            )
            .await;
            if matches!(result, Ok(Ok(reply)) if matches!(reply.status(), StatusCode::NO_CONTENT | StatusCode::NOT_FOUND))
            {
                let generation = *generation;
                let retired = self.retired.remove(&generation);
                tokio::task::spawn_blocking(move || drop(retired));
                break;
            }
        }
        Ok(())
    }

    fn status(&self) -> Status<'_> {
        Status {
            current: self.current,
            pending: self.pending.as_ref().map(|p| p.publication.generation),
            processes: self
                .active
                .iter()
                .map(|(name, worker)| {
                    (
                        name.as_str(),
                        WorkerStatus {
                            pid: worker.pid(),
                            digest: &worker.program.digest,
                        },
                    )
                })
                .collect(),
            faults: &self.faults,
        }
    }

    async fn restart(&mut self) -> Result<()> {
        let mut stopped = false;
        for worker in self.active.values() {
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
                generation: self.current,
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
            let revision = self.revision.clone().context("missing active revision")?;
            let programs = self
                .order
                .iter()
                .map(|name| self.active[name].program.clone())
                .collect();
            let generation = self
                .prepare(LaunchPlan {
                    contract_revision: revision,
                    programs,
                })
                .await?;
            self.commit(generation).await?;
        }
        Ok(())
    }
}

pub(crate) struct Control {
    listener: UnixListener,
    token: String,
    state: Arc<Mutex<Supervisor>>,
}

impl Control {
    pub(crate) fn bind(directory: &Path) -> Result<Self> {
        let metadata = symlink_metadata(directory)?;
        ensure!(
            metadata.is_dir()
                && metadata.uid() == unsafe { libc::geteuid() }
                && metadata.mode() & 0o077 == 0,
            "supervisor requires a private runtime directory",
        );
        let token = random_hex::<32>()?;
        OpenOptions::new()
            .create_new(true)
            .write(true)
            .mode(0o600)
            .open(directory.join("supervisor.token"))?
            .write_all(token.as_bytes())?;
        let gateway = Client::new(directory.join("control.sock"), &token);
        let state = Supervisor {
            directory: directory.to_owned(),
            gateway,
            revision: None,
            current: 0,
            sequence: 0,
            active: Workers::new(),
            order: Vec::new(),
            pending: None,
            retired: BTreeMap::new(),
            faults: VecDeque::new(),
            fault_sequence: 0,
        };
        Ok(Self {
            listener: UnixListener::bind(directory.join("supervisor.sock"))?,
            token: format!("Bearer {token}"),
            state: Arc::new(Mutex::new(state)),
        })
    }

    pub(crate) async fn serve(self, stop: impl Future<Output = ()>) -> Result<()> {
        tokio::pin!(stop);
        let mut connections = JoinSet::new();
        let mut maintenance = interval(Duration::from_millis(200));
        maintenance.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let state = self.state.clone();
        let monitor = tokio::spawn(async move {
            loop {
                maintenance.tick().await;
                let mut inner = state.lock().await;
                if let Err(error) = inner.maintain().await {
                    eprintln!("worker generation reconciliation failed: {error:#}");
                }
                if let Err(error) = inner.restart().await {
                    eprintln!("worker restart failed: {error:#}");
                    drop(inner);
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
        let workers = std::mem::take(&mut state.active);
        let retired = std::mem::take(&mut state.retired);
        let pending = state.pending.take();
        drop(state);
        for worker in workers
            .into_values()
            .chain(retired.into_values().flat_map(|r| r.workers.into_values()))
            .chain(pending.into_iter().flat_map(|p| p.workers.into_values()))
        {
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
                supervisor.commit(generation).await.map(|()| vec![])
            } else {
                if supervisor
                    .pending
                    .as_ref()
                    .is_some_and(|p| p.publication.generation == generation && !p.attempted)
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
