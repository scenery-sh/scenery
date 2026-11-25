use std::io;
use std::path::PathBuf;

use anyhow::{Context, Result, ensure};
use http_body_util::BodyExt;
use hyper::body::Incoming;
use hyper::{Method, Request, Response};
use hyper_util::rt::TokioIo;
use percent_encoding::{AsciiSet, CONTROLS, utf8_percent_encode};
use serde_json::json;
use sha2::{Digest, Sha256};
use tokio::net::UnixStream;

use crate::host::{Fault, Host, Reply, Scope, header, json_reply};
use crate::plan::Service;
use crate::process::hex;
use crate::values;

const PATH_COMPONENT: &AsciiSet = &CONTROLS
    .add(b'/')
    .add(b'?')
    .add(b'#')
    .add(b'%')
    .add(b' ')
    .add(b'\\')
    .add(b'"')
    .add(b'<')
    .add(b'>')
    .add(b'\x7f');

pub(crate) async fn serve(
    request: Request<Incoming>,
    scope: &Scope,
    service: &Service,
) -> Result<Reply, Fault> {
    let query = values::query_pairs(request.uri().query());
    let one = |key: &str| {
        query
            .get(key)
            .filter(|values| values.len() == 1)
            .map(|values| values[0].as_str())
    };
    let name = one("store")
        .and_then(|name| service.stores.get(name))
        .ok_or_else(Fault::forbidden)?;
    let action = request.uri().path().trim_start_matches("/host/v1/storage/");
    let method = request.method().clone();
    if !matches!(
        (action, method),
        ("read", Method::GET | Method::HEAD)
            | ("write", Method::PUT)
            | ("delete", Method::DELETE)
            | ("list", Method::GET)
            | ("stat", Method::HEAD)
    ) {
        return Err(Fault::new(
            405,
            "method_not_allowed",
            "unsupported storage method",
        ));
    }
    let key = if action == "list" {
        None
    } else {
        Some(
            one("key")
                .filter(|value| !value.is_empty())
                .ok_or_else(Fault::invalid)?,
        )
    };
    if query.contains_key("recursive") {
        return Err(Fault::invalid());
    }
    let configuration = config().map_err(Fault::internal)?;
    let store = configuration["stores"]
        .get(name)
        .ok_or_else(Fault::forbidden)?;
    let tenant = if store["tenant_scoped"] == true {
        scope
            .call
            .principal
            .as_ref()
            .map(|claims| claims.tenant.as_str())
            .filter(|tenant| !tenant.is_empty())
            .ok_or_else(|| Fault::new(403, "tenant_required", "storage requires a tenant"))?
    } else {
        ""
    };
    if store["kind"] == "local" {
        return crate::local_storage::serve(
            request,
            crate::local_storage::Selection {
                config: &configuration,
                store,
                name,
                tenant,
                key,
                query: &query,
            },
        )
        .await;
    }
    if store["kind"] != "proxy" {
        return Err(Fault::new(
            503,
            "unavailable",
            "storage backend is unavailable",
        ));
    }
    let socket = store["proxy_socket"]
        .as_str()
        .ok_or_else(Fault::forbidden)?;
    let namespace = namespace_binding(&configuration["namespace"]).map_err(Fault::internal)?;
    let name = utf8_percent_encode(name, PATH_COMPONENT);
    let mut path = format!("/v1/stores/{name}");
    if let Some(key) = key {
        path.push_str(&format!(
            "/objects/{}",
            utf8_percent_encode(key, PATH_COMPONENT)
        ));
    }
    let options = url::form_urlencoded::Serializer::new(String::new())
        .extend_pairs(
            query
                .iter()
                .filter(|(key, _)| !matches!(key.as_str(), "store" | "key"))
                .flat_map(|(key, values)| values.iter().map(move |value| (key, value))),
        )
        .finish();
    if !options.is_empty() {
        path.push('?');
        path.push_str(&options);
    }
    let (mut parts, incoming) = request.into_parts();
    parts.uri = path.parse().map_err(Fault::internal)?;
    parts.headers.remove("authorization");
    parts.headers.remove("x-worker-scope");
    header(&mut parts.headers, "host", "storage");
    header(
        &mut parts.headers,
        "x-scenery-storage-namespace",
        &namespace,
    );
    header(
        &mut parts.headers,
        "x-scenery-storage-tenant",
        &values::base64url(tenant.as_bytes()),
    );
    let stream = UnixStream::connect(socket).await.map_err(Fault::internal)?;
    let (mut sender, connection) = hyper::client::conn::http1::handshake(TokioIo::new(stream))
        .await
        .map_err(Fault::internal)?;
    tokio::spawn(connection);
    let (parts, body) = sender
        .send_request(Request::from_parts(parts, incoming))
        .await
        .map_err(Fault::internal)?
        .into_parts();
    Ok(Response::from_parts(
        parts,
        body.map_err(io::Error::other).boxed_unsync(),
    ))
}

pub(crate) fn failure(host: &Host, fault: Fault) -> Reply {
    let (code, diagnostic) = match fault.code {
        "system.internal" => ("internal", "SCN9000"),
        "transport.invalid_request" => ("invalid_argument", "SCN8001"),
        "permission_denied" => ("permission_denied", "SCN8005"),
        "tenant_required" => ("tenant_required", "SCN8005"),
        "unavailable" => ("capability_unavailable", "SCN8004"),
        "deadline_exceeded" => ("canceled", "SCN8003"),
        code => (code, "SCN8003"),
    };
    let mut failure = json!({
        "code": code,
        "diagnostic": diagnostic,
        "message": fault.envelope()["message"],
    });
    let original = fault.reply(host, None);
    if let Some(token) = original
        .headers()
        .get("x-report-token")
        .and_then(|token| token.to_str().ok())
    {
        failure["report_token"] = json!(token);
    }
    let mut reply = json_reply(original.status(), &failure);
    header(
        reply.headers_mut(),
        "x-storage-error",
        &values::base64url(values::encode(&failure).as_bytes()),
    );
    header(reply.headers_mut(), "cache-control", "no-store");
    reply
}

fn config() -> Result<serde_json::Value> {
    let config = std::env::var("SCENERY_STORAGE_CONFIG").context("storage is not configured")?;
    let data = if config.trim_start().starts_with('{') {
        config.into_bytes()
    } else {
        std::fs::read(PathBuf::from(config))?
    };
    let value = values::parse(&data)?;
    ensure!(
        value["kind"] == "scenery.storage.runtime",
        "invalid storage runtime configuration"
    );
    Ok(value)
}

fn namespace_binding(namespace: &serde_json::Value) -> Result<String> {
    // The existing storage service signs the Go struct's field order.
    use serde::{Deserialize, Serialize};
    #[derive(Deserialize, Serialize)]
    struct Namespace {
        root: String,
        binding: Binding,
        incarnation: String,
    }
    #[derive(Deserialize, Serialize)]
    struct Binding {
        app_id: String,
        app_root: String,
        worktree_key: String,
        user_id: u32,
        managed: bool,
    }
    let namespace: Namespace = serde_json::from_value(namespace.clone())?;
    let payload = serde_json::to_vec(&namespace)?;
    Ok(hex(&Sha256::digest(payload)))
}
