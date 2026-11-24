use std::fmt::Write;

use bytes::Bytes;
use http_body_util::{BodyExt, StreamBody};
use hyper::body::Incoming;
use hyper::header::{CONTENT_TYPE, HeaderMap, IF_MATCH, IF_NONE_MATCH};
use hyper::{Method, StatusCode};
use tokio::io::AsyncRead;
use tokio_util::io::ReaderStream;

use crate::{Context, Error, FramedStream, Result, full, serde_json};

pub struct StorageRead {
    pub headers: HeaderMap,
    body: Incoming,
}

impl StorageRead {
    pub async fn next_chunk(&mut self) -> Result<Option<Bytes>> {
        while let Some(frame) = self.body.frame().await {
            if let Ok(data) = frame?.into_data() {
                return Ok(Some(data));
            }
        }
        Ok(None)
    }
}

#[derive(Default)]
pub struct StorageWriteOptions<'a> {
    pub content_type: Option<&'a str>,
    pub if_match: Option<&'a str>,
    pub if_none_match: bool,
}

impl Context {
    pub async fn storage_read(&self, store: &str, key: &str) -> Result<StorageRead> {
        let path = object_path("read", store, key);
        let request = self.worker_request(Method::GET, &path, full(Bytes::new()))?;
        let response = self.host.send(request).await?;
        if response.status() != StatusCode::OK {
            return Err(
                format!("storage read returned HTTP {}", response.status().as_u16()).into(),
            );
        }
        let (parts, body) = response.into_parts();
        Ok(StorageRead {
            headers: parts.headers,
            body,
        })
    }

    pub async fn storage_write(
        &self,
        store: &str,
        key: &str,
        body: impl AsyncRead + Send + 'static,
        options: StorageWriteOptions<'_>,
    ) -> Result<serde_json::Value> {
        let stream = FramedStream {
            prefix: None,
            stream: ReaderStream::new(Box::pin(body)),
            _cancel_on_drop: None,
        };
        let body = StreamBody::new(stream).map_err(Error::from).boxed_unsync();
        let path = object_path("write", store, key);
        let mut request = self.worker_request(Method::PUT, &path, body)?;
        let headers = request.headers_mut();
        if let Some(media_type) = options.content_type {
            headers.insert(CONTENT_TYPE, media_type.parse()?);
        }
        if let Some(etag) = options.if_match {
            headers.insert(IF_MATCH, etag.parse()?);
        }
        if options.if_none_match {
            headers.insert(IF_NONE_MATCH, "*".parse()?);
        }
        let reply = self.host.send(request).await?;
        if reply.status() != StatusCode::CREATED {
            return Err(format!("storage write returned HTTP {}", reply.status().as_u16()).into());
        }
        let data = http_body_util::Limited::new(reply.into_body(), crate::MAX_METADATA)
            .collect()
            .await?
            .to_bytes();
        Ok(serde_json::from_slice(&data)?)
    }

    pub async fn storage_delete(
        &self,
        store: &str,
        key: &str,
        if_match: Option<&str>,
    ) -> Result<()> {
        let path = object_path("delete", store, key);
        let mut request = self.worker_request(Method::DELETE, &path, full(Bytes::new()))?;
        if let Some(etag) = if_match {
            request.headers_mut().insert(IF_MATCH, etag.parse()?);
        }
        let response = self.host.send(request).await?;
        if response.status() != StatusCode::NO_CONTENT {
            return Err(format!(
                "storage delete returned HTTP {}",
                response.status().as_u16()
            )
            .into());
        }
        Ok(())
    }

    pub async fn storage_list(
        &self,
        store: &str,
        prefix: &str,
        cursor: &str,
        limit: u32,
    ) -> Result<serde_json::Value> {
        let path = format!(
            "storage/list?store={}&prefix={}&cursor={}&limit={limit}",
            encode_query(store),
            encode_query(prefix),
            encode_query(cursor)
        );
        let request = self.worker_request(Method::GET, &path, full(Bytes::new()))?;
        Ok(serde_json::from_slice(
            &self.read_capability(request).await?,
        )?)
    }
}

fn object_path(action: &str, store: &str, key: &str) -> String {
    format!(
        "storage/{action}?store={}&key={}",
        encode_query(store),
        encode_query(key)
    )
}

fn encode_query(input: &str) -> String {
    let mut output = String::with_capacity(input.len());
    for byte in input.bytes() {
        match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' => {
                output.push(char::from(byte))
            }
            _ => write!(output, "%{byte:02X}").expect("write into string"),
        }
    }
    output
}
