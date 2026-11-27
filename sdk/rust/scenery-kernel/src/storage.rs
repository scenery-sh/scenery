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

use crate::config::Lookup;
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
    host: &Host,
    request: Request<Incoming>,
    scope: &Scope,
    service: &Service,
) -> Result<Reply, Fault> {
    let started = tokio::time::Instant::now();
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
    let action = request
        .uri()
        .path()
        .trim_start_matches("/host/v1/storage/")
        .to_owned();
    let method = request.method().clone();
    if !matches!(
        (action.as_str(), method),
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
    let child = scope.call.child_span()?;
    let result = serve_store(request, scope, name, &action, &query).await;
    let status_code;
    let code = match &result {
        Ok(reply) if reply.status().is_client_error() || reply.status().is_server_error() => {
            status_code = format!("http_{}", reply.status().as_u16());
            status_code.as_str()
        }
        Ok(_) => "",
        Err(fault) => fault.code,
    };
    // Object keys and contents are deliberately excluded from telemetry.
    let mut attrs = serde_json::Map::new();
    attrs.insert(
        "storage".into(),
        json!({"store": name, "operation": action}),
    );
    host.telemetry.typed_span(
        "STORAGE",
        &child,
        service,
        &format!("{name}.{action}"),
        started.elapsed(),
        code,
        attrs,
    );
    result
}

async fn serve_store(
    request: Request<Incoming>,
    scope: &Scope,
    name: &str,
    action: &str,
    query: &std::collections::BTreeMap<String, Vec<String>>,
) -> Result<Reply, Fault> {
    let one = |key: &str| {
        query
            .get(key)
            .filter(|values| values.len() == 1)
            .map(|values| values[0].as_str())
    };
    let recursive = match query.get("recursive") {
        None => false,
        Some(values) if values.len() == 1 && matches!(values[0].as_str(), "1" | "true") => true,
        Some(_) => return Err(Fault::invalid()),
    };
    if recursive && action != "delete" {
        return Err(Fault::invalid());
    }
    let key = if action == "list" {
        None
    } else if recursive {
        if query.contains_key("key") {
            Some(one("key").ok_or_else(Fault::invalid)?)
        } else {
            Some("")
        }
    } else {
        Some(
            one("key")
                .filter(|value| !value.is_empty())
                .ok_or_else(Fault::invalid)?,
        )
    };
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
    let selection = crate::local_storage::Selection {
        config: &configuration,
        store,
        name,
        tenant,
        key,
        recursive,
        query,
    };
    if store["kind"] == "local" {
        return crate::local_storage::serve(request, selection).await;
    }
    if store["kind"] == "s3" {
        return crate::s3_storage::serve(request, selection).await;
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
    if let Some(key) = key.filter(|key| !key.is_empty()) {
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
                .flat_map(|(key, values)| {
                    values.iter().map(move |value| {
                        if key == "recursive" && value == "true" {
                            (key.as_str(), "1")
                        } else {
                            (key.as_str(), value.as_str())
                        }
                    })
                }),
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
    load(&|name: &str| std::env::var_os(name))?.context("storage is not configured")
}

/// Reads `SCENERY_STORAGE_CONFIG`, inline JSON or a file path, and resolves
/// its `{"$env": "NAME"}` references.
fn load(lookup: Lookup) -> Result<Option<serde_json::Value>> {
    let Some(config) = lookup(STORAGE_CONFIG)
        .and_then(|value| value.into_string().ok())
        .filter(|value| !value.trim().is_empty())
    else {
        return Ok(None);
    };
    let data = if config.trim_start().starts_with('{') {
        ensure!(
            config.len() <= 4 << 20,
            "{STORAGE_CONFIG} exceeds 4 MiB"
        );
        config.into_bytes()
    } else {
        crate::config::read_file_limited(&PathBuf::from(&config))
            .with_context(|| format!("read {STORAGE_CONFIG} file {config}"))?
    };
    let mut value = values::parse(&data).with_context(|| format!("parse {STORAGE_CONFIG}"))?;
    ensure!(
        value["kind"] == "scenery.storage.runtime",
        "invalid storage runtime configuration"
    );
    crate::config::resolve_env(&mut value, lookup)
        .with_context(|| format!("resolve {STORAGE_CONFIG}"))?;
    Ok(Some(value))
}

/// Checks a production host's storage configuration once at startup, so a
/// missing secret or a malformed bucket stops the host before it serves.
pub(crate) fn validate(lookup: Lookup) -> Result<()> {
    let Some(config) = load(lookup)? else {
        return Ok(());
    };
    let stores = config["stores"]
        .as_object()
        .context("storage configuration has no stores")?;
    for (name, store) in stores {
        match store["kind"].as_str() {
            Some("s3") => {
                crate::s3_storage::Bucket::from_config(store, lookup)
                    .with_context(|| format!("storage store {name}"))?;
            }
            Some("local" | "proxy") => {}
            _ => anyhow::bail!("storage store {name} has an unsupported kind"),
        }
    }
    Ok(())
}

const STORAGE_CONFIG: &str = "SCENERY_STORAGE_CONFIG";

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
