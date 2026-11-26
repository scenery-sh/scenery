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
    /// Set once `worker-refresh-plan` rewrites the shared build mirrors until
    /// a refreshed generation commits or the mirrors are explicitly restored.
    declarations_dirty: bool,
    /// What each build mirror last received. The Go workspace and the Rust
    /// snapshot are synchronized at different times, so one shared record
    /// would mark files as copied into a mirror that never got them.
    go_materialized: Inputs,
    rust_materialized: Inputs,
    programs: Vec<Program>,
}

impl WatcherLoop {
    pub(crate) fn start(mut config: Development) -> Result<Self> {
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
        config.rebuild.discover_go_embeds(&config.root, &active)?;
        Ok(Self {
            programs: config.rebuild.programs.clone(),
            config,
            status,
            scanner,
            _watcher: watcher,
            active: active.clone(),
            declarations_dirty: false,
            go_materialized: active.clone(),
            rust_materialized: active,
            events,
        })
    }

    pub(crate) fn status(&self) -> Arc<Status> {
        self.status.clone()
    }

    pub(crate) fn initial(&self, revision: String) -> LaunchPlan {
        LaunchPlan {
            contract_revision: revision,
            programs: self.programs.clone(),
        }
    }

    /// Reconciles edits that landed while generation 1 workers were booting
    /// before declaring readiness or publishing the initial snapshot digest.
    pub(crate) async fn reconcile_startup(
        &mut self,
        directory: &Path,
        publisher: &Publisher,
        revision: &str,
    ) {
        let scanner = self.scanner.clone();
        let ignore = self.config.rebuild.generated.clone();
        if let Ok(Ok(inputs)) = tokio::task::spawn_blocking(move || scanner.scan(&ignore)).await
            && inputs != self.active
        {
            let _ = self
                .process_change(directory, publisher, revision, inputs, Instant::now())
                .await;
            return;
        }
        self.publish(dev_inputs::digest(&self.active), None);
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
            if self.should_skip_scan(&inputs, &seen) {
                continue;
            }
            seen = inputs.clone();
            if self
                .process_change(&directory, &publisher, &revision, inputs, detected)
                .await
            {
                seen = self.active.clone();
            }
        }
    }

    /// Unchanged broken declarations retry only when a new file edit lands;
    /// `inputs == self.active` retries mirror restoration in case restoring failed.
    pub(super) fn should_skip_scan(&self, inputs: &Inputs, seen: &Inputs) -> bool {
        inputs == seen && !(self.declarations_dirty && inputs == &self.active)
    }

    /// Processes a changed snapshot; returns `true` if a concurrent edit
    /// superseded publication so the caller resets `seen` to `self.active`.
    async fn process_change(
        &mut self,
        directory: &Path,
        publisher: &Publisher,
        revision: &str,
        inputs: Inputs,
        detected: Instant,
    ) -> bool {
        let digest = dev_inputs::digest(&inputs);
        if inputs == self.active {
            if self.declarations_dirty
                && let Err(error) = self.restore_mirrors(directory, revision, &inputs).await
            {
                let failure = diagnostic(&self.config, String::new(), &format!("{error:#}"));
                self.status.update.send_modify(|status| {
                    status.failure = Some((digest, failure));
                });
                eprintln!("application mirror recovery failed: {error:#}");
                return false;
            }
            self.publish(digest, None);
            return false;
        }
        let paths: Vec<Change> = dev_inputs::changed(&self.active, &inputs)
            .into_iter()
            .map(|path| {
                let appeared = self.active.contains_key(&path) != inputs.contains_key(&path);
                (path, appeared)
            })
            .collect();
        let rebuild_started = Instant::now();
        let declarations = self.declarations_dirty
            || paths.iter().any(|(path, _)| {
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
                directory,
                publisher,
                revision,
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
            Ok(None) => return true,
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
        false
    }

    async fn restore_mirrors(
        &mut self,
        directory: &Path,
        revision: &str,
        inputs: &Inputs,
    ) -> Result<()> {
        let mut plan = self.load_refresh_plan(directory, revision).await?;
        plan.discover_go_embeds(&self.config.root, inputs)?;
        ensure!(
            plan.host_config == self.config.rebuild.host_config,
            "application configuration changed; restart the application to load its new configuration"
        );
        ensure!(
            dev_inputs::digest(inputs) == plan.source_digest,
            "application changed while restoring build mirrors"
        );
        self.go_materialized = inputs.clone();
        self.rust_materialized = inputs.clone();
        *self.status.ignore.write().expect("generated roots") = plan.generated.clone();
        self.config.rebuild = plan;
        self.declarations_dirty = false;
        Ok(())
    }

    async fn load_refresh_plan(&mut self, directory: &Path, revision: &str) -> Result<Plan> {
        self.declarations_dirty = true;
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
        Ok(serde_json::from_reader(fs::File::open(file)?)?)
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
            let mut plan = self.load_refresh_plan(directory, revision).await?;
            plan.discover_go_embeds(&self.config.root, inputs)?;
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
            materialize_go(root, workspace, &mut self.go_materialized, inputs, selected)?;
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
            self.declarations_dirty = false;
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
/// Returns the paths that differed, mirrored or not.
fn materialize(
    root: &Path,
    mirror: &Path,
    held: &mut Inputs,
    inputs: &Inputs,
    mirrored: impl Fn(&Path) -> bool,
) -> Result<Vec<PathBuf>> {
    let received = dev_inputs::changed(held, inputs);
    for path in &received {
        if mirrored(path) {
            dev_inputs::sync(root, mirror, path, inputs.get(path))?;
        }
    }
    *held = inputs.clone();
    Ok(received)
}

/// Brings the Go workspace up to `inputs` before a build. Of the files that
/// are not Go sources the workspace holds only the embedded ones, so a
/// `//go:embed` directive added to a source can name a file that never
/// changed and was never copied. Every received source therefore brings the
/// files its directives select, whether or not those changed.
fn materialize_go(
    root: &Path,
    workspace: &Path,
    held: &mut Inputs,
    inputs: &Inputs,
    plan: &Plan,
) -> Result<()> {
    let received = materialize(root, workspace, held, inputs, |path| plan.go_mirrored(path))?;
    for source in received.iter().filter(|path| {
        dev_inputs::extension(path) == "go"
            && dev_build::go_source(path)
            && inputs.contains_key(*path)
    }) {
        let text = fs::read(workspace.join(source))?;
        for path in dev_build::embedded(&String::from_utf8_lossy(&text), source, inputs.keys()) {
            dev_inputs::sync(root, workspace, path, inputs.get(path))?;
        }
    }
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
#[path = "dev_test.rs"]
mod tests;
