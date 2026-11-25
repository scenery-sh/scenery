use std::future::Future;
use std::io;
use std::pin::Pin;
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll, Waker};
use std::time::Duration;

use hyper::{HeaderMap, StatusCode};
use tokio::io::{AsyncRead, AsyncWrite, ReadBuf};
use tokio::time::{Instant, Sleep};

use crate::plan::Policy;

pub(crate) const DEFAULT_HTTP_READ_TIMEOUT: Duration = Duration::from_secs(30);
pub(crate) const DEFAULT_HTTP_WRITE_TIMEOUT: Duration = Duration::from_secs(30);
pub(crate) const DEFAULT_HTTP_IDLE_TIMEOUT: Duration = Duration::from_secs(120);
pub(crate) const MAX_HEADER_READ_TIMEOUT: Duration = Duration::from_secs(10);

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) struct HttpTimeouts {
    pub header_read: Duration,
    pub read: Duration,
    pub write: Duration,
    pub idle: Duration,
}

struct DeadlineState {
    request_started: Instant,
    read_deadline: Option<Instant>,
    write_deadline: Option<Instant>,
    read_waker: Option<Waker>,
    write_waker: Option<Waker>,
    pending_write_timeout: Duration,
    upgraded: bool,
}

pub(crate) struct DeadlineControl {
    state: Mutex<DeadlineState>,
}

impl DeadlineControl {
    fn new(timeouts: HttpTimeouts) -> Self {
        let now = Instant::now();
        Self {
            state: Mutex::new(DeadlineState {
                request_started: now,
                read_deadline: Some(now + timeouts.header_read),
                write_deadline: Some(now + timeouts.write),
                read_waker: None,
                write_waker: None,
                pending_write_timeout: timeouts.write,
                upgraded: false,
            }),
        }
    }

    pub(crate) fn begin_request(&self, read_timeout: Duration, write_timeout: Duration) {
        let (read_waker, write_waker) = {
            let mut state = self.state.lock().expect("deadline lock");
            if state.upgraded {
                return;
            }
            let now = Instant::now();
            state.request_started = now;
            state.read_deadline = Some(now + read_timeout);
            state.write_deadline = None;
            state.pending_write_timeout = write_timeout;
            (state.read_waker.take(), state.write_waker.take())
        };
        if let Some(waker) = read_waker {
            waker.wake();
        }
        if let Some(waker) = write_waker {
            waker.wake();
        }
    }

    pub(crate) fn apply_policy(&self, policy: &Policy) {
        let read_waker = {
            let mut state = self.state.lock().expect("deadline lock");
            if state.upgraded {
                return;
            }
            state.read_deadline = Some(state.request_started + policy.read_timeout());
            state.pending_write_timeout = policy.write_timeout();
            state.read_waker.take()
        };
        if let Some(waker) = read_waker {
            waker.wake();
        }
    }

    pub(crate) fn begin_response(
        &self,
        status: StatusCode,
        headers: &HeaderMap,
        idle_timeout: Duration,
    ) {
        let (read_waker, write_waker) = {
            let mut state = self.state.lock().expect("deadline lock");
            if status == StatusCode::SWITCHING_PROTOCOLS {
                state.upgraded = true;
                state.read_deadline = None;
                state.write_deadline = None;
                (state.read_waker.take(), state.write_waker.take())
            } else if state.upgraded {
                return;
            } else {
                let sse = headers
                    .get("content-type")
                    .and_then(|value| value.to_str().ok())
                    .is_some_and(|value| {
                        value
                            .trim()
                            .to_ascii_lowercase()
                            .starts_with("text/event-stream")
                    });
                if sse {
                    state.read_deadline = None;
                    state.write_deadline = None;
                } else {
                    let now = Instant::now();
                    state.write_deadline = Some(now + state.pending_write_timeout);
                    state.read_deadline = Some(now + idle_timeout);
                }
                (state.read_waker.take(), state.write_waker.take())
            }
        };
        if let Some(waker) = read_waker {
            waker.wake();
        }
        if let Some(waker) = write_waker {
            waker.wake();
        }
    }

    fn read_deadline_and_register(&self, waker: &Waker) -> Option<Instant> {
        let mut state = self.state.lock().expect("deadline lock");
        state.read_waker = Some(waker.clone());
        state.read_deadline
    }

    fn write_deadline_and_register(&self, waker: &Waker) -> Option<Instant> {
        let mut state = self.state.lock().expect("deadline lock");
        state.write_waker = Some(waker.clone());
        state.write_deadline
    }

    #[cfg(test)]
    fn read_deadline(&self) -> Option<Instant> {
        self.state.lock().expect("deadline lock").read_deadline
    }

    #[cfg(test)]
    fn write_deadline(&self) -> Option<Instant> {
        self.state.lock().expect("deadline lock").write_deadline
    }
}

pub(crate) struct DeadlineStream<S> {
    inner: S,
    control: Arc<DeadlineControl>,
    read_sleep: Pin<Box<Sleep>>,
    read_armed: Option<Instant>,
    write_sleep: Pin<Box<Sleep>>,
    write_armed: Option<Instant>,
}

impl<S> DeadlineStream<S> {
    pub(crate) fn new(inner: S, timeouts: HttpTimeouts) -> (Self, Arc<DeadlineControl>) {
        let control = Arc::new(DeadlineControl::new(timeouts));
        let now = Instant::now();
        let stream = Self {
            inner,
            control: control.clone(),
            read_sleep: Box::pin(tokio::time::sleep_until(now)),
            read_armed: None,
            write_sleep: Box::pin(tokio::time::sleep_until(now)),
            write_armed: None,
        };
        (stream, control)
    }
}

fn poll_deadline(
    deadline: Option<Instant>,
    sleep: &mut Pin<Box<Sleep>>,
    armed: &mut Option<Instant>,
    cx: &mut Context<'_>,
    message: &'static str,
) -> Poll<io::Result<()>> {
    let Some(target) = deadline else {
        *armed = None;
        return Poll::Pending;
    };
    if Instant::now() >= target {
        return Poll::Ready(Err(io::Error::new(io::ErrorKind::TimedOut, message)));
    }
    if *armed != Some(target) {
        sleep.as_mut().reset(target);
        *armed = Some(target);
    }
    match sleep.as_mut().poll(cx) {
        Poll::Ready(()) => Poll::Ready(Err(io::Error::new(io::ErrorKind::TimedOut, message))),
        Poll::Pending => Poll::Pending,
    }
}

impl<S: AsyncRead + Unpin> AsyncRead for DeadlineStream<S> {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        let this = self.as_mut().get_mut();
        match Pin::new(&mut this.inner).poll_read(cx, buf) {
            Poll::Ready(result) => Poll::Ready(result),
            Poll::Pending => poll_deadline(
                this.control.read_deadline_and_register(cx.waker()),
                &mut this.read_sleep,
                &mut this.read_armed,
                cx,
                "http read timeout",
            ),
        }
    }
}

impl<S: AsyncWrite + Unpin> AsyncWrite for DeadlineStream<S> {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        let this = self.as_mut().get_mut();
        match Pin::new(&mut this.inner).poll_write(cx, buf) {
            Poll::Ready(result) => Poll::Ready(result),
            Poll::Pending => match poll_deadline(
                this.control.write_deadline_and_register(cx.waker()),
                &mut this.write_sleep,
                &mut this.write_armed,
                cx,
                "http write timeout",
            ) {
                Poll::Ready(Err(error)) => Poll::Ready(Err(error)),
                Poll::Ready(Ok(())) | Poll::Pending => Poll::Pending,
            },
        }
    }

    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        let this = self.as_mut().get_mut();
        match Pin::new(&mut this.inner).poll_flush(cx) {
            Poll::Ready(result) => Poll::Ready(result),
            Poll::Pending => poll_deadline(
                this.control.write_deadline_and_register(cx.waker()),
                &mut this.write_sleep,
                &mut this.write_armed,
                cx,
                "http write timeout",
            ),
        }
    }

    fn poll_shutdown(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.get_mut().inner).poll_shutdown(cx)
    }
}

#[cfg(test)]
mod tests {
    use std::convert::Infallible;

    use bytes::Bytes;
    use http_body_util::{BodyExt, Full};
    use hyper::body::Incoming;
    use hyper::header::HeaderValue;
    use hyper::service::service_fn;
    use hyper::{Request, Response, StatusCode};
    use hyper_util::rt::TokioIo;
    use serde_json::json;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    use super::*;

    #[tokio::test]
    async fn slow_request_body_trickle_times_out_at_read_deadline() {
        let timeouts = HttpTimeouts {
            header_read: Duration::from_secs(2),
            read: Duration::from_millis(90),
            write: Duration::from_secs(2),
            idle: Duration::from_secs(5),
        };
        let (server_io, mut client_io) = tokio::io::duplex(1024);
        let (stream, control) = DeadlineStream::new(server_io, timeouts);
        let server = tokio::spawn(async move {
            let service = service_fn(move |request: Request<Incoming>| {
                let control = control.clone();
                control.begin_request(timeouts.read, timeouts.write);
                async move {
                    let result = request.into_body().collect().await;
                    let status = if result.is_ok() {
                        StatusCode::OK
                    } else {
                        StatusCode::REQUEST_TIMEOUT
                    };
                    let response = Response::builder()
                        .status(status)
                        .body(Full::new(Bytes::new()))
                        .unwrap();
                    control.begin_response(response.status(), response.headers(), timeouts.idle);
                    Ok::<_, Infallible>(response)
                }
            });
            let mut builder = hyper::server::conn::http1::Builder::new();
            builder
                .timer(hyper_util::rt::TokioTimer::new())
                .header_read_timeout(timeouts.header_read);
            let _ = builder
                .serve_connection(TokioIo::new(stream), service)
                .await;
        });

        client_io
            .write_all(b"POST /auth/login/email HTTP/1.1\r\nHost: localhost\r\nContent-Length: 512\r\n\r\n")
            .await
            .unwrap();
        let started = Instant::now();
        let mut trickle_failed = false;
        for _ in 0..20 {
            tokio::time::sleep(Duration::from_millis(20)).await;
            if client_io.write_all(b"a").await.is_err() {
                trickle_failed = true;
                break;
            }
        }
        let elapsed = started.elapsed();
        assert!(
            tokio::time::timeout(Duration::from_secs(1), server)
                .await
                .is_ok(),
            "server task must terminate after read timeout"
        );
        assert!(
            trickle_failed || elapsed < Duration::from_millis(500),
            "trickled request body must be aborted promptly (elapsed={elapsed:?})"
        );
    }

    #[tokio::test]
    async fn declared_route_read_timeout_overrides_long_invocation_timeout() {
        let policy: Policy = serde_json::from_value(json!({
            "BindingAddress": "slow/binding",
            "AuthorizationStrategy": "public",
            "AuthorizationRules": [],
            "TotalInvocationTimeoutNanos": 2_400_000_000_000u64,
            "ReadTimeoutNanos": 80_000_000u64,
            "WriteTimeoutNanos": 500_000_000u64,
            "IdleTimeoutNanos": 5_000_000_000u64,
            "MaxRequestHeaderBytes": 0,
            "CORS": "none",
            "AllowedOrigins": [],
            "TransportStatuses": {},
        }))
        .unwrap();
        assert_eq!(policy.read_timeout(), Duration::from_millis(80));
        assert_eq!(policy.write_timeout(), Duration::from_millis(500));

        let timeouts = HttpTimeouts {
            header_read: Duration::from_secs(2),
            read: Duration::from_secs(30),
            write: Duration::from_secs(30),
            idle: Duration::from_secs(5),
        };
        let (server_io, mut client_io) = tokio::io::duplex(1024);
        let (stream, control) = DeadlineStream::new(server_io, timeouts);
        let (tx, rx) = tokio::sync::oneshot::channel();
        let server = tokio::spawn(async move {
            let tx = Arc::new(Mutex::new(Some(tx)));
            let service = service_fn(move |request: Request<Incoming>| {
                let control = control.clone();
                let policy = policy.clone();
                let tx = tx.clone();
                control.begin_request(timeouts.read, timeouts.write);
                async move {
                    control.apply_policy(&policy);
                    let err = request.into_body().collect().await.is_err();
                    if let Some(sender) = tx.lock().unwrap().take() {
                        let _ = sender.send(err);
                    }
                    Ok::<_, Infallible>(Response::new(Full::new(Bytes::new())))
                }
            });
            let _ = hyper::server::conn::http1::Builder::new()
                .timer(hyper_util::rt::TokioTimer::new())
                .serve_connection(TokioIo::new(stream), service)
                .await;
        });

        client_io
            .write_all(
                b"POST /api/upload HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\n\r\n",
            )
            .await
            .unwrap();
        let failed = tokio::time::timeout(Duration::from_millis(500), rx)
            .await
            .expect("route read timeout should fire quickly")
            .unwrap();
        assert!(failed);
        drop(client_io);
        let _ = server.await;
    }

    #[tokio::test]
    async fn slow_response_reader_times_out_at_write_deadline() {
        let timeouts = HttpTimeouts {
            header_read: Duration::from_secs(2),
            read: Duration::from_secs(2),
            write: Duration::from_millis(90),
            idle: Duration::from_secs(5),
        };
        // Small duplex buffer forces write backpressure when the client stops draining.
        let (server_io, mut client_io) = tokio::io::duplex(256);
        let (stream, control) = DeadlineStream::new(server_io, timeouts);
        let server = tokio::spawn(async move {
            let service = service_fn(move |_request: Request<Incoming>| {
                let control = control.clone();
                control.begin_request(timeouts.read, timeouts.write);
                async move {
                    let payload = Bytes::from(vec![b'x'; 64 * 1024]);
                    let response = Response::new(Full::new(payload));
                    control.begin_response(response.status(), response.headers(), timeouts.idle);
                    Ok::<_, Infallible>(response)
                }
            });
            let _ = hyper::server::conn::http1::Builder::new()
                .timer(hyper_util::rt::TokioTimer::new())
                .serve_connection(TokioIo::new(stream), service)
                .await;
        });

        client_io
            .write_all(b"GET /api/download HTTP/1.1\r\nHost: localhost\r\n\r\n")
            .await
            .unwrap();
        let mut head = [0u8; 32];
        let _ = client_io.read(&mut head).await.unwrap();
        // Client intentionally stops reading; server write deadline must abort the connection.
        assert!(
            tokio::time::timeout(Duration::from_millis(600), server)
                .await
                .is_ok(),
            "server connection must close when response write timeout expires"
        );
    }

    #[test]
    fn websocket_upgrade_and_sse_disable_write_deadline() {
        let timeouts = HttpTimeouts {
            header_read: Duration::from_secs(10),
            read: Duration::from_secs(30),
            write: Duration::from_secs(30),
            idle: Duration::from_secs(120),
        };
        let control = DeadlineControl::new(timeouts);
        let mut sse_headers = HeaderMap::new();
        sse_headers.insert(
            "content-type",
            HeaderValue::from_static("text/event-stream; charset=utf-8"),
        );
        control.begin_request(timeouts.read, timeouts.write);
        control.begin_response(StatusCode::OK, &sse_headers, timeouts.idle);
        assert!(control.write_deadline().is_none());
        assert!(control.read_deadline().is_none());

        control.begin_request(timeouts.read, timeouts.write);
        control.begin_response(
            StatusCode::SWITCHING_PROTOCOLS,
            &HeaderMap::new(),
            timeouts.idle,
        );
        assert!(control.write_deadline().is_none());
        assert!(control.read_deadline().is_none());
    }
}
