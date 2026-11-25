use std::collections::BTreeSet;
use std::fs;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::{Arc, RwLock};
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use notify::{Event, RecommendedWatcher, RecursiveMode, Watcher};
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use tokio::sync::{mpsc, watch};
use tokio::time::{Instant, sleep};
use tokio_util::sync::CancellationToken;

use crate::dev_build::{self, Plan};
use crate::dev_inputs::{self, Inputs, Scanner};
use crate::host::{Reply, empty, json_reply, stamp};
use crate::process::{Program, hex};
use crate::supervisor::{Generation, LaunchPlan, Publisher};

#[derive(Deserialize)]
pub(crate) struct Development {
    pub root: PathBuf,
    pub telemetry_path: Option<PathBuf>,
    pub app: Value,
    pub startup: Option<Value>,
    #[serde(default)]
    pub victoria: crate::otlp::Endpoints,
    pub cli: PathBuf,
    pub cli_environment: Vec<String>,
    environment: String,
    pub frontend_pid: i32,
    #[serde(default)]
    pub assistant_pid: i32,
    pub assistant_runtime_path: Option<PathBuf>,
    pub assistant_key_path: Option<PathBuf>,
    pub report_fd: i32,
    pub lock_fds: Vec<i32>,
    pub session_id: String,
    wait_schema: String,
    rebuild: Plan,
}

pub(crate) struct Status {
    scanner: Arc<Scanner>,
    ignore: RwLock<Vec<PathBuf>>,
    update: watch::Sender<Snapshot>,
    /// Counts the file events that may have changed an input, so a waiting
    /// request scans again when something happened instead of on a timer.
    events: watch::Sender<u64>,
    schema: String,
}

/// A changed input and whether it was added or removed, not edited.
type Change = (PathBuf, bool);

#[derive(Default)]
struct Snapshot {
    digest: String,
    published_at: String,
    failure: Option<(String, Value)>,
    timing: Option<Value>,
}

impl Status {
    pub(crate) async fn reply(
        &self,
        query: Option<&str>,
        publication: &watch::Receiver<Option<Arc<Generation>>>,
    ) -> Result<Reply> {
        let query = crate::values::query_pairs(query);
        let timeout = query
            .get("timeout_ms")
            .and_then(|values| values.first())
            .and_then(|value| value.parse::<u64>().ok())
            .unwrap_or(0)
            .min(120_000);
        let started = Instant::now();
        let deadline = started + Duration::from_millis(timeout);
        let mut updates = self.update.subscribe();
        let mut events = self.events.subscribe();
        let mut target = String::new();
        let mut scanned: Option<Instant> = None;
        loop {
            // A process that keeps writing files must not turn one waiting
            // request into a scan loop.
            if let Some(at) = scanned {
                tokio::time::sleep_until(at + Duration::from_millis(50)).await;
            }
            events.borrow_and_update();
            let scanner = self.scanner.clone();
            let ignore = self.ignore.read().expect("generated roots").clone();
            let current = tokio::task::spawn_blocking(move || scanner.scan(&ignore)).await?;
            scanned = Some(Instant::now());
            let current = match current {
                Ok(inputs) => dev_inputs::digest(&inputs),
                Err(error)
                    if error
                        .downcast_ref::<io::Error>()
                        .is_some_and(|e| e.kind() == io::ErrorKind::NotFound) =>
                {
                    return Ok(json_reply(
                        hyper::StatusCode::OK,
                        &json!({
                            "kind":"scenery.dev-runtime.wait", "schema_revision":self.schema,
                            "ok":false, "outcome":"source_missing", "source_freshness":"source_missing",
                            "waited_ms":started.elapsed().as_millis()
                        }),
                    ));
                }
                Err(error) => return Err(error),
            };
            if target.is_empty() {
                target = current.clone();
            }
            {
                let snapshot = updates.borrow_and_update();
                let serving = publication.borrow().as_ref().map(|generation| {
                    json!({
                        "generation": generation.number, "pid":std::process::id().to_string(),
                        "contract_revision":generation.identity.contract_revision,
                        "implementation_revision":generation.identity.implementation_revision,
                        "build_input_digest":generation.identity.build_input_digest,
                        "framework_source_digest":"",
                        "source_snapshot_digest":snapshot.digest,
                        "published_at":snapshot.published_at,
                    })
                });
                let failure = snapshot
                    .failure
                    .as_ref()
                    .filter(|(digest, _)| digest == &current);
                let outcome = if target != current {
                    "superseded"
                } else if serving.is_some() && snapshot.digest == current {
                    "served"
                } else if failure.is_some() {
                    "failed"
                } else if timeout == 0 {
                    "building"
                } else if Instant::now() >= deadline {
                    "timeout"
                } else {
                    ""
                };
                if !outcome.is_empty() {
                    let mut result = json!({
                        "kind":"scenery.dev-runtime.wait", "schema_revision":self.schema,
                        "ok": outcome == "served", "outcome":outcome,
                        "target_source_snapshot_digest":target,
                        "source_freshness":if outcome == "served" { "current" } else { "stale" },
                        "waited_ms":started.elapsed().as_millis()
                    });
                    if outcome == "superseded" {
                        result["current_source_snapshot_digest"] = json!(current);
                    }
                    if let Some(serving) = serving {
                        result["serving"] = serving;
                    }
                    if let Some(timing) = &snapshot.timing {
                        result["rebuild_timing"] = timing.clone();
                    }
                    if let Some((_, failure)) = failure {
                        result["compile_error"] = failure["stderr"].clone();
                        result["build_failure"] = failure.clone();
                    }
                    return Ok(json_reply(hyper::StatusCode::OK, &result));
                }
            }
            // A scan only reads what changed, but it still walks the tree:
            // repeat it when a file event or a publication says there is
            // something to see, and otherwise rarely.
            tokio::select! {
                _ = updates.changed() => {}
                _ = events.changed() => {}
                () = tokio::time::sleep_until(deadline.min(Instant::now() + Duration::from_secs(1))) => {}
            }
        }
    }
}

pub(crate) struct WatcherLoop {
    config: Development,
    _watcher: RecommendedWatcher,
    events: mpsc::Receiver<()>,
    status: Arc<Status>,
    scanner: Arc<Scanner>,
    active: Inputs,
    /// What each build mirror last received. The Go workspace and the Rust
    /// snapshot are synchronized at different times, so one shared record
    /// would mark files as copied into a mirror that never got them.
    go_materialized: Inputs,
    rust_materialized: Inputs,
    programs: Vec<Program>,
}

impl WatcherLoop {
    pub(crate) fn start(config: Development) -> Result<Self> {
        let (update, _) = watch::channel(Snapshot::default());
        let scanner = Arc::new(Scanner::new(config.root.clone()));
        let status = Arc::new(Status {
            scanner: scanner.clone(),
            ignore: RwLock::new(config.rebuild.generated.clone()),
            update,
            events: watch::channel(0).0,
            schema: config.wait_schema.clone(),
        });
        let (send, events) = mpsc::channel(1);
        let event_status = status.clone();
        let mut watcher = notify::recommended_watcher(move |event: notify::Result<Event>| {
            let relevant = match event {
                Err(_) => true,
                Ok(event) if event.need_rescan() => true,
                Ok(event) if event.kind.is_access() => false,
                Ok(event) => {
                    let ignored = event_status.ignore.read().expect("generated roots");
                    event.paths.iter().any(|path| {
                        path.strip_prefix(event_status.scanner.root())
                            .is_ok_and(|path| !dev_inputs::ignored(path, &ignored))
                    })
                }
            };
            if relevant {
                event_status.events.send_modify(|count| *count += 1);
                let _ = send.try_send(());
            }
        })?;
        watcher.watch(&config.root, RecursiveMode::Recursive)?;
        let active = scanner.scan(&config.rebuild.generated)?;
        ensure!(
            dev_inputs::digest(&active) == config.rebuild.source_digest,
            "application changed during startup; run it again to build the latest sources"
        );
        Ok(Self {
            programs: config.rebuild.programs.clone(),
            config,
            status,
            scanner,
            _watcher: watcher,
            active: active.clone(),
            go_materialized: active.clone(),
            rust_materialized: active,
            events,
        })
    }

    pub(crate) fn status(&self) -> Arc<Status> {
        self.status.clone()
    }

    pub(crate) fn initial(&self, revision: String) -> LaunchPlan {
        self.publish(dev_inputs::digest(&self.active), None);
        LaunchPlan {
            contract_revision: revision,
            programs: self.programs.clone(),
        }
    }

    fn publish(&self, digest: String, timing: Option<Value>) {
        self.status.update.send_replace(Snapshot {
            digest,
            published_at: stamp(time::OffsetDateTime::now_utc()),
            failure: None,
            timing,
        });
    }

    pub(crate) async fn run(mut self, directory: PathBuf, publisher: Publisher, revision: String) {
        let mut reconcile = tokio::time::interval(Duration::from_secs(5));
        reconcile.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut seen = self.active.clone();
        loop {
            let detected = tokio::select! {
                _ = reconcile.tick() => Instant::now(),
                Some(()) = self.events.recv() => {
                    let detected = Instant::now();
                    // Wait for a quiet moment, but not forever: a process
                    // that keeps writing must not starve every rebuild.
                    let settled = detected + Duration::from_millis(250);
                    while Instant::now() < settled
                        && tokio::time::timeout(Duration::from_millis(20), self.events.recv()).await.is_ok()
                    {}
                    detected
                }
            };
            let scanner = self.scanner.clone();
            let ignore = self.config.rebuild.generated.clone();
            let inputs = match tokio::task::spawn_blocking(move || scanner.scan(&ignore)).await {
                Ok(Ok(inputs)) => inputs,
                Ok(Err(error)) => {
                    eprintln!("could not read application inputs: {error:#}");
                    continue;
                }
                Err(error) => {
                    eprintln!("input scan failed: {error}");
                    continue;
                }
            };
            if inputs == seen {
                continue;
            }
            seen = inputs.clone();
            let digest = dev_inputs::digest(&inputs);
            if inputs == self.active {
                self.publish(digest, None);
                continue;
            }
            let paths: Vec<Change> = dev_inputs::changed(&self.active, &inputs)
                .into_iter()
                .map(|path| {
                    let appeared = self.active.contains_key(&path) != inputs.contains_key(&path);
                    (path, appeared)
                })
                .collect();
            let rebuild_started = Instant::now();
            let declarations = paths.iter().any(|(path, _)| {
                dev_inputs::declaration(path)
                    || self
                        .config
                        .rebuild
                        .declarations
                        .as_ref()
                        .is_some_and(|set| set.contains_key(path))
            });
            let mut module = String::new();
            let mut times = [0.0, 0.0, 0.0, 0.0];
            let result = self
                .refresh(
                    &directory,
                    &publisher,
                    &revision,
                    &inputs,
                    &paths,
                    declarations,
                    &mut module,
                    &mut times,
                )
                .await;
            if times[0] == 0.0 {
                times[0] = ms(rebuild_started.elapsed());
            }
            if declarations && times[3] == 0.0 {
                times[3] = times[0];
            }
            let (generation, rebuilt, failure) = match result {
                Ok(Some((generation, rebuilt))) => (generation, rebuilt, None),
                Ok(None) => {
                    seen = self.active.clone();
                    continue;
                }
                Err(error) => (
                    publisher.generation(),
                    BTreeSet::new(),
                    Some(format!("{error:#}")),
                ),
            };
            let skipped = self
                .config
                .rebuild
                .commands
                .iter()
                .map(|build| &build.module)
                .filter(|module| !rebuilt.contains(*module))
                .collect::<BTreeSet<_>>();
            let programs = self
                .programs
                .iter()
                .filter(|program| {
                    self.config.rebuild.commands.iter().any(|build| {
                        build.process == program.process && rebuilt.contains(&build.module)
                    })
                })
                .collect::<Vec<_>>();
            let rebuilt_services = programs.iter().map(|p| &p.service).collect::<Vec<_>>();
            let links = programs
                .iter()
                .filter(|p| p.runtime == "go")
                .map(|p| &p.service)
                .collect::<Vec<_>>();
            let rust = programs
                .iter()
                .filter(|p| p.runtime == "rust")
                .map(|p| p.service.split("/service/").next().unwrap_or(""))
                .collect::<Vec<_>>();
            let js = programs
                .iter()
                .filter(|p| matches!(p.runtime.as_str(), "js" | "ts"))
                .map(|p| &p.service)
                .collect::<Vec<_>>();
            let elapsed = ms(detected.elapsed());
            let mut timing = json!({
                "generation":generation, "declarations":declarations, "go":times[1] > 0.0 || declarations && self.config.rebuild.go_workspace.is_some(),
                "rebuilt":rebuilt_services, "go_links":links, "cargo":rust, "js":js, "rebuilt_modules":rebuilt, "skipped_modules":skipped, "skipped":skipped.len(),
                "detect_ms":ms(rebuild_started - detected), "compile_ms":times[0],
                "link_ms":times[1], "ready_ms":times[2], "contract_ms":times[3],
                "go_ms":times[1], "js_ms":0.0, "cargo_ms":0.0, "total_ms":elapsed,
                "trigger_kind":if declarations { "contract_or_config" } else { "body_only" },
            });
            let ok = failure.is_none();
            if failure.is_none() {
                timing["edit_to_ready_ms"] = json!(elapsed);
            }
            if let Some(error) = failure {
                let failure = diagnostic(&self.config, module, &error);
                self.status.update.send_modify(|status| {
                    status.failure = Some((digest, failure));
                    status.timing = Some(timing.clone());
                });
                eprintln!("application update failed: {error}");
            } else {
                self.active = inputs;
                self.publish(digest, Some(timing.clone()));
            }
            eprintln!("worker rebuild {timing}");
            if let Some(path) = self
                .config
                .telemetry_path
                .as_deref()
                .filter(|path| !path.as_os_str().is_empty())
            {
                timing["ok"] = json!(ok);
                if !ok {
                    timing["error_code"] = json!("SCN8003");
                }
                let event = json!({"at":stamp(time::OffsetDateTime::now_utc()), "app":self.config.app,
                    "kind":"rebuild", "generation":generation, "rebuild":timing});
                if let Err(error) = journal(path, &event).await {
                    eprintln!("could not record rebuild telemetry: {error:#}");
                }
            }
        }
    }

    #[allow(clippy::too_many_arguments)]
    async fn refresh(
        &mut self,
        directory: &Path,
        publisher: &Publisher,
        revision: &str,
        inputs: &Inputs,
        paths: &[Change],
        declarations: bool,
        module: &mut String,
        times: &mut [f64; 4],
    ) -> Result<Option<(u64, BTreeSet<String>)>> {
        let begin = Instant::now();
        let plan = if declarations {
            let staging = tempfile::tempdir_in(directory)?;
            let file = staging.path().join("rebuild.json");
            dev_build::run(
                &self.config.cli,
                &[
                    "worker-refresh-plan".into(),
                    self.config.root.display().to_string(),
                    revision.into(),
                    self.config.environment.clone(),
                    file.display().to_string(),
                ],
                &self.config.root,
                Some(&self.config.cli_environment),
                staging.path(),
            )
            .await?;
            let plan: Plan = serde_json::from_reader(fs::File::open(file)?)?;
            ensure!(
                plan.host_config == self.config.rebuild.host_config,
                "application configuration changed; restart the application to load its new configuration"
            );
            if dev_inputs::digest(inputs) != plan.source_digest {
                return Ok(None);
            }
            // The CLI rebuilt both mirrors from these inputs.
            self.go_materialized = inputs.clone();
            self.rust_materialized = inputs.clone();
            times[3] = ms(begin.elapsed());
            Some(plan)
        } else {
            None
        };
        let selected = plan.as_ref().unwrap_or(&self.config.rebuild);
        let root = &self.config.root;
        let workspace = if let Some(workspace) = &selected.go_workspace {
            let lock = dev_build::lock_file(&workspace.join(".scenery-workspace.lock")).await?;
            materialize(root, workspace, &mut self.go_materialized, inputs, |path| {
                selected.go_mirrored(path)
            })?;
            Some(lock)
        } else {
            None
        };
        let cargo_lock = if let Some(workspace) = &selected.rust_workspace
            && selected.commands.iter().any(|build| {
                build.cargo_lock().is_some()
                    && paths
                        .iter()
                        .any(|(path, appeared)| build.affected(selected, path, *appeared))
            })
            && let Some(lock) = selected
                .commands
                .iter()
                .find_map(|build| build.cargo_lock())
        {
            let lease = dev_build::lock_file(&lock).await?;
            ensure!(
                workspace.canonicalize()? == *workspace,
                "invalid Cargo source workspace"
            );
            // Every path the snapshot has not received yet, not only the
            // ones of this edit: an earlier refresh may have skipped Cargo.
            let programs = &self.programs;
            materialize(
                root,
                workspace,
                &mut self.rust_materialized,
                inputs,
                |path| {
                    !dev_inputs::declaration(path)
                        && selected.commands.iter().any(|command| {
                            programs.iter().any(|program| {
                                program.process == command.process && program.runtime == "rust"
                            }) && command.affected(selected, path, false)
                        })
                },
            )?;
            Some(lease)
        } else {
            None
        };
        let mut next = self.programs.clone();
        let mut rebuilt = BTreeSet::new();
        let mut go_dependencies = Vec::new();
        for (index, command) in selected.commands.iter().enumerate() {
            let program = next
                .iter_mut()
                .find(|program| program.process == command.process)
                .context("unknown worker build")?;
            let mut candidate = program.clone();
            if let Some(helper) = plan.as_ref().and_then(|plan| {
                plan.programs
                    .iter()
                    .find(|item| item.process == command.process)
            }) {
                // Runtime wiring belongs to the live host. The helper's
                // subprocess environment is only used to prepare commands.
                candidate.startup = helper.startup.clone();
            }
            if paths
                .iter()
                .any(|(path, appeared)| command.affected(selected, path, *appeared))
            {
                *module = command.module.clone();
                let start = Instant::now();
                let mut identity = Sha256::new();
                identity.update(dev_inputs::digest(inputs));
                identity.update(&program.identity.contract_revision);
                let identity = format!("sha256:{}", hex(&identity.finalize()));
                let built = command
                    .compile(root, directory, &candidate, &identity)
                    .await;
                times[1] += if program.runtime == "go" {
                    ms(start.elapsed())
                } else {
                    0.0
                };
                times[0] = ms(begin.elapsed());
                candidate = built?;
                if command.tool() == "go" {
                    let mut updated = command.clone();
                    let embeds = updated.refresh_go_roots(directory).await?;
                    go_dependencies.push((index, updated, embeds));
                }
            }
            if candidate.digest != program.digest
                || candidate.startup.as_ref().map(|v| v.get())
                    != program.startup.as_ref().map(|v| v.get())
            {
                rebuilt.insert(command.module.clone());
                *program = candidate;
            }
        }
        drop(workspace);
        drop(cargo_lock);
        times[0] = ms(begin.elapsed());
        let ready = Instant::now();
        let scanner = self.scanner.clone();
        let generated = selected.generated.clone();
        let generation = publisher
            .replace(
                LaunchPlan {
                    contract_revision: revision.into(),
                    programs: next.clone(),
                },
                // The check walks the tree: it runs on a blocking thread and
                // reads only what changed since the scan that started this.
                || async move {
                    let current =
                        tokio::task::spawn_blocking(move || scanner.scan(&generated)).await??;
                    Ok(current == *inputs)
                },
            )
            .await?;
        times[2] = ms(ready.elapsed());
        let Some(generation) = generation else {
            return Ok(None);
        };
        if let Some(plan) = plan {
            *self.status.ignore.write().expect("generated roots") = plan.generated.clone();
            self.config.rebuild = plan;
        }
        for (index, command, embeds) in go_dependencies {
            self.config.rebuild.commands[index] = command;
            self.config.rebuild.go_inputs.extend(embeds);
        }
        self.programs = next;
        Ok(Some((generation, rebuilt)))
    }
}

/// Copies into `mirror` the inputs it has not received since `held` and
/// records that it holds them now. Each build mirror keeps its own record:
/// a refresh that skips one mirror must not mark its files as copied.
fn materialize(
    root: &Path,
    mirror: &Path,
    held: &mut Inputs,
    inputs: &Inputs,
    mirrored: impl Fn(&Path) -> bool,
) -> Result<()> {
    for path in dev_inputs::changed(held, inputs) {
        if mirrored(&path) {
            dev_inputs::sync(root, mirror, &path, inputs.get(&path))?;
        }
    }
    *held = inputs.clone();
    Ok(())
}

fn diagnostic(config: &Development, module: String, stderr: &str) -> Value {
    use std::sync::LazyLock;
    static LOCATION: LazyLock<regex::Regex> = LazyLock::new(|| {
        regex::Regex::new(
        r"(?m)^\s*(?:(?:-->|at)\s+)?([^\n:()]+\.(?:go|rs|[cm]?[jt]sx?|scn))[:(]([0-9]+)[:,]([0-9]+)"
    ).expect("compiler location pattern")
    });
    let mut result = json!({"stderr": stderr});
    if !module.is_empty() {
        result["module"] = json!(module);
    }
    if let Some(location) = LOCATION.captures(stderr) {
        let source = Path::new(location[1].trim());
        let relative = source.strip_prefix(&config.root).unwrap_or(source);
        result["file"] = json!(relative);
        result["line"] = json!(location[2].parse::<u64>().unwrap_or(0));
        result["column"] = json!(location[3].parse::<u64>().unwrap_or(0));
    }
    result
}

fn ms(duration: Duration) -> f64 {
    duration.as_secs_f64() * 1000.0
}

pub(crate) fn write_endpoint(config: &Development, control: &Path, token: &str) -> Result<PathBuf> {
    use std::io::Write as _;
    let parent = config.root.join(".scenery");
    ensure!(
        parent.canonicalize()? == parent,
        "development descriptor must stay in the application workspace"
    );
    let file = parent.join("worker-dev.json");
    let mut temporary = tempfile::NamedTempFile::new_in(parent)?;
    serde_json::to_writer(
        &mut temporary,
        &json!({
            "root":config.root, "socket":control, "control":control, "token":token, "session_id":config.session_id,
        }),
    )?;
    temporary.flush()?;
    temporary.persist(&file)?;
    Ok(file)
}

pub(crate) async fn journal(path: &Path, event: &Value) -> Result<()> {
    use std::io::Write as _;
    use std::os::unix::fs::OpenOptionsExt as _;
    let parent = path.parent().context("missing telemetry directory")?;
    fs::create_dir_all(parent)?;
    let lock = path.with_extension("jsonl.lock");
    let _lease =
        tokio::time::timeout(Duration::from_millis(250), dev_build::lock_file(&lock)).await??;
    let mut record = serde_json::to_vec(event)?;
    record.push(b'\n');
    if fs::metadata(path).is_ok_and(|file| file.len() + record.len() as u64 > 4 << 20) {
        let root = parent.join("telemetry-archive");
        let stamp = time::OffsetDateTime::now_utc().format(time::macros::format_description!(
            "[year][month][day]T[hour][minute][second].[subsecond digits:9]Z"
        ))?;
        let archive = root.join(format!("cli-{stamp}"));
        fs::create_dir_all(&archive)?;
        fs::rename(path, archive.join("telemetry.jsonl"))?;
        let mut archives = fs::read_dir(&root)?
            .filter_map(|entry| entry.ok())
            .filter(|entry| {
                entry.file_type().is_ok_and(|kind| kind.is_dir())
                    && entry.file_name().to_string_lossy().starts_with("cli-")
            })
            .map(|entry| entry.path())
            .collect::<Vec<_>>();
        archives.sort();
        for archive in &archives[..archives.len().saturating_sub(8)] {
            let file = archive.join("telemetry.jsonl");
            if file.symlink_metadata().is_ok_and(|info| info.is_file()) {
                let _ = fs::remove_file(file);
                let _ = fs::remove_dir(archive);
            }
        }
    }
    fs::OpenOptions::new()
        .append(true)
        .create(true)
        .mode(0o600)
        .open(path)?
        .write_all(&record)?;
    Ok(())
}

pub(crate) fn shutdown(stop: CancellationToken) -> Reply {
    tokio::spawn(async move {
        sleep(Duration::from_millis(25)).await;
        stop.cancel();
    });
    empty(hyper::StatusCode::NO_CONTENT)
}

#[cfg(test)]
mod tests {
    use super::*;
    use http_body_util::BodyExt;

    #[test]
    fn each_mirror_receives_what_it_missed_since_its_own_last_sync() -> Result<()> {
        let temp = tempfile::tempdir()?;
        let base = temp.path().canonicalize()?;
        let (root, go, rust) = (base.join("app"), base.join("go"), base.join("rust"));
        for directory in [&root, &go, &rust] {
            fs::create_dir(directory)?;
        }
        let write = |name: &str, content: &str| fs::write(root.join(name), content);
        let read = |mirror: &Path, name: &str| fs::read_to_string(mirror.join(name));
        write("a.rs", "fn a() {}")?;
        write("b.rs", "fn b() {}")?;
        write("README.md", "first")?;
        let scanner = Scanner::new(root.clone());
        let rust_source = |path: &Path| dev_inputs::extension(path) == "rs";
        let any = |_: &Path| true;
        let (mut go_held, mut rust_held) = (Inputs::new(), Inputs::new());
        let started = scanner.scan(&[])?;
        materialize(&root, &go, &mut go_held, &started, any)?;
        materialize(&root, &rust, &mut rust_held, &started, rust_source)?;

        // A syntax error reaches the Rust snapshot and fails its build.
        write("a.rs", "fn a( {}")?;
        let broken = scanner.scan(&[])?;
        materialize(&root, &go, &mut go_held, &broken, any)?;
        materialize(&root, &rust, &mut rust_held, &broken, rust_source)?;
        assert_eq!(read(&rust, "a.rs")?, "fn a( {}");

        // The error is undone together with an edit that no Cargo build
        // reads: this refresh skips the Rust snapshot.
        write("a.rs", "fn a() {}")?;
        write("README.md", "second")?;
        let reverted = scanner.scan(&[])?;
        materialize(&root, &go, &mut go_held, &reverted, any)?;
        assert_eq!(read(&rust, "a.rs")?, "fn a( {}");

        // The next Rust edit must bring the reverted file along. One shared
        // record had marked it as copied, so Cargo kept compiling the error.
        write("b.rs", "fn b() -> u8 { 1 }")?;
        let edited = scanner.scan(&[])?;
        materialize(&root, &go, &mut go_held, &edited, any)?;
        materialize(&root, &rust, &mut rust_held, &edited, rust_source)?;
        for name in ["a.rs", "b.rs"] {
            assert_eq!(read(&rust, name)?, read(&root, name)?, "{name}");
        }
        assert!(!rust.join("README.md").exists());
        for name in ["a.rs", "b.rs", "README.md"] {
            assert_eq!(read(&go, name)?, read(&root, name)?, "{name}");
        }
        Ok(())
    }

    #[tokio::test]
    async fn wait_scans_again_only_when_a_file_event_or_publication_arrives() -> Result<()> {
        let root = tempfile::tempdir()?;
        fs::write(root.path().join("worker.go"), "package worker")?;
        let scanner = Arc::new(Scanner::new(root.path().into()));
        let digest = dev_inputs::digest(&scanner.scan(&[])?);
        let status = Arc::new(Status {
            scanner: scanner.clone(),
            ignore: RwLock::new(Vec::new()),
            events: watch::channel(0).0,
            schema: "fixture".into(),
            update: watch::channel(Snapshot::default()).0,
        });
        let publication = watch::channel(None).1;
        let waiting = tokio::spawn({
            let status = status.clone();
            async move {
                let reply = status.reply(Some("timeout_ms=30000"), &publication).await?;
                let body = reply.into_body().collect().await?.to_bytes();
                Ok::<Value, anyhow::Error>(serde_json::from_slice(&body)?)
            }
        });
        // The edit is announced like the file watcher announces it. The
        // waiting request must notice it at once, not on its next timer.
        tokio::time::sleep(Duration::from_millis(50)).await;
        fs::write(root.path().join("worker.go"), "package edited")?;
        let announced = Instant::now();
        status.events.send_modify(|count| *count += 1);
        let reply = waiting.await??;
        assert!(announced.elapsed() < Duration::from_millis(500));
        assert_eq!(reply["outcome"], "superseded");
        assert_eq!(reply["target_source_snapshot_digest"], digest);
        Ok(())
    }

    #[tokio::test]
    async fn wait_returns_only_the_failure_for_the_requested_source() -> Result<()> {
        let root = tempfile::tempdir()?;
        let file = root.path().join("worker.go");
        fs::write(&file, "bad source")?;
        let scanner = Arc::new(Scanner::new(root.path().into()));
        let (update, _) = watch::channel(Snapshot {
            failure: Some((
                dev_inputs::digest(&scanner.scan(&[])?),
                json!({"module":"worker","file":"worker.go","line":1,"column":2,"stderr":"unexpected token"}),
            )),
            ..Snapshot::default()
        });
        let status = Status {
            scanner,
            ignore: RwLock::new(Vec::new()),
            events: watch::channel(0).0,
            schema: "fixture".into(),
            update,
        };
        let publication = watch::channel(None).1;
        let start = Instant::now();
        let failed = status.reply(Some("timeout_ms=30000"), &publication).await?;
        let failed: Value =
            serde_json::from_slice(&failed.into_body().collect().await?.to_bytes())?;
        assert_eq!(failed["outcome"], "failed");
        assert_eq!(failed["build_failure"]["file"], "worker.go");
        assert!(start.elapsed() < Duration::from_secs(1));
        fs::write(file, "new source")?;
        let next = status.reply(None, &publication).await?;
        let next: Value = serde_json::from_slice(&next.into_body().collect().await?.to_bytes())?;
        assert_eq!(next["outcome"], "building");
        assert!(next.get("build_failure").is_none());
        Ok(())
    }
}
