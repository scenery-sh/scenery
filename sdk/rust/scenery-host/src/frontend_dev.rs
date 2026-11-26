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

fn scrub_error_text(source: &str) -> Cow<'_, str> {
    match rewrite(source) {
        Cow::Borrowed(text) => SCENERY_WORD.replace_all(text, "application"),
        Cow::Owned(text) => match SCENERY_WORD.replace_all(&text, "application") {
            Cow::Borrowed(_) => Cow::Owned(text),
            Cow::Owned(cleaned) => Cow::Owned(cleaned),
        },
    }
}

pub(crate) fn rewrite_hmr_text(source: &str) -> Cow<'_, str> {
    let is_error = match serde_json::from_str::<serde_json::Value>(source) {
        Ok(value) => value.get("type").and_then(|v| v.as_str()) == Some("error"),
        Err(_) => true,
    };
    if is_error {
        scrub_error_text(source)
    } else {
        rewrite(source)
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
    let media = upstream
        .headers()
        .get("content-type")
        .and_then(|value| value.to_str().ok())
        .map(|value| {
            value
                .split(';')
                .next()
                .unwrap_or("")
                .trim()
                .to_ascii_lowercase()
        });
    let textual = media.as_deref().is_some_and(|media| {
        media != "text/event-stream"
            && (media.starts_with("text/")
                || matches!(
                    media,
                    "application/javascript"
                        | "application/x-javascript"
                        | "application/json"
                        | "image/svg+xml"
                )
                || media.ends_with("+json"))
    });
    let status = upstream.status();
    let bodyless_status = status.is_informational()
        || status == hyper::StatusCode::NO_CONTENT
        || status == hyper::StatusCode::NOT_MODIFIED;
    let is_error = !bodyless_status
        && (!status.is_success() || media.as_deref() == Some("application/problem+json"));
    let (mut parts, payload) = upstream.into_parts();
    let has_scenery_bytes = |raw: &[u8]| {
        raw.windows(7)
            .any(|window| window.eq_ignore_ascii_case(b"scenery"))
            || forbidden(&String::from_utf8_lossy(raw))
    };
    let leaked_headers: Vec<_> = parts
        .headers
        .iter()
        .filter(|(name, value)| {
            has_scenery_bytes(name.as_str().as_bytes()) || has_scenery_bytes(value.as_bytes())
        })
        .map(|(name, _)| name.clone())
        .collect();
    for name in leaked_headers {
        parts.headers.remove(&name);
    }
    if bodyless_status {
        return Ok(Reply::from_parts(parts, body(Bytes::new())));
    }
    let has_content_encoding = parts.headers.contains_key("content-encoding");
    let has_content_range = parts.headers.contains_key("content-range");
    if is_error && has_content_encoding {
        parts.headers.remove("content-encoding");
        parts.headers.remove("content-length");
        parts.headers.insert(
            "content-type",
            hyper::header::HeaderValue::from_static("text/plain; charset=utf-8"),
        );
        return Ok(Reply::from_parts(
            parts,
            body(Bytes::from_static(b"upstream error")),
        ));
    }
    if !is_error && (!textual || has_content_encoding || has_content_range) {
        return Ok(Reply::from_parts(
            parts,
            payload.map_err(io::Error::other).boxed_unsync(),
        ));
    }
    let bytes = crate::transport::read(payload, TEXT_LIMIT).await?;
    let output = match std::str::from_utf8(&bytes) {
        Ok(text) => match if is_error {
            scrub_error_text(text)
        } else {
            rewrite(text)
        } {
            Cow::Owned(output) => {
                parts.headers.remove("content-length");
                Bytes::from(output)
            }
            Cow::Borrowed(_) => bytes,
        },
        Err(_) if is_error => {
            let lossy = String::from_utf8_lossy(&bytes);
            if SCENERY_WORD.is_match(&lossy) {
                parts.headers.remove("content-length");
                Bytes::from(scrub_error_text(&lossy).into_owned())
            } else {
                bytes
            }
        }
        Err(_) => bytes,
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
        let (ws_closed_tx, ws_closed_rx) = tokio::sync::oneshot::channel::<()>();
        let ws_closed_tx = std::sync::Arc::new(std::sync::Mutex::new(Some(ws_closed_tx)));
        let upstream_task = tokio::spawn(async move {
            while let Ok((socket, _)) = upstream.accept().await {
                let ws_closed_tx = ws_closed_tx.clone();
                tokio::spawn(async move {
                    let service = service_fn(move |request: Request<Incoming>| {
                        let ws_closed_tx = ws_closed_tx.clone();
                        async move {
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
                                assert!(
                                    !request.headers().contains_key("sec-websocket-extensions")
                                );
                                let upgrade = hyper::upgrade::on(request);
                                tokio::spawn(async move {
                                    let upgraded = upgrade.await.unwrap();
                                    let mut io = TokioIo::new(upgraded);
                                    // Send fragmented text frame interleaved with ping before closing.
                                    let part1 = br#"{"type":"error","err":{"file":"/@fs/repo/.sce"#;
                                    let part2 = br#"nery/src/App.tsx","message":"Scenery error"}}"#;
                                    let mut f1 = vec![0x01, part1.len() as u8];
                                    f1.extend_from_slice(part1);
                                    let ping = [0x89, 0x00];
                                    let mut f2 = vec![0x80, part2.len() as u8];
                                    f2.extend_from_slice(part2);
                                    io.write_all(&f1).await.unwrap();
                                    io.write_all(&ping).await.unwrap();
                                    io.write_all(&f2).await.unwrap();
                                    io.flush().await.unwrap();
                                    // Wait for client half-close EOF via proxy shutdown.
                                    let mut drain = [0u8; 8];
                                    while io.read(&mut drain).await.unwrap_or(0) > 0 {}
                                    if let Some(tx) = ws_closed_tx.lock().unwrap().take() {
                                        let _ = tx.send(());
                                    }
                                });
                                let mut response =
                                    crate::transport::response(StatusCode::SWITCHING_PROTOCOLS, "");
                                crate::host::header(
                                    response.headers_mut(),
                                    "connection",
                                    "upgrade",
                                );
                                crate::host::header(response.headers_mut(), "upgrade", "websocket");
                                return Ok::<_, Infallible>(response);
                            }
                            let (status, media, content) = match request.uri().path() {
                                "/error" => (
                                    StatusCode::INTERNAL_SERVER_ERROR,
                                    "application/problem+json",
                                    r#"{"detail":"Scenery fault at /@fs/repo/.scenery/x.ts"}"#,
                                ),
                                "/ok-problem" => (
                                    StatusCode::OK,
                                    "application/problem+json",
                                    r#"{"detail":"Scenery fault"}"#,
                                ),
                                "/module.js" => (
                                    StatusCode::OK,
                                    "application/javascript",
                                    r#"import Logo from "/src/components/SceneryLogo.tsx"; import "/@fs/repo/.scenery/gen/client.ts"; fetch("/api/scenery-report");"#,
                                ),
                                "/icon.svg" => (
                                    StatusCode::OK,
                                    "image/svg+xml",
                                    r#"<svg><title>Scenery</title><use href="/@fs/repo/.scenery/sprite.svg#a"/></svg>"#,
                                ),
                                "/events" => (StatusCode::OK, "text/event-stream", "data: ok\n\n"),
                                "/plain-500" => (
                                    StatusCode::INTERNAL_SERVER_ERROR,
                                    "",
                                    "Scenery internal error at /.scenery/x",
                                ),
                                "/gzip-500" => (
                                    StatusCode::BAD_GATEWAY,
                                    "text/plain",
                                    "Scenery encoded body",
                                ),
                                "/not-modified" => (StatusCode::NOT_MODIFIED, "", ""),
                                "/redirect-302" => (
                                    StatusCode::FOUND,
                                    "text/plain",
                                    "Redirecting from Scenery gateway to /.scenery/login",
                                ),
                                "/range-unsatisfiable" => (
                                    StatusCode::RANGE_NOT_SATISFIABLE,
                                    "text/plain",
                                    "Scenery range error",
                                ),
                                path => panic!("unexpected path {path}"),
                            };
                            let mut response = crate::transport::response(status, content);
                            if !media.is_empty() {
                                crate::host::header(response.headers_mut(), "content-type", media);
                            }
                            if request.uri().path() == "/plain-500" {
                                crate::host::header(
                                    response.headers_mut(),
                                    "x-scenery-debug",
                                    "leak",
                                );
                                response.headers_mut().insert(
                                    "x-debug-raw",
                                    hyper::header::HeaderValue::from_bytes(b"Scenery\xe9leak")
                                        .unwrap(),
                                );
                            }
                            if matches!(request.uri().path(), "/gzip-500" | "/not-modified") {
                                crate::host::header(
                                    response.headers_mut(),
                                    "content-encoding",
                                    "gzip",
                                );
                            }
                            if request.uri().path() == "/range-unsatisfiable" {
                                crate::host::header(
                                    response.headers_mut(),
                                    "content-range",
                                    "bytes */1024",
                                );
                            }
                            if request.uri().path() == "/redirect-302" {
                                crate::host::header(response.headers_mut(), "location", "/login");
                            }
                            Ok::<_, Infallible>(response)
                        }
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

        let ok_problem = http
            .get(format!("http://{front_address}/ok-problem"))
            .send()
            .await?
            .error_for_status()?
            .text()
            .await?;
        assert!(!forbidden(&ok_problem), "{ok_problem}");
        assert!(ok_problem.contains("application fault"));

        let plain_500 = http
            .get(format!("http://{front_address}/plain-500"))
            .send()
            .await?;
        assert_eq!(plain_500.status(), StatusCode::INTERNAL_SERVER_ERROR);
        assert!(plain_500.headers().get("x-scenery-debug").is_none());
        assert!(plain_500.headers().get("x-debug-raw").is_none());
        let plain_500_text = plain_500.text().await?;
        assert!(!forbidden(&plain_500_text), "{plain_500_text}");
        assert!(
            plain_500_text.contains("application internal error"),
            "{plain_500_text}"
        );

        let gzip_500 = http
            .get(format!("http://{front_address}/gzip-500"))
            .send()
            .await?;
        assert_eq!(gzip_500.status(), StatusCode::BAD_GATEWAY);
        assert!(gzip_500.headers().get("content-encoding").is_none());
        let gzip_500_text = gzip_500.text().await?;
        assert!(!forbidden(&gzip_500_text), "{gzip_500_text}");
        assert_eq!(gzip_500_text, "upstream error");

        let not_mod = http
            .get(format!("http://{front_address}/not-modified"))
            .send()
            .await?;
        assert_eq!(not_mod.status(), StatusCode::NOT_MODIFIED);
        assert_eq!(not_mod.headers()["content-encoding"], "gzip");
        assert!(not_mod.bytes().await?.is_empty());

        let no_redirect = reqwest::Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .build()?;
        let redirect_302 = no_redirect
            .get(format!("http://{front_address}/redirect-302"))
            .send()
            .await?;
        assert_eq!(redirect_302.status(), StatusCode::FOUND);
        assert_eq!(redirect_302.headers()["location"], "/login");
        let redirect_text = redirect_302.text().await?;
        assert!(!forbidden(&redirect_text), "{redirect_text}");
        assert!(
            redirect_text.contains("Redirecting from application gateway"),
            "{redirect_text}"
        );

        let range_416 = http
            .get(format!("http://{front_address}/range-unsatisfiable"))
            .send()
            .await?;
        assert_eq!(range_416.status(), StatusCode::RANGE_NOT_SATISFIABLE);
        assert_eq!(range_416.headers()["content-range"], "bytes */1024");
        let range_text = range_416.text().await?;
        assert!(!forbidden(&range_text), "{range_text}");
        assert!(
            range_text.contains("application range error"),
            "{range_text}"
        );

        let svg_text = http
            .get(format!("http://{front_address}/icon.svg"))
            .send()
            .await?
            .error_for_status()?
            .text()
            .await?;
        assert!(
            !svg_text.contains("/@fs/") && !svg_text.contains(".scenery"),
            "{svg_text}"
        );
        assert!(svg_text.contains("/@files/"));
        assert!(svg_text.contains("<title>Scenery</title>"));

        let module_text = http
            .get(format!("http://{front_address}/module.js"))
            .send()
            .await?
            .error_for_status()?
            .text()
            .await?;
        assert!(
            !module_text.contains("/@fs/") && !module_text.contains(".scenery"),
            "{module_text}"
        );
        assert!(module_text.contains("/src/components/SceneryLogo.tsx"));
        assert!(module_text.contains("/api/scenery-report"));
        assert!(module_text.contains("/@files/"));

        assert_eq!(
            rewrite_hmr_text(
                r#"{"type":"update","path":"/src/SceneryLogo.tsx","dep":"/@fs/repo/.scenery/x.ts"}"#
            ),
            r#"{"type":"update","path":"/src/SceneryLogo.tsx","dep":"/@files/repo/~LnNjZW5lcnk/x.ts"}"#
        );

        let sse_resp = http
            .get(format!("http://{front_address}/events"))
            .send()
            .await?
            .error_for_status()?;
        assert_eq!(sse_resp.text().await?, "data: ok\n\n");

        let ws_resp = http
            .get(format!("http://{front_address}/hmr-ws"))
            .header("connection", "upgrade")
            .header("upgrade", "websocket")
            .header("sec-websocket-extensions", "permessage-deflate")
            .send()
            .await?;
        assert_eq!(ws_resp.status(), StatusCode::SWITCHING_PROTOCOLS);
        let mut ws = ws_resp.upgrade().await?;
        // First expect interleaved ping control frame.
        let mut ping = [0u8; 2];
        ws.read_exact(&mut ping).await?;
        assert_eq!(ping, [0x89, 0x00]);
        // Then expect reassembled and scrubbed 0x81 text frame.
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

        drop(ws);
        tokio::time::timeout(Duration::from_secs(1), ws_closed_rx).await??;

        upstream_task.abort();
        proxy.abort();
        Ok(())
    }
}
