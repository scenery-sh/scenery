use std::fs::File;
use std::io::{Read, Write};
use std::os::unix::fs::OpenOptionsExt;
use std::path::{Path, PathBuf};
use std::sync::Arc;

use anyhow::{Context, Result, ensure};
use serde::Deserialize;
use serde_json::Value;
use tokio::net::UnixListener;
use tokio::signal::unix::{SignalKind, signal};
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;

use crate::durable::Engine;
use crate::host::Host;
use crate::plan::Bundle;
use crate::process::{DRAIN_TIMEOUT, Program};
use crate::supervisor::{Control, LaunchPlan};
use crate::{PublicListener, TELEMETRY_FLUSH_TIMEOUT, assistant, otlp, process, serve_private, serve_public};

#[derive(Deserialize)]
struct TestLaunch {
    bundle: Bundle,
    snapshot: Value,
    token: String,
    programs: Vec<Program>,
}

pub(crate) fn run_from_args(mut args: impl Iterator<Item = std::ffi::OsString>) -> Result<()> {
    let directory = PathBuf::from(args.next().context("missing test runtime directory")?);
    let plan_path = PathBuf::from(args.next().context("missing test launch plan")?);
    let listen = args
        .next()
        .context("missing test listener")?
        .into_string()
        .map_err(|_| anyhow::anyhow!("invalid test listen address"))?;
    ensure!(args.next().is_none(), "unexpected test host arguments");

    let file = File::open(&plan_path)?;
    let launch: TestLaunch = serde_json::from_reader(file.take(36 << 20))?;
    let _ = std::fs::remove_file(&plan_path);
    launch.bundle.verify()?;
    process::raise_descriptor_limit();
    tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()?
        .block_on(serve_test(directory, launch, listen))
}

async fn serve_test(directory: PathBuf, launch: TestLaunch, listen: String) -> Result<()> {
    let mut terminate = signal(SignalKind::terminate())?;
    let mut interrupt = signal(SignalKind::interrupt())?;
    let mut hangup = signal(SignalKind::hangup())?;
    let mut quit = signal(SignalKind::quit())?;
    let stop = CancellationToken::new();
    let stop_signal = stop.clone();
    let parent = unsafe { libc::getppid() };
    let signals = tokio::spawn(async move {
        let orphaned = async {
            while unsafe { libc::getppid() } == parent {
                tokio::time::sleep(std::time::Duration::from_millis(200)).await;
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

    let expected = launch
        .programs
        .iter()
        .map(|program| {
            (
                program.process.clone(),
                (program.service.clone(), program.runtime.clone()),
            )
        })
        .collect();
    let revision = launch.bundle.contract_revision.clone();
    let control = Control::bind(
        &directory,
        revision.clone(),
        Some(launch.token),
        expected,
        None,
    )?;
    let mut publication = control.publication().await;
    let assistants = assistant::Gateway::new(None, None, &launch.bundle.host.assistants)?;
    let export = otlp::Export::from_env(&|_| None)?;
    let mut host_instance = Host::new(
        launch.bundle,
        launch.snapshot,
        export,
        control.token().into(),
        None,
        (stop.clone(), assistants),
        publication.clone(),
    )?;
    host_instance.enable_test_harness();
    let host = Arc::new(host_instance);
    host.engine.prepare(&host).await?;

    let control_listener = UnixListener::bind(directory.join("control.sock"))?;
    let capability_listener = UnixListener::bind(directory.join("capabilities.sock"))?;
    let mut servers = JoinSet::new();
    let private_stop = CancellationToken::new();
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

    let plan = LaunchPlan {
        contract_revision: revision,
        programs: launch.programs,
    };
    tokio::select! {
        result = control.publish_initial(plan) => result?,
        () = stop.cancelled() => return Ok(()),
    }
    let shutdown = private_stop.clone();
    let mut supervisor = tokio::spawn(control.serve(async move { shutdown.cancelled().await }));

    let result = async {
        tokio::select! {
            result = publication.wait_for(Option::is_some) => { result?; }
            () = stop.cancelled() => return Ok(()),
        }
        let listener = PublicListener::bind(&listen)
            .await
            .with_context(|| format!("bind test application listener {listen}"))?;
        let address = listener.address()?;
        write_ready_file(&directory, &address)?;
        let engine = tokio::spawn(Engine::run(host.clone(), stop.clone()));
        let result = tokio::select! {
            result = serve_public(listener, host.clone(), stop.clone()) => result,
            result = &mut supervisor => result?.and_then(|()| {
                if stop.is_cancelled() { Ok(()) } else { Err(anyhow::anyhow!("test worker supervisor stopped")) }
            }),
            result = servers.join_next() => match result {
                Some(result) => result?,
                None => Ok(()),
            },
        };
        stop.cancel();
        let mut engine = engine;
        if tokio::time::timeout(DRAIN_TIMEOUT, &mut engine).await.is_err() {
            engine.abort();
            let _ = engine.await;
        }
        result
    }
    .await;

    stop.cancel();
    private_stop.cancel();
    if !supervisor.is_finished() {
        supervisor.await??;
    }
    servers.shutdown().await;
    host.telemetry.flush(TELEMETRY_FLUSH_TIMEOUT).await;
    signals.abort();
    let _ = std::fs::remove_dir_all(&directory);
    result
}

fn write_ready_file(directory: &Path, address: &str) -> Result<()> {
    let mut marker = std::fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .mode(0o600)
        .open(directory.join("ready"))?;
    writeln!(marker, "{address}")?;
    Ok(())
}
