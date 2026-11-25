use std::path::PathBuf;

use anyhow::Result;
use bytes::Bytes;
use http_body_util::{BodyExt, Full, Limited};
use hyper::body::Incoming;
use hyper::client::conn::http2::{SendRequest, handshake};
use hyper::header::{AUTHORIZATION, CONTENT_TYPE};
use hyper::{Method, Request, Response, StatusCode};
use hyper_util::rt::{TokioExecutor, TokioIo};
use scenery_wire::Delivery;
use tokio::net::UnixStream;
use tokio::sync::Mutex;

pub(crate) type Body = Full<Bytes>;

pub(crate) struct Client {
    socket: PathBuf,
    authorization: String,
    sender: Mutex<Option<SendRequest<Body>>>,
}

impl Client {
    pub(crate) fn new(socket: PathBuf, token: &str) -> Self {
        Self {
            socket,
            authorization: format!("Bearer {token}"),
            sender: Mutex::new(None),
        }
    }

    pub(crate) async fn request(
        &self,
        method: Method,
        path: &str,
        data: Vec<u8>,
    ) -> Result<Response<Incoming>> {
        self.exchange(method, path, data)
            .await
            .map_err(|(_, error)| error)
    }

    pub(crate) async fn exchange(
        &self,
        method: Method,
        path: &str,
        data: Vec<u8>,
    ) -> std::result::Result<Response<Incoming>, (Delivery, anyhow::Error)> {
        let request = Request::builder()
            .method(method)
            .uri(format!("http://worker{path}"))
            .header(AUTHORIZATION, &self.authorization)
            .header(CONTENT_TYPE, "application/json")
            .body(Full::new(data.into()))
            .map_err(|error| (Delivery::NotSent, error.into()))?;
        let mut pool = self.sender.lock().await;
        if pool.as_ref().is_none_or(SendRequest::is_closed) {
            let stream = UnixStream::connect(&self.socket)
                .await
                .map_err(|error| (Delivery::NotSent, error.into()))?;
            let (sender, connection) = handshake(TokioExecutor::new(), TokioIo::new(stream))
                .await
                .map_err(|error| (Delivery::NotSent, error.into()))?;
            tokio::spawn(connection);
            *pool = Some(sender);
        }
        let mut sender = pool.as_ref().expect("connection initialized").clone();
        drop(pool);
        sender
            .ready()
            .await
            .map_err(|error| (Delivery::NotSent, error.into()))?;
        sender.try_send_request(request).await.map_err(|mut error| {
            let delivery = if error.take_message().is_some() {
                Delivery::NotSent
            } else {
                Delivery::Unknown
            };
            (delivery, anyhow::anyhow!("{}", error.error()))
        })
    }
}

/// Decides what an `accept` error means for its listener. Running out of
/// descriptors, or a connection that was reset while it waited in the queue,
/// says nothing about the listener: the loop pauses briefly and goes on.
/// Only an error of the listening socket itself ends it.
pub(crate) async fn accept_failed(error: std::io::Error) -> std::io::Result<()> {
    use std::time::{Duration, Instant};
    static REPORTED: std::sync::Mutex<Option<Instant>> = std::sync::Mutex::new(None);
    if matches!(
        error.raw_os_error(),
        Some(libc::EBADF | libc::EINVAL | libc::ENOTSOCK | libc::EOPNOTSUPP)
    ) {
        return Err(error);
    }
    {
        let mut reported = REPORTED.lock().expect("accept log");
        if reported.is_none_or(|at| at.elapsed() >= Duration::from_secs(1)) {
            eprintln!("could not accept a connection: {error}");
            *reported = Some(Instant::now());
        }
    }
    tokio::time::sleep(Duration::from_millis(50)).await;
    Ok(())
}

pub(crate) async fn read(body: Incoming, limit: usize) -> Result<Bytes> {
    Limited::new(body, limit)
        .collect()
        .await
        .map(|body| body.to_bytes())
        .map_err(|error| anyhow::anyhow!("{error}"))
}

/// Reads a whole request body and then lifts the connection's read
/// deadline, which bounds the upload and not the work that follows it.
pub(crate) async fn read_request(request: Request<Incoming>, limit: usize) -> Result<Bytes> {
    let (parts, body) = request.into_parts();
    let result = read(body, limit).await;
    crate::http_timeout::request_body_finished(&parts.extensions);
    result
}

pub(crate) fn response(status: StatusCode, bytes: impl Into<Bytes>) -> Response<Body> {
    let mut response = Response::new(Full::new(bytes.into()));
    *response.status_mut() = status;
    response
}

pub(crate) async fn read_response(
    mut response: reqwest::Response,
    limit: usize,
) -> Result<Vec<u8>> {
    let mut bytes = Vec::new();
    while let Some(chunk) = response.chunk().await? {
        anyhow::ensure!(
            chunk.len() <= limit - bytes.len(),
            "response limit exceeded"
        );
        bytes.extend_from_slice(&chunk);
    }
    Ok(bytes)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn accept_errors_end_a_listener_only_when_its_socket_is_broken() {
        // Descriptor exhaustion and a connection reset in the accept queue
        // used to stop the whole host.
        for transient in [
            libc::EMFILE,
            libc::ENFILE,
            libc::ECONNABORTED,
            libc::ENOBUFS,
        ] {
            let started = std::time::Instant::now();
            assert!(
                accept_failed(std::io::Error::from_raw_os_error(transient))
                    .await
                    .is_ok()
            );
            assert!(
                started.elapsed() >= std::time::Duration::from_millis(40),
                "a failing accept must not spin"
            );
        }
        for fatal in [libc::EBADF, libc::EINVAL, libc::ENOTSOCK] {
            let error = accept_failed(std::io::Error::from_raw_os_error(fatal)).await;
            assert_eq!(error.unwrap_err().raw_os_error(), Some(fatal));
        }
    }
}
