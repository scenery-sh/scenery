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

pub(crate) async fn read(body: Incoming, limit: usize) -> Result<Bytes> {
    Limited::new(body, limit)
        .collect()
        .await
        .map(|body| body.to_bytes())
        .map_err(|error| anyhow::anyhow!("{error}"))
}

pub(crate) fn response(status: StatusCode, bytes: impl Into<Bytes>) -> Response<Body> {
    let mut response = Response::new(Full::new(bytes.into()));
    *response.status_mut() = status;
    response
}
