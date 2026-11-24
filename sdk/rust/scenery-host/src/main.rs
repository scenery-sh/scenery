mod process;
mod supervisor;
mod transport;

use std::ffi::OsStr;
use std::fs::Permissions;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::process::{Command, Stdio};
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use process::ProcessGroup;
use supervisor::Control;
use tokio::signal::unix::{SignalKind, signal};
use tokio::sync::oneshot;
use tokio::time::interval;

#[tokio::main(flavor = "current_thread")]
async fn main() {
    if let Err(error) = run().await {
        eprintln!("{error:#}");
        std::process::exit(1);
    }
}

async fn run() -> Result<()> {
    let mut args = std::env::args_os().skip(1);
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
