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
        .compile(&root, &artifacts, &program, "embed")
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
