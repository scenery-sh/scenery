mod abi;
mod auth;
mod connections;
mod durable;
mod frontend;
mod gateway;
mod host;
mod http_codec;
mod local_storage;
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
use tokio::time::interval;
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
    let input = if args
        .peek()
        .is_some_and(|argument| argument == "--development")
    {
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
        let file = File::open(plan)?;
        let Development {
            mut bundle,
            snapshot,
            token,
        } = serde_json::from_reader(file.take(36 << 20))?;
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
            development: true,
            token: Some(token),
            directory: directory.into(),
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
            development: false,
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
    development: bool,
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

async fn run(input: Input) -> Result<()> {
    let mut terminate = signal(SignalKind::terminate())?;
    let mut interrupt = signal(SignalKind::interrupt())?;
    let development = input.development;
    let parent = unsafe { libc::getppid() };
    let stop = CancellationToken::new();
    let stop_signal = stop.clone();
    let signals = tokio::spawn(async move {
        tokio::select! {
            _ = terminate.recv() => {}
            _ = interrupt.recv() => {}
            () = parent_exits(parent), if development => {}
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
    let control = Control::bind(
        directory,
        input.bundle.contract_revision.clone(),
        input.token,
        expected,
    )?;
    let mut publication = control.publication().await;
    let plan = if development {
        None
    } else {
        Some(LaunchPlan {
            contract_revision: input.bundle.contract_revision.clone(),
            programs: input
                .bundle
                .programs
                .iter()
                .map(|program| program.program(&input.root, &input.snapshot))
                .collect::<Result<_>>()?,
        })
    };
    let host = Arc::new(Host::new(
        input.bundle,
        input.snapshot,
        development,
        control.token().into(),
        development.then(|| directory.join("watcher.sock")),
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
    if let Some(plan) = plan {
        let launched = tokio::select! {
            result = control.publish_initial(plan) => result,
            () = stop.cancelled() => return Ok(()),
        };
        launched?;
    }
    let shutdown = private_stop.clone();
    let mut supervisor = tokio::spawn(control.serve(async move { shutdown.cancelled().await }));
    let result = async {
        if !development {
            // Subscription remains at the initially published generation.
            publication.mark_changed();
        }
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
    private_stop.cancel();
    if !supervisor.is_finished() {
        supervisor.await??;
    }
    servers.shutdown().await;
    signals.abort();
    result
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

async fn parent_exits(parent: libc::pid_t) {
    let mut poll = interval(Duration::from_millis(100));
    loop {
        if parent <= 1 || unsafe { libc::getppid() } != parent {
            return;
        }
        poll.tick().await;
    }
}
