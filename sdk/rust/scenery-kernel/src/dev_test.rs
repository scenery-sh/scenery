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
async fn a_new_embed_of_an_unchanged_file_builds_on_the_first_rebuild() -> Result<()> {
    let temp = tempfile::tempdir()?;
    let base = temp.path().canonicalize()?;
    let (root, workspace, artifacts) = (base.join("app"), base.join("go"), base.join("artifacts"));
    let write = |name: &str, content: &str| {
        let path = root.join(name);
        fs::create_dir_all(path.parent().expect("file parent"))?;
        fs::write(path, content)
    };
    write("go.mod", "module example.test/app\n\ngo 1.23\n")?;
    write("worker/main.go", "package main\n\nfunc main() {}\n")?;
    write("worker/assets/config.json", "{}\n")?;
    fs::create_dir(&artifacts)?;
    let scanner = Scanner::new(root.clone());
    let started = scanner.scan(&[])?;
    // The CLI's workspace has the Go sources and the files they embed
    // at startup, while the loop records the whole snapshot as held.
    for name in ["go.mod", "worker/main.go"] {
        dev_inputs::sync(
            &root,
            &workspace,
            Path::new(name),
            started.get(Path::new(name)),
        )?;
    }
    let mut held = started;
    let plan: Plan = serde_json::from_value(json!({
        "commands":[{
            "process":"worker", "module":"worker", "directory":workspace, "program":"go",
            "args":["build","-o","{output}","./worker"], "roots":["worker"],
        }],
        "programs":[], "source_digest":"", "host_config":{}, "go_workspace":workspace,
    }))?;
    let program: Program = serde_json::from_value(json!({
        "service":"app/service/worker","process":"worker","binary":"","digest":"","runtime":"go",
        "identity":{"contract_revision":"contract","implementation_revision":"","build_input_digest":"","go_target":"test"},
        "environment":[],
    }))?;

    write(
        "worker/main.go",
        "package main\n\nimport _ \"embed\"\n\n//go:embed assets/config.json\nvar config []byte\n\nfunc main() { println(len(config)) }\n",
    )?;
    let edited = scanner.scan(&[])?;
    materialize_go(&root, &workspace, &mut held, &edited, &plan)?;
    // Without the embedded file the build fails on its pattern.
    plan.commands[0]
        .compile(&root, &artifacts, &[], &program, "embed")
        .await?;
    Ok(())
}

#[tokio::test]
async fn failed_declaration_rebuild_keeps_declarations_dirty_until_restored() -> Result<()> {
    let temp = tempfile::Builder::new().prefix("dev-").tempdir_in("/tmp")?;
    let base = temp.path().canonicalize()?;
    let root = base.join("app");
    let state = base.join("state");
    fs::create_dir_all(&root)?;
    fs::create_dir_all(&state)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&state, fs::Permissions::from_mode(0o700))?;
    }
    fs::write(root.join("lib.rs"), "pub fn v() -> i32 { 1 }\n")?;
    let mirror_toml = state.join("Cargo.toml");
    fs::write(&mirror_toml, "broken = false\n")?;

    let helper = state.join("fake-cli.sh");
    // Fake CLI emulates `worker-refresh-plan` writing the live `Cargo.toml`
    // into the shared mirror before compilation is attempted.
    fs::write(
        &helper,
        format!(
            "#!/bin/sh\ncp '{root}/Cargo.toml' '{mirror}'\necho '{{}}'\n",
            root = root.display(),
            mirror = mirror_toml.display()
        ),
    )?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&helper, fs::Permissions::from_mode(0o755))?;
    }

    fs::write(root.join("Cargo.toml"), "[package]\nname = \"app\"\n")?;
    let scanner = Scanner::new(root.clone());
    let initial = scanner.scan(&[])?;
    let digest = dev_inputs::digest(&initial);
    let config: Development = serde_json::from_value(json!({
        "root": root,
        "app": {},
        "cli": helper,
        "cli_environment": [],
        "environment": "development",
        "frontend_pid": 0,
        "report_fd": -1,
        "lock_fds": [],
        "session_id": "s1",
        "wait_schema": "sha256:test",
        "rebuild": {
            "generated": [],
            "declarations": {},
            "commands": [{
                "process": "pricing", "module": "pricing", "directory": root,
                "program": "/usr/bin/false", "args": [], "roots": ["."],
            }],
            "programs": [{
                "service": "app/service/pricing", "process": "pricing", "binary": "/bin/sleep",
                "digest": "", "runtime": "rust",
                "identity": {"contract_revision": "c", "implementation_revision": "", "build_input_digest": "", "go_target": ""},
                "environment": [],
            }],
            "source_digest": digest,
            "host_config": {"ok": true},
        }
    }))?;
    let mut watcher = WatcherLoop::start(config)?;
    let control = crate::supervisor::Control::bind(
        &state,
        "c".into(),
        Some("t".repeat(32)),
        Default::default(),
        None,
    )?;
    let publication = control.publication().await;
    let publisher = control.publisher(publication);

    // Edit declaration `Cargo.toml`: `worker-refresh-plan` overwrites the mirror, then
    // rejects due to mismatched `host_config`/`source_digest` or failed compile.
    fs::write(root.join("Cargo.toml"), "[package]\nbroken syntax")?;
    let broken = watcher.scanner.scan(&[])?;
    let broken_copy = broken.clone();
    let _ = watcher
        .process_change(&state, &publisher, "c", broken, Instant::now())
        .await;
    assert!(watcher.declarations_dirty);
    assert!(watcher.should_skip_scan(&broken_copy, &broken_copy));
    assert!(!watcher.should_skip_scan(&initial, &initial));
    assert_eq!(
        fs::read_to_string(&mirror_toml)?,
        "[package]\nbroken syntax"
    );

    // Reverting `Cargo.toml` back to initial `S0` must restore the mirror and clear the flag.
    fs::write(root.join("Cargo.toml"), "[package]\nname = \"app\"\n")?;
    assert!(watcher.declarations_dirty);
    watcher.restore_mirrors(&state, "c", &initial).await.err(); // helper returned `{}` missing fields, flag remains dirty until valid plan
    assert!(watcher.declarations_dirty);

    fs::write(
        &helper,
        format!(
            "#!/bin/sh\ncp '{root}/Cargo.toml' '{mirror}'\ncat <<JSON > \"$5\"\n{{\"generated\":[],\"declarations\":{{}},\"commands\":[],\"programs\":[],\"source_digest\":\"{digest}\",\"host_config\":{{\"ok\":true}}}}\nJSON\n",
            root = root.display(),
            mirror = mirror_toml.display(),
            digest = digest,
        ),
    )?;
    watcher.restore_mirrors(&state, "c", &initial).await?;
    assert!(!watcher.declarations_dirty);
    assert_eq!(
        fs::read_to_string(&mirror_toml)?,
        "[package]\nname = \"app\"\n"
    );
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
    let failed: Value = serde_json::from_slice(&failed.into_body().collect().await?.to_bytes())?;
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

fn write_test_worker_responder(state: &Path) -> Result<(PathBuf, PathBuf)> {
    let out = std::process::Command::new("/bin/sh")
        .args(["-c", "command -v node"])
        .output()?;
    ensure!(out.status.success(), "node must be installed");
    let node = PathBuf::from(String::from_utf8(out.stdout)?.trim());
    let responder = state.join("responder.mjs");
    fs::write(
        &responder,
        concat!(
            "import fs from 'node:fs';\n",
            "import http2 from 'node:http2';\n",
            "const launch = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));\n",
            "const body = JSON.stringify(launch.identity);\n",
            "const srv = http2.createServer();\n",
            "srv.on('stream', (s) => {\n",
            "  s.respond({\n",
            "    ':status': 200,\n",
            "    'content-type': 'application/json',\n",
            "    'x-worker-contract': launch.identity.contract_revision,\n",
            "    'x-worker-implementation': launch.identity.implementation_revision,\n",
            "  });\n",
            "  s.end(body);\n",
            "});\n",
            "srv.listen(launch.worker_socket);\n",
            "fs.createReadStream(null, { fd: 4 }).on('end', () => process.exit(0)).resume();\n",
            "process.on('SIGTERM', () => process.exit(0));\n",
        ),
    )?;
    Ok((node, responder))
}

#[tokio::test]
async fn pending_migration_blocks_worker_publication_when_has_migrations_set() -> Result<()> {
    let temp = tempfile::Builder::new().prefix("dev-").tempdir_in("/tmp")?;
    let base = temp.path().canonicalize()?;
    let root = base.join("app");
    let state = base.join("state");
    let bin_dir = state.join("bin");
    fs::create_dir_all(&root)?;
    fs::create_dir_all(&bin_dir)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&state, fs::Permissions::from_mode(0o700))?;
    }
    let (node, responder) = write_test_worker_responder(&state)?;
    let worker_bin = state.join("worker.sh");
    fs::write(&worker_bin, "#!/bin/sh\nexit 1\n")?;
    let fake_go = bin_dir.join("go");
    fs::write(
        &fake_go,
        format!(
            "#!/bin/sh\nif [ \"$1\" = \"list\" ]; then\n  exit 0\nfi\nSUM=$(cat '{root}/worker.go')\ncat <<SH > \"$3\"\n#!/bin/sh\n# $SUM\nexec '{node}' '{responder}' \"\\$1\"\nSH\nchmod +x \"$3\"\n",
            root = root.display(),
            node = node.display(),
            responder = responder.display(),
        ),
    )?;
    let helper = state.join("fake-cli.sh");
    let gate = state.join("migration-pending");
    fs::write(&gate, "1")?;
    fs::write(
        &helper,
        format!(
            "#!/bin/sh\nif [ \"$1\" = \"worker-check-migrations\" ]; then\n  if [ -f '{gate}' ]; then\n    echo 'schema migration for orders is pending' >&2\n    exit 3\n  fi\n  exit 0\nfi\nexit 1\n",
            gate = gate.display()
        ),
    )?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        for file in [&worker_bin, &fake_go, &helper] {
            fs::set_permissions(file, fs::Permissions::from_mode(0o755))?;
        }
    }

    fs::write(root.join("worker.go"), "package main // v1\n")?;
    let scanner = Scanner::new(root.clone());
    let initial = scanner.scan(&[])?;
    let digest = dev_inputs::digest(&initial);
    let config: Development = serde_json::from_value(json!({
        "root": root,
        "app": {},
        "cli": helper,
        "cli_environment": [],
        "environment": "development",
        "frontend_pid": 0,
        "report_fd": -1,
        "lock_fds": [],
        "session_id": "s1",
        "wait_schema": "sha256:test",
        "rebuild": {
            "generated": [],
            "declarations": {},
            "has_migrations": true,
            "sql_requirements": [{"kind":"data_source","name":"orders_db","schema":"orders"}],
            "commands": [{
                "process": "orders", "module": "orders", "directory": root,
                "program": fake_go, "args": ["build", "-o", "{output}", "."], "roots": ["."]
            }],
            "programs": [{
                "service": "app/service/orders", "process": "orders", "binary": worker_bin,
                "digest": "", "runtime": "go",
                "identity": {"contract_revision": "c", "implementation_revision": "", "build_input_digest": "", "go_target": ""},
                "environment": []
            }],
            "source_digest": digest,
            "host_config": {}
        }
    }))?;
    let mut watcher = WatcherLoop::start(config)?;
    let expected = std::collections::BTreeMap::from([(
        "orders".to_owned(),
        ("app/service/orders".to_owned(), "go".to_owned()),
    )]);
    let control =
        crate::supervisor::Control::bind(&state, "c".into(), Some("t".repeat(32)), expected, None)?;
    let publication = control.publication().await;
    let publisher = control.publisher(publication.clone());
    let stop = CancellationToken::new();
    let supervisor_task = tokio::spawn(control.serve(stop.clone().cancelled_owned()));

    // Edit non-declaration source and attempt rebuild while migration is pending.
    fs::write(root.join("worker.go"), "package main // v2\n")?;
    let edited = watcher.scanner.scan(&[])?;
    let _ = watcher
        .process_change(&state, &publisher, "c", edited, Instant::now())
        .await;
    assert_eq!(publisher.generation(), 0);
    let reply = watcher.status().reply(None, &publication).await?;
    let body: Value = serde_json::from_slice(&reply.into_body().collect().await?.to_bytes())?;
    assert_eq!(body["outcome"], "failed");
    assert!(
        body["compile_error"]
            .as_str()
            .is_some_and(|text| text.contains("pending")),
        "{body}"
    );

    // Clear the pending migration gate and verify the next edit publishes cleanly.
    fs::remove_file(&gate)?;
    fs::write(root.join("worker.go"), "package main // v3\n")?;
    let ready = watcher.scanner.scan(&[])?;
    let _ = watcher
        .process_change(&state, &publisher, "c", ready, Instant::now())
        .await;
    assert_eq!(publisher.generation(), 1);
    stop.cancel();
    let _ = supervisor_task.await;
    Ok(())
}

#[tokio::test]
async fn bun_bundled_dependencies_in_ignored_dirs_trigger_rebuild_and_update_wait_status()
-> Result<()> {
    let temp = tempfile::Builder::new().prefix("dev-").tempdir_in("/tmp")?;
    let base = temp.path().canonicalize()?;
    let root = base.join("app");
    let state = base.join("state");
    fs::create_dir_all(root.join("svc/dist"))?;
    fs::create_dir_all(root.join("node_modules/pkg"))?;
    fs::create_dir_all(root.join("node_modules/.store/linked"))?;
    fs::create_dir_all(root.join("node_modules/linked"))?;
    fs::create_dir_all(&state)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&state, fs::Permissions::from_mode(0o700))?;
        std::os::unix::fs::symlink(
            "../.store/linked/index.js",
            root.join("node_modules/linked/index.js"),
        )?;
    }
    let (node, responder) = write_test_worker_responder(&state)?;
    fs::write(root.join("svc/index.ts"), "export const v = 1;\n")?;
    fs::write(root.join("svc/dist/vendor.ts"), "export const dep = 'a';\n")?;
    fs::write(root.join("svc/dist/extra.ts"), "export const extra = 1;\n")?;
    fs::write(
        root.join("svc/dist/extra2.ts"),
        "export const extra2 = 'v1';\n",
    )?;
    fs::write(
        root.join("svc/dist/extra3.ts"),
        "export const extra3 = 1;\n",
    )?;
    fs::write(
        root.join("node_modules/pkg/index.js"),
        "module.exports = 'p1';\n",
    )?;
    fs::write(
        root.join("node_modules/.store/linked/index.js"),
        "module.exports = 'linked-v1';\n",
    )?;

    // Helper mutating `svc/dist/extra2.ts` during `worker-check-migrations` once when requested,
    // exercising a concurrent edit between Bun compilation and `publisher.replace`.
    let mutate_gate = state.join("mutate-extra2");
    let late_gate = state.join("create-late");
    let helper = state.join("fake-cli.sh");
    fs::write(
        &helper,
        format!(
            "#!/bin/sh\nif [ \"$1\" = \"worker-check-migrations\" ]; then\n  if [ -f '{gate}' ]; then\n    printf 'export const extra2 = \"v2\";\\n' > '{root}/svc/dist/extra2.ts'\n    rm -f '{gate}'\n  fi\n  exit 0\nfi\nexit 1\n",
            gate = mutate_gate.display(),
            root = root.display(),
        ),
    )?;

    // Fake `bun` bundler reading arguments `--outfile=...` and `--metafile=...` and emitting an executable worker wrapper.
    let fake_bun = state.join("bun");
    fs::write(
        &fake_bun,
        format!(
            "#!/bin/sh\nOUT=''\nMETA=''\nfor arg in \"$@\"; do\n  case \"$arg\" in\n    --outfile=*) OUT=\"${{arg#--outfile=}}\" ;;\n    --metafile=*) META=\"${{arg#--metafile=}}\" ;;\n  esac\ndone\nif [ -f '{late}' ]; then\n  printf 'export const late = 1;\\n' > '{root}/svc/late.ts'\n  rm -f '{late}'\nfi\n{{\n  echo '#!/bin/sh'\n  cat '{root}/svc/index.ts' '{root}/svc/dist/vendor.ts' '{root}/node_modules/pkg/index.js' | sed 's/^/# /'\n  EXTRA=''\n  if grep -q 'extra3' '{root}/svc/index.ts'; then\n    cat '{root}/svc/dist/extra3.ts' | sed 's/^/# /'\n    EXTRA=',\"'{root}'/svc/dist/extra3.ts\":{{}}'\n  elif grep -q 'linked' '{root}/svc/index.ts'; then\n    cat '{root}/node_modules/linked/index.js' | sed 's/^/# /'\n    EXTRA=',\"'{root}'/node_modules/linked/index.js\":{{}}'\n  elif grep -q 'extra' '{root}/svc/index.ts'; then\n    if grep -q 'extra2' '{root}/svc/index.ts'; then\n      cat '{root}/svc/dist/extra2.ts' | sed 's/^/# /'\n      EXTRA=',\"'{root}'/svc/dist/extra2.ts\":{{}}'\n    else\n      cat '{root}/svc/dist/extra.ts' | sed 's/^/# /'\n      EXTRA=',\"'{root}'/svc/dist/extra.ts\":{{}}'\n    fi\n  fi\n  echo \"exec '{node}' '{responder}' \\\"\\$1\\\"\"\n}} > \"$OUT\"\nchmod +x \"$OUT\"\nprintf '{{\"inputs\":{{\"{root}/svc/index.ts\":{{}},\"{root}/svc/dist/vendor.ts\":{{}},\"{root}/node_modules/pkg/index.js\":{{}}%s}}}}' \"$EXTRA\" > \"$META\"\n",
            root = root.display(),
            late = late_gate.display(),
            node = node.display(),
            responder = responder.display(),
        ),
    )?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        for file in [&fake_bun, &helper] {
            fs::set_permissions(file, fs::Permissions::from_mode(0o755))?;
        }
    }

    let scanner = Scanner::new(root.clone());
    scanner.set_explicit(BTreeSet::from([
        PathBuf::from("svc/dist/vendor.ts"),
        PathBuf::from("node_modules/pkg/index.js"),
    ]));
    let initial = scanner.scan(&[])?;
    let digest = dev_inputs::digest(&initial);
    let config: Development = serde_json::from_value(json!({
        "root": root,
        "app": {},
        "cli": helper,
        "cli_environment": [],
        "environment": "development",
        "frontend_pid": 0,
        "report_fd": -1,
        "lock_fds": [],
        "session_id": "s1",
        "wait_schema": "sha256:test",
        "rebuild": {
            "generated": [],
            "declarations": {},
            "has_migrations": true,
            "sql_requirements": [{"kind":"data_source","name":"js_db","schema":"js"}],
            "js_dependencies": ["svc/dist/vendor.ts", "node_modules/pkg/index.js"],
            "commands": [{
                "process": "jsw", "module": "svc", "directory": "svc", "workspace": ".",
                "program": fake_bun,
                "args": ["build", "--outfile={output}", "--metafile={metafile}", "svc/index.ts"],
                "dependencies": ["svc/dist/vendor.ts", "node_modules/pkg/index.js"]
            }],
            "programs": [{
                "service": "app/service/jsw", "process": "jsw", "binary": fake_bun,
                "digest": "", "runtime": "ts",
                "identity": {"contract_revision": "c", "implementation_revision": "", "build_input_digest": "", "go_target": "bun"},
                "environment": []
            }],
            "source_digest": digest,
            "host_config": {}
        }
    }))?;

    let mut watcher = WatcherLoop::start(config)?;
    let expected = std::collections::BTreeMap::from([(
        "jsw".to_owned(),
        ("app/service/jsw".to_owned(), "ts".to_owned()),
    )]);
    let control =
        crate::supervisor::Control::bind(&state, "c".into(), Some("t".repeat(32)), expected, None)?;
    let publication = control.publication().await;
    let publisher = control.publisher(publication.clone());
    let stop = CancellationToken::new();
    let supervisor_task = tokio::spawn(control.serve(stop.clone().cancelled_owned()));

    // Edit `node_modules/pkg/index.js`: scanner sees the change, status moves to
    // building, rebuild runs, and status reaches served.
    fs::write(
        root.join("node_modules/pkg/index.js"),
        "module.exports = 'p2';\n",
    )?;
    let building = watcher.status().reply(None, &publication).await?;
    let building: Value =
        serde_json::from_slice(&building.into_body().collect().await?.to_bytes())?;
    assert_eq!(building["outcome"], "building");

    let edited = watcher.scanner.scan(&[])?;
    watcher
        .process_change(&state, &publisher, "c", edited, Instant::now())
        .await;
    assert_eq!(publisher.generation(), 1);
    let served = watcher.status().reply(None, &publication).await?;
    let served: Value = serde_json::from_slice(&served.into_body().collect().await?.to_bytes())?;
    assert_eq!(served["outcome"], "served");

    // Edit `svc/index.ts` so the bundle picks up `svc/dist/extra.ts`.
    fs::write(root.join("svc/index.ts"), "export const extra = true;\n")?;
    let with_extra = watcher.scanner.scan(&[])?;
    let sync_seen = watcher
        .process_change(&state, &publisher, "c", with_extra, Instant::now())
        .await;
    assert!(sync_seen);
    assert_eq!(publisher.generation(), 2);
    assert!(watcher.scanner.is_explicit(Path::new("svc/dist/extra.ts")));

    // Editing `svc/dist/extra.ts` now triggers generation 3 automatically.
    fs::write(root.join("svc/dist/extra.ts"), "export const extra = 2;\n")?;
    let edited_extra = watcher.scanner.scan(&[])?;
    watcher
        .process_change(&state, &publisher, "c", edited_extra, Instant::now())
        .await;
    assert_eq!(publisher.generation(), 3);

    // Discover `svc/dist/extra2.ts` while `check_migrations` mutates it from v1 to v2 before
    // `publisher.replace`: the stale v1 build must be superseded rather than published as v2.
    fs::write(&mutate_gate, "1")?;
    fs::write(root.join("svc/index.ts"), "export const extra2 = true;\n")?;
    let discover_extra2 = watcher.scanner.scan(&[])?;
    let superseded = watcher
        .process_change(&state, &publisher, "c", discover_extra2, Instant::now())
        .await;
    assert!(superseded);
    assert_eq!(publisher.generation(), 3);
    assert!(watcher.scanner.is_explicit(Path::new("svc/dist/extra2.ts")));

    let with_v2 = watcher.scanner.scan(&[])?;
    let resync = watcher
        .process_change(&state, &publisher, "c", with_v2, Instant::now())
        .await;
    assert!(!resync);
    // Candidate 4 (built from v1) was discarded, so the recompiled v2 build commits as generation 5.
    assert_eq!(publisher.generation(), 5);

    // Discovering a symlinked `node_modules` dependency must publish cleanly without livelocking
    // and track edits to the symlink target.
    fs::write(root.join("svc/index.ts"), "export const linked = true;\n")?;
    let with_linked = watcher.scanner.scan(&[])?;
    assert!(
        watcher
            .process_change(&state, &publisher, "c", with_linked, Instant::now())
            .await
    );
    assert_eq!(publisher.generation(), 6);
    assert!(
        watcher
            .scanner
            .is_explicit(Path::new("node_modules/linked/index.js"))
    );
    assert!(
        watcher
            .scanner
            .has_explicit_file(&[], Path::new("node_modules/linked/index.js"))
    );

    fs::write(
        root.join("node_modules/.store/linked/index.js"),
        "module.exports = 'linked-v2';\n",
    )?;
    let edited_linked = watcher.scanner.scan(&[])?;
    assert!(
        !watcher
            .process_change(&state, &publisher, "c", edited_linked, Instant::now())
            .await
    );
    assert_eq!(publisher.generation(), 7);

    // Creating an unrelated file during explicit-dependency discovery must supersede rather than
    // commit without building the late file.
    fs::write(&late_gate, "1")?;
    fs::write(root.join("svc/index.ts"), "export const extra3 = true;\n")?;
    let discover_extra3 = watcher.scanner.scan(&[])?;
    assert!(
        watcher
            .process_change(&state, &publisher, "c", discover_extra3, Instant::now())
            .await
    );
    assert_eq!(publisher.generation(), 7);
    let with_late = watcher.scanner.scan(&[])?;
    assert!(
        !watcher
            .process_change(&state, &publisher, "c", with_late, Instant::now())
            .await
    );
    assert_eq!(publisher.generation(), 8);

    let final_served = watcher.status().reply(None, &publication).await?;
    let final_served: Value =
        serde_json::from_slice(&final_served.into_body().collect().await?.to_bytes())?;
    assert_eq!(final_served["outcome"], "served");

    // Editing a file outside worker, declaration, and migration inputs (e.g. a
    // frontend page served by Vite) advances the snapshot digest without bumping
    // generation or emitting a no-op worker rebuild.
    let frontend = root.join("apps/nextnext/src/Page.tsx");
    fs::create_dir_all(frontend.parent().unwrap())?;
    fs::write(&frontend, "export const Page = () => null;\n")?;
    let with_frontend = watcher.scanner.scan(&[])?;
    assert!(
        !watcher
            .process_change(&state, &publisher, "c", with_frontend, Instant::now())
            .await
    );
    assert_eq!(publisher.generation(), 8);
    let after_frontend = watcher.status().reply(None, &publication).await?;
    let after_frontend: Value =
        serde_json::from_slice(&after_frontend.into_body().collect().await?.to_bytes())?;
    assert_eq!(after_frontend["outcome"], "served");

    stop.cancel();
    let _ = supervisor_task.await;
    Ok(())
}

#[test]
fn trigger_buckets_sanitizes_paths_without_leaking_filenames() {
    let paths = vec![
        (PathBuf::from("solar/designs/secret_customer.go"), false),
        (PathBuf::from("solar/projects/other.go"), false),
        (PathBuf::from("app.scn"), false),
        (PathBuf::from("Makefile"), false),
        (PathBuf::from("apps/nextnext/src/Page.tsx"), true),
    ];
    assert_eq!(
        trigger_buckets(&paths),
        vec!["apps:.tsx", "root", "root:.scn", "solar:.go"]
    );
}
