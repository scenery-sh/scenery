mod abi;
mod auth;
mod auth_google;
mod auth_organizations;
mod auth_sessions;
mod browser_auth;
mod connections;
mod dev;
mod dev_build;
mod dev_inputs;
mod durable;
mod frontend;
mod gateway;
mod host;
mod http_codec;
mod local_storage;
mod otlp;
mod plan;
mod policy;
mod process;
mod schedule;
mod storage;
mod supervisor;
mod telemetry;
mod transport;
mod values;

use std::convert::Infallible;
use std::ffi::{OsStr, OsString};
use std::fs::{File, Permissions};
use std::io::{Read, Write};
use std::os::fd::{FromRawFd, OwnedFd, RawFd};
use std::os::unix::fs::{FileTypeExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use hyper::service::service_fn;
use hyper_util::rt::TokioIo;
use serde::Deserialize;
use serde_json::Value;
use tokio::net::{TcpListener, UnixListener};
use tokio::signal::unix::{SignalKind, signal};
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;

use crate::durable::Engine;
use crate::host::Host;
use crate::plan::{Bundle, bundle_path};
use crate::process::{CONFIG_SNAPSHOT_FD, DRAIN_TIMEOUT};
use crate::supervisor::{Control, LaunchPlan};

fn main() {
    if let Err(error) = bootstrap() {
        eprintln!("{error:#}");
        std::process::exit(1);
    }
}

fn bootstrap() -> Result<()> {
    let mut args = std::env::args_os().skip(1).peekable();
    let input = if args.peek().is_some_and(|argument| argument == "--dev") {
        args.next();
        let directory = args
            .next()
            .context("missing development runtime directory")?;
        let plan = args.next().context("missing development launch plan")?;
        let listen = args.next().context("missing development listener")?;
        ensure!(
            args.next().is_none(),
            "unexpected development host arguments"
        );
        let directory = PathBuf::from(directory);
        let file = File::open(&plan)?;
        let Development {
            mut bundle,
            snapshot,
            token,
            config,
        } = serde_json::from_reader(file.take(36 << 20))?;
        let resources = DevResources::inherit(&config, directory.clone())?;
        std::fs::remove_file(plan)?;
        if let Some(frontend) = &mut bundle.frontend
            && frontend.upstream.is_none()
        {
            frontend.root = frontend.root.canonicalize()?;
        }
        Input {
            bundle,
            snapshot,
            listen: listen
                .into_string()
                .map_err(|_| anyhow::anyhow!("invalid listen address"))?,
            dev: Some((config, resources)),
            token: Some(token),
            directory,
            _temporary: None,
            root: PathBuf::new(),
        }
    } else {
        let snapshot = inherited_snapshot()?;
        let root = std::env::current_exe()?
            .parent()
            .context("invalid bundle path")?
            .canonicalize()?;
        let file = File::open(root.join("manifest.json"))?;
        let mut bundle: Bundle = serde_json::from_reader(file.take(32 << 20))?;
        if let Some(frontend) = &mut bundle.frontend {
            ensure!(
                frontend.upstream.is_none(),
                "deployment frontend may not have a development upstream"
            );
            frontend.root = bundle_path(&root, &frontend.root)?;
            ensure!(
                frontend.root.join("index.html").is_file(),
                "frontend dist has no index.html"
            );
        }
        let directory = tempfile::Builder::new()
            .prefix("app-workers-")
            .permissions(Permissions::from_mode(0o700))
            .tempdir_in("/tmp")?;
        Input {
            bundle,
            snapshot,
            listen: parse_listen(args)?,
            dev: None,
            token: None,
            directory: directory.path().into(),
            _temporary: Some(directory),
            root,
        }
    };
    input.bundle.verify()?;
    tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?
        .block_on(tokio::task::LocalSet::new().run_until(run(input)))
}

struct Input {
    bundle: Bundle,
    snapshot: Value,
    listen: String,
    dev: Option<(dev::Development, DevResources)>,
    token: Option<String>,
    directory: PathBuf,
    _temporary: Option<tempfile::TempDir>,
    root: PathBuf,
}

#[derive(Deserialize)]
struct Development {
    bundle: Bundle,
    snapshot: Value,
    token: String,
    config: dev::Development,
}

fn parse_listen(args: impl Iterator<Item = OsString>) -> Result<String> {
    let mut listen = None;
    let mut port = None;
    let mut args = args.into_iter();
    while let Some(arg) = args.next() {
        let arg = arg.to_str().context("invalid listener argument")?;
        let (name, assigned) = arg
            .split_once('=')
            .map_or((arg, None), |(key, value)| (key, Some(value)));
        let value = match assigned {
            Some(value) => value.to_owned(),
            None => args
                .next()
                .and_then(|value| value.into_string().ok())
                .context("missing listener argument value")?,
        };
        match name {
            "--listen" | "-listen" => listen = Some(value),
            "--port" | "-port" => port = Some(value.parse::<u16>()?),
            _ => anyhow::bail!("unknown host argument {name}"),
        }
    }
    Ok(listen
        .or_else(|| port.map(|port| format!("127.0.0.1:{port}")))
        .or_else(|| {
            std::env::var("SCENERY_LISTEN_ADDR")
                .ok()
                .filter(|address| !address.is_empty())
        })
        .unwrap_or_else(|| "127.0.0.1:4000".into()))
}

fn inherited_snapshot() -> Result<Value> {
    let Some(value) = std::env::var_os(CONFIG_SNAPSHOT_FD).filter(|value| value != OsStr::new(""))
    else {
        return Ok(Value::Null);
    };
    let fd: RawFd = value
        .to_str()
        .context("invalid configuration descriptor")?
        .trim()
        .parse()?;
    ensure!(
        fd >= 3 && unsafe { libc::fcntl(fd, libc::F_GETFD) } != -1,
        "configuration descriptor is unavailable"
    );
    let fd = unsafe { OwnedFd::from_raw_fd(fd) };
    let mut bytes = Vec::new();
    File::from(fd).take((4 << 20) + 1).read_to_end(&mut bytes)?;
    ensure!(
        bytes.len() <= 4 << 20,
        "configuration snapshot exceeds limit"
    );
    values::parse(&bytes)
}

async fn run(mut input: Input) -> Result<()> {
    let mut terminate = signal(SignalKind::terminate())?;
    let mut interrupt = signal(SignalKind::interrupt())?;
    let development = input.dev.is_some();
    let stop = CancellationToken::new();
    let stop_signal = stop.clone();
    let signals = tokio::spawn(async move {
        tokio::select! {
            _ = terminate.recv() => {}
            _ = interrupt.recv() => {}
        }
        stop_signal.cancel();
    });

    let directory = &input.directory;
    let expected = input
        .bundle
        .programs
        .iter()
        .map(|program| {
            (
                program.process.clone(),
                (program.service.clone(), program.runtime.clone()),
            )
        })
        .collect();
    let events = input.dev.as_ref().and_then(|(config, _)| {
        config
            .telemetry_path
            .clone()
            .map(|path| supervisor::EventTarget {
                path,
                app: config.app.clone(),
            })
    });
    let control = Control::bind(
        directory,
        input.bundle.contract_revision.clone(),
        input.token,
        expected,
        events,
    )?;
    let mut publication = control.publication().await;
    let revision = input.bundle.contract_revision.clone();
    let publisher = control.publisher(publication.clone());
    let mut resources = None;
    let mut startup = None;
    let mut victoria = otlp::Endpoints::new();
    let watcher = if let Some((mut config, owned)) = input.dev.take() {
        resources = Some(owned);
        let endpoint = dev::write_endpoint(
            &config,
            &directory.join("control.sock"),
            control.token().trim_start_matches("Bearer "),
        )?;
        resources.as_mut().expect("development resources").endpoint = Some(endpoint);
        startup = config.startup.take().zip(config.telemetry_path.clone());
        victoria = std::mem::take(&mut config.victoria);
        Some(dev::WatcherLoop::start(config)?)
    } else {
        None
    };
    let plan = if let Some(watcher) = &watcher {
        watcher.initial(revision.clone())
    } else {
        LaunchPlan {
            contract_revision: revision.clone(),
            programs: input
                .bundle
                .programs
                .iter()
                .map(|program| program.program(&input.root, &input.snapshot))
                .collect::<Result<_>>()?,
        }
    };
    let host = Arc::new(Host::new(
        input.bundle,
        input.snapshot,
        victoria,
        control.token().into(),
        watcher.as_ref().map(dev::WatcherLoop::status),
        stop.clone(),
        publication.clone(),
    )?);
    host.engine.prepare(&host).await?;
    let control_listener = UnixListener::bind(directory.join("control.sock"))?;
    let capability_listener = UnixListener::bind(directory.join("capabilities.sock"))?;
    let mut servers = JoinSet::new();
    let private_stop = CancellationToken::new();
    servers.spawn_local(serve_private(
        control_listener,
        host.clone(),
        private_stop.clone(),
        false,
    ));
    servers.spawn_local(serve_private(
        capability_listener,
        host.clone(),
        private_stop.clone(),
        true,
    ));
    if let Some(resources) = &mut resources
        && let Some(listener) = resources.reports.take()
    {
        servers.spawn_local(serve_reports(
            TcpListener::from_std(listener)?,
            host.clone(),
            private_stop.clone(),
        ));
    }
    tokio::select! {
        result = control.publish_initial(plan) => result?,
        () = stop.cancelled() => return Ok(()),
    }
    let rebuild =
        watcher.map(|watcher| tokio::spawn(watcher.run(directory.clone(), publisher, revision)));
    let shutdown = private_stop.clone();
    let mut supervisor = tokio::spawn(control.serve(async move { shutdown.cancelled().await }));
    let result = async {
        tokio::select! {
            result = publication.wait_for(Option::is_some) => { result?; }
            () = stop.cancelled() => return Ok(()),
        }
        // No public socket is bound until the entire initial process set has
        // attested and the supervisor has committed its build identity.
        let listener = PublicListener::bind(&input.listen).await.context("bind public application listener")?;
        let address = listener.address()?;
        if development {
            let mut marker = std::fs::OpenOptions::new().create_new(true).write(true).mode(0o600)
                .open(directory.join("ready"))?;
            writeln!(marker, "{address}")?;
        }
        if let Some((mut record, path)) = startup
            && let Some(at) = record["at"].as_str().and_then(|timestamp| time::OffsetDateTime::parse(
                timestamp, &time::format_description::well_known::Rfc3339).ok())
        {
            record["duration_ms"] = serde_json::json!((time::OffsetDateTime::now_utc() - at).whole_milliseconds().max(0));
            if let Err(error) = dev::journal(&path, &record).await {
                eprintln!("could not record startup telemetry: {error:#}");
            }
        }
        eprintln!("application ready on {address}");
        let engine = tokio::spawn(Engine::run(host.clone(), stop.clone()));
        let scheduler = tokio::spawn(schedule::run(host.clone(), stop.clone()));
        let result = tokio::select! {
            result = serve_public(listener, host.clone(), stop.clone()) => result,
            result = &mut supervisor => result?.and_then(|()| {
                if stop.is_cancelled() { Ok(()) } else { Err(anyhow::anyhow!("worker supervisor stopped")) }
            }),
            result = servers.join_next() => match result {
                Some(result) => result?,
                None => Ok(()),
            },
        };
        stop.cancel();
        engine.abort();
        let _ = engine.await;
        let _ = scheduler.await;
        result
    }.await;
    stop.cancel();
    if let Some(rebuild) = rebuild {
        rebuild.abort();
        let _ = rebuild.await;
    }
    private_stop.cancel();
    if !supervisor.is_finished() {
        supervisor.await??;
    }
    servers.shutdown().await;
    signals.abort();
    result
}

struct DevResources {
    directory: PathBuf,
    _locks: Vec<OwnedFd>,
    reports: Option<std::net::TcpListener>,
    endpoint: Option<PathBuf>,
    frontend: i32,
}

impl DevResources {
    fn inherit(config: &dev::Development, directory: PathBuf) -> Result<Self> {
        let mut resources = Self {
            directory,
            _locks: Vec::new(),
            reports: None,
            endpoint: None,
            frontend: config.frontend_pid,
        };
        for &fd in &config.lock_fds {
            resources._locks.push(inherit_fd(fd)?);
        }
        let listener = std::net::TcpListener::from(inherit_fd(config.report_fd)?);
        listener.set_nonblocking(true)?;
        resources.reports = Some(listener);
        Ok(resources)
    }
}

fn inherit_fd(fd: RawFd) -> Result<OwnedFd> {
    ensure!(
        fd >= 3 && unsafe { libc::fcntl(fd, libc::F_SETFD, libc::FD_CLOEXEC) } == 0,
        "development descriptor is unavailable"
    );
    Ok(unsafe { OwnedFd::from_raw_fd(fd) })
}

impl Drop for DevResources {
    fn drop(&mut self) {
        if self.frontend > 0 {
            // The inherited frontend remains our child. Keep the leader
            // unreaped until its group has been stopped.
            unsafe {
                libc::kill(-self.frontend, libc::SIGKILL);
                libc::waitpid(self.frontend, std::ptr::null_mut(), 0);
            }
        }
        if let Some(endpoint) = &self.endpoint {
            let _ = std::fs::remove_file(endpoint);
        }
        let _ = std::fs::remove_dir_all(&self.directory);
    }
}

async fn serve_reports(
    listener: TcpListener,
    host: Arc<Host>,
    stop: CancellationToken,
) -> Result<()> {
    let token = format!("Bearer {}", std::env::var("SCENERY_DEV_REPORT_TOKEN")?);
    let mut connections = JoinSet::new();
    loop {
        tokio::select! {
            () = stop.cancelled() => return Ok(()),
            socket = listener.accept() => {
                let (socket, _) = socket?;
                let host = host.clone();
                let token = token.clone();
                connections.spawn(async move {
                    let service = service_fn(move |request: hyper::Request<hyper::body::Incoming>| {
                        let host = host.clone();
                        let token = token.clone();
                        async move {
                            use hyper::{Method, StatusCode};
                            let status = if request.method() != Method::POST || request.uri().path() != "/report"
                                || !host::authorized(request.headers(), &token) {
                                StatusCode::UNAUTHORIZED
                            } else {
                                match transport::read(request.into_body(), 1 << 20).await {
                                    Ok(raw) if host.telemetry.ingest(&raw).is_ok() => StatusCode::NO_CONTENT,
                                    _ => StatusCode::BAD_REQUEST,
                                }
                            };
                            Ok::<_, Infallible>(host::empty(status))
                        }
                    });
                    let _ = hyper::server::conn::http1::Builder::new().serve_connection(TokioIo::new(socket), service).await;
                });
            }
            _ = connections.join_next(), if !connections.is_empty() => {}
        }
    }
}

#[derive(Clone, Copy)]
struct LocalExecutor;

impl<F: Future<Output = ()> + 'static> hyper::rt::Executor<F> for LocalExecutor {
    fn execute(&self, future: F) {
        tokio::task::spawn_local(future);
    }
}

async fn serve_private(
    listener: UnixListener,
    host: Arc<Host>,
    stop: CancellationToken,
    capability: bool,
) -> Result<()> {
    let mut connections = JoinSet::new();
    loop {
        tokio::select! {
            () = stop.cancelled() => return Ok(()),
            result = listener.accept() => {
                let (socket, _) = result?;
                let host = host.clone();
                connections.spawn_local(async move {
                    let service = service_fn(move |request| {
                        let host = host.clone();
                        async move {
                            Ok::<_, Infallible>(if capability {
                                host.capability(request).await
                            } else {
                                host.control(request).await
                            })
                        }
                    });
                    let _ = hyper::server::conn::http2::Builder::new(LocalExecutor)
                        .serve_connection(TokioIo::new(socket), service).await;
                });
            }
            _ = connections.join_next(), if !connections.is_empty() => {}
        }
    }
}

trait PublicStream: tokio::io::AsyncRead + tokio::io::AsyncWrite + Send + Unpin {}
impl<T: tokio::io::AsyncRead + tokio::io::AsyncWrite + Send + Unpin> PublicStream for T {}

enum PublicListener {
    Tcp(TcpListener),
    Unix {
        listener: UnixListener,
        path: PathBuf,
        identity: (u64, u64),
    },
}

impl PublicListener {
    async fn bind(address: &str) -> Result<Self> {
        match std::env::var("SCENERY_LISTEN_NETWORK")
            .unwrap_or_default()
            .trim()
            .to_ascii_lowercase()
            .as_str()
        {
            "" | "tcp" => Ok(Self::Tcp(TcpListener::bind(address).await?)),
            "unix" => {
                let path = PathBuf::from(address);
                ensure!(
                    path.is_absolute(),
                    "Unix listener requires an absolute socket path"
                );
                let parent = path.parent().context("missing Unix listener directory")?;
                std::fs::create_dir_all(parent)?;
                let listener = UnixListener::bind(&path)?;
                let metadata = std::fs::symlink_metadata(&path)?;
                ensure!(
                    metadata.file_type().is_socket(),
                    "Unix listener path changed"
                );
                Ok(Self::Unix {
                    listener,
                    path,
                    identity: (metadata.dev(), metadata.ino()),
                })
            }
            _ => anyhow::bail!("unsupported listener network"),
        }
    }

    fn address(&self) -> Result<String> {
        match self {
            Self::Tcp(socket) => Ok(socket.local_addr()?.to_string()),
            Self::Unix { path, .. } => Ok(path.display().to_string()),
        }
    }

    async fn accept(&self) -> Result<Box<dyn PublicStream>> {
        match self {
            Self::Tcp(listener) => Ok(Box::new(listener.accept().await?.0)),
            Self::Unix { listener, .. } => Ok(Box::new(listener.accept().await?.0)),
        }
    }
}

impl Drop for PublicListener {
    fn drop(&mut self) {
        if let Self::Unix { path, identity, .. } = self
            && let Ok(current) = std::fs::symlink_metadata(&*path)
            && current.file_type().is_socket()
            && (current.dev(), current.ino()) == *identity
        {
            let _ = std::fs::remove_file(path);
        }
    }
}

async fn serve_public(
    listener: PublicListener,
    host: Arc<Host>,
    stop: CancellationToken,
) -> Result<()> {
    let mut connections = JoinSet::new();
    loop {
        tokio::select! {
            () = stop.cancelled() => break,
            result = listener.accept() => {
                let socket = result?;
                let host = host.clone();
                let stop = stop.clone();
                connections.spawn(async move {
                    let service = service_fn(move |request| {
                        let host = host.clone();
                        async move { Ok::<_, Infallible>(gateway::public(&host, request).await) }
                    });
                    let mut builder = hyper::server::conn::http1::Builder::new();
                    builder.timer(hyper_util::rt::TokioTimer::new()).header_read_timeout(Duration::from_secs(10));
                    let connection = builder.serve_connection(TokioIo::new(socket), service).with_upgrades();
                    tokio::pin!(connection);
                    tokio::select! {
                        _ = &mut connection => {}
                        () = stop.cancelled() => {
                            connection.as_mut().graceful_shutdown();
                            let _ = tokio::time::timeout(DRAIN_TIMEOUT, connection).await;
                        }
                    }
                });
            }
            _ = connections.join_next(), if !connections.is_empty() => {}
        }
    }
    while connections.join_next().await.is_some() {}
    Ok(())
}
