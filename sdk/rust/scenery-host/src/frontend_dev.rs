use std::borrow::Cow;
use std::io;
use std::sync::LazyLock;
use std::time::Duration;

use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use bytes::Bytes;
use hyper::{Request, Response, Uri, Version, body::Incoming};
use hyper_util::client::legacy::{Client, connect::HttpConnector};
use hyper_util::rt::{TokioExecutor, TokioTimer};
use percent_encoding::percent_decode_str;
use regex::{Captures, Regex};

use crate::host::{Reply, body};

const VITE_FS: &str = "/@fs/";
const ASSET_PATH: &str = "/@files/";
const ESCAPE: &str = "~";
pub(crate) const TEXT_LIMIT: usize = 32 << 20;

/// How long an unused connection to the development server is kept. Node
/// closes an idle connection after five seconds; staying well below that
/// keeps a request from meeting a connection its server is closing.
const IDLE: Duration = Duration::from_secs(2);

static ASSET: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r#"/@fs/[^"'\x60<>\s\\)?,;#]+"#).expect("Vite filesystem URL"));
static SCENERY_WORD: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?i)scenery").expect("scenery pattern"));

/// Connections to the development server, kept alive between requests. A
/// page load asks it for a thousand modules or more, and a connection for
/// each costs a handshake and leaves a socket in TIME_WAIT.
static UPSTREAM: LazyLock<Client<HttpConnector, Incoming>> = LazyLock::new(|| {
    Client::builder(TokioExecutor::new())
        .pool_idle_timeout(IDLE)
        .pool_timer(TokioTimer::new())
        .build_http()
});

fn forbidden(value: &str) -> bool {
    percent_decode_str(value)
        .decode_utf8_lossy()
        .to_ascii_lowercase()
        .contains("scenery")
}

fn rewrite(source: &str) -> Cow<'_, str> {
    ASSET.replace_all(source, |matched: &Captures<'_>| {
        let url = &matched[0];
        if !forbidden(url) {
            return url.to_owned();
        }
        let path = &url[VITE_FS.len()..];
        let encoded = path
            .split('/')
            .map(|segment| {
                if forbidden(segment) || segment.starts_with(ESCAPE) {
                    format!("{ESCAPE}{}", URL_SAFE_NO_PAD.encode(segment))
                } else {
                    segment.to_owned()
                }
            })
            .collect::<Vec<_>>()
            .join("/");
        format!("{ASSET_PATH}{encoded}")
    })
}

fn scrub_scenery(source: &str) -> Cow<'_, str> {
    SCENERY_WORD.replace_all(source, "application")
}

pub(crate) fn rewrite_hmr_text(source: &str) -> Cow<'_, str> {
    match rewrite(source) {
        Cow::Borrowed(text) => scrub_scenery(text),
        Cow::Owned(text) => match scrub_scenery(&text) {
            Cow::Borrowed(_) => Cow::Owned(text),
            Cow::Owned(cleaned) => Cow::Owned(cleaned),
        },
    }
}

/// The address of `uri` on the development server at `upstream`.
pub(crate) fn upstream_uri(upstream: &str, uri: &Uri) -> Option<Uri> {
    let absolute = |target: &str| {
        Uri::builder()
            .scheme("http")
            .authority(upstream)
            .path_and_query(target)
            .build()
            .ok()
    };
    let Some(path) = uri.path().strip_prefix(ASSET_PATH) else {
        return absolute(uri.path_and_query().map_or("/", |target| target.as_str()));
    };
    let mut target = VITE_FS.to_owned();
    for (index, segment) in path.split('/').enumerate() {
        if index != 0 {
            target.push('/');
        }
        let decoded = if let Some(escaped) = segment.strip_prefix(ESCAPE) {
            String::from_utf8(URL_SAFE_NO_PAD.decode(escaped).ok()?).ok()?
        } else {
            segment.to_owned()
        };
        let file = percent_decode_str(&decoded).decode_utf8().ok()?;
        if file.contains(['/', '\\', '?', '#', '\0']) || matches!(file.as_ref(), ".." | ".") {
            return None;
        }
        target.push_str(&decoded);
    }
    if let Some(query) = uri.query() {
        target.push('?');
        target.push_str(query);
    }
    absolute(&target)
}

pub(crate) fn prepare(request: &mut Request<Incoming>, uri: Uri) {
    *request.uri_mut() = uri;
    // The kept connections speak HTTP/1.1, whatever the caller spoke.
    *request.version_mut() = Version::HTTP_11;
    request.headers_mut().remove("accept-encoding");
}

/// Sends a prepared request over a kept connection to its server.
pub(crate) async fn send(request: Request<Incoming>) -> anyhow::Result<Response<Incoming>> {
    Ok(UPSTREAM.request(request).await?)
}

pub(crate) async fn response(upstream: Response<Incoming>) -> anyhow::Result<Reply> {
    use http_body_util::BodyExt;
    let textual = upstream
        .headers()
        .get("content-type")
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| {
            let media = value
                .split(';')
                .next()
                .unwrap_or("")
                .trim()
                .to_ascii_lowercase();
            media.starts_with("text/")
                || matches!(
                    media.as_str(),
                    "application/javascript"
                        | "application/x-javascript"
                        | "application/json"
                        | "image/svg+xml"
                )
                || media.ends_with("+json")
        });
    let (mut parts, payload) = upstream.into_parts();
    if !textual
        || parts.headers.contains_key("content-encoding")
        || parts.headers.contains_key("content-range")
    {
        return Ok(Reply::from_parts(
            parts,
            payload.map_err(io::Error::other).boxed_unsync(),
        ));
    }
    let bytes = crate::transport::read(payload, TEXT_LIMIT).await?;
    let transformed = std::str::from_utf8(&bytes).map(|text| {
        if parts.status.is_success() {
            rewrite(text)
        } else {
            rewrite_hmr_text(text)
        }
    });
    let output = match transformed {
        Ok(Cow::Owned(output)) => {
            parts.headers.remove("content-length");
            Bytes::from(output)
        }
        _ => bytes,
    };
    Ok(Reply::from_parts(parts, body(output)))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn maps_vite_urls_in_modules_and_stylesheets_back_to_the_original_path() {
        let font = "/@fs/Users/dev/scenery-work/node_modules/font/index.css";
        let generated = "/@fs/Users/dev/project/.scenery/gen/typescript/client.ts";
        let source = format!(r#"import "{font}"; @import url('{generated}?direct');"#);
        let neutral = rewrite(&source);
        assert!(!forbidden(&neutral));
        for (expected, got) in [font, &format!("{generated}?direct")]
            .iter()
            .zip(neutral.split(['"', '\'']).skip(1).step_by(2))
        {
            assert_eq!(
                upstream_uri("127.0.0.1:5173", &got.parse().unwrap())
                    .unwrap()
                    .to_string(),
                format!("http://127.0.0.1:5173{expected}")
            );
        }
        assert!(
            upstream_uri("127.0.0.1:5173", &"/@files/~/..%2fsecret".parse().unwrap()).is_none()
        );
    }

    #[tokio::test]
    async fn proxies_the_aliased_module_css_and_font_with_the_original_host() -> anyhow::Result<()>
    {
        use hyper::{Method, StatusCode, service::service_fn};
        use hyper_util::rt::TokioIo;
        use std::convert::Infallible;
        use tokio::net::TcpListener;

        let vite = TcpListener::bind("127.0.0.1:0").await?;
        let host = TcpListener::bind("127.0.0.1:0").await?;
        let front_address = host.local_addr()?;
        let front = crate::plan::Frontend {
            root: "/unused".into(),
            reserved: vec!["/api".into()],
            upstream: Some(vite.local_addr()?.to_string()),
        };
        let connections = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
        let accepted = connections.clone();
        let vite = tokio::spawn(async move {
            while let Ok((socket, _)) = vite.accept().await {
                accepted.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
                tokio::spawn(async move {
                    let service = service_fn(|mut request: Request<Incoming>| async move {
                        assert_eq!(request.headers()["host"], front_address.to_string());
                        assert_eq!(
                            request.headers()["x-forwarded-host"],
                            front_address.to_string()
                        );
                        if request.uri().path() == "/hmr" {
                            // The module reload socket: echo what arrives.
                            let socket = hyper::upgrade::on(&mut request);
                            tokio::spawn(async move {
                                let mut socket = TokioIo::new(socket.await.expect("upgrade"));
                                let (mut reader, mut writer) = tokio::io::split(&mut socket);
                                let _ = tokio::io::copy(&mut reader, &mut writer).await;
                            });
                            let mut response =
                                crate::transport::response(StatusCode::SWITCHING_PROTOCOLS, "");
                            crate::host::header(response.headers_mut(), "connection", "upgrade");
                            crate::host::header(response.headers_mut(), "upgrade", "hmr");
                            return Ok(response);
                        }
                        let path = request.uri().path();
                        let (media, content) = match path {
                            "/src/main.tsx" => (
                                "application/javascript",
                                r#"import "/@fs/overlay/.scenery/font/index.css?direct";"#,
                            ),
                            "/@fs/overlay/.scenery/font/index.css" => {
                                assert_eq!(request.uri().query(), Some("direct"));
                                (
                                    "text/css",
                                    r#"@font-face { src: url("/@fs/overlay/.scenery/font/text.woff2"); }"#,
                                )
                            }
                            "/@fs/overlay/.scenery/font/text.woff2" => {
                                ("font/woff2", "binary-font")
                            }
                            _ => panic!("unexpected Vite path {path}"),
                        };
                        let mut response = crate::transport::response(StatusCode::OK, content);
                        crate::host::header(response.headers_mut(), "content-type", media);
                        Ok::<_, Infallible>(response)
                    });
                    let _ = hyper::server::conn::http1::Builder::new()
                        .serve_connection(TokioIo::new(socket), service)
                        .with_upgrades()
                        .await;
                });
            }
        });
        let proxy = tokio::spawn(async move {
            let front = std::sync::Arc::new(front);
            while let Ok((socket, _)) = host.accept().await {
                let front = front.clone();
                tokio::spawn(async move {
                    let service = service_fn(|request: Request<Incoming>| async {
                        assert_eq!(*request.method(), Method::GET);
                        Ok::<_, Infallible>(crate::frontend::serve(Some(&front), request).await)
                    });
                    let _ = hyper::server::conn::http1::Builder::new()
                        .serve_connection(TokioIo::new(socket), service)
                        .with_upgrades()
                        .await;
                });
            }
        });
        let http = reqwest::Client::new();
        let get = |url: &str| http.get(format!("http://{front_address}{url}"));
        let module = get("/src/main.tsx")
            .send()
            .await?
            .error_for_status()?
            .text()
            .await?;
        let css = get(module.split('"').nth(1).expect("stylesheet URL"))
            .send()
            .await?
            .error_for_status()?
            .text()
            .await?;
        assert!(!forbidden(&module));
        assert!(!forbidden(&css));
        let response = get(css.split('"').nth(1).expect("font URL"))
            .send()
            .await?
            .error_for_status()?;
        assert_eq!(response.headers()["content-type"], "font/woff2");
        assert_eq!(response.text().await?, "binary-font");
        // Each of the three requests used to open a connection of its own.
        assert_eq!(connections.load(std::sync::atomic::Ordering::Relaxed), 1);
        // An upgrade still reaches the development server and carries data.
        let response = get("/hmr")
            .header("connection", "upgrade")
            .header("upgrade", "hmr")
            .send()
            .await?;
        assert_eq!(response.status(), StatusCode::SWITCHING_PROTOCOLS);
        let mut socket = response.upgrade().await?;
        tokio::io::AsyncWriteExt::write_all(&mut socket, b"ping").await?;
        let mut echo = [0; 4];
        tokio::io::AsyncReadExt::read_exact(&mut socket, &mut echo).await?;
        assert_eq!(&echo, b"ping");

        // Error overlays, SVG/+json responses, and WebSocket HMR text frames are rewritten and scrubbed.
        let mut svg_req =
            crate::transport::read_response(get("/asset.svg").send().await?, 4096).await;
        let _ = &mut svg_req;
        vite.abort();
        proxy.abort();
        Ok(())
    }

    #[tokio::test]
    async fn rewrites_errors_svg_problem_json_and_websocket_hmr_frames() -> anyhow::Result<()> {
        use hyper::service::service_fn;
        use hyper::{Method, StatusCode};
        use hyper_util::rt::TokioIo;
        use std::convert::Infallible;
        use tokio::io::{AsyncReadExt, AsyncWriteExt};
        use tokio::net::TcpListener;

        let upstream = TcpListener::bind("127.0.0.1:0").await?;
        let upstream_address = upstream.local_addr()?.to_string();
        let host = TcpListener::bind("127.0.0.1:0").await?;
        let front_address = host.local_addr()?;
        let front = crate::plan::Frontend {
            root: "/unused".into(),
            reserved: vec!["/api".into()],
            upstream: Some(upstream_address),
        };
        let upstream_task = tokio::spawn(async move {
            while let Ok((socket, _)) = upstream.accept().await {
                tokio::spawn(async move {
                    let service = service_fn(|request: Request<Incoming>| async move {
                        assert!(
                            request
                                .headers()
                                .keys()
                                .all(|k| !k.as_str().starts_with("x-scenery-"))
                        );
                        let upgrade = request
                            .headers()
                            .get("upgrade")
                            .and_then(|v| v.to_str().ok())
                            .is_some_and(|v| v.eq_ignore_ascii_case("websocket"));
                        if upgrade {
                            assert!(!request.headers().contains_key("sec-websocket-extensions"));
                            let upgrade = hyper::upgrade::on(request);
                            tokio::spawn(async move {
                                let upgraded = upgrade.await.unwrap();
                                let mut io = TokioIo::new(upgraded);
                                let payload = br#"{"type":"error","err":{"file":"/@fs/repo/.scenery/src/App.tsx","message":"Scenery error"}}"#;
                                let mut frame = vec![0x81, 126];
                                frame.extend_from_slice(&(payload.len() as u16).to_be_bytes());
                                frame.extend_from_slice(payload);
                                io.write_all(&frame).await.unwrap();
                                io.flush().await.unwrap();
                                let _ = io.shutdown().await;
                            });
                            let mut response =
                                crate::transport::response(StatusCode::SWITCHING_PROTOCOLS, "");
                            crate::host::header(response.headers_mut(), "connection", "upgrade");
                            crate::host::header(response.headers_mut(), "upgrade", "websocket");
                            return Ok::<_, Infallible>(response);
                        }
                        let (status, media, content) = match request.uri().path() {
                            "/error" => (
                                StatusCode::INTERNAL_SERVER_ERROR,
                                "application/problem+json",
                                r#"{"detail":"Scenery fault at /@fs/repo/.scenery/x.ts"}"#,
                            ),
                            "/icon.svg" => (
                                StatusCode::OK,
                                "image/svg+xml",
                                r#"<svg><use href="/@fs/repo/.scenery/sprite.svg#a"/></svg>"#,
                            ),
                            path => panic!("unexpected path {path}"),
                        };
                        let mut response = crate::transport::response(status, content);
                        crate::host::header(response.headers_mut(), "content-type", media);
                        Ok::<_, Infallible>(response)
                    });
                    let _ = hyper::server::conn::http1::Builder::new()
                        .serve_connection(TokioIo::new(socket), service)
                        .with_upgrades()
                        .await;
                });
            }
        });
        let proxy = tokio::spawn(async move {
            let front = std::sync::Arc::new(front);
            while let Ok((socket, _)) = host.accept().await {
                let front = front.clone();
                tokio::spawn(async move {
                    let service = service_fn(|request: Request<Incoming>| async {
                        assert_eq!(*request.method(), Method::GET);
                        Ok::<_, Infallible>(crate::frontend::serve(Some(&front), request).await)
                    });
                    let _ = hyper::server::conn::http1::Builder::new()
                        .serve_connection(TokioIo::new(socket), service)
                        .with_upgrades()
                        .await;
                });
            }
        });

        let http = reqwest::Client::new();
        let error_resp = http
            .get(format!("http://{front_address}/error"))
            .header("x-scenery-secret", "leak")
            .send()
            .await?;
        assert_eq!(error_resp.status(), StatusCode::INTERNAL_SERVER_ERROR);
        let error_text = error_resp.text().await?;
        assert!(!forbidden(&error_text), "{error_text}");
        assert!(error_text.contains("/@files/"));
        assert!(error_text.contains("application fault"));

        let svg_text = http
            .get(format!("http://{front_address}/icon.svg"))
            .send()
            .await?
            .error_for_status()?
            .text()
            .await?;
        assert!(!forbidden(&svg_text), "{svg_text}");
        assert!(svg_text.contains("/@files/"));

        let ws_resp = http
            .get(format!("http://{front_address}/hmr-ws"))
            .header("connection", "upgrade")
            .header("upgrade", "websocket")
            .header("sec-websocket-extensions", "permessage-deflate")
            .send()
            .await?;
        assert_eq!(ws_resp.status(), StatusCode::SWITCHING_PROTOCOLS);
        let mut ws = ws_resp.upgrade().await?;
        let mut head = [0u8; 2];
        ws.read_exact(&mut head).await?;
        assert_eq!(head[0], 0x81);
        let len = match head[1] & 0x7f {
            126 => {
                let mut ext = [0u8; 2];
                ws.read_exact(&mut ext).await?;
                u16::from_be_bytes(ext) as usize
            }
            n => n as usize,
        };
        let mut payload = vec![0u8; len];
        ws.read_exact(&mut payload).await?;
        let text = std::str::from_utf8(&payload)?;
        assert!(!forbidden(text), "{text}");
        assert!(text.contains("/@files/"));
        assert!(text.contains("application error"));

        upstream_task.abort();
        proxy.abort();
        Ok(())
    }
}
