mod abi;
mod assistant;
#[cfg(test)]
mod assistant_test;
mod assistant_token;

mod auth;
mod auth_google;
mod auth_organizations;
mod auth_sessions;
mod browser_auth;
#[cfg(test)]
mod browser_auth_test;
mod config;
mod connections;
mod database;
mod dev;
mod dev_build;
mod dev_inputs;
mod durable;
mod events;
mod frontend;
mod frontend_dev;
mod gateway;
mod gitignore;
mod host;
mod http_codec;
mod http_sse;
mod http_timeout;
mod json;
mod local_storage;
mod mcp;

mod otlp;
mod plan;
mod policy;
mod process;
mod s3_storage;
mod schedule;
mod schedule_slots;
mod storage;
mod supervisor;
mod telemetry;
mod test_mode;
mod transport;
mod values;

use std::convert::Infallible;
use std::fs::{File, Permissions};
use std::io::{Read, Write};
use std::os::fd::{FromRawFd, OwnedFd, RawFd};
use std::os::unix::fs::{FileTypeExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::PathBuf;
use std::sync::Arc;

use anyhow::{Context, Result, ensure};
use hyper::service::service_fn;
use hyper_util::rt::{TokioExecutor, TokioIo};
use serde::Deserialize;
use serde_json::Value;
use tokio::net::{TcpListener, UnixListener};
use tokio::signal::unix::{SignalKind, signal};
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;

use crate::durable::Engine;
use crate::host::Host;
use crate::plan::{Bundle, bundle_path};
use crate::process::DRAIN_TIMEOUT;
use crate::supervisor::{Control, LaunchPlan};

fn main() {
    if let Err(error) = bootstrap() {
        eprintln!("{error:#}");
        std::process::exit(1);
    }
}

fn bootstrap() -> Result<()> {
    let mut args = std::env::args_os().skip(1).peekable();
    if args.peek().is_some_and(|argument| argument == "--test") {
        args.next();
        return test_mode::run_from_args(args);
    }
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
        let arguments = config::Arguments::parse(args)?;
        let lookup = |name: &str| std::env::var_os(name);
        let snapshot = config::load_snapshot(arguments.config.as_deref(), &lookup)?;
        storage::validate(&lookup)?;
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
            listen: arguments.listen_address(&lookup)?,
            dev: None,
            token: None,
            directory: directory.path().into(),
            _temporary: Some(directory),
            root,
        }
    };
    input.bundle.verify()?;
    process::raise_descriptor_limit();
    // Requests run on a pool of threads: decoding one large payload or
    // hashing a file no longer stalls every other connection of the host.
    tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()?
        .block_on(run(input))
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

async fn run(mut input: Input) -> Result<()> {
    let mut terminate = signal(SignalKind::terminate())?;
    let mut interrupt = signal(SignalKind::interrupt())?;
    let mut hangup = signal(SignalKind::hangup())?;
    let mut quit = signal(SignalKind::quit())?;
    let development = input.dev.is_some();
    let stop = CancellationToken::new();
    let stop_signal = stop.clone();
    // A development host stops with the supervisor that started it.
    let parent = development.then(|| unsafe { libc::getppid() });
    let signals = tokio::spawn(async move {
        let orphaned = async {
            let Some(parent) = parent else {
                return std::future::pending().await;
            };
            while unsafe { libc::getppid() } == parent {
                tokio::time::sleep(std::time::Duration::from_millis(250)).await;
            }
        };
        tokio::select! {
            _ = terminate.recv() => {}
            _ = interrupt.recv() => {}
            _ = hangup.recv() => {}
            _ = quit.recv() => {}
            _ = orphaned => {}
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
    let (assistant_runtime, assistant_key) = if let Some((config, _)) = &input.dev {
        (
            config.assistant_runtime_path.clone(),
            config.assistant_key_path.clone(),
        )
    } else {
        (
            std::env::var_os("SCENERY_ASSISTANT_RUNTIME_CONFIG")
                .filter(|path| !path.is_empty())
                .map(PathBuf::from),
            std::env::var_os("SCENERY_ASSISTANT_TOKEN_KEY_FILE")
                .filter(|path| !path.is_empty())
                .map(PathBuf::from),
        )
    };
    let assistants = assistant::Gateway::new(
        assistant_runtime.as_deref(),
        assistant_key.as_deref(),
        &input.bundle.host.assistants,
    )?;
    let mut resources = None;
    let mut startup = None;
    // A development host exports to its worktree's Victoria; a production
    // host to the collector the standard OTLP variables name, if any.
    let mut export = None;
    let mut watcher = if let Some((mut config, owned)) = input.dev.take() {
        resources = Some(owned);
        let endpoint = dev::write_endpoint(
            &config,
            &directory.join("control.sock"),
            control.token().trim_start_matches("Bearer "),
        )?;
        resources.as_mut().expect("development resources").endpoint = Some(endpoint);
        startup = config.startup.take().zip(config.telemetry_path.clone());
        export = Some(otlp::Export::local(std::mem::take(&mut config.victoria))?);
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
    let export = match export {
        Some(export) => export,
        None => otlp::Export::from_env(&|name: &str| std::env::var_os(name))?,
    };
    let host = Arc::new(Host::new(
        input.bundle,
        input.snapshot,
        export,
        control.token().into(),
        watcher
            .as_ref()
            .map_or(host::Mode::Production, |w| host::Mode::Development(w.status())),
        (stop.clone(), assistants),
        publication.clone(),
    )?);
    host.engine.prepare(&host).await?;
    let control_listener = UnixListener::bind(directory.join("control.sock"))?;
    let capability_listener = UnixListener::bind(directory.join("capabilities.sock"))?;
    let mut servers = JoinSet::new();
    let private_stop = CancellationToken::new();
    assistant::Gateway::listeners(&host, &private_stop, &mut servers).await?;
    servers.spawn(serve_private(
        control_listener,
        host.clone(),
        private_stop.clone(),
        false,
    ));
    servers.spawn(serve_private(
        capability_listener,
        host.clone(),
        private_stop.clone(),
        true,
    ));
    if let Some(resources) = &mut resources
        && let Some(listener) = resources.reports.take()
    {
        servers.spawn(serve_reports(
            TcpListener::from_std(listener)?,
            host.clone(),
            private_stop.clone(),
        ));
    }
    tokio::select! {
        result = control.publish_initial(plan) => result?,
        () = stop.cancelled() => return Ok(()),
    }
    let shutdown = private_stop.clone();
    let mut supervisor = tokio::spawn(control.serve(async move { shutdown.cancelled().await }));
    let mut rebuild = None;
    let result = async {
        tokio::select! {
            result = publication.wait_for(Option::is_some) => { result?; }
            () = stop.cancelled() => return Ok(()),
        }
        if let Some(mut watcher) = watcher.take() {
            watcher.reconcile_startup(directory, &publisher, &revision).await;
            rebuild = Some(tokio::spawn(watcher.run(directory.clone(), publisher, revision)));
        }
        // No public socket is bound until the entire initial process set has
        // attested and the supervisor has committed its build identity.
        let listener = PublicListener::bind(&input.listen)
            .await
            .with_context(|| format!("bind public application listener {}", input.listen))?;
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
        // Running durable executions get the drain window, then release
        // their leases; the workers they call stay up until that is done.
        let mut engine = engine;
        if tokio::time::timeout(2 * DRAIN_TIMEOUT, &mut engine).await.is_err() {
            engine.abort();
            let _ = engine.await;
        }
        let mut scheduler = scheduler;
        if tokio::time::timeout(DRAIN_TIMEOUT, &mut scheduler)
            .await
            .is_err()
        {
            scheduler.abort();
            let _ = scheduler.await;
        }
        result
    }.await;
    stop.cancel();
    if let Some(rebuild) = rebuild {
        rebuild.abort();
        let _ = rebuild.await;
    }
    host.assistants.clear_runs();
    private_stop.cancel();
    if !supervisor.is_finished() {
        supervisor.await??;
    }
    servers.shutdown().await;
    // Spans and logs of the last requests and of the shutdown itself still
    // reach the collector, within a bound that cannot delay termination.
    host.telemetry.flush(TELEMETRY_FLUSH_TIMEOUT).await;
    signals.abort();
    result
}

const TELEMETRY_FLUSH_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(5);

struct DevResources {
    directory: PathBuf,
    _locks: Vec<OwnedFd>,
    reports: Option<std::net::TcpListener>,
    endpoint: Option<PathBuf>,
    frontend: i32,
    assistants: i32,
}

impl DevResources {
    fn inherit(config: &dev::Development, directory: PathBuf) -> Result<Self> {
        let mut resources = Self {
            directory,
            _locks: Vec::new(),
            reports: None,
            endpoint: None,
            frontend: config.frontend_pid,
            assistants: config.assistant_pid,
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
        if self.assistants > 0 {
            // Signal the assistant supervisor group and bound its exit wait
            // before forced cleanup so an unresponsive helper cannot orphan
            // the frontend or leak locks.
            unsafe {
                libc::kill(-self.assistants, libc::SIGTERM);
                libc::kill(self.assistants, libc::SIGTERM);
            }
            let deadline = std::time::Instant::now() + DRAIN_TIMEOUT;
            loop {
                let waited =
                    unsafe { libc::waitpid(self.assistants, std::ptr::null_mut(), libc::WNOHANG) };
                if waited == self.assistants {
                    break;
                }
                if waited == -1 {
                    if std::io::Error::last_os_error().kind() == std::io::ErrorKind::Interrupted {
                        continue;
                    }
                    break;
                }
                if std::time::Instant::now() >= deadline {
                    unsafe {
                        libc::kill(-self.assistants, libc::SIGKILL);
                        libc::kill(self.assistants, libc::SIGKILL);
                        while libc::waitpid(self.assistants, std::ptr::null_mut(), 0) == -1
                            && std::io::Error::last_os_error().kind()
                                == std::io::ErrorKind::Interrupted
                        {}
                    }
                    break;
                }
                std::thread::sleep(std::time::Duration::from_millis(10));
            }
        }
        if self.frontend > 0 {
            // The inherited frontend remains our child. Keep the leader
            // unreaped until its group has been stopped.
            unsafe {
                libc::kill(-self.frontend, libc::SIGKILL);
                while libc::waitpid(self.frontend, std::ptr::null_mut(), 0) == -1
                    && std::io::Error::last_os_error().kind() == std::io::ErrorKind::Interrupted
                {
                }
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
                let (socket, _) = match socket {
                    Ok(pair) => pair,
                    Err(error) => {
                        transport::accept_failed(error).await?;
                        continue;
                    }
                };
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
                let (socket, _) = match result {
                    Ok(pair) => pair,
                    Err(error) => {
                        transport::accept_failed(error).await?;
                        continue;
                    }
                };
                let host = host.clone();
                connections.spawn(async move {
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
                    let _ = hyper::server::conn::http2::Builder::new(TokioExecutor::new())
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
        match std::env::var(crate::config::LISTEN_NETWORK)
            .unwrap_or_default()
            .trim()
            .to_ascii_lowercase()
            .as_str()
        {
            "" | "tcp" => Ok(Self::Tcp(TcpListener::bind(address).await?)),
            "unix" => Self::bind_unix(std::path::Path::new(address)),
            _ => anyhow::bail!("unsupported listener network"),
        }
    }

    fn bind_unix(path: &std::path::Path) -> Result<Self> {
        let path = path.to_path_buf();
        ensure!(
            path.is_absolute(),
            "Unix listener requires an absolute socket path"
        );
        let parent = path.parent().context("missing Unix listener directory")?;
        std::fs::create_dir_all(parent)?;
        match std::fs::symlink_metadata(&path) {
            Ok(existing) => {
                ensure!(
                    existing.file_type().is_socket(),
                    "Unix listener path exists and is not a socket"
                );
                std::fs::remove_file(&path)?;
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) => return Err(error.into()),
        }
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

    fn address(&self) -> Result<String> {
        match self {
            Self::Tcp(socket) => Ok(socket.local_addr()?.to_string()),
            Self::Unix { path, .. } => Ok(path.display().to_string()),
        }
    }

    async fn accept(&self) -> std::io::Result<(Box<dyn PublicStream>, Option<std::net::IpAddr>)> {
        match self {
            Self::Tcp(listener) => {
                let (stream, peer) = listener.accept().await?;
                Ok((Box::new(stream), Some(peer.ip())))
            }
            Self::Unix { listener, .. } => Ok((Box::new(listener.accept().await?.0), None)),
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
    let timeouts = host.bundle.host.http_timeouts();
    let mut connections = JoinSet::new();
    loop {
        tokio::select! {
            () = stop.cancelled() => break,
            result = listener.accept() => {
                // Running out of descriptors, or a connection reset while it
                // waited in the queue, must not take the application down.
                let (socket, peer) = match result {
                    Ok(pair) => pair,
                    Err(error) => {
                        transport::accept_failed(error).await?;
                        continue;
                    }
                };
                let host = host.clone();
                let stop = stop.clone();
                connections.spawn(async move {
                    let (stream, deadlines) = http_timeout::DeadlineStream::new(socket, timeouts);
                    let service = service_fn(move |mut request: hyper::Request<hyper::body::Incoming>| {
                        let host = host.clone();
                        let deadlines = deadlines.clone();
                        deadlines.begin_request(timeouts.read, timeouts.write);
                        let had_body = !hyper::body::Body::is_end_stream(request.body());
                        if !had_body {
                            deadlines.request_body_finished();
                        }
                        request.extensions_mut().insert(deadlines.clone());
                        if let Some(peer) = peer {
                            request.extensions_mut().insert(peer);
                        }
                        async move {
                            let response = tokio::select! {
                                response = gateway::public(&host, request) => response,
                                () = deadlines.wait_write_expired() => {
                                    return Err(std::io::Error::new(
                                        std::io::ErrorKind::TimedOut,
                                        "http write timeout",
                                    ));
                                }
                            };
                            deadlines.finish_unread_request(had_body).await;
                            deadlines.begin_response(
                                response.status(),
                                response.headers(),
                                timeouts.idle,
                            );
                            Ok::<_, std::io::Error>(
                                response
                                    .map(|body| http_timeout::ResponseBodyGuard::new(body, deadlines)),
                            )
                        }
                    });
                    let builder = http_timeout::public_http1_builder();
                    let connection = builder.serve_connection(TokioIo::new(stream), service).with_upgrades();
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

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn unix_listener_replaces_stale_socket_and_refuses_regular_file() -> Result<()> {
        let dir = tempfile::Builder::new()
            .prefix("sock-")
            .tempdir_in("/tmp")?;
        let sock = dir.path().join("public.sock");
        let _first = std::os::unix::net::UnixListener::bind(&sock)?;
        drop(_first);
        assert!(sock.exists());

        // Binding via UnixListener directly refuses an unlinked stale socket,
        // whereas PublicListener::bind("unix") clears a stale socket cleanly.
        let bound = PublicListener::bind_unix(&sock)?;
        drop(bound);
        assert!(!sock.exists());

        let regular = dir.path().join("not-a-socket");
        std::fs::write(&regular, b"keep me")?;
        assert!(PublicListener::bind_unix(&regular).is_err());
        assert_eq!(std::fs::read(&regular)?, b"keep me");
        Ok(())
    }
}
