use std::collections::BTreeMap;
use std::ffi::OsString;
use std::sync::{Arc, Mutex};

use bytes::Bytes;
use http_body_util::{BodyExt, Full};
use hyper::Response;
use hyper::service::service_fn;
use hyper_util::rt::TokioIo;
use serde_json::{Value, json};
use time::macros::datetime;

use super::*;

const ACCESS_KEY: &str = "AKIAIOSFODNN7EXAMPLE";
const SECRET: &str = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY";

fn headers(pairs: &[(&str, &str)]) -> Vec<(String, String)> {
    pairs
        .iter()
        .map(|(name, value)| ((*name).to_owned(), (*value).to_owned()))
        .collect()
}

/// The examples of the Amazon S3 SigV4 documentation ("Signature
/// Calculations for the Authorization Header: Transferring Payload in a
/// Single Chunk").
#[test]
fn signatures_match_the_published_aws_examples() {
    let sign =
        |method: &str, path: &str, query: &str, headers: &[(String, String)], payload: &str| {
            let canonical = canonical_request(method, path, query, headers, payload);
            let scope = "20130524/us-east-1/s3/aws4_request";
            (
                hex(&Sha256::digest(canonical.as_bytes())),
                signature(
                    SECRET,
                    "20130524",
                    "us-east-1",
                    &string_to_sign("20130524T000000Z", scope, &canonical),
                ),
            )
        };
    let (hash, get) = sign(
        "GET",
        "/test.txt",
        "",
        &headers(&[
            ("host", "examplebucket.s3.amazonaws.com"),
            ("range", "bytes=0-9"),
            ("x-amz-content-sha256", EMPTY_PAYLOAD),
            ("x-amz-date", "20130524T000000Z"),
        ]),
        EMPTY_PAYLOAD,
    );
    assert_eq!(
        hash,
        "7344ae5b7ee6c3e7e6b0fe0640412a37625d1fbfff95c48bbb2dc43964946972"
    );
    assert_eq!(
        get,
        "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
    );
    let payload = hex(&Sha256::digest(b"Welcome to Amazon S3."));
    let (_, put) = sign(
        "PUT",
        &format!("/{}", encode("test$file.text")),
        "",
        &headers(&[
            ("date", "Fri, 24 May 2013 00:00:00 GMT"),
            ("host", "examplebucket.s3.amazonaws.com"),
            ("x-amz-content-sha256", &payload),
            ("x-amz-date", "20130524T000000Z"),
            ("x-amz-storage-class", "REDUCED_REDUNDANCY"),
        ]),
        &payload,
    );
    assert_eq!(
        put,
        "98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd"
    );
    let (_, list) = sign(
        "GET",
        "/",
        &canonical_query(&[("prefix", "J"), ("max-keys", "2")]),
        &headers(&[
            ("host", "examplebucket.s3.amazonaws.com"),
            ("x-amz-content-sha256", EMPTY_PAYLOAD),
            ("x-amz-date", "20130524T000000Z"),
        ]),
        EMPTY_PAYLOAD,
    );
    assert_eq!(
        list,
        "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"
    );
}

#[test]
fn signed_requests_carry_their_scope_and_encode_keys_strictly() {
    let bucket = Bucket::from_config(
        &json!({"region": "us-east-1", "bucket": "examplebucket",
            "access_key_id": ACCESS_KEY, "secret_access_key": SECRET, "session_token": "token"}),
        &|_: &str| None,
    )
    .unwrap();
    assert_eq!(
        bucket.url(Some("a b/c+d=ü"), &[]).as_str(),
        "https://examplebucket.s3.us-east-1.amazonaws.com/a%20b/c%2Bd%3D%C3%BC"
    );
    let url = bucket.url(None, &[("prefix", "x/y z"), ("list-type", "2")]);
    assert_eq!(url.query(), Some("list-type=2&prefix=x%2Fy%20z"));
    let mut headers = HeaderMap::new();
    sign(
        &Method::GET,
        &url,
        &mut headers,
        EMPTY_PAYLOAD,
        &bucket.credentials,
        &bucket.region,
        datetime!(2026-11-26 10:00:00 UTC),
    );
    let authorization = headers["authorization"].to_str().unwrap();
    assert!(authorization.starts_with(
        "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20261126/us-east-1/s3/aws4_request, \
         SignedHeaders=host;x-amz-content-sha256;x-amz-date;x-amz-security-token, Signature="
    ));
    assert!(headers["authorization"].is_sensitive());
    assert_eq!(headers["x-amz-security-token"], "token");
    assert_eq!(headers["host"], "examplebucket.s3.us-east-1.amazonaws.com");
}

#[test]
fn bucket_configuration_has_safe_defaults_and_fails_fast() {
    let environment = |name: &str| -> Option<OsString> {
        match name {
            "AWS_ACCESS_KEY_ID" => Some("env-key".into()),
            "AWS_SECRET_ACCESS_KEY" => Some("env-secret".into()),
            _ => None,
        }
    };
    let minio = Bucket::from_config(
        &json!({"endpoint": "http://127.0.0.1:9000", "region": "us-east-1",
            "bucket": "media", "prefix": "app"}),
        &environment,
    )
    .unwrap();
    assert!(minio.path_style, "a custom endpoint defaults to path style");
    assert_eq!(minio.prefix, "app/");
    assert_eq!(minio.credentials.access_key_id, "env-key");
    assert_eq!(minio.credentials.secret_access_key, "env-secret");
    assert_eq!(
        minio.url(Some("k"), &[]).as_str(),
        "http://127.0.0.1:9000/media/k"
    );
    let virtual_host = Bucket::from_config(
        &json!({"endpoint": "https://account.r2.cloudflarestorage.com", "region": "auto",
            "bucket": "media", "path_style": false}),
        &environment,
    )
    .unwrap();
    assert_eq!(
        virtual_host.url(None, &[]).as_str(),
        "https://media.account.r2.cloudflarestorage.com/"
    );
    for (store, expected) in [
        (json!({"bucket": "b"}), "region is required"),
        (json!({"region": "r"}), "bucket is required"),
        (json!({"region": "r", "bucket": "a/b"}), "valid bucket name"),
        (
            json!({"region": "r", "bucket": "b", "endpoint": "https://u:p@host"}),
            "origin",
        ),
        (
            json!({"region": "r", "bucket": "b", "endpoint": "https://host/base"}),
            "origin",
        ),
        (
            json!({"region": "r", "bucket": "b", "prefix": "../up"}),
            "valid key prefix",
        ),
        (
            json!({"region": "r", "bucket": "b", "path_style": "yes"}),
            "boolean",
        ),
        (
            json!({"region": "r", "bucket": "b", "access_key_id": "k"}),
            "secret_access_key is required",
        ),
    ] {
        let error = Bucket::from_config(&store, &environment).err().unwrap();
        assert!(error.to_string().contains(expected), "{store}: {error}");
    }
    let error = Bucket::from_config(&json!({"region": "r", "bucket": "b"}), &|_: &str| None)
        .err()
        .unwrap();
    assert!(error.to_string().contains("AWS_ACCESS_KEY_ID"), "{error}");
}

#[test]
fn xml_entities_and_list_elements_decode() {
    assert_eq!(
        unescape("&quot;a&amp;b&quot; &#60;&#x3E; &bogus;"),
        "\"a&b\" <> &bogus;"
    );
    let document = "<R><Contents><Key>a</Key></Contents><Contents><Key>b&amp;c</Key></Contents>\
        <IsTruncated>false</IsTruncated></R>";
    let keys: Vec<_> = elements(document, "Contents")
        .filter_map(|entry| element(entry, "Key").map(unescape))
        .collect();
    assert_eq!(keys, ["a", "b&c"]);
    assert_eq!(element(document, "IsTruncated"), Some("false"));
}

struct Stored {
    data: Bytes,
    etag: String,
    headers: HeaderMap,
}

#[derive(Default)]
struct Mock {
    objects: BTreeMap<String, Stored>,
    writes: Vec<HeaderMap>,
    version: u64,
}

type Shared = Arc<Mutex<Mock>>;

/// A path-style S3 endpoint for bucket `media` that verifies every SigV4
/// signature with the secret it shares with the test.
async fn mock_s3() -> (String, Shared) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let endpoint = format!("http://{}", listener.local_addr().unwrap());
    let state = Shared::default();
    let shared = state.clone();
    tokio::spawn(async move {
        while let Ok((socket, _)) = listener.accept().await {
            let state = shared.clone();
            tokio::spawn(async move {
                let service = service_fn(move |request: Request<hyper::body::Incoming>| {
                    let state = state.clone();
                    async move {
                        let (parts, body) = request.into_parts();
                        let body = body.collect().await?.to_bytes();
                        Ok::<_, hyper::Error>(answer(&state, parts, body))
                    }
                });
                let _ = hyper::server::conn::http1::Builder::new()
                    .serve_connection(TokioIo::new(socket), service)
                    .await;
            });
        }
    });
    (endpoint, state)
}

fn reply(status: u16, headers: &[(&str, String)], body: impl Into<Bytes>) -> Response<Full<Bytes>> {
    let mut response = Response::new(Full::new(body.into()));
    *response.status_mut() = StatusCode::from_u16(status).unwrap();
    for (name, value) in headers {
        response.headers_mut().insert(
            HeaderName::from_bytes(name.as_bytes()).unwrap(),
            HeaderValue::from_str(value).unwrap(),
        );
    }
    response
}

fn error(status: u16, code: &str) -> Response<Full<Bytes>> {
    reply(status, &[], format!("<Error><Code>{code}</Code></Error>"))
}

fn verified(parts: &hyper::http::request::Parts) -> bool {
    let Some(authorization) = parts
        .headers
        .get("authorization")
        .and_then(|v| v.to_str().ok())
    else {
        return false;
    };
    let field = |name: &str| {
        authorization
            .split(", ")
            .find_map(|part| part.split_once(&format!("{name}=")).map(|(_, value)| value))
            .unwrap_or_default()
    };
    let date = parts.headers["x-amz-date"].to_str().unwrap();
    let signed: Vec<_> = field("SignedHeaders")
        .split(';')
        .map(|name| {
            let value = parts.headers[name].to_str().unwrap().to_owned();
            (name.to_owned(), value)
        })
        .collect();
    let payload = parts.headers["x-amz-content-sha256"].to_str().unwrap();
    let canonical = canonical_request(
        parts.method.as_str(),
        parts.uri.path(),
        parts.uri.query().unwrap_or_default(),
        &signed,
        payload,
    );
    let scope = format!("{}/us-east-1/s3/aws4_request", &date[..8]);
    let expected = signature(
        SECRET,
        &date[..8],
        "us-east-1",
        &string_to_sign(date, &scope, &canonical),
    );
    field("Credential").starts_with(ACCESS_KEY) && field("Signature") == expected
}

fn answer(
    state: &Shared,
    parts: hyper::http::request::Parts,
    body: Bytes,
) -> Response<Full<Bytes>> {
    if !verified(&parts) {
        return error(403, "SignatureDoesNotMatch");
    }
    let mut state = state.lock().unwrap();
    let path = parts.uri.path();
    let Some(object) = path.strip_prefix("/media/").map(decode) else {
        if path != "/media" {
            return error(404, "NoSuchBucket");
        }
        return list(&state, parts.uri.query().unwrap_or_default());
    };
    let current = state.objects.get(&object);
    let if_match = parts
        .headers
        .get("if-match")
        .map(|value| value.to_str().unwrap().to_owned());
    if if_match.is_some_and(|etag| current.is_none_or(|stored| stored.etag != etag)) {
        return error(412, "PreconditionFailed");
    }
    match parts.method {
        Method::PUT => {
            if parts
                .headers
                .get("if-none-match")
                .is_some_and(|value| value == "*")
                && current.is_some()
            {
                return error(412, "PreconditionFailed");
            }
            let digest = hex(&Sha256::digest(&body));
            if parts.headers["x-amz-content-sha256"] != digest.as_str()
                || parts.headers["content-length"] != body.len().to_string().as_str()
                || parts.headers.contains_key("transfer-encoding")
            {
                return error(400, "BadDigest");
            }
            state.version += 1;
            let etag = format!("\"{}{}\"", &digest[..24], state.version);
            let mut stored = HeaderMap::new();
            for (name, value) in &parts.headers {
                if name.as_str().starts_with("x-amz-meta-") || name == "content-type" {
                    stored.insert(name.clone(), value.clone());
                }
            }
            state.writes.push(parts.headers.clone());
            state.objects.insert(
                object,
                Stored {
                    data: body,
                    etag: etag.clone(),
                    headers: stored,
                },
            );
            reply(200, &[("etag", etag)], "")
        }
        Method::DELETE => {
            if let Some(expected) = parts
                .headers
                .get("if-match")
                .and_then(|value| value.to_str().ok())
                && current.is_none_or(|stored| stored.etag != expected)
            {
                return error(412, "PreconditionFailed");
            }
            state.objects.remove(&object);
            reply(204, &[], "")
        }
        Method::GET | Method::HEAD => {
            let Some(stored) = current else {
                return error(404, "NoSuchKey");
            };
            let mut response_headers: Vec<(&str, String)> = vec![
                ("etag", stored.etag.clone()),
                ("last-modified", "Wed, 25 Nov 2026 10:00:00 GMT".into()),
            ];
            for (name, value) in &stored.headers {
                response_headers.push((name.as_str(), value.to_str().unwrap().to_owned()));
            }
            let (status, data) = match parts.headers.get("range").map(|v| v.to_str().unwrap()) {
                Some(range) => {
                    let (start, end) = range.trim_start_matches("bytes=").split_once('-').unwrap();
                    let (start, end): (usize, usize) =
                        (start.parse().unwrap(), end.parse().unwrap());
                    (206, stored.data.slice(start..=end))
                }
                None => (200, stored.data.clone()),
            };
            if parts.method == Method::HEAD {
                response_headers.push(("content-length", data.len().to_string()));
                return reply(status, &response_headers, "");
            }
            reply(status, &response_headers, data)
        }
        _ => error(405, "MethodNotAllowed"),
    }
}

fn escape(text: &str) -> String {
    text.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
}

fn list(state: &Mock, query: &str) -> Response<Full<Bytes>> {
    let query: BTreeMap<String, String> = url::form_urlencoded::parse(query.as_bytes())
        .map(|(key, value)| (key.into_owned(), value.into_owned()))
        .collect();
    assert_eq!(query["list-type"], "2");
    let prefix = query.get("prefix").cloned().unwrap_or_default();
    let delimiter = query.get("delimiter").cloned().unwrap_or_default();
    let limit: usize = query["max-keys"].parse().unwrap();
    let after = query.get("continuation-token").cloned().unwrap_or_default();
    let mut entries = BTreeMap::new();
    for (key, stored) in &state.objects {
        let Some(rest) = key.strip_prefix(&prefix) else {
            continue;
        };
        match rest.find(&delimiter).filter(|_| !delimiter.is_empty()) {
            Some(index) => entries.insert(format!("{prefix}{}", &rest[..=index]), None),
            None => entries.insert(key.clone(), Some(stored)),
        };
    }
    let remaining: Vec<_> = entries
        .into_iter()
        .filter(|(name, _)| *name > after)
        .collect();
    let page = &remaining[..remaining.len().min(limit)];
    let mut xml = String::from("<ListBucketResult>");
    for (name, stored) in page {
        match stored {
            Some(stored) => xml.push_str(&format!(
                "<Contents><Key>{}</Key><LastModified>2026-11-25T10:00:00.000Z</LastModified>\
                 <ETag>{}</ETag><Size>{}</Size></Contents>",
                escape(name),
                escape(&stored.etag),
                stored.data.len()
            )),
            None => xml.push_str(&format!(
                "<CommonPrefixes><Prefix>{}</Prefix></CommonPrefixes>",
                escape(name)
            )),
        }
    }
    let truncated = remaining.len() > page.len();
    xml.push_str(&format!("<IsTruncated>{truncated}</IsTruncated>"));
    if truncated {
        xml.push_str(&format!(
            "<NextContinuationToken>{}</NextContinuationToken>",
            escape(&page.last().unwrap().0)
        ));
    }
    xml.push_str("</ListBucketResult>");
    reply(200, &[("content-type", "application/xml".into())], xml)
}

struct Store {
    config: Value,
    store: Value,
}

impl Store {
    fn new(endpoint: &str, extra: Value) -> Self {
        let mut store = json!({"kind": "s3", "endpoint": endpoint, "region": "us-east-1",
            "bucket": "media", "prefix": "app", "tenant_scoped": true,
            "access_key_id": ACCESS_KEY, "secret_access_key": SECRET});
        for (key, value) in extra.as_object().unwrap() {
            store[key] = value.clone();
        }
        Self {
            config: json!({}),
            store,
        }
    }

    async fn call(
        &self,
        method: &str,
        tenant: &str,
        query: &[(&str, &str)],
        headers: &[(&str, &str)],
        body: &'static [u8],
    ) -> Result<(StatusCode, HeaderMap, Bytes), StatusCode> {
        let query: BTreeMap<String, Vec<String>> = query
            .iter()
            .map(|(key, value)| ((*key).to_owned(), vec![(*value).to_owned()]))
            .collect();
        let mut request = Request::builder()
            .method(method)
            .uri("/host/v1/storage/object");
        for (name, value) in headers {
            request = request.header(*name, *value);
        }
        let request = request.body(Full::new(Bytes::from_static(body))).unwrap();
        let recursive = query
            .get("recursive")
            .is_some_and(|values| matches!(values.first().map(String::as_str), Some("1" | "true")));
        let selection = Selection {
            config: &self.config,
            store: &self.store,
            name: "files",
            tenant,
            key: query.get("key").map(|values| values[0].as_str()),
            recursive,
            query: &query,
        };
        match serve(request, selection).await {
            Ok(reply) => {
                let (parts, body) = reply.into_parts();
                Ok((
                    parts.status,
                    parts.headers,
                    body.collect().await.unwrap().to_bytes(),
                ))
            }
            Err(fault) => Err(fault.status),
        }
    }
}

fn object(headers: &HeaderMap) -> Value {
    let encoded = headers["x-scenery-storage-object"].to_str().unwrap();
    values::parse(&URL_SAFE_NO_PAD.decode(encoded).unwrap()).unwrap()
}

#[tokio::test]
async fn objects_round_trip_with_metadata_preconditions_and_tenant_isolation() {
    let (endpoint, state) = mock_s3().await;
    let store = Store::new(&endpoint, json!({}));
    let metadata = values::base64url(br#"{"Author":"Ana"}"#);
    let key = [("key", "docs/a.txt")];
    let (status, _, body) = store
        .call(
            "PUT",
            "acme",
            &key,
            &[
                ("content-type", "text/plain"),
                ("x-storage-metadata", &metadata),
            ],
            b"hello world",
        )
        .await
        .unwrap();
    assert_eq!(status, StatusCode::CREATED);
    let written = values::parse(&body).unwrap();
    assert_eq!(written["tenant"], "acme");
    assert_eq!(written["size_bytes"], 11);
    assert_eq!(written["metadata"]["Author"], "Ana");
    assert_eq!(written["sha256"], hex(&Sha256::digest(b"hello world")));
    let etag = written["etag"].as_str().unwrap().to_owned();
    assert!(
        state
            .lock()
            .unwrap()
            .objects
            .contains_key("app/files/tenants/YWNtZQ/docs/a.txt"),
        "objects live beneath the store and tenant"
    );

    let (status, headers, body) = store.call("GET", "acme", &key, &[], b"").await.unwrap();
    assert_eq!((status, &body[..]), (StatusCode::OK, &b"hello world"[..]));
    let read = object(&headers);
    assert_eq!(
        (read["etag"].as_str(), read["metadata"]["Author"].as_str()),
        (Some(etag.as_str()), Some("Ana"))
    );
    assert_eq!(read["content_type"], "text/plain");
    assert_eq!(read["modified_at"], "2026-11-25T10:00:00Z");
    assert_eq!(headers["x-storage-metadata"], metadata.as_str());

    let ranged = [("key", "docs/a.txt"), ("offset", "6"), ("length", "3")];
    let (_, headers, body) = store.call("GET", "acme", &ranged, &[], b"").await.unwrap();
    assert_eq!(
        (&body[..], headers["content-length"].to_str().unwrap()),
        (&b"wor"[..], "3")
    );
    let (status, headers, body) = store.call("HEAD", "acme", &key, &[], b"").await.unwrap();
    assert_eq!(
        (status, body.len(), object(&headers)["size_bytes"].as_u64()),
        (StatusCode::OK, 0, Some(11))
    );

    // Another tenant cannot see the object.
    assert_eq!(
        store
            .call("GET", "globex", &key, &[], b"")
            .await
            .unwrap_err(),
        StatusCode::NOT_FOUND
    );

    // Conditional writes and deletes.
    let absent = [("if-none-match", "*")];
    assert_eq!(
        store
            .call("PUT", "acme", &key, &absent, b"x")
            .await
            .unwrap_err(),
        StatusCode::PRECONDITION_FAILED
    );
    let stale = [("if-match", "\"stale\"")];
    assert_eq!(
        store
            .call("PUT", "acme", &key, &stale, b"x")
            .await
            .unwrap_err(),
        StatusCode::PRECONDITION_FAILED
    );
    let both = [("if-match", etag.as_str()), ("if-none-match", "*")];
    assert_eq!(
        store
            .call("PUT", "acme", &key, &both, b"x")
            .await
            .unwrap_err(),
        StatusCode::BAD_REQUEST
    );
    let current = [("if-match", etag.as_str())];
    let (status, _, body) = store
        .call("PUT", "acme", &key, &current, b"v2")
        .await
        .unwrap();
    assert_eq!(status, StatusCode::CREATED);
    let second = values::parse(&body).unwrap()["etag"]
        .as_str()
        .unwrap()
        .to_owned();
    assert_ne!(second, etag);
    assert_eq!(
        store
            .call("DELETE", "acme", &key, &current, b"")
            .await
            .unwrap_err(),
        StatusCode::PRECONDITION_FAILED
    );
    let current = [("if-match", second.as_str())];
    assert_eq!(
        store
            .call("DELETE", "acme", &key, &current, b"")
            .await
            .unwrap()
            .0,
        StatusCode::NO_CONTENT
    );
    assert_eq!(
        store.call("GET", "acme", &key, &[], b"").await.unwrap_err(),
        StatusCode::NOT_FOUND
    );
    assert_eq!(
        store
            .call("DELETE", "acme", &key, &[], b"")
            .await
            .unwrap()
            .0,
        StatusCode::NO_CONTENT
    );
    assert_eq!(
        store
            .call("DELETE", "acme", &key, &current, b"")
            .await
            .unwrap_err(),
        StatusCode::PRECONDITION_FAILED
    );

    // Every upload carried its signed digest and exact length.
    for write in &state.lock().unwrap().writes {
        assert_eq!(write["x-amz-content-sha256"], write["x-amz-meta-sha256"]);
    }
}

#[tokio::test]
async fn listing_pages_relative_keys_and_rejects_foreign_cursors() {
    let (endpoint, _) = mock_s3().await;
    let store = Store::new(&endpoint, json!({}));
    for key in [
        "list/1",
        "list/2",
        "list/3",
        "list/4",
        "list/5",
        "list/sub/x",
        "other",
    ] {
        store
            .call("PUT", "acme", &[("key", key)], &[], b"data")
            .await
            .unwrap();
    }
    store
        .call("PUT", "globex", &[("key", "list/9")], &[], b"data")
        .await
        .unwrap();
    let mut keys = Vec::new();
    let mut cursor = String::new();
    loop {
        let query = [
            ("prefix", "list/"),
            ("limit", "2"),
            ("cursor", cursor.as_str()),
        ];
        let (_, _, body) = store.call("GET", "acme", &query, &[], b"").await.unwrap();
        let page = values::parse(&body).unwrap();
        let objects = page["objects"].as_array().unwrap();
        assert!(objects.len() <= 2);
        for object in objects {
            assert_eq!(
                (object["tenant"].as_str(), object["size_bytes"].as_u64()),
                (Some("acme"), Some(4))
            );
            assert!(
                object["etag"]
                    .as_str()
                    .is_some_and(|etag| etag.starts_with('"'))
            );
            keys.push(object["key"].as_str().unwrap().to_owned());
        }
        match page["next_cursor"].as_str() {
            Some(next) => cursor = next.to_owned(),
            None => break,
        }
    }
    assert_eq!(
        keys,
        [
            "list/1",
            "list/2",
            "list/3",
            "list/4",
            "list/5",
            "list/sub/x"
        ]
    );

    let grouped = [("prefix", "list/"), ("delimiter", "/")];
    let (_, _, body) = store.call("GET", "acme", &grouped, &[], b"").await.unwrap();
    let page = values::parse(&body).unwrap();
    assert_eq!(page["objects"].as_array().unwrap().len(), 5);
    assert_eq!(page["prefixes"], json!(["list/sub/"]));

    // A cursor belongs to its tenant, prefix and delimiter.
    let first = [("prefix", "list/"), ("limit", "1")];
    let (_, _, body) = store.call("GET", "acme", &first, &[], b"").await.unwrap();
    let cursor = values::parse(&body).unwrap()["next_cursor"]
        .as_str()
        .unwrap()
        .to_owned();
    let foreign = [("prefix", "list/"), ("cursor", cursor.as_str())];
    assert_eq!(
        store
            .call("GET", "globex", &foreign, &[], b"")
            .await
            .unwrap_err(),
        StatusCode::BAD_REQUEST
    );
    let bad = [("prefix", "../")];
    assert_eq!(
        store.call("GET", "acme", &bad, &[], b"").await.unwrap_err(),
        StatusCode::BAD_REQUEST
    );
}

#[tokio::test]
async fn limits_access_and_backend_failures_map_to_storage_faults() {
    let (endpoint, _) = mock_s3().await;
    let limited = Store::new(&endpoint, json!({"max_object_bytes": 4}));
    assert_eq!(
        limited
            .call("PUT", "acme", &[("key", "big")], &[], b"12345")
            .await
            .unwrap_err(),
        StatusCode::PAYLOAD_TOO_LARGE
    );
    let read_only = Store::new(&endpoint, json!({"access": "read_only"}));
    assert_eq!(
        read_only
            .call("PUT", "acme", &[("key", "k")], &[], b"x")
            .await
            .unwrap_err(),
        StatusCode::FORBIDDEN
    );
    let store = Store::new(&endpoint, json!({}));
    for key in ["../escape", "a//b", "a/./b", "back\\slash"] {
        assert_eq!(
            store
                .call("GET", "acme", &[("key", key)], &[], b"")
                .await
                .unwrap_err(),
            StatusCode::BAD_REQUEST,
            "{key}"
        );
    }
    let wrong = Store::new(&endpoint, json!({"secret_access_key": "wrong"}));
    assert_eq!(
        wrong
            .call("GET", "acme", &[("key", "k")], &[], b"")
            .await
            .unwrap_err(),
        StatusCode::FORBIDDEN
    );
    let unreachable = Store::new("http://127.0.0.1:1", json!({}));
    assert_eq!(
        unreachable
            .call("GET", "acme", &[("key", "k")], &[], b"")
            .await
            .unwrap_err(),
        StatusCode::SERVICE_UNAVAILABLE
    );
}

#[tokio::test]
async fn delete_prefix_is_scoped_and_ignores_unsafe_listed_keys() {
    let (endpoint, state) = mock_s3().await;
    let store = Store::new(&endpoint, json!({}));
    for (tenant, key) in [
        ("acme", "logs/2026/a.txt"),
        ("acme", "logs/2026/b.txt"),
        ("acme", "keep/c.txt"),
        ("globex", "logs/2026/a.txt"),
    ] {
        store
            .call("PUT", tenant, &[("key", key)], &[], b"data")
            .await
            .unwrap();
    }
    // Inject a traversal key under acme's prefix directly into the mock bucket
    // to verify delete_prefix never normalizes and deletes outside the partition.
    let poisoned = "app/files/tenants/YWNtZQ/logs/2026/../../keep/c.txt".to_owned();
    state.lock().unwrap().objects.insert(
        poisoned.clone(),
        Stored {
            data: Bytes::from_static(b"poison"),
            etag: "\"poison\"".into(),
            headers: HeaderMap::new(),
        },
    );
    let (status, _, _) = store
        .call(
            "DELETE",
            "acme",
            &[("key", "logs/"), ("recursive", "1")],
            &[],
            b"",
        )
        .await
        .unwrap();
    assert_eq!(status, StatusCode::NO_CONTENT);
    let objects = &state.lock().unwrap().objects;
    assert!(!objects.contains_key("app/files/tenants/YWNtZQ/logs/2026/a.txt"));
    assert!(!objects.contains_key("app/files/tenants/YWNtZQ/logs/2026/b.txt"));
    assert!(objects.contains_key("app/files/tenants/YWNtZQ/keep/c.txt"));
    assert!(objects.contains_key("app/files/tenants/Z2xvYmV4/logs/2026/a.txt"));
    assert!(objects.contains_key(&poisoned));
}
