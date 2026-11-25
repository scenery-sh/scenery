use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Seek, Write};
use std::os::fd::{AsFd, AsRawFd};
use std::os::unix::fs::OpenOptionsExt;
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, OnceLock};
use std::time::{Duration, Instant};

use anyhow::{Context, Result, ensure};
use hyper::{Method, StatusCode};
use scenery_wire::{
    CONTRACT_HEADER, IMPLEMENTATION_HEADER, Identity, Launch, MAX_METADATA, READY_PATH, VERSION,
};
use serde::{Deserialize, Serialize};
use serde_json::value::RawValue;
use tokio::time::{sleep, timeout};
use tokio_util::sync::CancellationToken;

use crate::transport::{Client, read};

pub(crate) const CONFIG_SNAPSHOT_FD: &str = "SCENERY_CONFIG_SNAPSHOT_FD";

const STARTUP_TIMEOUT: Duration = Duration::from_secs(10);
pub(crate) const DRAIN_TIMEOUT: Duration = Duration::from_secs(5);

/// The descriptor limit the host was started with.
static INHERITED_DESCRIPTORS: OnceLock<libc::rlimit> = OnceLock::new();

/// Raises the soft descriptor limit to the hard one. Every connection,
/// worker socket and open stream is a descriptor, and the inherited soft
/// limit (256 under launchd, 1024 under systemd) is far below what a host
/// serves. The Go runtime this host replaces did the same at startup.
pub(crate) fn raise_descriptor_limit() {
    let mut limit = libc::rlimit {
        rlim_cur: 0,
        rlim_max: 0,
    };
    if unsafe { libc::getrlimit(libc::RLIMIT_NOFILE, &mut limit) } != 0 {
        return;
    }
    let _ = INHERITED_DESCRIPTORS.set(limit);
    let wanted = limit.rlim_max.min(descriptor_ceiling());
    if limit.rlim_cur < wanted {
        limit.rlim_cur = wanted;
        unsafe { libc::setrlimit(libc::RLIMIT_NOFILE, &limit) };
    }
}

/// Darwin reports an unlimited hard limit but rejects a soft limit above
/// the kernel's per-process maximum.
#[cfg(target_os = "macos")]
fn descriptor_ceiling() -> libc::rlim_t {
    let mut value: libc::c_int = 0;
    let mut size = std::mem::size_of::<libc::c_int>();
    let read = unsafe {
        libc::sysctlbyname(
            c"kern.maxfilesperproc".as_ptr(),
            (&raw mut value).cast(),
            &raw mut size,
            std::ptr::null_mut(),
            0,
        )
    };
    if read == 0 && value > 0 {
        value as libc::rlim_t
    } else {
        // `OPEN_MAX`, which every Darwin kernel accepts.
        10_240
    }
}

#[cfg(not(target_os = "macos"))]
fn descriptor_ceiling() -> libc::rlim_t {
    libc::rlim_t::MAX
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct BuildIdentity {
    pub(crate) contract_revision: String,
    pub(crate) implementation_revision: String,
    pub(crate) build_input_digest: String,
    pub(crate) go_target: String,
}

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Program {
    pub(crate) service: String,
    pub(crate) process: String,
    pub(crate) binary: PathBuf,
    pub(crate) digest: String,
    pub(crate) runtime: String,
    pub(crate) identity: BuildIdentity,
    pub(crate) environment: Vec<String>,
    pub(crate) startup: Option<Box<RawValue>>,
    #[serde(skip)]
    pub(crate) artifact: Option<Arc<tempfile::TempPath>>,
}

pub(crate) struct Worker {
    pub(crate) program: Program,
    group: ProcessGroup,
    pub(crate) launch: Launch,
    pub(crate) client: Client,
    config: PathBuf,
    fault_recorded: AtomicBool,
    /// Cancelled once no generation in service runs this process any more.
    /// Calls end with it; a generation swap alone never ends a call on a
    /// worker that the next generation carried over.
    pub(crate) retired: CancellationToken,
}

impl Worker {
    pub(crate) async fn spawn(
        directory: &Path,
        host_socket: &Path,
        program: Program,
    ) -> Result<Self> {
        let binary = program.binary.clone();
        let digest = tokio::task::spawn_blocking(move || file_digest(&binary)).await??;
        ensure!(
            digest == program.digest,
            "worker {} executable digest changed",
            program.service,
        );

        let key = random_hex::<12>()?;
        let config = directory.join(format!("{key}.json"));
        let launch = Launch {
            worker_socket: directory.join(format!("{key}.sock")),
            host_socket: host_socket.to_owned(),
            token: random_hex::<32>()?,
            identity: Identity {
                abi: VERSION,
                service: program.service.clone(),
                contract_revision: program.identity.contract_revision.clone(),
                implementation_revision: program.identity.implementation_revision.clone(),
            },
        };
        let startup = program
            .startup
            .as_ref()
            .map(|raw| private_input(directory, raw))
            .transpose()?;
        let mut command = Command::new(&program.binary);
        command
            .arg(&config)
            .env_clear()
            .stdin(Stdio::null())
            .stdout(Stdio::from(std::io::stderr().as_fd().try_clone_to_owned()?))
            .stderr(Stdio::inherit());
        for entry in &program.environment {
            let (key, value) = entry
                .split_once('=')
                .context("invalid worker environment")?;
            command.env(key, value);
        }
        if let Some(inherited) = INHERITED_DESCRIPTORS.get().copied() {
            // A worker starts with the limit the host itself was given, as
            // it would under the Go runtime, and not with the raised one.
            unsafe {
                command.pre_exec(move || {
                    if libc::setrlimit(libc::RLIMIT_NOFILE, &inherited) == -1 {
                        return Err(io::Error::last_os_error());
                    }
                    Ok(())
                });
            }
        }
        if let Some(file) = &startup {
            let fd = file.as_raw_fd();
            command.env(CONFIG_SNAPSHOT_FD, "3");
            // Only syscalls are allowed between fork and exec. dup2 makes
            // the descriptor inheritable and leaves the parent copy private.
            unsafe {
                command.pre_exec(move || {
                    let result = if fd == 3 {
                        libc::fcntl(fd, libc::F_SETFD, 0)
                    } else {
                        libc::dup2(fd, 3)
                    };
                    if result == -1 {
                        return Err(io::Error::last_os_error());
                    }
                    Ok(())
                });
            }
        }
        let group = write_launch(&config, &launch).and_then(|()| ProcessGroup::spawn(&mut command));
        let group = match group {
            Ok(group) => group,
            Err(error) => {
                let _ = fs::remove_file(&config);
                return Err(error).with_context(|| format!("launch worker {}", program.service));
            }
        };
        let client = Client::new(launch.worker_socket.clone(), &launch.token);
        let worker = Self {
            program,
            group,
            launch,
            client,
            config,
            fault_recorded: AtomicBool::new(false),
            retired: CancellationToken::new(),
        };
        if let Err(error) = worker.wait_ready().await {
            tokio::task::spawn_blocking(move || drop(worker));
            return Err(error);
        }
        Ok(worker)
    }

    async fn wait_ready(&self) -> Result<()> {
        timeout(STARTUP_TIMEOUT, async {
            loop {
                if let Some(status) = self.exited()? {
                    anyhow::bail!("exited before readiness: {status}");
                }
                match self.client.request(Method::GET, READY_PATH, vec![]).await {
                    Ok(reply) if reply.status() == StatusCode::OK => {
                        for (name, expected) in [
                            (CONTRACT_HEADER, &self.launch.identity.contract_revision),
                            (
                                IMPLEMENTATION_HEADER,
                                &self.launch.identity.implementation_revision,
                            ),
                        ] {
                            let mut values = reply.headers().get_all(name).iter();
                            ensure!(
                                values
                                    .next()
                                    .is_some_and(|value| value.as_bytes() == expected.as_bytes())
                                    && values.next().is_none(),
                                "worker readiness response has invalid revision attestation",
                            );
                        }
                        let bytes = read(reply.into_body(), MAX_METADATA).await?;
                        let identity: Identity = serde_json::from_slice(&bytes)?;
                        ensure!(
                            identity == self.launch.identity,
                            "worker readiness identity differs from launch identity",
                        );
                        return Ok(());
                    }
                    _ => sleep(Duration::from_millis(10)).await,
                }
            }
        })
        .await
        .context("readiness timed out")?
        .with_context(|| format!("start worker {}", self.program.service))
    }

    pub(crate) fn exited(&self) -> io::Result<Option<ProcessExit>> {
        self.group.exited()
    }

    pub(crate) fn signal_shutdown(&self, force: bool) {
        let _ = self
            .group
            .signal(if force { libc::SIGKILL } else { libc::SIGTERM });
    }

    pub(crate) fn record_fault(&self) -> bool {
        !self.fault_recorded.swap(true, Ordering::Relaxed)
    }

    pub(crate) fn pid(&self) -> u32 {
        self.group.child.id()
    }

    pub(crate) fn still_matches(&self, program: &Program) -> bool {
        self.program.digest == program.digest
            && self.program.identity == program.identity
            && self.program.service == program.service
            && self.program.runtime == program.runtime
            && self.program.environment == program.environment
            && self.program.startup.as_ref().map(|config| config.get())
                == program.startup.as_ref().map(|config| config.get())
            && matches!(self.exited(), Ok(None))
    }
}

impl Drop for Worker {
    fn drop(&mut self) {
        self.group.stop();
        let _ = fs::remove_file(&self.config);
        let _ = fs::remove_file(&self.launch.worker_socket);
    }
}

pub(crate) struct ProcessGroup {
    child: Child,
    stopped: bool,
}

impl ProcessGroup {
    pub(crate) fn spawn(command: &mut Command) -> Result<Self> {
        let child = command.process_group(0).spawn()?;
        Ok(Self {
            child,
            stopped: false,
        })
    }

    // Keep the leader's PID reserved until the last group signal, even if
    // it exits before its children or a replacement is ready.
    pub(crate) fn exited(&self) -> io::Result<Option<ProcessExit>> {
        let mut info = std::mem::MaybeUninit::<libc::siginfo_t>::zeroed();
        let result = unsafe {
            libc::waitid(
                libc::P_PID,
                self.child.id() as libc::id_t,
                info.as_mut_ptr(),
                libc::WEXITED | libc::WNOHANG | libc::WNOWAIT,
            )
        };
        if result == -1 {
            return Err(io::Error::last_os_error());
        }
        let info = unsafe { info.assume_init() };
        if unsafe { info.si_pid() } == 0 {
            return Ok(None);
        }
        let status = unsafe { info.si_status() };
        Ok(Some(if info.si_code == libc::CLD_EXITED {
            ProcessExit {
                code: Some(status),
                signal: None,
            }
        } else {
            ProcessExit {
                code: None,
                signal: Some(status),
            }
        }))
    }

    fn signal(&self, signal: libc::c_int) -> io::Result<()> {
        // process_group(0) makes the child's PID the new process group ID.
        if unsafe { libc::kill(-(self.child.id() as libc::pid_t), signal) } == 0 {
            return Ok(());
        }
        Err(io::Error::last_os_error())
    }

    fn stop(&mut self) {
        if self.stopped {
            return;
        }
        self.stopped = true;
        let _ = self.signal(libc::SIGTERM);
        let deadline = Instant::now() + DRAIN_TIMEOUT;
        while matches!(self.exited(), Ok(None)) && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(10));
        }
        // The process group can still contain descendants after its leader
        // exits. Remove them before releasing the group's numeric identity.
        let _ = self.signal(libc::SIGKILL);
        let _ = self.child.wait();
    }
}

impl Drop for ProcessGroup {
    fn drop(&mut self) {
        self.stop();
    }
}

#[derive(Debug)]
pub(crate) struct ProcessExit {
    pub(crate) code: Option<i32>,
    pub(crate) signal: Option<i32>,
}

impl std::fmt::Display for ProcessExit {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        if let Some(code) = self.code {
            write!(formatter, "exited with status {code}")
        } else if let Some(signal) = self.signal {
            write!(formatter, "exited after signal {signal}")
        } else {
            formatter.write_str("exited")
        }
    }
}

pub(crate) fn random_hex<const N: usize>() -> Result<String> {
    let mut bytes = [0u8; N];
    getrandom::fill(&mut bytes).map_err(|error| anyhow::anyhow!("{error}"))?;
    Ok(hex(&bytes))
}

pub(crate) fn hex(bytes: &[u8]) -> String {
    use std::fmt::Write;
    let mut encoded = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        write!(&mut encoded, "{byte:02x}").expect("writing to String");
    }
    encoded
}

pub(crate) fn file_digest(binary: &Path) -> Result<String> {
    Ok(format!("sha256:{}", hex(&sha256_file(binary)?)))
}

pub(crate) fn sha256_file(path: &Path) -> io::Result<[u8; 32]> {
    let mut file = File::open(path)?;
    let mut hash = ring::digest::Context::new(&ring::digest::SHA256);
    let mut buffer = [0; 64 << 10];
    loop {
        let length = file.read(&mut buffer)?;
        if length == 0 {
            break;
        }
        hash.update(&buffer[..length]);
    }
    Ok(hash
        .finish()
        .as_ref()
        .try_into()
        .expect("SHA-256 digest size"))
}

fn private_input(directory: &Path, raw: &RawValue) -> Result<File> {
    let mut file = tempfile::tempfile_in(directory)?;
    file.write_all(raw.get().as_bytes())?;
    file.rewind()?;
    Ok(file)
}

fn write_launch(path: &Path, launch: &Launch) -> Result<()> {
    let file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)?;
    serde_json::to_writer(file, launch)?;
    Ok(())
}

#[cfg(test)]
impl Worker {
    pub(crate) fn test_worker(directory: &Path) -> Result<Self> {
        let identity = BuildIdentity {
            contract_revision: "contract".into(),
            implementation_revision: "implementation".into(),
            build_input_digest: "inputs".into(),
            go_target: "test".into(),
        };
        let launch = Launch {
            worker_socket: directory.join("worker.sock"),
            host_socket: directory.join("host.sock"),
            token: "test-worker-token".into(),
            identity: Identity {
                abi: VERSION,
                service: "app/service/tools".into(),
                contract_revision: identity.contract_revision.clone(),
                implementation_revision: identity.implementation_revision.clone(),
            },
        };
        let binary = PathBuf::from("/bin/sleep");
        Ok(Self {
            program: Program {
                service: launch.identity.service.clone(),
                process: "worker-tools".into(),
                binary: binary.clone(),
                digest: String::new(),
                runtime: "go".into(),
                identity,
                environment: vec![],
                startup: None,
                artifact: None,
            },
            group: ProcessGroup::spawn(Command::new(binary).arg("60"))?,
            client: Client::new(launch.worker_socket.clone(), &launch.token),
            config: directory.join("worker.json"),
            launch,
            fault_recorded: AtomicBool::new(false),
            retired: CancellationToken::new(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn startup_raises_the_descriptor_limit_to_the_hard_limit() {
        let current = || {
            let mut limit = libc::rlimit {
                rlim_cur: 0,
                rlim_max: 0,
            };
            assert_eq!(
                unsafe { libc::getrlimit(libc::RLIMIT_NOFILE, &mut limit) },
                0
            );
            limit
        };
        let before = current();
        raise_descriptor_limit();
        let after = current();
        assert_eq!(after.rlim_max, before.rlim_max);
        assert_eq!(
            after.rlim_cur,
            before
                .rlim_max
                .min(descriptor_ceiling())
                .max(before.rlim_cur)
        );
        // Workers are started with the limit the host inherited.
        assert_eq!(
            INHERITED_DESCRIPTORS.get().unwrap().rlim_cur,
            before.rlim_cur
        );
    }

    #[test]
    fn exit_observation_keeps_the_group_reserved_until_cleanup() -> Result<()> {
        let mut group = ProcessGroup::spawn(Command::new("/bin/sh").args(["-c", "exit 7"]))?;
        let deadline = Instant::now() + Duration::from_secs(2);
        loop {
            if let Some(exit) = group.exited()? {
                assert_eq!(exit.code, Some(7));
                break;
            }
            ensure!(Instant::now() < deadline, "fixture process did not exit");
            std::thread::sleep(Duration::from_millis(10));
        }
        assert_eq!(group.exited()?.and_then(|exit| exit.code), Some(7));
        group.stop();
        assert_eq!(
            group.signal(0).unwrap_err().raw_os_error(),
            Some(libc::ESRCH)
        );
        Ok(())
    }
}
