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
    header_read: Duration,
    pending_write_timeout: Duration,
    connection_idle: Duration,
    /// Set between response completion and the first byte of the next HTTP
    /// request so idle keep-alive waits for `connection_idle` and switches
    /// back to `header_read` once header bytes arrive.
    awaiting_next_request: bool,
    /// Set once the request body has been consumed: the read deadline
    /// bounds the upload, not the handler that runs after it.
    body_finished: bool,
    response_body_ended: bool,
    unflushed_writes: bool,
    event_stream: bool,
    upgrade_pending: bool,
    /// The inactivity allowance of the response in flight, if it has one.
    idle_timeout: Option<Duration>,
    upgraded: bool,
}

pub(crate) struct DeadlineControl {
    state: Mutex<DeadlineState>,
    notify: tokio::sync::Notify,
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
                header_read: timeouts.header_read,
                pending_write_timeout: timeouts.write,
                connection_idle: timeouts.idle,
                awaiting_next_request: false,
                body_finished: false,
                response_body_ended: false,
                unflushed_writes: false,
                event_stream: false,
                upgrade_pending: false,
                idle_timeout: None,
                upgraded: false,
            }),
            notify: tokio::sync::Notify::new(),
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
            state.write_deadline = Some(now + write_timeout);
            state.pending_write_timeout = write_timeout;
            state.awaiting_next_request = false;
            state.body_finished = false;
            state.response_body_ended = false;
            state.unflushed_writes = false;
            state.event_stream = false;
            state.upgrade_pending = false;
            state.idle_timeout = None;
            (state.read_waker.take(), state.write_waker.take())
        };
        if let Some(waker) = read_waker {
            waker.wake();
        }
        if let Some(waker) = write_waker {
            waker.wake();
        }
        self.notify.notify_waiters();
    }

    pub(crate) fn apply_policy(&self, policy: &Policy) {
        let (read_waker, write_waker) = {
            let mut state = self.state.lock().expect("deadline lock");
            if state.upgraded {
                return;
            }
            if !state.body_finished {
                state.read_deadline = Some(state.request_started + policy.read_timeout());
            }
            state.pending_write_timeout = policy.write_timeout();
            if state.idle_timeout.is_none() {
                state.write_deadline =
                    Some(state.request_started + policy.pre_response_write_timeout());
            }
            (state.read_waker.take(), state.write_waker.take())
        };
        if let Some(waker) = read_waker {
            waker.wake();
        }
        if let Some(waker) = write_waker {
            waker.wake();
        }
        self.notify.notify_waiters();
    }

    /// Disarms the read deadline once the request body is consumed. The
    /// connection is still polled for a client that goes away, and that
    /// poll must not end an invocation that outlives the read timeout.
    pub(crate) fn request_body_finished(&self) {
        let read_waker = {
            let mut state = self.state.lock().expect("deadline lock");
            if state.upgraded || state.idle_timeout.is_some() {
                return;
            }
            state.body_finished = true;
            state.read_deadline = None;
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
            state.connection_idle = idle_timeout;
            state.awaiting_next_request = false;
            let now = Instant::now();
            if state.write_deadline.is_some_and(|deadline| now >= deadline) {
                // Keep the already-expired write deadline armed so `poll_write`
                // aborts instead of flushing late headers or SSE/101 streams.
                (state.read_waker.take(), state.write_waker.take())
            } else if status == StatusCode::SWITCHING_PROTOCOLS {
                state.upgrade_pending = true;
                state.unflushed_writes = true;
                state.response_body_ended = false;
                state.read_deadline = None;
                if state.write_deadline.is_none() {
                    state.write_deadline = Some(now + state.pending_write_timeout);
                }
                (state.read_waker.take(), state.write_waker.take())
            } else if state.upgraded {
                return;
            } else {
                // Event streams stay open across long silences: a client waits
                // on them across quiet pauses while header/chunk flushes stay bounded.
                let events = headers
                    .get("content-type")
                    .and_then(|value| value.to_str().ok())
                    .is_some_and(|value| {
                        let media = value.trim().to_ascii_lowercase();
                        media.starts_with("text/event-stream")
                            || media.starts_with("application/x-ndjson")
                    });
                state.unflushed_writes = true;
                if state.write_deadline.is_none() {
                    state.write_deadline = Some(now + state.pending_write_timeout);
                }
                if events {
                    state.event_stream = true;
                    state.read_deadline = None;
                    state.idle_timeout = None;
                } else {
                    state.event_stream = false;
                    state.read_deadline = Some(now + idle_timeout);
                    state.idle_timeout = Some(idle_timeout);
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
        self.notify.notify_waiters();
    }

    pub(crate) async fn wait_write_expired(&self) {
        loop {
            let notified = self.notify.notified();
            tokio::pin!(notified);
            notified.as_mut().enable();
            let deadline = self.state.lock().expect("deadline lock").write_deadline;
            match deadline {
                Some(target) if Instant::now() >= target => return,
                Some(target) => {
                    tokio::select! {
                        () = tokio::time::sleep_until(target) => {
                            if self.write_expired() {
                                return;
                            }
                        }
                        () = &mut notified => {}
                    }
                }
                None => notified.await,
            }
        }
    }

    fn on_request_bytes_read(&self) {
        let read_waker = {
            let mut state = self.state.lock().expect("deadline lock");
            if state.upgraded || !state.awaiting_next_request {
                return;
            }
            state.awaiting_next_request = false;
            state.read_deadline = Some(Instant::now() + state.header_read);
            state.read_waker.take()
        };
        if let Some(waker) = read_waker {
            waker.wake();
        }
    }

    /// Moves the response deadlines forward after the client accepted
    /// bytes. They bound inactivity, so a slow but live download finishes.
    fn response_progressed(&self) {
        let mut changed = false;
        {
            let mut state = self.state.lock().expect("deadline lock");
            state.unflushed_writes = true;
            if let Some(idle) = state.idle_timeout {
                let now = Instant::now();
                state.write_deadline = Some(now + state.pending_write_timeout);
                state.read_deadline = Some(now + idle);
            } else if (state.event_stream || state.upgrade_pending || state.response_body_ended)
                && !state.upgraded
            {
                changed = state.write_deadline.is_none();
                state.write_deadline = Some(Instant::now() + state.pending_write_timeout);
            }
        }
        if changed {
            self.notify.notify_waiters();
        }
    }

    fn response_body_finished(&self) {
        let (read_waker, changed) = {
            let mut state = self.state.lock().expect("deadline lock");
            if state.upgraded {
                return;
            }
            state.response_body_ended = true;
            if !state.unflushed_writes && !state.upgrade_pending {
                state.idle_timeout = None;
                state.awaiting_next_request = true;
                state.read_deadline = Some(Instant::now() + state.connection_idle);
                (state.read_waker.take(), true)
            } else {
                (None, false)
            }
        };
        if let Some(waker) = read_waker {
            waker.wake();
        }
        if changed {
            self.notify.notify_waiters();
        }
    }

    fn on_response_flushed(&self) {
        let (read_waker, write_waker, changed) = {
            let mut state = self.state.lock().expect("deadline lock");
            state.unflushed_writes = false;
            if state.upgrade_pending {
                state.upgrade_pending = false;
                state.upgraded = true;
                state.response_body_ended = false;
                state.event_stream = false;
                state.idle_timeout = None;
                state.read_deadline = None;
                state.write_deadline = None;
                (state.read_waker.take(), state.write_waker.take(), true)
            } else if state.response_body_ended && !state.upgraded {
                state.response_body_ended = false;
                state.event_stream = false;
                state.write_deadline = None;
                state.idle_timeout = None;
                state.awaiting_next_request = true;
                state.read_deadline = Some(Instant::now() + state.connection_idle);
                (state.read_waker.take(), state.write_waker.take(), true)
            } else if state.event_stream && !state.upgraded {
                let had_deadline = state.write_deadline.take().is_some();
                (None, state.write_waker.take(), had_deadline)
            } else {
                (None, None, false)
            }
        };
        if let Some(waker) = read_waker {
            waker.wake();
        }
        if let Some(waker) = write_waker {
            waker.wake();
        }
        if changed {
            self.notify.notify_waiters();
        }
    }

    fn write_expired(&self) -> bool {
        self.state
            .lock()
            .expect("deadline lock")
            .write_deadline
            .is_some_and(|deadline| Instant::now() >= deadline)
    }

    fn read_deadline_and_register(&self, waker: &Waker) -> Option<Instant> {
        let mut state = self.state.lock().expect("deadline lock");
        state.read_waker = Some(waker.clone());
        state.read_deadline
    }

    fn write_deadline_and_register(&self, waker: &Waker) -> Option<Instant> {
        let (deadline, changed) = {
            let mut state = self.state.lock().expect("deadline lock");
            state.write_waker = Some(waker.clone());
            let mut armed_now = false;
            if state.write_deadline.is_none()
                && !state.upgraded
                && (state.event_stream || state.upgrade_pending || state.response_body_ended)
            {
                state.write_deadline = Some(Instant::now() + state.pending_write_timeout);
                armed_now = true;
            }
            (state.write_deadline, armed_now)
        };
        if changed {
            self.notify.notify_waiters();
        }
        deadline
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

/// Tells the connection of `extensions` that its request body is consumed.
pub(crate) fn request_body_finished(extensions: &hyper::http::Extensions) {
    if let Some(deadlines) = extensions.get::<Arc<DeadlineControl>>() {
        deadlines.request_body_finished();
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
        let before = buf.filled().len();
        match Pin::new(&mut this.inner).poll_read(cx, buf) {
            Poll::Ready(Ok(())) => {
                if buf.filled().len() > before {
                    this.control.on_request_bytes_read();
                }
                Poll::Ready(Ok(()))
            }
            Poll::Ready(Err(error)) => Poll::Ready(Err(error)),
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
        if this.control.write_expired() {
            return Poll::Ready(Err(io::Error::new(
                io::ErrorKind::TimedOut,
                "http write timeout",
            )));
        }
        match Pin::new(&mut this.inner).poll_write(cx, buf) {
            Poll::Ready(result) => {
                if matches!(result, Ok(written) if written > 0) {
                    this.control.response_progressed();
                }
                Poll::Ready(result)
            }
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
        if this.control.write_expired() {
            return Poll::Ready(Err(io::Error::new(
                io::ErrorKind::TimedOut,
                "http write timeout",
            )));
        }
        match Pin::new(&mut this.inner).poll_flush(cx) {
            Poll::Ready(result) => {
                if result.is_ok() {
                    this.control.on_response_flushed();
                }
                Poll::Ready(result)
            }
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

pub(crate) struct ResponseBodyGuard<B> {
    inner: B,
    control: Arc<DeadlineControl>,
    finished: bool,
}

impl<B> ResponseBodyGuard<B> {
    pub(crate) fn new(inner: B, control: Arc<DeadlineControl>) -> Self {
        Self {
            inner,
            control,
            finished: false,
        }
    }

    fn finish(&mut self) {
        if !self.finished {
            self.finished = true;
            self.control.response_body_finished();
        }
    }
}

impl<B> Drop for ResponseBodyGuard<B> {
    fn drop(&mut self) {
        self.finish();
    }
}

impl<B: hyper::body::Body + Unpin> hyper::body::Body for ResponseBodyGuard<B> {
    type Data = B::Data;
    type Error = B::Error;

    fn poll_frame(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<hyper::body::Frame<Self::Data>, Self::Error>>> {
        let this = self.get_mut();
        let result = Pin::new(&mut this.inner).poll_frame(cx);
        if matches!(result, Poll::Ready(None | Some(Err(_)))) {
            this.finish();
        }
        result
    }

    fn is_end_stream(&self) -> bool {
        self.inner.is_end_stream()
    }

    fn size_hint(&self) -> hyper::body::SizeHint {
        self.inner.size_hint()
    }
}

#[cfg(test)]
#[path = "http_timeout_test.rs"]
mod tests;
