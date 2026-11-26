use super::*;
use std::collections::BTreeMap;
use std::time::Duration;
use tokio::sync::{Notify, mpsc};

#[test]
fn standalone_context_keeps_cancellation_channel_open() {
    run(async {
        let mut ctx =
            Context::with_principal(Some("usr_1"), Some("tnt_1")).with_execution_id("exec_1");
        assert_eq!(ctx.invocation.principal.as_deref(), Some("usr_1"));
        assert_eq!(ctx.invocation.tenant_id.as_deref(), Some("tnt_1"));
        assert_eq!(ctx.invocation.execution_id.as_deref(), Some("exec_1"));
        let mut cloned = ctx.clone();
        assert!(
            tokio::time::timeout(Duration::from_millis(20), ctx.canceled())
                .await
                .is_err(),
            "standalone Context::canceled() must remain pending while live"
        );
        drop(ctx);
        assert!(
            tokio::time::timeout(Duration::from_millis(20), cloned.canceled())
                .await
                .is_err(),
            "cloned standalone Context::canceled() must remain pending"
        );
        Ok::<(), Error>(())
    })
    .unwrap();
}

#[test]
fn shutdown_drains_an_admitted_invocation_and_its_telemetry() {
    run(async { tokio::time::timeout(Duration::from_secs(3), prove_shutdown_drain()).await? })
        .unwrap();
}

async fn prove_shutdown_drain() -> Result<()> {
    let directory = std::env::temp_dir().join(format!("sw-{}", std::process::id()));
    std::fs::create_dir(&directory)?;
    struct RemoveTestDirectory(PathBuf);
    impl Drop for RemoveTestDirectory {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }
    let _cleanup = RemoveTestDirectory(directory.clone());
    let worker_socket = directory.join("worker");
    let host_socket = directory.join("host");
    let collector = UnixListener::bind(&host_socket)?;
    let (received, mut batch) = mpsc::unbounded_channel();
    let receiver = tokio::spawn(async move {
        let (socket, _) = collector.accept().await.unwrap();
        let handler = service_fn(move |request: Request<Incoming>| {
            let received = received.clone();
            async move {
                assert_eq!(request.uri().path(), "/host/v1/telemetry");
                assert_eq!(request.headers()["x-worker-scope"], "test-scope");
                let body = request.into_body().collect().await?.to_bytes();
                received.send(body).unwrap();
                Ok::<_, hyper::Error>(json(br#"{"accepted":1}"#.to_vec()))
            }
        });
        let _ = hyper::server::conn::http2::Builder::new(TokioExecutor::new())
            .serve_connection(TokioIo::new(socket), handler)
            .await;
    });
    let stop = Arc::new(Notify::new());
    let stopping = Arc::clone(&stop);
    let entered = Arc::new(Notify::new());
    let worker_entered = Arc::clone(&entered);
    let release = Arc::new(Notify::new());
    let worker_release = Arc::clone(&release);
    let launch = Launch {
        worker_socket: worker_socket.clone(),
        host_socket,
        token: "test-token".into(),
        identity: Identity {
            abi: 1,
            service: "example/service/worker".into(),
            contract_revision: "contract".into(),
            implementation_revision: "implementation".into(),
        },
        parent_death_fd: None,
    };
    let worker = tokio::spawn(async move {
        serve_until(
            launch,
            move |_, context| {
                let entered = Arc::clone(&worker_entered);
                let release = Arc::clone(&worker_release);
                Box::pin(async move {
                    entered.notify_one();
                    release.notified().await;
                    context
                        .telemetry(&[TelemetryRecord {
                            kind: "log",
                            name: "drain",
                            message: "handler completed",
                            elapsed_ns: 0,
                            attributes: BTreeMap::new(),
                        }])
                        .await?;
                    Ok(Output::json(RawValue::from_string("{}".into())?))
                })
            },
            stopping.notified(),
        )
        .await
    });
    let socket = loop {
        if let Ok(socket) = UnixStream::connect(&worker_socket).await {
            break socket;
        }
        sleep(Duration::from_millis(1)).await;
    };
    let (mut client, connection) =
        hyper::client::conn::http2::handshake(TokioExecutor::new(), TokioIo::new(socket)).await?;
    let connection = tokio::spawn(connection);
    let request = Request::post("/worker/v1/invoke")
        .header(AUTHORIZATION, "Bearer test-token")
        .body(full(Bytes::from_static(
            br#"{"service":"example/service/worker","operation":"example/operation/test","mode":"direct","context":{"invocation_id":"test","scope":"test-scope","generation":1,"contract_revision":"contract"},"input":{}}"#,
        )))?;
    let response = tokio::spawn(async move { client.send_request(request).await });
    entered.notified().await;
    stop.notify_one();
    while UnixStream::connect(&worker_socket).await.is_ok() {
        sleep(Duration::from_millis(1)).await;
    }
    release.notify_one();
    let response = response.await??;
    assert_eq!(response.status(), StatusCode::OK);
    response.into_body().collect().await?;
    let batch: serde_json::Value =
        serde_json::from_slice(&batch.recv().await.ok_or("telemetry batch missing")?)?;
    assert_eq!(batch[0]["message"], "handler completed");
    worker.await??;
    receiver.abort();
    connection.abort();
    Ok(())
}

#[cfg(unix)]
#[test]
fn serve_stops_when_parent_death_pipe_closes() {
    use std::os::fd::IntoRawFd;

    run(async {
        tokio::time::timeout(Duration::from_secs(3), async {
            let directory = std::env::temp_dir().join(format!("sw-parent-{}", std::process::id()));
            let _ = std::fs::remove_dir_all(&directory);
            std::fs::create_dir(&directory)?;
            struct RemoveTestDirectory(PathBuf);
            impl Drop for RemoveTestDirectory {
                fn drop(&mut self) {
                    let _ = std::fs::remove_dir_all(&self.0);
                }
            }
            let _cleanup = RemoveTestDirectory(directory.clone());
            let worker_socket = directory.join("worker");
            let (reader, writer) = std::io::pipe()?;
            let launch = Launch {
                worker_socket: worker_socket.clone(),
                host_socket: directory.join("host"),
                token: "test-token".into(),
                identity: Identity {
                    abi: 1,
                    service: "example/service/worker".into(),
                    contract_revision: "contract".into(),
                    implementation_revision: "implementation".into(),
                },
                parent_death_fd: Some(reader.into_raw_fd()),
            };
            let task = tokio::spawn(async move {
                serve(launch, |_, _| {
                    Box::pin(async { Ok(Output::json(RawValue::from_string("{}".into())?)) })
                })
                .await
            });
            while UnixStream::connect(&worker_socket).await.is_err() {
                sleep(Duration::from_millis(1)).await;
            }
            drop(writer);
            task.await??;
            Ok::<(), Error>(())
        })
        .await?
    })
    .unwrap();
}

#[test]
fn event_emit_failure_attaches_delivery_and_normalizes_server_errors() {
    let forbidden = event_emit_failure(
        StatusCode::FORBIDDEN,
        br#"{"code":"permission_denied","message":"denied"}"#,
    );
    assert_eq!(forbidden.code.as_deref(), Some("permission_denied"));
    assert_eq!(
        forbidden.meta.as_ref().and_then(|m| m["delivery"].as_str()),
        Some("not_sent")
    );

    let internal = event_emit_failure(
        StatusCode::INTERNAL_SERVER_ERROR,
        br#"{"code":"system.internal","message":"contract implementation failure"}"#,
    );
    assert_eq!(internal.code.as_deref(), Some("unavailable"));
    assert_eq!(
        internal.meta.as_ref().and_then(|m| m["delivery"].as_str()),
        Some("unknown")
    );

    let check_not_sent = |payload: String| {
        let err = run(async move {
            let (_tx, canceled) = watch::channel(());
            let ctx = Context {
                invocation: InvocationContext {
                    invocation_id: "inv".into(),
                    scope: "scope".into(),
                    generation: 1,
                    contract_revision: "rev".into(),
                    principal: None,
                    tenant_id: None,
                    traceparent: None,
                    deadline: None,
                    locale: None,
                    execution_id: None,
                    metadata: None,
                },
                host: Host {
                    socket: Arc::new(PathBuf::from("/nonexistent")),
                    token: Arc::from("token"),
                    sender: Arc::new(Mutex::new(None)),
                },
                canceled,
                _keep_canceled_open: None,
            };
            ctx.emit_event("accepted", &payload).await.map(|_| ())
        })
        .unwrap_err();
        let failure = err
            .downcast_ref::<BindingFailure>()
            .expect("binding failure");
        assert_eq!(failure.code.as_deref(), Some("unavailable"));
        assert_eq!(
            failure.meta.as_ref().and_then(|m| m["delivery"].as_str()),
            Some("not_sent")
        );
    };
    check_not_sent("x".repeat(MAX_METADATA + 1));
    check_not_sent("small".into());
    let bad_reply = unavailable_failure(Delivery::Unknown, "invalid event receipt");
    assert_eq!(
        bad_reply.meta.as_ref().and_then(|m| m["delivery"].as_str()),
        Some("unknown")
    );
}
