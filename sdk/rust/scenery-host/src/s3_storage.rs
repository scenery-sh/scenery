//! An S3-compatible object store behind the Worker ABI storage capability:
//! AWS S3, Cloudflare R2, MinIO, and Google Cloud Storage interoperability.
//! Requests are signed with AWS Signature Version 4.
//!
//! A store keeps its objects beneath `<prefix><store>/shared/` or, for a
//! tenant-scoped store, `<prefix><store>/tenants/<base64url(tenant)>/`, so
//! neither another store nor another tenant can address them.

use std::collections::BTreeMap;
use std::fmt::Display;
use std::sync::OnceLock;
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use bytes::Bytes;
use http_body_util::{BodyExt, StreamBody};
use hyper::body::{Body, Frame};
use hyper::header::{HeaderName, HeaderValue};
use hyper::{HeaderMap, Method, Request, StatusCode};
use percent_encoding::{AsciiSet, NON_ALPHANUMERIC, utf8_percent_encode};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use time::OffsetDateTime;
use time::macros::format_description;
use tokio::io::{AsyncSeekExt, AsyncWriteExt};
use tokio_util::io::ReaderStream;
use url::Url;

use crate::config::Lookup;
use crate::host::{Fault, Reply, empty, header, json_reply, stamp};
use crate::local_storage::{Selection, get_usize, object_reply, path_valid, user_metadata};
use crate::process::hex;
use crate::values;

/// SigV4 leaves only RFC 3986 unreserved characters unencoded.
const UNRESERVED: &AsciiSet = &NON_ALPHANUMERIC
    .remove(b'-')
    .remove(b'.')
    .remove(b'_')
    .remove(b'~');
const EMPTY_PAYLOAD: &str = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";
const SHA256_HEADER: &str = "x-amz-meta-sha256";
const METADATA_HEADER: &str = "x-amz-meta-metadata";
const CURSOR_LIMIT: usize = 12 << 10;
const LIST_LIMIT: usize = 4 << 20;

/// The static credentials a request is signed with.
#[derive(Clone)]
pub(crate) struct Credentials {
    pub access_key_id: String,
    pub secret_access_key: String,
    pub session_token: Option<String>,
}

/// One configured bucket and the key prefix its stores share.
pub(crate) struct Bucket {
    endpoint: Url,
    region: String,
    bucket: String,
    path_style: bool,
    prefix: String,
    credentials: Credentials,
}

impl Bucket {
    /// Reads a store of kind `s3`. Without `access_key_id` in the store the
    /// standard `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and
    /// `AWS_SESSION_TOKEN` variables supply the credentials.
    pub(crate) fn from_config(store: &Value, lookup: Lookup) -> Result<Self> {
        let text = |field: &str| -> Result<Option<String>> {
            match &store[field] {
                Value::Null => Ok(None),
                Value::String(value) if !value.is_empty() => Ok(Some(value.clone())),
                _ => anyhow::bail!("storage field {field} must be a non-empty string"),
            }
        };
        let region = text("region")?.context("storage field region is required")?;
        let bucket = text("bucket")?.context("storage field bucket is required")?;
        ensure!(
            bucket.len() <= 255
                && bucket
                    .bytes()
                    .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'.' | b'_')),
            "storage field bucket is not a valid bucket name"
        );
        let configured = text("endpoint")?;
        let endpoint = match &configured {
            Some(endpoint) => {
                Url::parse(endpoint).context("storage field endpoint is not a URL")?
            }
            None => Url::parse(&format!("https://s3.{region}.amazonaws.com"))
                .context("storage field region is invalid")?,
        };
        ensure!(
            matches!(endpoint.scheme(), "http" | "https")
                && endpoint.host_str().is_some()
                && endpoint.username().is_empty()
                && endpoint.password().is_none()
                && endpoint.path() == "/"
                && endpoint.query().is_none()
                && endpoint.fragment().is_none(),
            "storage field endpoint must be an http or https origin without credentials or path"
        );
        // A custom endpoint (MinIO, R2, GCS) addresses buckets by path,
        // which needs no wildcard DNS or certificate; AWS uses virtual hosts.
        let path_style = match &store["path_style"] {
            Value::Null => configured.is_some(),
            Value::Bool(value) => *value,
            _ => anyhow::bail!("storage field path_style must be a boolean"),
        };
        let mut prefix = text("prefix")?.unwrap_or_default();
        ensure!(
            path_valid(&prefix, true),
            "storage field prefix is not a valid key prefix"
        );
        if !prefix.is_empty() && !prefix.ends_with('/') {
            prefix.push('/');
        }
        let variable = |name: &str| lookup(name).and_then(|value| value.into_string().ok());
        let credentials = match text("access_key_id")? {
            Some(access_key_id) => Credentials {
                access_key_id,
                secret_access_key: text("secret_access_key")?
                    .context("storage field secret_access_key is required")?,
                session_token: text("session_token")?,
            },
            None => Credentials {
                access_key_id: variable("AWS_ACCESS_KEY_ID")
                    .filter(|value| !value.is_empty())
                    .context("storage needs access_key_id or AWS_ACCESS_KEY_ID")?,
                secret_access_key: variable("AWS_SECRET_ACCESS_KEY")
                    .filter(|value| !value.is_empty())
                    .context("storage needs secret_access_key or AWS_SECRET_ACCESS_KEY")?,
                session_token: variable("AWS_SESSION_TOKEN").filter(|value| !value.is_empty()),
            },
        };
        Ok(Self {
            endpoint,
            region,
            bucket,
            path_style,
            prefix,
            credentials,
        })
    }

    /// The URL of one object, or of the bucket when `object` is `None`.
    fn url(&self, object: Option<&str>, query: &[(&str, &str)]) -> Url {
        let mut url = self.endpoint.clone();
        let mut path = String::new();
        if self.path_style {
            path.push('/');
            path.push_str(&encode(&self.bucket));
        } else {
            let host = format!(
                "{}.{}",
                self.bucket,
                self.endpoint.host_str().unwrap_or_default()
            );
            url.set_host(Some(&host)).expect("valid bucket host");
        }
        if let Some(object) = object {
            path.push('/');
            let segments: Vec<_> = object.split('/').map(encode).collect();
            path.push_str(&segments.join("/"));
        } else if path.is_empty() {
            path.push('/');
        }
        url.set_path(&path);
        let query = canonical_query(query);
        url.set_query((!query.is_empty()).then_some(query.as_str()));
        url
    }

    async fn send(
        &self,
        method: Method,
        url: Url,
        mut headers: HeaderMap,
        payload: &str,
        body: Option<reqwest::Body>,
    ) -> Result<reqwest::Response, Fault> {
        sign(
            &method,
            &url,
            &mut headers,
            payload,
            &self.credentials,
            &self.region,
            OffsetDateTime::now_utc(),
        );
        let mut request = client().request(method, url).headers(headers);
        if let Some(body) = body {
            request = request.body(body);
        }
        request.send().await.map_err(|error| {
            eprintln!("storage backend is unreachable: {}", error.without_url());
            unavailable()
        })
    }

    async fn head(&self, object: &str) -> Result<Option<HeaderMap>, Fault> {
        let url = self.url(Some(object), &[]);
        let response = self
            .send(Method::HEAD, url, HeaderMap::new(), EMPTY_PAYLOAD, None)
            .await?;
        match response.status() {
            StatusCode::NOT_FOUND => Ok(None),
            status if status.is_success() => Ok(Some(response.headers().clone())),
            _ => Err(failure(response).await),
        }
    }
}

fn client() -> &'static reqwest::Client {
    static CLIENT: OnceLock<reqwest::Client> = OnceLock::new();
    CLIENT.get_or_init(|| {
        reqwest::Client::builder()
            .connect_timeout(Duration::from_secs(10))
            .read_timeout(Duration::from_secs(60))
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .expect("storage HTTP client")
    })
}

fn encode(value: &str) -> String {
    utf8_percent_encode(value, UNRESERVED).to_string()
}

fn canonical_query(query: &[(&str, &str)]) -> String {
    let mut pairs: Vec<_> = query
        .iter()
        .map(|(key, value)| (encode(key), encode(value)))
        .collect();
    pairs.sort();
    pairs
        .iter()
        .map(|(key, value)| format!("{key}={value}"))
        .collect::<Vec<_>>()
        .join("&")
}

/// Signs a request with AWS Signature Version 4 for service `s3`. It adds
/// `host`, `x-amz-date`, `x-amz-content-sha256`, the session token if any,
/// and `authorization`, and signs every header the request carries. `url`
/// must already hold the SigV4 encoding of its path and query.
pub(crate) fn sign(
    method: &Method,
    url: &Url,
    headers: &mut HeaderMap,
    payload: &str,
    credentials: &Credentials,
    region: &str,
    now: OffsetDateTime,
) {
    let stamp = now
        .format(format_description!(
            "[year][month][day]T[hour][minute][second]Z"
        ))
        .expect("valid SigV4 time");
    let date = &stamp[..8];
    let host = match url.port() {
        Some(port) => format!("{}:{port}", url.host_str().unwrap_or_default()),
        None => url.host_str().unwrap_or_default().to_owned(),
    };
    let mut set = |name: &'static str, value: &str| {
        headers.insert(
            HeaderName::from_static(name),
            HeaderValue::from_str(value).expect("valid signed header"),
        );
    };
    set("host", &host);
    set("x-amz-date", &stamp);
    set("x-amz-content-sha256", payload);
    if let Some(token) = &credentials.session_token {
        set("x-amz-security-token", token);
    }
    let names: Vec<String> = {
        let mut names: Vec<_> = headers
            .keys()
            .map(|name| name.as_str().to_owned())
            .collect();
        names.sort();
        names.dedup();
        names
    };
    let canonical = canonical_request(
        method.as_str(),
        url.path(),
        url.query().unwrap_or_default(),
        &names
            .iter()
            .map(|name| {
                let values: Vec<_> = headers
                    .get_all(name.as_str())
                    .iter()
                    .map(|value| String::from_utf8_lossy(value.as_bytes()).into_owned())
                    .collect();
                (name.clone(), values.join(","))
            })
            .collect::<Vec<_>>(),
        payload,
    );
    let scope = format!("{date}/{region}/s3/aws4_request");
    let signature = signature(
        &credentials.secret_access_key,
        date,
        region,
        &string_to_sign(&stamp, &scope, &canonical),
    );
    let authorization = format!(
        "AWS4-HMAC-SHA256 Credential={}/{scope}, SignedHeaders={}, Signature={signature}",
        credentials.access_key_id,
        names.join(";")
    );
    let mut value = HeaderValue::from_str(&authorization).expect("valid authorization");
    value.set_sensitive(true);
    headers.insert(hyper::header::AUTHORIZATION, value);
}

/// The SigV4 canonical request. `headers` are lowercase names in sorted
/// order with their values; `path` and `query` are already encoded.
pub(crate) fn canonical_request(
    method: &str,
    path: &str,
    query: &str,
    headers: &[(String, String)],
    payload: &str,
) -> String {
    let mut canonical = format!("{method}\n{path}\n{query}\n");
    for (name, value) in headers {
        let value = value.split_whitespace().collect::<Vec<_>>().join(" ");
        canonical.push_str(&format!("{name}:{value}\n"));
    }
    let names: Vec<_> = headers.iter().map(|(name, _)| name.as_str()).collect();
    canonical.push_str(&format!("\n{}\n{payload}", names.join(";")));
    canonical
}

pub(crate) fn string_to_sign(stamp: &str, scope: &str, canonical: &str) -> String {
    format!(
        "AWS4-HMAC-SHA256\n{stamp}\n{scope}\n{}",
        hex(&Sha256::digest(canonical.as_bytes()))
    )
}

pub(crate) fn signature(secret: &str, date: &str, region: &str, string_to_sign: &str) -> String {
    use ring::hmac::{HMAC_SHA256, Key, sign};
    let step = |key: &[u8], data: &str| sign(&Key::new(HMAC_SHA256, key), data.as_bytes());
    let key = step(format!("AWS4{secret}").as_bytes(), date);
    let key = step(key.as_ref(), region);
    let key = step(key.as_ref(), "s3");
    let key = step(key.as_ref(), "aws4_request");
    hex(step(key.as_ref(), string_to_sign).as_ref())
}

/// Serves one storage capability call for a store of kind `s3`.
pub(crate) async fn serve<B>(request: Request<B>, selection: Selection<'_>) -> Result<Reply, Fault>
where
    B: Body<Data = Bytes> + Unpin,
    B::Error: Display,
{
    let Selection {
        store,
        name,
        tenant,
        key,
        recursive,
        query,
        ..
    } = selection;
    if tenant.len() > 256
        || tenant.chars().any(char::is_control)
        || key.is_some_and(|key| !path_valid(key, recursive))
    {
        return Err(Fault::invalid());
    }
    let bucket = Bucket::from_config(store, &|name: &str| std::env::var_os(name))
        .map_err(Fault::internal)?;
    let partition = Partition::new(&bucket, name, tenant).map_err(Fault::internal)?;
    if matches!(*request.method(), Method::PUT | Method::DELETE) && store["access"] == "read_only" {
        return Err(Fault::forbidden());
    }
    let Some(key) = key else {
        return partition.list(query).await;
    };
    match *request.method() {
        Method::HEAD => {
            let headers = bucket
                .head(&partition.object(key))
                .await?
                .ok_or_else(not_found)?;
            object_reply(&partition.describe(key, &headers)?)
        }
        Method::GET => partition.read(key, query).await,
        Method::PUT => {
            partition
                .write(request, key, store["max_object_bytes"].as_u64())
                .await
        }
        Method::DELETE if recursive => partition.delete_prefix(request.headers(), key).await,
        Method::DELETE => partition.delete(request.headers(), key).await,
        _ => Err(Fault::invalid()),
    }
}

/// The objects of one store and tenant.
struct Partition<'a> {
    bucket: &'a Bucket,
    name: &'a str,
    tenant: &'a str,
    root: String,
}

impl<'a> Partition<'a> {
    fn new(bucket: &'a Bucket, name: &'a str, tenant: &'a str) -> Result<Self> {
        ensure!(
            !name.is_empty() && path_valid(name, false) && !name.contains('/'),
            "storage store name is invalid"
        );
        let scope = if tenant.is_empty() {
            "shared".to_owned()
        } else {
            format!("tenants/{}", URL_SAFE_NO_PAD.encode(tenant))
        };
        Ok(Self {
            bucket,
            name,
            tenant,
            root: format!("{}{name}/{scope}/", bucket.prefix),
        })
    }

    fn object(&self, key: &str) -> String {
        format!("{}{key}", self.root)
    }

    /// The Worker ABI object record of `key` from its response headers.
    fn describe(&self, key: &str, headers: &HeaderMap) -> Result<Value, Fault> {
        let text = |name: &str| headers.get(name).and_then(|value| value.to_str().ok());
        let size: u64 = text("content-length")
            .and_then(|value| value.parse().ok())
            .ok_or_else(|| Fault::internal("storage backend omitted the object size"))?;
        let mut object = json!({
            "store": self.name, "key": key, "size_bytes": size,
            "content_type": text("content-type").unwrap_or("application/octet-stream"),
            "etag": text("etag").unwrap_or_default(),
            "sha256": text(SHA256_HEADER).unwrap_or_default(),
            "modified_at": text("last-modified")
                .and_then(|value| httpdate::parse_http_date(value).ok())
                .map_or_else(|| stamp(OffsetDateTime::now_utc()), |at| stamp(at.into())),
        });
        if !self.tenant.is_empty() {
            object["tenant"] = json!(self.tenant);
        }
        if let Some(encoded) = text(METADATA_HEADER) {
            let metadata = URL_SAFE_NO_PAD
                .decode(encoded)
                .ok()
                .and_then(|raw| values::parse(&raw).ok())
                .filter(Value::is_object)
                .ok_or_else(|| Fault::internal("storage object metadata is malformed"))?;
            object["metadata"] = metadata;
        }
        Ok(object)
    }

    async fn read(&self, key: &str, query: &BTreeMap<String, Vec<String>>) -> Result<Reply, Fault> {
        let object = self.object(key);
        let offset = get_usize(query, "offset")?;
        let length = get_usize(query, "length")?;
        if offset.is_none() && length.is_none() {
            let url = self.bucket.url(Some(&object), &[]);
            let response = self
                .bucket
                .send(Method::GET, url, HeaderMap::new(), EMPTY_PAYLOAD, None)
                .await?;
            if !response.status().is_success() {
                return Err(failure(response).await);
            }
            let mut reply = object_reply(&self.describe(key, response.headers())?)?;
            *reply.body_mut() = stream(response);
            return Ok(reply);
        }
        let (offset, length) = (offset.unwrap_or(0), length.unwrap_or(u64::MAX));
        // The range is read from the version the metadata describes; a
        // write in between is retried against the new version.
        for _ in 0..3 {
            let headers = self.bucket.head(&object).await?.ok_or_else(not_found)?;
            let described = self.describe(key, &headers)?;
            let size = described["size_bytes"].as_u64().unwrap_or_default();
            if offset > size {
                return Err(Fault::invalid());
            }
            let length = (size - offset).min(length);
            let mut reply = object_reply(&described)?;
            header(reply.headers_mut(), "content-length", &length.to_string());
            if length == 0 {
                return Ok(reply);
            }
            let mut request = HeaderMap::new();
            let range = format!("bytes={offset}-{}", offset + length - 1);
            header(&mut request, "range", &range);
            if let Some(etag) = headers.get("etag") {
                request.insert(hyper::header::IF_MATCH, etag.clone());
            }
            let url = self.bucket.url(Some(&object), &[]);
            let response = self
                .bucket
                .send(Method::GET, url, request, EMPTY_PAYLOAD, None)
                .await?;
            match response.status() {
                StatusCode::PRECONDITION_FAILED => continue,
                StatusCode::PARTIAL_CONTENT => {
                    *reply.body_mut() = stream(response);
                    return Ok(reply);
                }
                _ => return Err(failure(response).await),
            }
        }
        Err(Fault::new(
            409,
            "aborted",
            "storage object changed while it was read",
        ))
    }

    async fn write<B>(
        &self,
        request: Request<B>,
        key: &str,
        limit: Option<u64>,
    ) -> Result<Reply, Fault>
    where
        B: Body<Data = Bytes> + Unpin,
        B::Error: Display,
    {
        let (parts, body) = request.into_parts();
        let (if_match, absent) = preconditions(&parts.headers)?;
        let media_type = parts
            .headers
            .get("content-type")
            .and_then(|value| value.to_str().ok())
            .map(str::to_owned)
            .unwrap_or_else(|| {
                mime_guess::from_path(key)
                    .first_or_octet_stream()
                    .to_string()
            });
        if media_type.len() > 255 {
            return Err(Fault::invalid());
        }
        let metadata = user_metadata(&parts.headers)?;
        let (file, size, digest) = spool(body, limit).await?;
        let mut headers = HeaderMap::new();
        header(&mut headers, "content-type", &media_type);
        header(&mut headers, "content-length", &size.to_string());
        header(&mut headers, SHA256_HEADER, &digest);
        if let Some(metadata) = &metadata {
            let encoded = values::base64url(values::encode(metadata).as_bytes());
            header(&mut headers, METADATA_HEADER, &encoded);
        }
        if let Some(etag) = &if_match {
            headers.insert(hyper::header::IF_MATCH, etag.clone());
        }
        if absent {
            header(&mut headers, "if-none-match", "*");
        }
        let body = reqwest::Body::wrap_stream(ReaderStream::new(file));
        let url = self.bucket.url(Some(&self.object(key)), &[]);
        let response = self
            .bucket
            .send(Method::PUT, url, headers, &digest, Some(body))
            .await?;
        if !response.status().is_success() {
            return Err(failure(response).await);
        }
        let mut object = json!({
            "store": self.name, "key": key, "size_bytes": size, "content_type": media_type,
            "etag": response.headers().get("etag").and_then(|value| value.to_str().ok()).unwrap_or_default(),
            "sha256": digest, "modified_at": stamp(OffsetDateTime::now_utc()),
        });
        if !self.tenant.is_empty() {
            object["tenant"] = json!(self.tenant);
        }
        if let Some(metadata) = metadata {
            object["metadata"] = metadata;
        }
        Ok(json_reply(StatusCode::CREATED, &object))
    }

    /// Deletes `key`. `If-Match` is forwarded on the signed `DELETE` request
    /// for atomic backend enforcement and checked via `HEAD` first for
    /// backends that do not implement conditional deletes; `If-None-Match: *`
    /// checks absence first. Deleting an absent object succeeds, as it does
    /// locally.
    async fn delete(&self, request: &HeaderMap, key: &str) -> Result<Reply, Fault> {
        let object = self.object(key);
        let (if_match, absent) = preconditions(request)?;
        if if_match.is_some() || absent {
            let current = self.bucket.head(&object).await?;
            let matches = match (&current, &if_match) {
                (Some(_), None) => false,
                (None, None) => true,
                (Some(headers), Some(expected)) => headers.get("etag") == Some(expected),
                (None, Some(_)) => false,
            };
            if !matches {
                return Err(precondition_failed());
            }
            if current.is_none() {
                return Ok(empty(StatusCode::NO_CONTENT));
            }
        }
        let mut headers = HeaderMap::new();
        if let Some(expected) = &if_match {
            headers.insert(hyper::header::IF_MATCH, expected.clone());
        }
        let url = self.bucket.url(Some(&object), &[]);
        let response = self
            .bucket
            .send(Method::DELETE, url, headers, EMPTY_PAYLOAD, None)
            .await?;
        if !response.status().is_success() && response.status() != StatusCode::NOT_FOUND {
            return Err(failure(response).await);
        }
        Ok(empty(StatusCode::NO_CONTENT))
    }

    /// Deletes every object under `prefix` within this partition.
    async fn delete_prefix(&self, request: &HeaderMap, prefix: &str) -> Result<Reply, Fault> {
        if request.contains_key(hyper::header::IF_MATCH)
            || request.contains_key(hyper::header::IF_NONE_MATCH)
        {
            return Err(Fault::invalid());
        }
        let full_prefix = format!("{}{prefix}", self.root);
        let mut token: Option<String> = None;
        loop {
            let mut parameters = vec![
                ("list-type", "2"),
                ("prefix", full_prefix.as_str()),
                ("max-keys", "1000"),
            ];
            if let Some(current) = token.as_deref() {
                parameters.push(("continuation-token", current));
            }
            let url = self.bucket.url(None, &parameters);
            let response = self
                .bucket
                .send(Method::GET, url, HeaderMap::new(), EMPTY_PAYLOAD, None)
                .await?;
            if !response.status().is_success() {
                return Err(failure(response).await);
            }
            let document = read_text(response, LIST_LIMIT).await?;
            let keys: Vec<String> = elements(&document, "Contents")
                .filter_map(|entry| element(entry, "Key").map(unescape))
                .filter(|object| {
                    object
                        .strip_prefix(&self.root)
                        .is_some_and(|relative| {
                            relative.starts_with(prefix) && path_valid(relative, false)
                        })
                })
                .collect();
            for object in keys {
                let url = self.bucket.url(Some(&object), &[]);
                let expected_suffix = object
                    .split('/')
                    .map(encode)
                    .collect::<Vec<_>>()
                    .join("/");
                let expected_path = if self.bucket.path_style {
                    format!("/{}/{expected_suffix}", encode(&self.bucket.bucket))
                } else {
                    format!("/{expected_suffix}")
                };
                if url.path() != expected_path {
                    return Err(Fault::internal("storage backend returned an unsafe key"));
                }
                let delete = self
                    .bucket
                    .send(Method::DELETE, url, HeaderMap::new(), EMPTY_PAYLOAD, None)
                    .await?;
                if !delete.status().is_success() && delete.status() != StatusCode::NOT_FOUND {
                    return Err(failure(delete).await);
                }
            }
            if element(&document, "IsTruncated") == Some("true")
                && let Some(next) = element(&document, "NextContinuationToken").map(unescape)
            {
                token = Some(next);
            } else {
                break;
            }
        }
        Ok(empty(StatusCode::NO_CONTENT))
    }

    /// Lists one page with `ListObjectsV2`. Keys and prefixes are relative
    /// to the partition; the cursor wraps the backend's continuation token
    /// and is bound to this store, tenant, prefix and delimiter.
    async fn list(&self, query: &BTreeMap<String, Vec<String>>) -> Result<Reply, Fault> {
        let value = |key: &str| {
            query
                .get(key)
                .and_then(|values| values.first())
                .map_or("", String::as_str)
        };
        let (prefix, delimiter) = (value("prefix"), value("delimiter"));
        let limit = get_usize(query, "limit")?
            .filter(|limit| *limit != 0)
            .unwrap_or(100)
            .min(1000);
        if !path_valid(prefix, true) || !matches!(delimiter, "" | "/") {
            return Err(Fault::invalid());
        }
        let binding = hex(&Sha256::digest(
            values::encode(&json!(["s3-list", self.root, prefix, delimiter])).as_bytes(),
        ));
        let cursor = value("cursor");
        let token = if cursor.is_empty() {
            None
        } else {
            let decoded = (cursor.len() <= CURSOR_LIMIT)
                .then(|| URL_SAFE_NO_PAD.decode(cursor).ok())
                .flatten()
                .and_then(|raw| values::parse(&raw).ok())
                .filter(|cursor| cursor["binding"] == binding.as_str())
                .ok_or_else(Fault::invalid)?;
            Some(
                decoded["token"]
                    .as_str()
                    .ok_or_else(Fault::invalid)?
                    .to_owned(),
            )
        };
        let full_prefix = format!("{}{prefix}", self.root);
        let limit = limit.to_string();
        let mut parameters = vec![
            ("list-type", "2"),
            ("prefix", full_prefix.as_str()),
            ("max-keys", limit.as_str()),
        ];
        if !delimiter.is_empty() {
            parameters.push(("delimiter", delimiter));
        }
        if let Some(token) = &token {
            parameters.push(("continuation-token", token));
        }
        let url = self.bucket.url(None, &parameters);
        let response = self
            .bucket
            .send(Method::GET, url, HeaderMap::new(), EMPTY_PAYLOAD, None)
            .await?;
        if !response.status().is_success() {
            return Err(failure(response).await);
        }
        let document = read_text(response, LIST_LIMIT).await?;
        let relative = |key: String| key.strip_prefix(&self.root).map(str::to_owned);
        let mut objects = Vec::new();
        for entry in elements(&document, "Contents") {
            let Some(key) = element(entry, "Key").map(unescape).and_then(relative) else {
                continue;
            };
            let mut object = json!({
                "store": self.name, "key": key,
                "size_bytes": element(entry, "Size").and_then(|size| size.parse::<u64>().ok()).unwrap_or_default(),
                "etag": element(entry, "ETag").map(unescape).unwrap_or_default(),
                "modified_at": element(entry, "LastModified")
                    .and_then(|at| OffsetDateTime::parse(at, &time::format_description::well_known::Rfc3339).ok())
                    .map(stamp).unwrap_or_default(),
            });
            if !self.tenant.is_empty() {
                object["tenant"] = json!(self.tenant);
            }
            objects.push(object);
        }
        let prefixes: Vec<_> = elements(&document, "CommonPrefixes")
            .filter_map(|entry| element(entry, "Prefix").map(unescape).and_then(relative))
            .collect();
        let mut result = json!({"objects": objects});
        if !prefixes.is_empty() {
            result["prefixes"] = json!(prefixes);
        }
        if element(&document, "IsTruncated") == Some("true")
            && let Some(next) = element(&document, "NextContinuationToken").map(unescape)
        {
            let cursor = json!({"binding": binding, "token": next});
            result["next_cursor"] = json!(values::base64url(values::encode(&cursor).as_bytes()));
        }
        Ok(json_reply(StatusCode::OK, &result))
    }
}

/// The `If-Match` etag and whether `If-None-Match: *` asks for an absent
/// object. Both at once can never hold.
fn preconditions(headers: &HeaderMap) -> Result<(Option<HeaderValue>, bool), Fault> {
    let if_match = headers
        .get(hyper::header::IF_MATCH)
        .filter(|value| !value.is_empty())
        .cloned();
    let absent = headers
        .get(hyper::header::IF_NONE_MATCH)
        .is_some_and(|value| value == "*");
    if absent && if_match.is_some() {
        return Err(Fault::invalid());
    }
    Ok((if_match, absent))
}

/// Writes the request body to an unlinked temporary file. S3 needs the
/// length and digest before the upload starts, and the signed digest lets
/// the backend reject a payload altered in transit.
async fn spool<B>(mut body: B, limit: Option<u64>) -> Result<(tokio::fs::File, u64, String), Fault>
where
    B: Body<Data = Bytes> + Unpin,
    B::Error: Display,
{
    let file = tempfile::tempfile().map_err(Fault::internal)?;
    let mut file = tokio::fs::File::from_std(file);
    let mut hash = Sha256::new();
    let mut size = 0_u64;
    while let Some(frame) = body.frame().await {
        if let Ok(bytes) = frame.map_err(|_| Fault::invalid())?.into_data() {
            size = size
                .checked_add(bytes.len() as u64)
                .ok_or_else(Fault::invalid)?;
            if limit.is_some_and(|limit| limit > 0 && size > limit) {
                return Err(Fault::new(
                    413,
                    "resource_exhausted",
                    "storage object exceeds size limit",
                ));
            }
            file.write_all(&bytes).await.map_err(Fault::internal)?;
            hash.update(&bytes);
        }
    }
    file.flush().await.map_err(Fault::internal)?;
    file.rewind().await.map_err(Fault::internal)?;
    Ok((file, size, hex(&hash.finalize())))
}

fn stream(response: reqwest::Response) -> crate::host::Body {
    let stream = futures_util::StreamExt::map(response.bytes_stream(), |chunk| {
        chunk.map(Frame::data).map_err(std::io::Error::other)
    });
    StreamBody::new(stream).boxed_unsync()
}

async fn read_text(response: reqwest::Response, limit: usize) -> Result<String, Fault> {
    let mut stream = response.bytes_stream();
    let mut data = Vec::new();
    while let Some(chunk) = futures_util::StreamExt::next(&mut stream).await {
        data.extend_from_slice(&chunk.map_err(|_| unavailable())?);
        if data.len() > limit {
            return Err(Fault::internal("storage listing exceeds its limit"));
        }
    }
    String::from_utf8(data).map_err(Fault::internal)
}

fn not_found() -> Fault {
    Fault::new(404, "not_found", "storage object is unavailable")
}

fn precondition_failed() -> Fault {
    Fault::new(412, "failed_precondition", "storage precondition failed")
}

fn unavailable() -> Fault {
    Fault::new(503, "unavailable", "storage backend is unavailable")
}

/// Maps a backend error to the storage failure a worker sees. The backend's
/// error code is logged for the operator; its message never reaches the worker.
async fn failure(response: reqwest::Response) -> Fault {
    let status = response.status();
    let code = read_text(response, 64 << 10)
        .await
        .ok()
        .and_then(|body| element(&body, "Code").map(unescape))
        .unwrap_or_default();
    match status.as_u16() {
        404 => not_found(),
        // A concurrent conditional write answers 409.
        409 | 412 => precondition_failed(),
        400 | 411 | 413 | 414 | 416 => {
            eprintln!("storage backend rejected a request: {status} {code}");
            Fault::invalid()
        }
        401 | 403 => {
            eprintln!("storage backend refused access: {status} {code}");
            Fault::forbidden()
        }
        _ => {
            eprintln!("storage backend failed: {status} {code}");
            unavailable()
        }
    }
}

/// The contents of each `<name>…</name>` element. S3 list results do not
/// nest an element in one of the same name.
fn elements<'a>(xml: &'a str, name: &str) -> impl Iterator<Item = &'a str> {
    let open = format!("<{name}>");
    let close = format!("</{name}>");
    let mut rest = xml;
    std::iter::from_fn(move || {
        let start = rest.find(&open)? + open.len();
        let end = rest[start..].find(&close)? + start;
        let content = &rest[start..end];
        rest = &rest[end + close.len()..];
        Some(content)
    })
}

fn element<'a>(xml: &'a str, name: &str) -> Option<&'a str> {
    elements(xml, name).next()
}

/// Decodes XML character and entity references.
fn unescape(text: &str) -> String {
    let mut output = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(start) = rest.find('&') {
        output.push_str(&rest[..start]);
        rest = &rest[start..];
        let Some(end) = rest.find(';') else { break };
        let entity = &rest[1..end];
        let decoded = match entity {
            "amp" => Some('&'),
            "lt" => Some('<'),
            "gt" => Some('>'),
            "quot" => Some('"'),
            "apos" => Some('\''),
            _ => entity
                .strip_prefix("#x")
                .map(|hex| u32::from_str_radix(hex, 16))
                .or_else(|| entity.strip_prefix('#').map(str::parse))
                .and_then(Result::ok)
                .and_then(char::from_u32),
        };
        match decoded {
            Some(character) => {
                output.push(character);
                rest = &rest[end + 1..];
            }
            None => {
                output.push('&');
                rest = &rest[1..];
            }
        }
    }
    output.push_str(rest);
    output
}

/// Decodes a percent-encoded URL path component (used by tests to read the
/// keys a request addressed).
#[cfg(test)]
pub(crate) fn decode(value: &str) -> String {
    percent_encoding::percent_decode_str(value)
        .decode_utf8_lossy()
        .into_owned()
}

#[cfg(test)]
#[path = "s3_storage_test.rs"]
mod tests;
