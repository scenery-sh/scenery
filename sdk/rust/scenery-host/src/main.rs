mod process;
mod supervisor;
mod transport;

use std::ffi::{OsStr, OsString};
use std::fs::Permissions;
use std::os::fd::{FromRawFd, OwnedFd, RawFd};
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::process::{Command, Stdio};
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use process::{CONFIG_SNAPSHOT_FD, ProcessGroup};
use supervisor::Control;
use tokio::signal::unix::{SignalKind, signal};
use tokio::sync::oneshot;
use tokio::time::interval;

fn main() {
    if let Err(error) = bootstrap() {
        eprintln!("{error:#}");
        std::process::exit(1);
    }
}

fn bootstrap() -> Result<()> {
    let mut args = std::env::args_os().skip(1).peekable();
    let snapshot = if args.peek().is_some_and(|arg| arg == "--supervisor") {
        None
    } else {
        inherited_snapshot()?
    };
    tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?
        .block_on(run(args, snapshot))
}

fn inherited_snapshot() -> Result<Option<OwnedFd>> {
    let value = match std::env::var(CONFIG_SNAPSHOT_FD) {
        Ok(value) => value,
        Err(std::env::VarError::NotPresent) => return Ok(None),
        Err(error) => return Err(error.into()),
    };
    if value.trim().is_empty() {
        return Ok(None);
    }
    let fd: RawFd = value
        .trim()
        .parse()
        .context("invalid configuration descriptor")?;
    ensure!(
        fd >= 3 && unsafe { libc::fcntl(fd, libc::F_GETFD) } != -1,
        "configuration descriptor is unavailable",
    );
    // Claim the inherited descriptor before the runtime opens any files.
    Ok(Some(unsafe { OwnedFd::from_raw_fd(fd) }))
}

async fn run(mut args: impl Iterator<Item = OsString>, snapshot: Option<OwnedFd>) -> Result<()> {
    let first = args.next();
    let mut terminate = signal(SignalKind::terminate())?;
    let mut interrupt = signal(SignalKind::interrupt())?;
    let signaled = async {
        tokio::select! {
            _ = terminate.recv() => {}
            _ = interrupt.recv() => {}
        }
    };
    if first.as_deref() == Some(OsStr::new("--supervisor")) {
        let directory = args.next().context("missing runtime directory")?;
        ensure!(args.next().is_none(), "unexpected supervisor arguments");
        let parent = unsafe { libc::getppid() };
        return Control::bind(Path::new(&directory))?
            .serve(async {
                tokio::select! {
                    () = signaled => {}
                    () = parent_exits(parent) => {}
                }
            })
            .await;
    }

    let exe = std::env::current_exe()?;
    let gateway = exe.with_file_name("worker-gateway");
    let directory = tempfile::Builder::new()
        .prefix("app-workers-")
        .permissions(Permissions::from_mode(0o700))
        .tempdir_in("/tmp")?;
    let control = Control::bind(directory.path())?;
    let (stop, shutdown) = oneshot::channel();
    let mut supervisor = tokio::spawn(control.serve(async {
        let _ = shutdown.await;
    }));
    let child = ProcessGroup::spawn(
        Command::new(gateway)
            .arg(directory.path())
            .args(first.into_iter().chain(args))
            .stdin(Stdio::inherit()),
    )?;
    // The gateway inherited the deployment snapshot. Worker startup has
    // separate, service-scoped descriptors.
    drop(snapshot);
    let mut poll = interval(Duration::from_millis(100));
    tokio::pin!(signaled);
    let result = loop {
        tokio::select! {
            () = &mut signaled => break Ok(()),
            result = &mut supervisor => {
                return result?.and_then(|()| anyhow::bail!("worker supervisor stopped unexpectedly"));
            }
            _ = poll.tick() => {
                if let Some(status) = child.exited()? {
                    break if status.code == Some(0) { Ok(()) } else { Err(anyhow::anyhow!("gateway {status}")) };
                }
            }
        }
    };
    child.shutdown().await;
    let _ = stop.send(());
    supervisor.await??;
    result
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
