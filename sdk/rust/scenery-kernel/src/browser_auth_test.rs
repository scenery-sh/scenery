use std::convert::Infallible;
use std::sync::Arc;

use anyhow::{Result, ensure};
use base64::{Engine as _, engine::general_purpose::STANDARD};
use hyper::{Method, Request, StatusCode, body::Incoming, service::service_fn};
use hyper_util::rt::TokioIo;
use serde_json::{Value, json};
use tokio::net::TcpListener;
use tokio_util::sync::CancellationToken;

use crate::auth::Authentication;
use crate::host::{Host, Reply, empty, json_reply};
use crate::plan::Bundle;
use crate::process::BuildIdentity;
use crate::supervisor::Generation;
use crate::{assistant, durable, gateway, transport};

/// A browser sends `text/plain` and form bodies cross-site without asking.
/// The JSON routes must refuse them before they touch a session, and answer
/// the preflight of the origins the application trusts.
#[tokio::test]
async fn auth_routes_refuse_cross_site_forms_and_answer_trusted_origins() -> Result<()> {
    let app = TcpListener::bind("127.0.0.1:0").await?;
    let base = format!("http://{}", app.local_addr()?);
    let (host, generation) = auth_host(&base, "http://127.0.0.1:9")?;
    let mut host = Arc::try_unwrap(host).ok().expect("unshared host");
    host.origins = gateway::Origins::new("https://app.example", false);
    let host = Arc::new(host);
    let app_server = serve(app, move |request| {
        let (host, generation) = (host.clone(), generation.clone());
        async move { gateway::ingress(&host, generation, request, false).await }
    });
    let http = reqwest::Client::builder().no_proxy().build()?;
    let forged = r#"{"email":"attacker@evil.test","password":"attacker-pass-123","x":"="}"#;
    for route in ["login/email", "signup/email", "email-verification/confirm"] {
        for media in [
            Some("text/plain"),
            Some("application/x-www-form-urlencoded"),
            None,
        ] {
            let mut request = http
                .post(format!("{base}/auth/{route}"))
                .header("origin", "https://evil.example")
                .body(forged);
            if let Some(media) = media {
                request = request.header("content-type", media);
            }
            let response = request.send().await?;
            assert_eq!(
                response.status(),
                StatusCode::UNSUPPORTED_MEDIA_TYPE,
                "{route} {media:?}"
            );
            assert!(response.headers().get("set-cookie").is_none());
            assert!(
                response
                    .headers()
                    .get("access-control-allow-origin")
                    .is_none()
            );
            assert_eq!(response.headers()["vary"], "Origin");
        }
    }

    let preflight = |origin: &'static str| {
        http.request(Method::OPTIONS, format!("{base}/auth/login/email"))
            .header("origin", origin)
            .header("access-control-request-method", "POST")
            .header("access-control-request-headers", "content-type")
            .send()
    };
    let allowed = preflight("https://app.example").await?;
    assert_eq!(allowed.status(), StatusCode::NO_CONTENT);
    assert_eq!(
        allowed.headers()["access-control-allow-origin"],
        "https://app.example"
    );
    assert_eq!(
        allowed.headers()["access-control-allow-credentials"],
        "true"
    );
    assert_eq!(allowed.headers()["access-control-allow-methods"], "POST");
    assert_eq!(
        allowed.headers()["access-control-allow-headers"],
        "content-type"
    );
    assert_eq!(
        preflight("https://evil.example").await?.status(),
        StatusCode::FORBIDDEN
    );

    // The grant is on the actual response too, also when it is an error.
    let refused = http
        .post(format!("{base}/auth/login/email"))
        .header("origin", "https://app.example")
        .header("content-type", "text/plain")
        .body(forged)
        .send()
        .await?;
    assert_eq!(refused.status(), StatusCode::UNSUPPORTED_MEDIA_TYPE);
    assert_eq!(
        refused.headers()["access-control-allow-origin"],
        "https://app.example"
    );
    app_server.abort();
    Ok(())
}

#[tokio::test]
#[ignore = "requires an empty local PostgreSQL database in DATABASE_URL"]
async fn development_email_session_and_oauth_connection_routes() -> Result<()> {
    let (db, _driver) = durable::connect().await?;
    let clean: bool = db
        .query_one(
            "SELECT to_regclass('scenery.scenery_auth_users') IS NULL",
            &[],
        )
        .await?
        .get(0);
    ensure!(clean, "connection test requires an empty database");

    let app = TcpListener::bind("127.0.0.1:0").await?;
    let base = format!("http://{}", app.local_addr()?);
    let issuer = TcpListener::bind("127.0.0.1:0").await?;
    let issuer_url = format!("http://{}", issuer.local_addr()?);
    let mock = serve(issuer, |request| async move {
        let route = request.uri().path();
        match (request.method(), route) {
            (&Method::POST, "/token") => {
                let body = transport::read(request.into_body(), 4096).await.unwrap();
                let params: std::collections::BTreeMap<_, _> =
                    url::form_urlencoded::parse(&body).collect();
                assert_eq!(params["grant_type"], "authorization_code");
                assert_eq!(params["code"], "mock-code");
                assert_eq!(params["client_id"], "example-client");
                assert!(!params["code_verifier"].is_empty());
                json_reply(
                    StatusCode::OK,
                    &json!({
                        "access_token":"example-access", "refresh_token":"example-refresh",
                        "token_type":"Bearer", "scope":"files.read", "expires_in":3600,
                    }),
                )
            }
            (&Method::GET, "/account") => {
                assert_eq!(request.headers()["authorization"], "Bearer example-access");
                json_reply(
                    StatusCode::OK,
                    &json!({"id":"example-account","email":"account@example.test"}),
                )
            }
            (&Method::POST, "/revoke") => {
                let body = transport::read(request.into_body(), 4096).await.unwrap();
                let params: std::collections::BTreeMap<_, _> =
                    url::form_urlencoded::parse(&body).collect();
                assert_eq!(params["token"], "example-refresh");
                empty(StatusCode::OK)
            }
            _ => empty(StatusCode::NOT_FOUND),
        }
    });
    let (host, generation) = auth_host(&base, &issuer_url)?;
    let app_server = serve(app, {
        let host = host.clone();
        move |request| {
            let host = host.clone();
            let generation = generation.clone();
            async move {
                let private = request.headers().contains_key("x-test-private");
                gateway::ingress(&host, generation, request, private).await
            }
        }
    });
    let http = reqwest::Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let bootstrap = || {
        http.post(format!("{base}/users/dev-bootstrap"))
            .header("x-test-private", "1")
            .json(&json!({}))
    };
    let response = bootstrap().send().await?.error_for_status()?;
    assert!(
        response.headers()["set-cookie"]
            .to_str()?
            .contains("HttpOnly")
    );
    let session: Value = response.json().await?;
    let user = session["user"]["id"].as_str().unwrap();
    let workspace = session["current_tenant_id"].as_str().unwrap();
    assert!(crate::connections::valid_uuid(user));
    assert_eq!(workspace, "00000000-0000-0000-0000-000000000001");
    assert_eq!(session["user"]["email"], "Developer@example.test");
    let repeated: Value = bootstrap().send().await?.error_for_status()?.json().await?;
    assert_eq!(repeated["user"]["id"], user);
    assert_eq!(repeated["current_tenant_id"], workspace);
    // A developing host signs in its default identity publicly too; explicit
    // identity claims remain private.
    let public: Value = http
        .post(format!("{base}/users/dev-bootstrap"))
        .json(&json!({}))
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    assert_eq!(public["user"]["id"], user);
    let claimed = http
        .post(format!("{base}/users/dev-bootstrap"))
        .json(&json!({"user_id": "owner"}))
        .send()
        .await?;
    assert_eq!(claimed.status(), StatusCode::FORBIDDEN);

    let token = session["token"].as_str().unwrap();
    let path = format!("{base}/auth/connections/files");
    assert_eq!(
        http.get(&path).send().await?.status(),
        StatusCode::UNAUTHORIZED
    );
    let start = http
        .post(format!("{path}/start"))
        .bearer_auth(token)
        .json(&json!({"scopes":["files.read"],"redirect_path":"/settings?tab=connections"}))
        .send()
        .await?
        .error_for_status()?;
    let cookies = start.headers()["set-cookie"].to_str()?.to_owned();
    assert!(
        cookies.contains("HttpOnly; SameSite=Lax") && !cookies.to_lowercase().contains("scenery")
    );
    let start: Value = start.json().await?;
    let authorize = url::Url::parse(start["authorize_url"].as_str().unwrap())?;
    let state = authorize
        .query_pairs()
        .find(|(k, _)| k == "state")
        .unwrap()
        .1;
    assert!(
        authorize
            .query_pairs()
            .any(|(k, v)| k == "code_challenge_method" && v == "S256")
    );
    let callback = || {
        http.get(format!("{path}/callback"))
            .query(&[("state", state.as_ref()), ("code", "mock-code")])
    };
    assert_eq!(callback().send().await?.status(), StatusCode::BAD_REQUEST);
    let cookie = cookies.split(';').next().unwrap();
    let response = callback().header("cookie", cookie).send().await?;
    assert_eq!(response.status(), StatusCode::FOUND);
    assert_eq!(
        response.headers()["location"],
        format!("{base}/settings?connection_connected=files&tab=connections")
    );
    assert!(
        response.headers()["set-cookie"]
            .to_str()?
            .contains("Max-Age=0")
    );
    assert_eq!(
        callback().header("cookie", cookie).send().await?.status(),
        StatusCode::BAD_REQUEST
    );

    let status: Value = http
        .get(&path)
        .bearer_auth(token)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    assert_eq!(status["scopes"], json!(["files.read"]));
    assert_eq!(status["subject"], "example-account");
    assert_eq!(status["status"], "active");
    assert!(!status.to_string().contains("example-access"));
    let stored = db.query_one(
        "SELECT access_token_ciphertext,refresh_token_ciphertext FROM scenery.scenery_auth_connections
         WHERE user_id=$1::text::uuid AND provider_id='files'", &[&user],
    ).await?;
    let access: Vec<u8> = stored.get(0);
    let refresh: Vec<u8> = stored.get(1);
    assert_eq!(access[0], 1);
    assert_eq!(refresh[0], 1);
    assert!(!access.windows(14).any(|part| part == b"example-access"));
    let disconnected: Value = http
        .post(format!("{path}/disconnect"))
        .bearer_auth(token)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    assert_eq!(disconnected["status"], "disconnected");
    assert_eq!(disconnected["revocation_status"], "succeeded");
    assert!(disconnected.get("subject").is_none());
    let cleared: bool = db.query_one(
        "SELECT access_token_ciphertext IS NULL AND refresh_token_ciphertext IS NULL
         FROM scenery.scenery_auth_connections WHERE user_id=$1::text::uuid AND provider_id='files'",
        &[&user],
    ).await?.get(0);
    assert!(cleared);

    app_server.abort();
    mock.abort();
    Ok(())
}

/// Builds a host with the standard auth routes and one OAuth connection.
fn auth_host(base: &str, issuer_url: &str) -> Result<(Arc<Host>, Arc<Generation>)> {
    let bundle: Bundle = serde_json::from_value(json!({
        "version":1,"contract_revision":"contract","programs":[],
        "reports":{"schema_revision":"sha256:fixture","producer_version":"test"},
        "host":{"Name":"connections","Workers":[]},"types":{},
        "host_config_keys":["auth.jwt_secret"],
        "auth":{
            "Enabled":true,"GoogleOAuth":{"Enabled":false},
            "DevBootstrap":{"Enabled":true,"DefaultUserEmail":"Developer@example.test","DefaultTenantID":""},
            "Connections":[{
                "id":"files","protocol":"oauth2","issuer":issuer_url,"client_auth":"none",
                "authorize_url":format!("{issuer_url}/authorize"),"token_url":format!("{issuer_url}/token"),
                "account_url":format!("{issuer_url}/account"),"revoke_url":format!("{issuer_url}/revoke"),
                "subject_field":"id","allowed_scopes":["files.read"]
            }],
            "bootstrap_sql":include_str!("../../../../auth/db/gen/schema.sql"),
        }
    }))?;
    let generation = Arc::new(Generation {
        number: 1,
        identity: BuildIdentity {
            contract_revision: "contract".into(),
            implementation_revision: "implementation".into(),
            build_input_digest: "inputs".into(),
            go_target: "test".into(),
        },
        workers: Default::default(),
        retired: CancellationToken::new(),
    });
    let (_sender, receiver) = tokio::sync::watch::channel(Some(generation.clone()));
    let mut host = Host::new(
        bundle,
        json!({
            "secrets":{
                "auth.jwt_secret":STANDARD.encode("test-key"),
                "auth.token_cipher_key":STANDARD.encode(STANDARD.encode([9;32])),
            },
            "values":{
                "auth.connections.files.client_id":"example-client",
                "auth.connections.files.callback_uri":format!("{base}/auth/connections/files/callback"),
            },
        }),
        Default::default(),
        "control".into(),
        crate::host::Mode::Production,
        (
            CancellationToken::new(),
            assistant::Gateway::new(None, None, &[])?,
        ),
        receiver,
    )?;
    host.development = true;
    host.auth = Authentication::new(host.bundle.auth.as_ref(), &host.configuration, true)?;
    Ok((Arc::new(host), generation))
}

fn serve<F, R>(listener: TcpListener, route: F) -> tokio::task::JoinHandle<()>
where
    F: Fn(Request<Incoming>) -> R + Clone + Send + 'static,
    R: Future<Output = Reply> + Send + 'static,
{
    tokio::spawn(async move {
        while let Ok((socket, _)) = listener.accept().await {
            let route = route.clone();
            tokio::spawn(async move {
                let service = service_fn(move |request| {
                    let response = route(request);
                    async { Ok::<_, Infallible>(response.await) }
                });
                let _ = hyper::server::conn::http1::Builder::new()
                    .serve_connection(TokioIo::new(socket), service)
                    .await;
            });
        }
    })
}
