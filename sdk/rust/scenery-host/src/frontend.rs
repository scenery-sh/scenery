use std::io;
use std::path::{Path, PathBuf};
use std::sync::LazyLock;

use http_body_util::{BodyExt, StreamBody};
use hyper::body::{Frame, Incoming};
use hyper::{Method, Request, Response, StatusCode};
use hyper_util::rt::TokioIo;
use percent_encoding::percent_decode_str;
use regex::Regex;
use serde_json::json;
use tokio::io::copy_bidirectional;
use tokio::net::TcpStream;
use tokio_util::io::ReaderStream;

use crate::host::{Reply, empty, header, json_reply};
use crate::plan::Frontend;

static HASHED_ASSET: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"-[a-zA-Z0-9_-]{8,}\.[a-zA-Z0-9]+$").expect("valid asset pattern")
});

pub(crate) async fn serve(frontend: Option<&Frontend>, request: Request<Incoming>) -> Reply {
    let Some(frontend) = frontend else {
        return not_found();
    };
    let Ok(decoded) = percent_decode_str(request.uri().path()).decode_utf8() else {
        return not_found();
    };
    if decoded.to_ascii_lowercase().contains("scenery")
        || frontend.reserved.iter().any(|prefix| {
            let prefix = prefix.trim_end_matches('/');
            !prefix.is_empty()
                && (decoded == prefix
                    || decoded
                        .strip_prefix(prefix)
                        .is_some_and(|tail| tail.starts_with('/')))
        })
    {
        return not_found();
    }
    if let Some(upstream) = &frontend.upstream {
        return match proxy(upstream, request).await {
            Ok(reply) => reply,
            Err(_) => empty(StatusCode::BAD_GATEWAY),
        };
    }
    if request.method() != Method::GET && request.method() != Method::HEAD {
        let mut response = empty(StatusCode::METHOD_NOT_ALLOWED);
        header(response.headers_mut(), "allow", "GET, HEAD");
        return response;
    }
    let name = decoded.strip_prefix('/').unwrap_or_default();
    let validated = name.strip_suffix('/').unwrap_or(name);
    if !name.is_empty() && !valid_path(validated) || name.ends_with(".br") || name.ends_with(".gz")
    {
        return not_found();
    }
    let mut selected = name;
    let mut path = asset(&frontend.root, selected).await;
    let accept = request
        .headers()
        .get("accept")
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default();
    if path.is_none()
        && (name.is_empty()
            || Path::new(validated).extension().is_none()
                && (accept.is_empty() || accept.contains("text/html") || accept.contains("*/*")))
    {
        selected = "index.html";
        path = asset(&frontend.root, selected).await;
    }
    let Some(path) = path else { return not_found() };
    let Ok(file) = tokio::fs::File::open(path).await else {
        return not_found();
    };
    let Ok(info) = file.metadata().await else {
        return not_found();
    };
    let media = mime_guess::from_path(selected).first_or_octet_stream();
    let immutable = selected.starts_with("assets/") && HASHED_ASSET.is_match(selected);
    let mut reply = empty(StatusCode::OK);
    let cache = if immutable {
        "public, max-age=31536000, immutable"
    } else {
        "no-cache"
    };
    header(reply.headers_mut(), "cache-control", cache);
    header(reply.headers_mut(), "content-type", media.essence_str());
    header(
        reply.headers_mut(),
        "content-length",
        &info.len().to_string(),
    );
    if request.method() == Method::GET {
        let stream =
            futures_util::StreamExt::map(ReaderStream::new(file), |chunk| chunk.map(Frame::data));
        *reply.body_mut() = StreamBody::new(stream).boxed_unsync();
    }
    reply
}

fn valid_path(path: &str) -> bool {
    !path.contains(['\\', '\0'])
        && path.split('/').enumerate().all(|(index, part)| {
            !part.is_empty() && (!part.starts_with('.') || (index == 0 && part == ".well-known"))
        })
}

async fn asset(root: &Path, name: &str) -> Option<PathBuf> {
    let path = root.join(name);
    if tokio::fs::canonicalize(&path).await.ok()? != path {
        return None;
    }
    tokio::fs::metadata(&path)
        .await
        .ok()?
        .is_file()
        .then_some(path)
}

pub(crate) fn not_found() -> Reply {
    json_reply(
        StatusCode::NOT_FOUND,
        &json!({"code":"not_found","message":"endpoint not found"}),
    )
}

async fn proxy(upstream: &str, mut request: Request<Incoming>) -> anyhow::Result<Reply> {
    let (mut sender, connection) =
        hyper::client::conn::http1::handshake(TokioIo::new(TcpStream::connect(upstream).await?))
            .await?;
    tokio::spawn(connection.with_upgrades());
    let upgrade = request
        .headers()
        .contains_key("upgrade")
        .then(|| hyper::upgrade::on(&mut request));
    if upgrade.is_none() {
        strip_hop_headers(request.headers_mut());
    }
    header(request.headers_mut(), "host", upstream);
    let mut response = sender.send_request(request).await?;
    if response.status() == StatusCode::SWITCHING_PROTOCOLS {
        if let Some(client) = upgrade {
            let target = hyper::upgrade::on(&mut response);
            tokio::spawn(async move {
                if let (Ok(client), Ok(target)) = tokio::join!(client, target) {
                    let _ =
                        copy_bidirectional(&mut TokioIo::new(client), &mut TokioIo::new(target))
                            .await;
                }
            });
        }
    } else {
        strip_hop_headers(response.headers_mut());
    }
    let (parts, payload) = response.into_parts();
    Ok(Response::from_parts(
        parts,
        payload.map_err(io::Error::other).boxed_unsync(),
    ))
}

fn strip_hop_headers(headers: &mut hyper::HeaderMap) {
    let extra: Vec<_> = headers
        .get_all("connection")
        .iter()
        .filter_map(|value| value.to_str().ok())
        .flat_map(|value| value.split(','))
        .map(|key| key.trim().to_owned())
        .collect();
    for key in extra {
        headers.remove(key);
    }
    for key in [
        "connection",
        "proxy-connection",
        "keep-alive",
        "upgrade",
        "te",
        "trailer",
        "transfer-encoding",
    ] {
        headers.remove(key);
    }
}
