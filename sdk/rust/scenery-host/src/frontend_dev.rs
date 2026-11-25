use std::borrow::Cow;
use std::io;
use std::sync::LazyLock;

use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use bytes::Bytes;
use hyper::{Request, Uri, body::Incoming};
use percent_encoding::percent_decode_str;
use regex::{Captures, Regex};

use crate::host::{Reply, body};

const VITE_FS: &str = "/@fs/";
const ASSET_PATH: &str = "/@files/";
const ESCAPE: &str = "~";
const TEXT_LIMIT: usize = 32 << 20;

static ASSET: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r#"/@fs/[^"'\x60<>\s\\)?,;#]+"#).expect("Vite filesystem URL"));

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

pub(crate) fn upstream_uri(uri: &Uri) -> Option<Uri> {
    let Some(path) = uri.path().strip_prefix(ASSET_PATH) else {
        return Some(uri.clone());
    };
    let mut upstream = VITE_FS.to_owned();
    for (index, segment) in path.split('/').enumerate() {
        if index != 0 {
            upstream.push('/');
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
        upstream.push_str(&decoded);
    }
    if let Some(query) = uri.query() {
        upstream.push('?');
        upstream.push_str(query);
    }
    upstream.parse().ok()
}

pub(crate) fn prepare(request: &mut Request<Incoming>, uri: Uri) {
    *request.uri_mut() = uri;
    request.headers_mut().remove("accept-encoding");
}

pub(crate) async fn response(upstream: hyper::Response<Incoming>) -> anyhow::Result<Reply> {
    use http_body_util::BodyExt;
    let textual = upstream
        .headers()
        .get("content-type")
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| {
            matches!(
                value.split(';').next().unwrap_or(""),
                "text/html"
                    | "text/css"
                    | "text/javascript"
                    | "application/javascript"
                    | "application/json"
            )
        });
    let (mut parts, payload) = upstream.into_parts();
    if !textual
        || !parts.status.is_success()
        || parts.headers.contains_key("content-encoding")
        || parts.headers.contains_key("content-range")
    {
        return Ok(Reply::from_parts(
            parts,
            payload.map_err(io::Error::other).boxed_unsync(),
        ));
    }
    let bytes = crate::transport::read(payload, TEXT_LIMIT).await?;
    let output = match std::str::from_utf8(&bytes).map(rewrite) {
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
                upstream_uri(&got.parse().unwrap())
                    .unwrap()
                    .path_and_query()
                    .unwrap()
                    .as_str(),
                *expected
            );
        }
        assert!(upstream_uri(&"/@files/~/..%2fsecret".parse().unwrap()).is_none());
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
        let vite = tokio::spawn(async move {
            while let Ok((socket, _)) = vite.accept().await {
                tokio::spawn(async move {
                    let service = service_fn(|request: Request<Incoming>| async move {
                        assert_eq!(request.headers()["host"], front_address.to_string());
                        assert_eq!(
                            request.headers()["x-forwarded-host"],
                            front_address.to_string()
                        );
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
                        .await;
                });
            }
        });
        let http = reqwest::Client::new();
        let get = |url: &str| http.get(format!("http://{front_address}{url}")).send();
        let module = get("/src/main.tsx")
            .await?
            .error_for_status()?
            .text()
            .await?;
        let css = get(module.split('"').nth(1).expect("stylesheet URL"))
            .await?
            .error_for_status()?
            .text()
            .await?;
        assert!(!forbidden(&module));
        assert!(!forbidden(&css));
        let response = get(css.split('"').nth(1).expect("font URL"))
            .await?
            .error_for_status()?;
        assert_eq!(response.headers()["content-type"], "font/woff2");
        assert_eq!(response.text().await?, "binary-font");
        vite.abort();
        proxy.abort();
        Ok(())
    }
}
