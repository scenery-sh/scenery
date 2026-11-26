use super::*;
use bytes::Bytes;
use http_body_util::{Empty, Full};
use hyper::header::HeaderValue;
use hyper::service::service_fn;
use hyper_util::rt::TokioIo;
use tokio::net::TcpListener;
use tokio_util::sync::CancellationToken;

#[test]
fn metadata_and_references_are_limited_by_their_stored_encoding() {
    let headers = |note: &str| {
        let raw = format!(r#"{{"note":"{note}"}}"#);
        let mut headers = HeaderMap::new();
        let value = HeaderValue::from_str(&values::base64url(raw.as_bytes())).unwrap();
        headers.insert("x-storage-metadata", value);
        headers
    };
    let stored = user_metadata(&headers(&"a".repeat(11_000))).ok().flatten();
    assert_eq!(stored.unwrap()["note"].as_str().map(str::len), Some(11_000));
    // 11 KiB on the wire, 66 KiB once every `<` is stored as `\u003c`.
    let escaping = "<".repeat(11_000);
    let Err(fault) = user_metadata(&headers(&escaping)) else {
        panic!("metadata beyond the stored limit was accepted");
    };
    assert_eq!(fault.status, StatusCode::BAD_REQUEST);
    // Such a reference could be published but never read again.
    let reference = json!({"object": {"metadata": {"note": escaping}}});
    assert!(values::encode(&reference).len() > RECORD_LIMIT);
    assert!(encode_reference(&reference).is_err());
    assert!(encode_reference(&json!({"object": {"metadata": {"note": "a"}}})).is_ok());
}

fn setup_local_store(root: &Path) -> Result<(Value, Value)> {
    let generation_id = "0123456789abcdef0123456789abcdef";
    let incarnation = "fedcba9876543210fedcba9876543210";
    let uid = rustix::process::getuid().as_raw();
    let binding = json!({
        "app_id": "app",
        "app_root": root,
        "worktree_key": "main",
        "user_id": uid,
        "managed": true,
    });
    let gen_dir = root.join("generations").join(generation_id);
    std::fs::create_dir_all(gen_dir.join("staging"))?;
    std::fs::File::create(root.join("mutation.lock"))?;
    std::fs::File::create(root.join("maintenance.lock"))?;
    write_private_json(
        &root.join("owner.json"),
        &json!({
            "kind": "scenery.storage.owner",
            "state": "ready",
            "generation": generation_id,
            "incarnation": incarnation,
            "binding": binding,
        }),
    )?;
    write_private_json(
        &gen_dir.join("generation.json"),
        &json!({
            "kind": "scenery.storage.generation",
            "generation": generation_id,
            "incarnation": incarnation,
            "binding": binding,
        }),
    )?;
    set_private_tree(root)?;
    let config = json!({
        "namespace": {
            "root": root,
            "binding": binding,
            "incarnation": incarnation,
        }
    });
    let store = json!({
        "kind": "local",
        "root": root,
        "access": "read_write",
        "tenant_scoped": false,
        "max_object_bytes": 1024 * 1024,
    });
    Ok((config, store))
}

fn write_private_json(path: &Path, value: &Value) -> Result<()> {
    use std::os::unix::fs::OpenOptionsExt;
    let mut file = std::fs::OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(path)?;
    file.write_all(values::encode(value).as_bytes())?;
    Ok(())
}

fn set_private_tree(path: &Path) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;
    for entry in std::fs::read_dir(path)? {
        let entry = entry?;
        let child = entry.path();
        if entry.file_type()?.is_dir() {
            set_private_tree(&child)?;
        } else {
            std::fs::set_permissions(&child, std::fs::Permissions::from_mode(0o600))?;
        }
    }
    std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700))?;
    Ok(())
}

async fn local_request(
    request: Request<Incoming>,
    config: &Value,
    store: &Value,
    key: Option<&str>,
    recursive: bool,
    query: &BTreeMap<String, Vec<String>>,
) -> Result<Reply, Fault> {
    serve(
        request,
        Selection {
            config,
            store,
            name: "files",
            tenant: "",
            key,
            recursive,
            query,
        },
    )
    .await
}

async fn into_incoming(
    method: Method,
    body: Bytes,
    headers: &[(&str, &str)],
) -> Result<Request<Incoming>> {
    let listener = TcpListener::bind("127.0.0.1:0").await?;
    let addr = listener.local_addr()?;
    let (tx, rx) = tokio::sync::oneshot::channel();
    let server = tokio::spawn(async move {
        let (socket, _) = listener.accept().await.unwrap();
        let tx = std::sync::Mutex::new(Some(tx));
        let service = service_fn(move |req: Request<Incoming>| {
            if let Some(tx) = tx.lock().unwrap().take() {
                let _ = tx.send(req);
            }
            async { Ok::<_, std::convert::Infallible>(empty(StatusCode::OK)) }
        });
        let _ = hyper::server::conn::http1::Builder::new()
            .serve_connection(TokioIo::new(socket), service)
            .await;
    });
    let stream = tokio::net::TcpStream::connect(addr).await?;
    let (mut sender, conn) = hyper::client::conn::http1::handshake(TokioIo::new(stream)).await?;
    tokio::spawn(conn);
    let mut builder = Request::builder().method(method).uri("http://localhost/");
    for (name, value) in headers {
        builder = builder.header(*name, *value);
    }
    let req = builder.body(Full::new(body))?;
    let _ = sender.send_request(req).await;
    let incoming = rx.await?;
    server.abort();
    Ok(incoming)
}

#[tokio::test]
async fn delete_prefix_removes_matching_objects_and_enforces_rules() -> Result<()> {
    let dir = tempfile::tempdir()?;
    let root = dir.path().canonicalize()?;
    let (config, store) = setup_local_store(&root)?;
    let query = BTreeMap::new();

    for (key, body) in [
        ("keep.txt", "keep"),
        ("tmp/a.txt", "one"),
        ("tmp/nested/b.txt", "two"),
    ] {
        let req = into_incoming(Method::PUT, Bytes::from_static(body.as_bytes()), &[]).await?;
        let reply = local_request(req, &config, &store, Some(key), false, &query)
            .await
            .map_err(|f| anyhow::anyhow!("put failed: {}", f.code))?;
        assert_eq!(reply.status(), StatusCode::CREATED);
    }

    // Conditionals are rejected on prefix delete.
    let bad = into_incoming(Method::DELETE, Bytes::new(), &[("if-match", "\"x\"")]).await?;
    let err = local_request(bad, &config, &store, Some("tmp/"), true, &query)
        .await
        .unwrap_err();
    assert_eq!(err.status, StatusCode::BAD_REQUEST);

    // Read-only stores reject mutations.
    let mut ro = store.clone();
    ro["access"] = json!("read_only");
    let req = into_incoming(Method::DELETE, Bytes::new(), &[]).await?;
    let err = local_request(req, &config, &ro, Some("tmp/"), true, &query)
        .await
        .unwrap_err();
    assert_eq!(err.status, StatusCode::FORBIDDEN);

    // Deleting `tmp/` removes only matching keys.
    let req = into_incoming(Method::DELETE, Bytes::new(), &[]).await?;
    let reply = local_request(req, &config, &store, Some("tmp/"), true, &query)
        .await
        .map_err(|f| anyhow::anyhow!("delete prefix failed: {}", f.code))?;
    assert_eq!(reply.status(), StatusCode::NO_CONTENT);

    let list_req = into_incoming(Method::GET, Bytes::new(), &[]).await?;
    let list_reply = local_request(list_req, &config, &store, None, false, &query)
        .await
        .map_err(|f| anyhow::anyhow!("list failed: {}", f.code))?;
    let listed: Value =
        serde_json::from_slice(&list_reply.into_body().collect().await?.to_bytes())?;
    let keys: Vec<_> = listed["objects"]
        .as_array()
        .unwrap()
        .iter()
        .map(|o| o["key"].as_str().unwrap())
        .collect();
    assert_eq!(keys, ["keep.txt"]);

    // Deleting empty prefix `""` empties the partition and stays idempotent.
    let req = into_incoming(Method::DELETE, Bytes::new(), &[]).await?;
    let reply = local_request(req, &config, &store, Some(""), true, &query)
        .await
        .map_err(|f| anyhow::anyhow!("delete all failed: {}", f.code))?;
    assert_eq!(reply.status(), StatusCode::NO_CONTENT);
    let req = into_incoming(Method::DELETE, Bytes::new(), &[]).await?;
    let reply = local_request(req, &config, &store, Some(""), true, &query)
        .await
        .map_err(|f| anyhow::anyhow!("repeat delete failed: {}", f.code))?;
    assert_eq!(reply.status(), StatusCode::NO_CONTENT);

    Ok(())
}

#[tokio::test]
async fn scoped_body_drops_storage_stream_and_releases_maintenance_lock_on_cancel() -> Result<()> {
    let dir = tempfile::tempdir()?;
    let root = dir.path().canonicalize()?;
    let (config, store) = setup_local_store(&root)?;
    let query = BTreeMap::new();

    let put_req = into_incoming(Method::PUT, Bytes::from_static(b"hello world"), &[]).await?;
    let put_reply = local_request(put_req, &config, &store, Some("blob.bin"), false, &query)
        .await
        .map_err(|f| anyhow::anyhow!("put failed: {}", f.code))?;
    assert_eq!(put_reply.status(), StatusCode::CREATED);

    let get_req = into_incoming(Method::GET, Bytes::new(), &[]).await?;
    let reply = local_request(get_req, &config, &store, Some("blob.bin"), false, &query)
        .await
        .map_err(|f| anyhow::anyhow!("get failed: {}", f.code))?;
    let cancel = CancellationToken::new();
    let _body = crate::transport::ScopedBody::boxed(
        reply.into_body(),
        cancel.clone(),
        tokio::time::Instant::now() + Duration::from_secs(30),
    );

    // While `_body` is unread and active, `maintenance.lock` stays held shared.
    let root_dir = File::from(fs::open(
        &root,
        OFlags::DIRECTORY | OFlags::NOFOLLOW | OFlags::CLOEXEC,
        Mode::empty(),
    )?);
    let maint = open_file(&root_dir, Path::new("maintenance.lock"), OFlags::RDWR)?;
    assert_eq!(
        fs::flock(&maint, FlockOperation::NonBlockingLockExclusive),
        Err(Errno::AGAIN)
    );

    // Cancelling the scope immediately drops the inner storage stream without polling `_body`.
    cancel.cancel();
    tokio::time::timeout(Duration::from_secs(1), async {
        while fs::flock(&maint, FlockOperation::NonBlockingLockExclusive).is_err() {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await?;
    let _ = Empty::<Bytes>::new();
    Ok(())
}
