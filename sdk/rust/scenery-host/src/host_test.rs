use super::*;

#[test]
fn bindings_answer_admission_failures_with_their_declared_outcomes() {
    let policy: Policy = serde_json::from_value(json!({
        "BindingAddress":"api", "AuthorizationStrategy":"deny_unless_allowed",
        "AuthorizationRules":[], "TotalInvocationTimeoutNanos":0, "MaxRequestHeaderBytes":0,
        "CORS":"none", "AllowedOrigins":[],
        "TransportStatuses":{"admission.unauthenticated":499, "admission.forbidden":404},
    }))
    .unwrap();
    let unauthenticated =
        || Fault::new(401, "unauthenticated", "invalid or missing authentication");
    for (fault, status, code) in [
        (unauthenticated(), 499, "admission.unauthenticated"),
        (Fault::forbidden(), 404, "admission.forbidden"),
        (Fault::invalid(), 400, "transport.invalid_request"),
    ] {
        let message = fault.message;
        let (answered, problem) = fault.problem(Some(&policy));
        assert_eq!(answered, status);
        // Generated clients select the failure branch by this code.
        assert_eq!(problem, json!({"code": code, "message": message}));
    }
    // Without a mapping the outcome keeps the status of the fault.
    let plain = Policy {
        transport_statuses: Default::default(),
        ..policy
    };
    let (status, problem) = unauthenticated().problem(Some(&plain));
    assert_eq!(status, 401);
    assert_eq!(problem["code"], "admission.unauthenticated");
    // Host capabilities are no binding and keep the internal code.
    let (status, problem) = Fault::forbidden().problem(None);
    assert_eq!(status, 403);
    assert_eq!(problem["code"], "permission_denied");
}

#[test]
fn control_calls_forward_only_a_real_invocation_credential() {
    let forwarded = |invocation: Option<&'static str>| {
        let mut headers = HeaderMap::new();
        headers.insert("authorization", HeaderValue::from_static("Bearer control"));
        if let Some(value) = invocation {
            headers.insert(
                "x-app-invocation-authorization",
                HeaderValue::from_static(value),
            );
        }
        forward_invocation_authorization(&mut headers);
        assert!(!headers.contains_key("x-app-invocation-authorization"));
        headers.get("authorization").cloned()
    };
    assert_eq!(forwarded(Some("Bearer user")).unwrap(), "Bearer user");
    // `scenery call` without `--as` used to send the header empty,
    // which turned every internal binding call into a 401.
    assert_eq!(forwarded(Some("")), None);
    assert_eq!(forwarded(Some("  ")), None);
    assert_eq!(forwarded(None), None);
}

#[tokio::test]
async fn production_worker_metadata_respects_declared_log_filters_and_api_cron() {
    let sink = telemetry::Sink::new(
        "inbox",
        false,
        &crate::plan::Observability {
            logs: crate::plan::EndpointFilter {
                include_endpoints: vec![],
                exclude_endpoints: vec!["orders.Mute".into()],
            },
            tracing: Default::default(),
        },
        Default::default(),
        &crate::plan::ReportIdentity {
            schema_revision: "sha256:fixture".into(),
            producer_version: "v1.0.0".into(),
        },
    )
    .unwrap();
    let generation = Arc::new(Generation {
        number: 1,
        identity: crate::process::BuildIdentity {
            contract_revision: String::new(),
            implementation_revision: String::new(),
            build_input_digest: String::new(),
            go_target: String::new(),
        },
        workers: Default::default(),
        retired: CancellationToken::new(),
    });
    let mut call = Call::new(
        generation,
        &HeaderMap::new(),
        "POST",
        "/orders",
        Duration::from_secs(5),
    )
    .unwrap();
    call.principal = Some(Claims {
        user: "user-1".into(),
        tenant: "tenant-1".into(),
        ..Claims::default()
    });
    call.cron_idempotency_key = Some("cron-key-1".into());
    let service = Service {
        address: "app/service/orders".into(),
        module: "orders".into(),
        name: "orders".into(),
        process: "orders".into(),
        runtime: "go".into(),
        operations: Default::default(),
        publishers: Default::default(),
        clients: Default::default(),
        bindings: vec![],
        durable: Default::default(),
        schedules: vec![],
        stores: Default::default(),
    };
    let enabled = worker_invocation_metadata(&sink, &call, &service, "app/binding/create", "sync");
    assert_eq!(enabled["logs_enabled"], true);
    assert_eq!(
        enabled["api"],
        json!({"exposed": false, "auth_required": true})
    );
    assert_eq!(enabled["cron_idempotency_key"], "cron-key-1");
    let muted = worker_invocation_metadata(&sink, &call, &service, "app/binding/mute", "sync");
    assert_eq!(muted["logs_enabled"], false);
}

#[tokio::test]
async fn mcp_call_binding_over_durable_execution_attempts_durable_dispatch() {
    let (_sender, receiver) = watch::channel(None);
    let policy = json!({
        "BindingAddress":"orders/binding/mcp_sync", "AuthorizationStrategy":"public",
        "AuthorizationRules":[], "TotalInvocationTimeoutNanos":0, "MaxRequestHeaderBytes":0,
        "CORS":"none", "AllowedOrigins":[], "TransportStatuses":{},
    });
    let service: Service = serde_json::from_value(json!({
        "Address": "orders/service/orders", "Module": "orders", "Name": "orders",
        "Process": "worker-orders", "Runtime": "go",
        "Operations": {"orders/operation/sync": {"Input": "json", "Outcomes": {"result.ok": "json"}}},
        "Clients": {},
        "Bindings": [{
            "Address": "orders/binding/mcp_sync", "Operation": "orders/operation/sync",
            "Visibility": "", "Delivery": "call", "Execution": "orders/execution/sync",
            "HTTP": null, "MCP": true, "Policy": policy,
        }],
        "Durable": {
            "orders/execution/sync": {
                "Operation": "orders/operation/sync",
                "Registration": {
                    "Address": "orders/execution/sync", "EngineAddress": "orders/engine/main",
                    "ExternalName": "sync", "Service": "orders", "Revision": 1,
                    "DefaultTimeout": 5_000_000_000_u64, "DefaultLease": 5_000_000_000_u64,
                    "MaxAttempts": 1, "RetryInitial": 1000, "RetryMax": 1000,
                    "RetryBackoff": 1.0, "RetryJitter": 0.0, "SuccessRetention": 0, "FailureRetention": 0,
                    "MaxConcurrency": 1, "DeduplicationRetention": 0, "DeduplicationConflict": "return_existing"
                },
                "Dedupe": [], "Concurrency": []
            }
        },
        "Schedules": [], "Stores": {},
    }))
    .unwrap();
    let bundle: Bundle = serde_json::from_value(json!({
        "version": 1, "contract_revision": "contract", "programs": [],
        "reports": {"schema_revision": "sha256:fixture", "producer_version": "test"},
        "host": {"Name": "test", "Workers": []}, "types": {},
        "host_config_keys": [],
    }))
    .unwrap();
    let host = Host::new(
        bundle,
        Value::Null,
        Default::default(),
        "control".into(),
        None,
        (
            CancellationToken::new(),
            crate::assistant::Gateway::new(None, None, &[]).unwrap(),
        ),
        receiver,
    )
    .unwrap();
    let generation = Arc::new(Generation {
        number: 1,
        identity: crate::process::BuildIdentity {
            contract_revision: "contract".into(),
            implementation_revision: "impl".into(),
            build_input_digest: "digest".into(),
            go_target: "worker".into(),
        },
        workers: Default::default(),
        retired: CancellationToken::new(),
    });
    let call = Call::new(
        generation,
        &HeaderMap::new(),
        "MCP",
        "/",
        Duration::from_secs(5),
    )
    .unwrap();
    let err = host
        .dispatch(&call, &service, &service.bindings[0], json!({}))
        .await
        .err()
        .expect("durable dispatch must run before direct worker lookup");
    // Without durable dispatch it would fail with unavailable (missing worker process);
    // taking the durable path attempts enqueue and returns dispatch.rejected when DB is absent.
    assert_eq!(err.code, "dispatch.rejected");
}
