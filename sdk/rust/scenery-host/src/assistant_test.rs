use std::convert::Infallible;
use std::fs::Permissions;
use std::os::unix::fs::PermissionsExt;
use std::sync::{Arc, Mutex};

use base64::{
    Engine,
    engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD},
};
use http_body_util::Full;
use hyper::body::Incoming;
use hyper::service::service_fn;
use hyper::{Method, Request, StatusCode};
use hyper_util::rt::{TokioExecutor, TokioIo};
use serde_json::{Value, json};
use tokio::net::{TcpListener, UnixListener};
use tokio_util::sync::CancellationToken;

use crate::plan::Bundle;
use crate::supervisor::Generation;
use crate::{assistant, auth, process, transport};

const ASSISTANT: &str = "app/assistant/tools";
const SECRET: &str = "fake-runtime-test-signing-secret";
const VERSION: &str = "2025-11-25";
const OPERATION: &str = "app/operation/tool";
const BINDING: &str = "app/binding/tool";
const PUBLIC_ROUTE: &str = "/assistants/tools/v1/conversations";
/// Text deltas of 900 KiB that make a helper replay larger than 16 MiB.
const LONG_HISTORY: u64 = 20;

#[tokio::test(flavor = "multi_thread")]
async fn assistant_and_mcp_use_the_public_and_worker_protocols() -> anyhow::Result<()> {
    exercise().await
}

async fn exercise() -> anyhow::Result<()> {
    let short = tempfile::Builder::new()
        .prefix("tools-test-")
        .tempdir_in("/tmp")?;
    let mcp_port = TcpListener::bind("127.0.0.1:0").await?;
    let mcp_address = mcp_port.local_addr()?;
    drop(mcp_port);
    let control_listener = TcpListener::bind("127.0.0.1:0").await?;
    let helper = format!("http://{}", control_listener.local_addr()?);
    let runtime = short.path().join("descriptors.json");
    let key = short.path().join("key");
    write_private(
        &runtime,
        &json!({"assistants":[{
            "assistant_address":ASSISTANT, "control_address":helper, "control_token":"private",
            "mcp_listen_address":mcp_address, "mcp_bridge_secret":SECRET,
            "runtime_revision":"runtime", "capability_revision":"capabilities",
        }]})
        .to_string(),
    )?;
    write_private(&key, "0123456789abcdef0123456789abcdef")?;
    let bundle = bundle();
    let assistants = assistant::Gateway::new(Some(&runtime), Some(&key), &bundle.host.assistants)?;
    let worker = Arc::new(process::Worker::test_worker(short.path())?);
    let worker_socket = UnixListener::bind(&worker.launch.worker_socket)?;
    let worker_token = worker.launch.token.clone();
    let worker_server = tokio::spawn(async move {
        let (socket, _) = worker_socket.accept().await.unwrap();
        let service = service_fn(move |request: Request<Incoming>| {
            let worker_token = worker_token.clone();
            async move {
                assert_eq!(request.uri().path(), scenery_wire::INVOKE_PATH);
                assert_eq!(
                    request.headers()["authorization"],
                    format!("Bearer {worker_token}")
                );
                let input = transport::read(request.into_body(), 16 << 10)
                    .await
                    .unwrap();
                let invocation: scenery_wire::Invocation = serde_json::from_slice(&input).unwrap();
                assert_eq!(invocation.binding.as_deref(), Some(BINDING));
                assert_eq!(invocation.operation, OPERATION);
                assert_eq!(invocation.context.principal.as_deref(), Some("user-one"));
                assert_eq!(invocation.context.tenant_id.as_deref(), Some("tenant-one"));
                assert!(
                    invocation
                        .context
                        .traceparent
                        .as_deref()
                        .unwrap()
                        .contains("4bf92f3577b34da6a3ce929d0e0e4736")
                );
                let data: Value = serde_json::from_str(invocation.input.get()).unwrap();
                let body =
                    json!({"outcome":{"kind":"result","name":"ok","value":data}}).to_string();
                let mut reply = transport::response(StatusCode::OK, body);
                crate::host::header(reply.headers_mut(), "content-type", "application/json");
                crate::host::header(
                    reply.headers_mut(),
                    scenery_wire::CONTRACT_HEADER,
                    "contract",
                );
                crate::host::header(
                    reply.headers_mut(),
                    scenery_wire::IMPLEMENTATION_HEADER,
                    "implementation",
                );
                Ok::<_, Infallible>(reply)
            }
        });
        let _ = hyper::server::conn::http2::Builder::new(TokioExecutor::new())
            .serve_connection(TokioIo::new(socket), service)
            .await;
    });
    let generation = Arc::new(Generation {
        number: 1,
        identity: worker.program.identity.clone(),
        workers: [(worker.program.process.clone(), worker)].into(),
        retired: CancellationToken::new(),
    });
    let (_publication, rx) = tokio::sync::watch::channel(Some(generation));
    let stop = CancellationToken::new();
    let host = Arc::new(crate::host::Host::new(
        bundle,
        json!({"secrets":{"auth.jwt_secret":STANDARD.encode("test-auth-key")}}),
        Default::default(),
        "unused".into(),
        None,
        (stop.clone(), assistants),
        rx,
    )?);
    let issue = |user: &str| {
        host.auth.issue(auth::Claims {
            user: user.into(),
            tenant: "tenant-one".into(),
            ..Default::default()
        })
    };
    let access = issue("user-one")?;
    let stranger = issue("user-two")?;

    let fake_state = Arc::new(Mutex::new(Value::Null));
    let fake = fake_helper(control_listener, fake_state.clone());
    let public_listener = TcpListener::bind("127.0.0.1:0").await?;
    let base = format!("http://{}", public_listener.local_addr()?);
    let mut servers = tokio::task::JoinSet::new();
    assistant::Gateway::listeners(&host, &stop, &mut servers).await?;
    servers.spawn(crate::serve_public(
        crate::PublicListener::Tcp(public_listener),
        host,
        stop.clone(),
    ));
    let http = reqwest::Client::new();
    let private = format!("http://{mcp_address}/");
    let create = http
        .post(format!("{base}{PUBLIC_ROUTE}"))
        .bearer_auth(&access)
        .header(
            "traceparent",
            "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
        )
        .json(&json!({"message":{"role":"user","content":"Run a test"}}))
        .send()
        .await?
        .error_for_status()?
        .json::<Value>()
        .await?;
    let events = create["events_url"].as_str().unwrap();
    let denied = http
        .get(format!("{base}{events}"))
        .bearer_auth(&stranger)
        .send()
        .await?;
    assert_eq!(denied.status(), StatusCode::NOT_FOUND);
    let oversized = http
        .post(format!("{base}{PUBLIC_ROUTE}"))
        .bearer_auth(&access)
        .header("x-big", "a".repeat(5000))
        .json(&json!({"message":{"role":"user","content":"Run a test"}}))
        .send()
        .await?;
    assert_eq!(
        oversized.status(),
        StatusCode::REQUEST_HEADER_FIELDS_TOO_LARGE
    );
    assert!(
        !oversized
            .text()
            .await?
            .to_ascii_lowercase()
            .contains("scenery")
    );

    let make_call = |typ: &str, id: Option<u64>, params: Value| {
        let mut body = json!({"jsonrpc":"2.0","method":typ,"params":params});
        if let Some(id) = id {
            body["id"] = json!(id);
        }
        http.post(&private)
            .header("accept", "application/json, text/event-stream")
            .header("mcp-protocol-version", VERSION)
            .header(
                "scenery-assistant-assertion",
                assertion(&fake_state.lock().unwrap(), "capabilities"),
            )
            .json(&body)
    };
    let bad = make_call(
        "initialize",
        Some(1),
        json!({"protocolVersion":"2024-11-05"}),
    )
    .send()
    .await?;
    assert_eq!(bad.status(), StatusCode::BAD_REQUEST);
    let init = make_call("initialize", Some(2), json!({"protocolVersion":VERSION,"clientInfo":{"name":"test","version":"1"},"capabilities":{}}))
        .send().await?.error_for_status()?;
    let session = init.headers()["mcp-session-id"].clone();
    assert_eq!(
        init.json::<Value>().await?["result"]["protocolVersion"],
        VERSION
    );
    let send = |typ: &str, id: Option<u64>, params: Value| {
        make_call(typ, id, params).header("mcp-session-id", session.clone())
    };
    assert_eq!(
        send("notifications/initialized", None, json!({}))
            .send()
            .await?
            .status(),
        StatusCode::ACCEPTED
    );
    let list: Value = send("tools/list", Some(3), json!({}))
        .send()
        .await?
        .json()
        .await?;
    assert_eq!(list["result"]["tools"][0]["inputSchema"]["type"], "object");
    let call = |name: &str, value: &str| {
        send(
            "tools/call",
            Some(4),
            json!({"name":name,"arguments":{"message":value}}),
        )
    };
    let (success, worker_pid) = {
        let reply = call("read", "hello").send().await?;
        let worker = reply.headers()["x-app-worker-pid"].clone();
        (reply.json::<Value>().await?, worker)
    };
    assert!(!worker_pid.is_empty());
    assert_eq!(
        success["result"]["structuredContent"]["value"],
        json!({"message":"hello"})
    );
    let oversized: Value = call("write", &"a".repeat(128)).send().await?.json().await?;
    assert_eq!(
        oversized["result"]["structuredContent"]["problem"]["code"],
        "resource_exhausted"
    );
    let unapproved: Value = call("write", "hello").send().await?.json().await?;
    assert_eq!(
        unapproved["result"]["structuredContent"]["problem"]["code"],
        "permission_denied"
    );
    let missing: Value = call("missing", "hello").send().await?.json().await?;
    assert_eq!(missing["error"]["code"], -32602);

    let stream = http
        .get(format!("{base}{events}"))
        .bearer_auth(&access)
        .send()
        .await?
        .error_for_status()?;
    assert!(
        stream.headers()["content-type"]
            .to_str()?
            .starts_with("application/x-ndjson")
    );
    let stream = stream.text().await?;
    assert!(!stream.contains("private-provider-session"));
    assert!(stream.contains("assistant runtime"));
    assert!(!stream.contains("eve/runtime"));
    let approval = stream
        .lines()
        .map(|line| serde_json::from_str::<Value>(line).unwrap())
        .find(|event| event["type"] == "assistant.approval.required")
        .unwrap();
    let conversation = create["conversation_id"].as_str().unwrap();
    let id = approval["data"]["approval_id"].as_str().unwrap();
    let approve = format!("{base}{PUBLIC_ROUTE}/{conversation}/approvals/{id}");
    http.post(approve)
        .bearer_auth(&access)
        .json(&json!({"decision":"approve"}))
        .send()
        .await?
        .error_for_status()?;
    let mismatch: Value = call("write", "different").send().await?.json().await?;
    assert_eq!(
        mismatch["result"]["structuredContent"]["problem"]["code"],
        "permission_denied"
    );
    let approved: Value = call("write", "hello").send().await?.json().await?;
    assert_eq!(
        approved["result"]["structuredContent"]["value"],
        json!({"message":"hello"})
    );
    let reused: Value = call("write", "hello").send().await?.json().await?;
    assert_eq!(
        reused["result"]["structuredContent"]["problem"]["code"],
        "permission_denied"
    );
    let response = http
        .post(format!("{base}{PUBLIC_ROUTE}/{conversation}/turns"))
        .bearer_auth(&access)
        .json(&json!({"message":{"role":"user","content":"continue"}}))
        .send()
        .await?
        .error_for_status()?
        .json::<Value>()
        .await?;
    let run = response["run_id"].as_str().unwrap();
    let cancel = http
        .post(format!(
            "{base}{PUBLIC_ROUTE}/{conversation}/runs/{run}/cancel"
        ))
        .bearer_auth(&access)
        .send()
        .await?
        .error_for_status()?
        .json::<Value>()
        .await?;
    assert_eq!(cancel, json!({"run_id":run,"state":"cancelled"}));
    let stale_assertion = assertion(&fake_state.lock().unwrap(), "stale");
    let stale = http
        .post(&private)
        .header("accept", "application/json, text/event-stream")
        .header("scenery-assistant-assertion", stale_assertion)
        .json(&json!({"jsonrpc":"2.0","method":"ping","id":12}))
        .send()
        .await?;
    assert_eq!(stale.status(), StatusCode::CONFLICT);

    // The helper replays the whole session on every connection. Once that
    // history outgrew 16 MiB the relay used to stop at the cap, and no later
    // event of the conversation ever reached the client again.
    fake_state.lock().unwrap()["long_history"] = json!(true);
    let long = http
        .get(format!("{base}{events}?after={LONG_HISTORY}"))
        .bearer_auth(&access)
        .send()
        .await?
        .error_for_status()?
        .text()
        .await?;
    let last: Value = serde_json::from_str(long.lines().last().unwrap())?;
    assert_eq!(last["type"], "assistant.run.completed");
    assert_eq!(last["sequence"], LONG_HISTORY + 2);
    stop.cancel();
    servers.shutdown().await;
    fake.abort();
    worker_server.abort();
    Ok(())
}

fn fake_helper(listener: TcpListener, state: Arc<Mutex<Value>>) -> tokio::task::JoinHandle<()> {
    tokio::spawn(async move {
        loop {
            let (socket, _) = listener.accept().await.unwrap();
            let state = state.clone();
            tokio::spawn(async move {
                let service = service_fn(|request| fake_control(request, &state));
                let _ = hyper::server::conn::http1::Builder::new()
                    .serve_connection(TokioIo::new(socket), service)
                    .await;
            });
        }
    })
}

async fn fake_control(
    request: Request<Incoming>,
    state: &Mutex<Value>,
) -> Result<hyper::Response<Full<bytes::Bytes>>, Infallible> {
    assert_eq!(
        request.headers()["x-scenery-assistant-control-token"],
        "private"
    );
    if request.method() == Method::GET {
        assert_eq!(
            request.uri().path(),
            "/scenery/v1/control/sessions/private-provider-session/events"
        );
        let (run, long) = {
            let state = state.lock().unwrap();
            (state["run_id"].clone(), state["long_history"] == true)
        };
        let short = [
            ("run.started", json!({})),
            ("text.delta", json!({"text":"/eve/"})),
            ("text.delta", json!({"text":"runtime"})),
            ("approval.wait", json!({"input":{"message":"hello"}})),
        ];
        // About 17 MiB of deltas, then the event the client is waiting for.
        let filler = json!({"text":"a".repeat(900 << 10)});
        let history = std::iter::once(("run.started", json!({})))
            .chain((0..LONG_HISTORY).map(|_| ("text.delta", filler.clone())))
            .chain([("run.completed", json!({}))]);
        let events: Vec<_> = if long {
            history.collect()
        } else {
            short.into_iter().collect()
        };
        let data = events
            .into_iter().enumerate().map(|(i, (typ, data))| json!({
                "kind":"scenery.assistant.control.event",
                "schema_revision":"sha256:0a6e5fc603f3bb2ea15292b5910af273887003be5a7ae604745501b831a65df8",
                "type":typ, "run_id":run, "sequence":i+1, "assistant_address":ASSISTANT,
                "private_session_id":"private-provider-session","continuation_token":"private-continuation",
                "occurred_at":"2026-11-25T00:00:00Z","capability_name":"write","approval_id":"helper-approval",
                "runtime_revision":"runtime","capability_revision":"capabilities","data":data,
            }).to_string() + "\n").collect::<String>();
        let mut reply = transport::response(StatusCode::OK, data);
        crate::host::header(reply.headers_mut(), "content-type", "application/x-ndjson");
        return Ok(reply);
    }
    let body = transport::read(request.into_body(), 16 << 10)
        .await
        .unwrap();
    let input: Value = serde_json::from_slice(&body).unwrap();
    assert_eq!(input["principal"], "user-one");
    let typ = match input["type"].as_str().unwrap() {
        "conversation.create" => {
            *state.lock().unwrap() = input.clone();
            "conversation.created"
        }
        "conversation.turn" => "conversation.turn.accepted",
        "approval.resolve" => "approval.resolved",
        "run.cancel" => "run.cancelled",
        _ => panic!("unknown control request"),
    };
    Ok(transport::response(StatusCode::OK,json!({
        "kind":"scenery.assistant.control.response",
        "schema_revision":"sha256:8b0cefb8d74e4097e7c058205e55b26675c671e2922b9df8fcd8a86403390e4d",
        "type":typ,"request_id":input["request_id"],"assistant_address":ASSISTANT,
        "runtime_revision":"runtime","capability_revision":"capabilities",
        "private_session_id":"private-provider-session","continuation_token":"private-continuation",
        "approval_id":input["approval_id"].as_str().unwrap_or_default(),
        "decision":input["decision"].as_str().unwrap_or_default(),"run_id":input["run_id"],
    }).to_string()))
}

fn assertion(state: &Value, revision: &str) -> String {
    let payload = URL_SAFE_NO_PAD.encode(json!({
        "audience":"scenery","assistant_address":ASSISTANT, "principal":state["principal"],
        "conversation_digest":state["conversation_digest"],"run_id":state["run_id"],
        "capability_revision":revision,"expires_at":time::OffsetDateTime::now_utc().unix_timestamp()+30,"nonce":"test",
    }).to_string());
    let key = ring::hmac::Key::new(ring::hmac::HMAC_SHA256, SECRET.as_bytes());
    let signature = URL_SAFE_NO_PAD.encode(ring::hmac::sign(&key, payload.as_bytes()));
    format!("{payload}.{signature}")
}

fn write_private(path: &std::path::Path, body: &str) -> anyhow::Result<()> {
    std::fs::write(path, body)?;
    std::fs::set_permissions(path, Permissions::from_mode(0o600))?;
    Ok(())
}

fn bundle() -> Bundle {
    let policy = json!({"BindingAddress":BINDING,"AuthorizationStrategy":"public","AuthorizationRules":[],
        "TotalInvocationTimeoutNanos":0,"MaxRequestHeaderBytes":4096,"CORS":"none","AllowedOrigins":[],"TransportStatuses":{}});
    let capability = |name, approval| {
        json!({
            "name":name,"input_schema":{"type":"object"},"output_schema":{"type":"object"},
            "operation_address":OPERATION,"execution_address":"app/execution/tool","origin":{"kind":"local","address":BINDING},
            "limits":{"max_input_bytes":64,"max_result_bytes":256},
            "effect":{"read_only":true,"destructive":false,"idempotent":true,"open_world":false},"approval":approval,
        })
    };
    serde_json::from_value(json!({
        "version":1,"contract_revision":"contract","programs":[],
        "reports":{"schema_revision":"sha256:fixture","producer_version":"test"},
        "host":{"Name":"test","Workers":[{
            "Address":"app/service/tools","Module":"app","Name":"tools","Process":"worker-tools","Runtime":"go",
            "Operations":{"app/operation/tool":{"Input":"json","Outcomes":{"result.ok":"json"}}},
            "Clients":{},"Bindings":[{"Address":BINDING,"Operation":OPERATION,"Policy":policy,"MCP":true,
                "Visibility":"","Delivery":"call","Execution":"app/execution/tool"}],
            "Durable":{},"Schedules":[],"Stores":{},
        }],"MCPServers":{"app/mcp_server/tools":{
            "protocol_version":VERSION,"capabilities":[capability("read","never"), capability("write","always")],
        }},"Assistants":[{
            "address":ASSISTANT,"path":"/assistants/tools","mcp_server":"app/mcp_server/tools",
            "authentication":"auth","session_access":"initiator","runtime_revision":"runtime",
            "capability_revision":"capabilities","policy":policy,
        }]},
        "types":{},
        "auth":{"Enabled":true,"GoogleOAuth":{"Enabled":false},"DevBootstrap":{"Enabled":false,"DefaultTenantID":""},"Connections":[]},
        "host_config_keys":["auth.jwt_secret"],
    })).unwrap()
}
