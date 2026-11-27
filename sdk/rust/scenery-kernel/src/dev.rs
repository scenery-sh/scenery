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
use crate::dev_inputs::{self, Inputs, Scanner, materialize, materialize_go};
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
        scanner.set_roots(config.rebuild.roots.clone());
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
                            .is_ok_and(|path| {
                                event_status.scanner.is_explicit(path)
                                    || (!dev_inputs::ignored(path, &ignored)
                                        && event_status.scanner.in_scope(path))
                            })
                    })
                }
            };
            if relevant {
                event_status.events.send_modify(|count| *count += 1);
                let _ = send.try_send(());
            }
        })?;
        watcher.watch(&config.root, RecursiveMode::Recursive)?;
        scanner.set_explicit(config.rebuild.explicit_js_dependencies());
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

    /// Publishes the initial snapshot digest immediately so the host can bind
    /// its public listener and declare readiness before running the startup
    /// filesystem reconciliation scan asynchronously.
    pub(crate) fn publish_initial(&self) {
        self.publish(dev_inputs::digest(&self.active), None);
    }

    /// Reconciles edits that landed while generation 1 workers were booting.
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
        self.reconcile_startup(&directory, &publisher, &revision)
            .await;
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
                let failure =
                    dev_build::diagnostic(&self.config.root, String::new(), &format!("{error:#}"));
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
        let any_worker_affected = self.config.rebuild.commands.iter().any(|build| {
            paths
                .iter()
                .any(|(path, appeared)| build.affected(&self.config.rebuild, path, *appeared))
        });
        let any_migration_affected = self.config.rebuild.has_migrations
            && paths
                .iter()
                .any(|(path, _)| dev_inputs::extension(path) == "sql");
        if !declarations && !any_worker_affected && !any_migration_affected {
            // Edits outside worker/declaration/migration inputs (such as frontend
            // dev files served by Vite) only advance the published snapshot digest
            // without running a rebuild or logging a no-op worker rebuild event.
            self.active = inputs;
            self.publish(digest, None);
            return false;
        }
        let buckets = trigger_buckets(&paths);
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
        let (generation, rebuilt, committed, failure) = match result {
            Ok(Some((generation, rebuilt, committed))) => {
                (generation, rebuilt, Some(committed), None)
            }
            Ok(None) => return true,
            Err(error) => (
                publisher.generation(),
                BTreeSet::new(),
                None,
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
        let python = programs
            .iter()
            .filter(|p| p.runtime == "python")
            .map(|p| &p.service)
            .collect::<Vec<_>>();
        let elapsed = ms(detected.elapsed());
        let mut timing = json!({
            "generation":generation, "declarations":declarations, "go":times[1] > 0.0 || declarations && self.config.rebuild.go_workspace.is_some(),
            "rebuilt":rebuilt_services, "go_links":links, "cargo":rust, "js":js, "python":python, "rebuilt_modules":rebuilt, "skipped_modules":skipped, "skipped":skipped.len(),
            "detect_ms":ms(rebuild_started - detected), "compile_ms":times[0],
            "link_ms":times[1], "ready_ms":times[2], "contract_ms":times[3],
            "go_ms":times[1], "js_ms":0.0, "cargo_ms":0.0, "total_ms":elapsed,
            "trigger_kind":if declarations { "contract_or_config" } else { "body_only" },
            "trigger_buckets":buckets,
        });
        let ok = failure.is_none();
        if failure.is_none() {
            timing["edit_to_ready_ms"] = json!(elapsed);
        }
        let mut sync_seen = false;
        if let Some(error) = failure {
            let failure = dev_build::diagnostic(&self.config.root, module, &error);
            self.status.update.send_modify(|status| {
                status.failure = Some((digest, failure));
                status.timing = Some(timing.clone());
            });
            eprintln!("application update failed: {error}");
        } else {
            let committed = committed.unwrap_or_else(|| inputs.clone());
            sync_seen = committed != inputs;
            let published_digest = dev_inputs::digest(&committed);
            self.active = committed;
            self.publish(published_digest, Some(timing.clone()));
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
        sync_seen
    }

    async fn restore_mirrors(
        &mut self,
        directory: &Path,
        revision: &str,
        inputs: &Inputs,
    ) -> Result<()> {
        let mut plan = self.load_refresh_plan(directory, revision).await?;
        let effective = if plan.roots != self.config.rebuild.roots {
            let scanner = self.scanner.clone();
            let generated = plan.generated.clone();
            let roots = plan.roots.clone();
            let explicit = plan.explicit_js_dependencies();
            tokio::task::spawn_blocking(move || {
                scanner.scan_with_roots(&generated, &roots, &explicit)
            })
            .await??
        } else {
            inputs.clone()
        };
        plan.discover_go_embeds(&self.config.root, &effective)?;
        ensure!(
            plan.host_config == self.config.rebuild.host_config,
            "application configuration changed; restart the application to load its new configuration"
        );
        ensure!(
            dev_inputs::digest(&effective) == plan.source_digest,
            "application changed while restoring build mirrors"
        );
        self.go_materialized = effective.clone();
        self.rust_materialized = effective.clone();
        self.active = effective;
        *self.status.ignore.write().expect("generated roots") = plan.generated.clone();
        self.scanner.set_roots(plan.roots.clone());
        self.scanner.set_explicit(plan.explicit_js_dependencies());
        self.config.rebuild = plan;
        self.declarations_dirty = false;
        Ok(())
    }

    async fn load_refresh_plan(&mut self, directory: &Path, revision: &str) -> Result<Plan> {
        self.declarations_dirty = true;
        let staging = tempfile::tempdir_in(directory)?;
        let file = staging.path().join("rebuild.json");
        let explicit_file = staging.path().join("explicit.json");
        let commands: std::collections::BTreeMap<&str, &BTreeSet<PathBuf>> = self
            .config
            .rebuild
            .commands
            .iter()
            .filter(|build| build.tool() == "bun" && !build.dependencies.is_empty())
            .map(|build| (build.process.as_str(), &build.dependencies))
            .collect();
        serde_json::to_writer(
            fs::File::create(&explicit_file)?,
            &json!({
                "js_dependencies": self.scanner.explicit_paths(),
                "commands": commands,
            }),
        )?;
        dev_build::run(
            &self.config.cli,
            &[
                "worker-refresh-plan".into(),
                self.config.root.display().to_string(),
                revision.into(),
                self.config.environment.clone(),
                file.display().to_string(),
                explicit_file.display().to_string(),
            ],
            &self.config.root,
            Some(&self.config.cli_environment),
            staging.path(),
        )
        .await?;
        Ok(serde_json::from_reader(fs::File::open(file)?)?)
    }

    async fn check_migrations(&self, directory: &Path, selected: &Plan) -> Result<()> {
        if !selected.has_migrations {
            return Ok(());
        }
        let staging = tempfile::tempdir_in(directory)?;
        let sql_file = staging.path().join("sql.json");
        let raw = selected
            .sql_requirements
            .as_deref()
            .map_or("[]", serde_json::value::RawValue::get);
        fs::write(&sql_file, raw)?;
        dev_build::run(
            &self.config.cli,
            &[
                "worker-check-migrations".into(),
                self.config.root.display().to_string(),
                self.config.environment.clone(),
                sql_file.display().to_string(),
            ],
            &self.config.root,
            Some(&self.config.cli_environment),
            staging.path(),
        )
        .await?;
        Ok(())
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
    ) -> Result<Option<(u64, BTreeSet<String>, Inputs)>> {
        let begin = Instant::now();
        let mut rescanned_inputs: Option<Inputs> = None;
        let plan = if declarations {
            let mut plan = self.load_refresh_plan(directory, revision).await?;
            let effective = if plan.roots != self.config.rebuild.roots {
                let scanner = self.scanner.clone();
                let generated = plan.generated.clone();
                let roots = plan.roots.clone();
                let explicit = self.scanner.explicit_paths();
                let scanned = tokio::task::spawn_blocking(move || {
                    scanner.scan_with_roots(&generated, &roots, &explicit)
                })
                .await??;
                rescanned_inputs.insert(scanned) as &Inputs
            } else {
                inputs
            };
            plan.discover_go_embeds(&self.config.root, effective)?;
            ensure!(
                plan.host_config == self.config.rebuild.host_config,
                "application configuration changed; restart the application to load its new configuration"
            );
            if dev_inputs::digest(effective) != plan.source_digest {
                return Ok(None);
            }
            // The CLI rebuilt both mirrors from these inputs.
            self.go_materialized = effective.clone();
            self.rust_materialized = effective.clone();
            times[3] = ms(begin.elapsed());
            Some(plan)
        } else {
            None
        };
        let inputs = rescanned_inputs.as_ref().unwrap_or(inputs);
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
        let mut bun_dependencies = Vec::new();
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
                    .compile(root, directory, &selected.generated, &candidate, &identity)
                    .await;
                times[1] += if program.runtime == "go" {
                    ms(start.elapsed())
                } else {
                    0.0
                };
                times[0] = ms(begin.elapsed());
                let (built, deps) = built?;
                candidate = built;
                if let Some(deps) = deps {
                    bun_dependencies.push((index, deps));
                }
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
        let previous_explicit = self.scanner.explicit_paths();
        let mut next_explicit = collect_bun_explicit(selected, &bun_dependencies);
        let has_new_explicit = next_explicit.iter().any(|path| {
            !inputs.contains_key(path)
                && (!previous_explicit.contains(path)
                    || self.scanner.has_explicit_file(&selected.generated, path))
        });
        let (expected_inputs, expected_explicit) = if has_new_explicit {
            let union_explicit: BTreeSet<PathBuf> =
                previous_explicit.union(&next_explicit).cloned().collect();
            let scanner_pre = self.scanner.clone();
            let gen_pre = selected.generated.clone();
            let prev_pre = previous_explicit.clone();
            let exp_pre = union_explicit.clone();
            let (current_base, baseline) = tokio::task::spawn_blocking(move || {
                scanner_pre.scan_transition(&gen_pre, &prev_pre, &exp_pre)
            })
            .await??;
            if current_base != *inputs {
                if plan.is_none() {
                    self.record_bun_dependencies(&bun_dependencies, next_explicit);
                }
                return Ok(None);
            }
            let baseline_digest = dev_inputs::digest(&baseline);
            let mut next_bun = Vec::with_capacity(bun_dependencies.len());
            for (index, _) in &bun_dependencies {
                let command = &selected.commands[*index];
                let program = next
                    .iter_mut()
                    .find(|program| program.process == command.process)
                    .context("unknown worker build")?;
                let mut identity = Sha256::new();
                identity.update(&baseline_digest);
                identity.update(&program.identity.contract_revision);
                let identity = format!("sha256:{}", hex(&identity.finalize()));
                let (built, deps) = command
                    .compile(root, directory, &selected.generated, program, &identity)
                    .await?;
                if built.digest != program.digest {
                    rebuilt.insert(command.module.clone());
                    *program = built;
                }
                if let Some(deps) = deps {
                    next_bun.push((*index, deps));
                }
            }
            bun_dependencies = next_bun;
            next_explicit = collect_bun_explicit(selected, &bun_dependencies);
            if next_explicit.iter().any(|p| {
                !baseline.contains_key(p)
                    && (!union_explicit.contains(p)
                        || self.scanner.has_explicit_file(&selected.generated, p))
            }) {
                if plan.is_none() {
                    self.record_bun_dependencies(&bun_dependencies, next_explicit);
                }
                return Ok(None);
            }
            (baseline, union_explicit)
        } else {
            (inputs.clone(), previous_explicit)
        };
        self.check_migrations(directory, selected).await?;
        times[0] = ms(begin.elapsed());
        let check_explicit = expected_explicit.clone();
        let next_explicit_for_scan = next_explicit.clone();
        let explicit_differs = next_explicit_for_scan != expected_explicit;
        let committed_slot = Arc::new(std::sync::Mutex::new(None));
        let committed_write = committed_slot.clone();
        let ready = Instant::now();
        let scanner = self.scanner.clone();
        let generated = selected.generated.clone();
        let roots = selected.roots.clone();
        let generation = publisher
            .replace(
                LaunchPlan {
                    contract_revision: revision.into(),
                    programs: next.clone(),
                },
                // The check walks the tree: it runs on a blocking thread and
                // reads only what changed since the scan that started this.
                || async move {
                    let (current, updated) =
                        tokio::task::spawn_blocking(move || -> Result<(Inputs, Option<Inputs>)> {
                            if explicit_differs {
                                let (current, updated) = scanner.scan_transition_with_roots(
                                    &generated,
                                    &roots,
                                    &check_explicit,
                                    &next_explicit_for_scan,
                                )?;
                                Ok((current, Some(updated)))
                            } else {
                                Ok((
                                    scanner.scan_with_roots(
                                        &generated,
                                        &roots,
                                        &check_explicit,
                                    )?,
                                    None,
                                ))
                            }
                        })
                        .await??;
                    if current != expected_inputs {
                        return Ok(false);
                    }
                    let committed = if let Some(updated) = updated {
                        if !updated
                            .iter()
                            .all(|(path, input)| current.get(path) == Some(input))
                        {
                            return Ok(false);
                        }
                        updated
                    } else {
                        current
                    };
                    *committed_write.lock().expect("committed inputs") = Some(committed);
                    Ok(true)
                },
            )
            .await?;
        times[2] = ms(ready.elapsed());
        let Some(generation) = generation else {
            if plan.is_none() && has_new_explicit {
                self.record_bun_dependencies(&bun_dependencies, next_explicit);
            }
            return Ok(None);
        };
        if let Some(plan) = plan {
            *self.status.ignore.write().expect("generated roots") = plan.generated.clone();
            self.scanner.set_roots(plan.roots.clone());
            self.config.rebuild = plan;
            self.declarations_dirty = false;
        }
        for (index, command, embeds) in go_dependencies {
            self.config.rebuild.commands[index] = command;
            self.config.rebuild.go_inputs.extend(embeds);
        }
        self.record_bun_dependencies(&bun_dependencies, next_explicit);
        self.programs = next;
        let committed = committed_slot
            .lock()
            .expect("committed inputs")
            .take()
            .context("missing verified snapshot inputs")?;
        Ok(Some((generation, rebuilt, committed)))
    }

    fn record_bun_dependencies(
        &mut self,
        bun_dependencies: &[(usize, BTreeSet<PathBuf>)],
        next_explicit: BTreeSet<PathBuf>,
    ) {
        for (index, deps) in bun_dependencies {
            self.config.rebuild.commands[*index].dependencies = deps.clone();
        }
        self.config.rebuild.js_dependencies = next_explicit.clone();
        self.scanner.set_explicit(next_explicit);
    }
}

fn collect_bun_explicit(
    selected: &Plan,
    bun_dependencies: &[(usize, BTreeSet<PathBuf>)],
) -> BTreeSet<PathBuf> {
    let mut explicit = BTreeSet::new();
    for (index, command) in selected.commands.iter().enumerate() {
        if command.tool() == "bun" {
            if let Some((_, deps)) = bun_dependencies.iter().find(|(i, _)| *i == index) {
                explicit.extend(deps.iter().cloned());
            } else {
                explicit.extend(command.dependencies.iter().cloned());
            }
        }
    }
    if !selected.commands.iter().any(|c| c.tool() == "bun") {
        explicit.clone_from(&selected.js_dependencies);
    }
    explicit
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

/// Summarizes changed paths into sanitized `<top-dir>:<ext>` buckets (never raw
/// filenames or full paths) so rebuild telemetry explains what triggered work.
pub(super) fn trigger_buckets(paths: &[Change]) -> Vec<String> {
    let mut buckets = BTreeSet::new();
    for (path, _) in paths {
        let mut components = path.components();
        let first = components
            .next()
            .and_then(|c| c.as_os_str().to_str())
            .unwrap_or("");
        let has_subdir = components.next().is_some();
        let root = if has_subdir && !first.is_empty() {
            first
        } else {
            "root"
        };
        let ext = dev_inputs::extension(path);
        let bucket = if ext.is_empty() {
            root.to_owned()
        } else {
            format!("{root}:.{ext}")
        };
        buckets.insert(bucket);
    }
    buckets.into_iter().take(8).collect()
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
