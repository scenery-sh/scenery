use std::collections::HashMap;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::{LazyLock, Mutex};
use std::time::{Duration, SystemTime};

use http_body_util::{BodyExt, StreamBody};
use hyper::body::{Frame, Incoming};
use hyper::{Method, Request, StatusCode};
use hyper_util::rt::TokioIo;
use percent_encoding::percent_decode_str;
use regex::Regex;
use serde_json::json;
use sha2::{Digest, Sha256};
use tokio::io::{AsyncReadExt, AsyncSeekExt, copy_bidirectional};
use tokio_util::io::ReaderStream;

use crate::host::{Reply, empty, header, json_reply};
use crate::plan::Frontend;

static HASHED_ASSET: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"-[a-zA-Z0-9_-]{8,}\.[a-zA-Z0-9]+$").expect("valid asset pattern")
});

/// The ETag of a file with the size and modification time it was computed
/// for.
struct Tagged {
    size: u64,
    modified: SystemTime,
    tag: String,
}

/// The ETags of the served files by path. Hashing a file on every request
/// made a revalidation or a range read cost as much as the whole transfer.
static ETAGS: LazyLock<Mutex<HashMap<PathBuf, Tagged>>> = LazyLock::new(Mutex::default);

/// A file is remembered only once it has been quiet for this long: a rewrite
/// within the resolution of its timestamp would otherwise keep its stamp.
const SETTLED: Duration = Duration::from_secs(2);

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
    let Some(mut path) = path else {
        return not_found();
    };
    let brotli = asset(&frontend.root, &format!("{selected}.br")).await;
    let gzip = asset(&frontend.root, &format!("{selected}.gz")).await;
    let vary = brotli.is_some() || gzip.is_some();
    let accepted = request
        .headers()
        .get("accept-encoding")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("");
    let encoding = select_encoding(accepted, brotli.is_some(), gzip.is_some());
    if let Some(variant) = match encoding {
        "br" => brotli,
        "gzip" => gzip,
        _ => None,
    } {
        path = variant;
    }
    let Ok(mut file) = tokio::fs::File::open(&path).await else {
        return not_found();
    };
    let Ok(info) = file.metadata().await else {
        return not_found();
    };
    let Ok(tag) = etag(&path, &mut file, &info).await else {
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
    header(reply.headers_mut(), "etag", &tag);
    if vary {
        header(reply.headers_mut(), "vary", "Accept-Encoding");
    }
    if request
        .headers()
        .get_all("if-none-match")
        .iter()
        .filter_map(|v| v.to_str().ok())
        .any(|v| {
            v.split(',')
                .map(str::trim)
                .any(|value| value == "*" || value.strip_prefix("W/").unwrap_or(value) == tag)
        })
    {
        *reply.status_mut() = StatusCode::NOT_MODIFIED;
        return reply;
    }
    if !encoding.is_empty() {
        header(reply.headers_mut(), "content-encoding", encoding);
    }
    header(reply.headers_mut(), "content-type", media.essence_str());
    header(reply.headers_mut(), "accept-ranges", "bytes");
    let size = info.len();
    let mut offset = 0;
    let mut length = size;
    if let Some(range) = request.headers().get("range").and_then(|v| v.to_str().ok())
        && request
            .headers()
            .get("if-range")
            .is_none_or(|value| value == tag.as_str())
    {
        let Some((start, end)) = byte_range(range, size) else {
            *reply.status_mut() = StatusCode::RANGE_NOT_SATISFIABLE;
            reply.headers_mut().remove("content-encoding");
            header(
                reply.headers_mut(),
                "content-range",
                &format!("bytes */{size}"),
            );
            return reply;
        };
        *reply.status_mut() = StatusCode::PARTIAL_CONTENT;
        offset = start;
        length = end - start + 1;
        header(
            reply.headers_mut(),
            "content-range",
            &format!("bytes {start}-{end}/{size}"),
        );
    }
    header(reply.headers_mut(), "content-length", &length.to_string());
    if request.method() == Method::GET {
        if file.seek(io::SeekFrom::Start(offset)).await.is_err() {
            return not_found();
        }
        let stream = futures_util::StreamExt::map(ReaderStream::new(file.take(length)), |chunk| {
            chunk.map(Frame::data)
        });
        *reply.body_mut() = StreamBody::new(stream).boxed_unsync();
    }
    reply
}

/// The ETag of the open `file` at `path`: the one remembered for its size
/// and modification time, or else a hash of its content.
async fn etag(
    path: &Path,
    file: &mut tokio::fs::File,
    info: &std::fs::Metadata,
) -> io::Result<String> {
    let stamp = info.modified().ok().map(|modified| (info.len(), modified));
    if let Some(stamp) = stamp
        && let Some(known) = ETAGS.lock().expect("etags").get(path)
        && (known.size, known.modified) == stamp
    {
        return Ok(known.tag.clone());
    }
    let mut hash = Sha256::new();
    let mut buffer = vec![0; 32 << 10];
    loop {
        let count = file.read(&mut buffer).await?;
        if count == 0 {
            break;
        }
        hash.update(&buffer[..count]);
    }
    let tag = format!("\"{}\"", crate::process::hex(&hash.finalize()));
    if let Some((size, modified)) = stamp
        && modified.elapsed().is_ok_and(|quiet| quiet >= SETTLED)
    {
        let known = Tagged {
            size,
            modified,
            tag: tag.clone(),
        };
        ETAGS.lock().expect("etags").insert(path.into(), known);
    }
    Ok(tag)
}

fn select_encoding(accept: &str, brotli: bool, gzip: bool) -> &'static str {
    let mut quality = std::collections::HashMap::new();
    for item in accept.split(',') {
        let mut parts = item.split(';').map(str::trim);
        let encoding = parts.next().unwrap_or_default().to_ascii_lowercase();
        let q = parts
            .find_map(|parameter| parameter.strip_prefix("q="))
            .map_or(1.0, |q| q.parse::<f64>().unwrap_or_default());
        quality.insert(encoding, q);
    }
    let q = |encoding| {
        *quality
            .get(encoding)
            .or_else(|| quality.get("*"))
            .unwrap_or(&0.0)
    };
    let br = if brotli { q("br") } else { 0.0 };
    let gz = if gzip { q("gzip") } else { 0.0 };
    if br > 0.0 && br >= gz {
        "br"
    } else if gz > 0.0 {
        "gzip"
    } else {
        ""
    }
}

fn byte_range(value: &str, length: u64) -> Option<(u64, u64)> {
    let (start, end) = value.strip_prefix("bytes=")?.trim().split_once('-')?;
    let last = length.checked_sub(1)?;
    if start.is_empty() {
        let suffix = end.parse::<u64>().ok()?;
        return (suffix > 0).then(|| (length.saturating_sub(suffix), last));
    }
    let start = start.parse().ok()?;
    let end = if end.is_empty() {
        last
    } else {
        last.min(end.parse().ok()?)
    };
    (start <= end).then_some((start, end))
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
    let Some(uri) = crate::frontend_dev::upstream_uri(upstream, request.uri()) else {
        return Ok(not_found());
    };
    crate::frontend_dev::prepare(&mut request, uri);
    let upgrade = request
        .headers()
        .contains_key("upgrade")
        .then(|| hyper::upgrade::on(&mut request));
    if upgrade.is_none() {
        strip_hop_headers(request.headers_mut());
    }
    let original_host = request.headers().get("host").cloned();
    let peer = request.extensions().get::<std::net::IpAddr>().copied();
    let headers = request.headers_mut();
    headers.remove("forwarded");
    headers.remove("x-forwarded-for");
    headers.remove("x-forwarded-host");
    headers.remove("x-forwarded-proto");
    if let Some(original_host) = original_host {
        headers.insert("x-forwarded-host", original_host);
    }
    if let Some(peer) = peer {
        header(headers, "x-forwarded-for", &peer.to_string());
    }
    header(headers, "x-forwarded-proto", "http");
    let mut response = crate::frontend_dev::send(request).await?;
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
    crate::frontend_dev::response(response).await
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

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn remembers_an_etag_for_the_size_and_time_of_its_file() -> io::Result<()> {
        let directory = tempfile::tempdir()?;
        let path = directory.path().join("app.js");
        let write = |content: &str, modified: SystemTime| {
            std::fs::write(&path, content)?;
            let file = std::fs::File::options().write(true).open(&path)?;
            file.set_modified(modified)
        };
        let tag = || async {
            let mut file = tokio::fs::File::open(&path).await?;
            let info = file.metadata().await?;
            etag(&path, &mut file, &info).await
        };
        let earlier = SystemTime::now() - Duration::from_secs(3600);
        write("first", earlier)?;
        let first = tag().await?;
        // The same size and time: the file is not read again.
        write("again", earlier)?;
        assert_eq!(tag().await?, first);
        // A new time or a new size is a new file.
        write("again", earlier + Duration::from_secs(1))?;
        let second = tag().await?;
        assert_ne!(second, first);
        write("longer content", earlier + Duration::from_secs(1))?;
        assert_ne!(tag().await?, second);
        // A file written moments ago may change again within its timestamp,
        // so it is hashed every time.
        let now = SystemTime::now();
        write("fresh", now)?;
        let fresh = tag().await?;
        write("other", now)?;
        assert_ne!(tag().await?, fresh);
        Ok(())
    }

    #[test]
    fn selects_an_available_encoding_and_validates_byte_ranges() {
        assert_eq!(select_encoding("gzip;q=0.8, br;q=0.6", true, true), "gzip");
        assert_eq!(select_encoding("*;q=0.5, gzip;q=0", true, true), "br");
        assert_eq!(select_encoding("br;q=0,gzip;q=0", true, true), "");
        assert_eq!(byte_range("bytes=1-7", 5), Some((1, 4)));
        assert_eq!(byte_range("bytes=-3", 5), Some((2, 4)));
        assert_eq!(byte_range("bytes=2-", 5), Some((2, 4)));
        assert_eq!(byte_range("bytes=5-", 5), None);
        assert_eq!(byte_range("bytes=-0", 5), None);
    }
}
