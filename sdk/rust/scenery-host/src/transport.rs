use std::io::Write;
use std::path::PathBuf;

use anyhow::Result;
use bytes::Bytes;
use flate2::{Compression, write::GzEncoder};
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

pub(crate) struct ScopedBody {
    state: std::sync::Arc<std::sync::Mutex<ScopedBodyState>>,
    watch: tokio::task::JoinHandle<()>,
}

struct ScopedBodyState {
    inner: Option<crate::host::Body>,
    waker: Option<std::task::Waker>,
    ended: bool,
}

impl ScopedBody {
    pub(crate) fn boxed(
        inner: crate::host::Body,
        cancel: tokio_util::sync::CancellationToken,
        deadline: tokio::time::Instant,
    ) -> crate::host::Body {
        let state = std::sync::Arc::new(std::sync::Mutex::new(ScopedBodyState {
            inner: Some(inner),
            waker: None,
            ended: false,
        }));
        let watcher = state.clone();
        let watch = tokio::spawn(async move {
            crate::abi::cancelled(cancel, deadline).await;
            let (dropped, waker) = {
                let mut state = watcher.lock().expect("scoped body");
                state.ended = true;
                (state.inner.take(), state.waker.take())
            };
            drop(dropped);
            if let Some(waker) = waker {
                waker.wake();
            }
        });
        BodyExt::boxed_unsync(Self { state, watch })
    }
}

impl Drop for ScopedBody {
    fn drop(&mut self) {
        self.watch.abort();
        let dropped = self.state.lock().expect("scoped body").inner.take();
        drop(dropped);
    }
}

impl hyper::body::Body for ScopedBody {
    type Data = Bytes;
    type Error = std::io::Error;

    fn poll_frame(
        self: std::pin::Pin<&mut Self>,
        cx: &mut std::task::Context<'_>,
    ) -> std::task::Poll<Option<Result<hyper::body::Frame<Bytes>, std::io::Error>>> {
        let mut guard = self.state.lock().expect("scoped body");
        if guard.ended {
            return std::task::Poll::Ready(Some(Err(std::io::Error::other(
                "capability scope ended",
            ))));
        }
        if guard.inner.is_none() {
            return std::task::Poll::Ready(None);
        }
        guard.waker = Some(cx.waker().clone());
        let inner = guard.inner.as_mut().expect("checked body");
        let result = std::pin::Pin::new(inner).poll_frame(cx);
        if matches!(result, std::task::Poll::Ready(None | Some(Err(_)))) {
            let dropped = guard.inner.take();
            drop(guard);
            drop(dropped);
        }
        result
    }

    fn size_hint(&self) -> hyper::body::SizeHint {
        self.state
            .lock()
            .expect("scoped body")
            .inner
            .as_ref()
            .map_or_else(hyper::body::SizeHint::default, hyper::body::Body::size_hint)
    }
}

pub(crate) struct GzipStream<B> {
    inner: B,
    encoder: Option<GzEncoder<Vec<u8>>>,
    pending_trailers: Option<hyper::body::Frame<Bytes>>,
    finished: bool,
}

impl<B> GzipStream<B> {
    pub(crate) fn new(inner: B) -> Self {
        Self {
            inner,
            encoder: Some(GzEncoder::new(Vec::new(), Compression::default())),
            pending_trailers: None,
            finished: false,
        }
    }
}

impl<B: hyper::body::Body<Data = Bytes> + Unpin> hyper::body::Body for GzipStream<B>
where
    B::Error: Into<std::io::Error>,
{
    type Data = Bytes;
    type Error = std::io::Error;

    fn poll_frame(
        self: std::pin::Pin<&mut Self>,
        cx: &mut std::task::Context<'_>,
    ) -> std::task::Poll<Option<std::io::Result<hyper::body::Frame<Bytes>>>> {
        use std::task::Poll::{Pending, Ready};
        let this = self.get_mut();
        if let Some(trailers) = this.pending_trailers.take() {
            this.finished = true;
            return Ready(Some(Ok(trailers)));
        }
        if this.finished {
            return Ready(None);
        }
        loop {
            match std::pin::Pin::new(&mut this.inner).poll_frame(cx) {
                Pending => return Pending,
                Ready(Some(Ok(frame))) => {
                    if let Some(data) = frame.data_ref() {
                        if data.is_empty() {
                            continue;
                        }
                        let Some(encoder) = this.encoder.as_mut() else {
                            this.finished = true;
                            return Ready(None);
                        };
                        if let Err(error) = encoder.write_all(data) {
                            this.finished = true;
                            return Ready(Some(Err(error)));
                        }
                        let out = std::mem::take(encoder.get_mut());
                        if !out.is_empty() {
                            return Ready(Some(Ok(hyper::body::Frame::data(Bytes::from(out)))));
                        }
                    } else if frame.is_trailers() {
                        if let Some(encoder) = this.encoder.take() {
                            match encoder.finish() {
                                Ok(out) if !out.is_empty() => {
                                    this.pending_trailers = Some(frame);
                                    return Ready(Some(Ok(hyper::body::Frame::data(Bytes::from(
                                        out,
                                    )))));
                                }
                                Err(error) => {
                                    this.finished = true;
                                    return Ready(Some(Err(error)));
                                }
                                Ok(_) => {}
                            }
                        }
                        this.finished = true;
                        return Ready(Some(Ok(frame)));
                    }
                }
                Ready(Some(Err(error))) => {
                    this.finished = true;
                    this.encoder = None;
                    return Ready(Some(Err(error.into())));
                }
                Ready(None) => {
                    this.finished = true;
                    let Some(encoder) = this.encoder.take() else {
                        return Ready(None);
                    };
                    return Ready(match encoder.finish() {
                        Ok(out) if !out.is_empty() => {
                            Some(Ok(hyper::body::Frame::data(Bytes::from(out))))
                        }
                        Ok(_) => return Ready(None),
                        Err(error) => Some(Err(error)),
                    });
                }
            }
        }
    }
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
