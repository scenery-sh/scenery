use std::io;
use std::pin::Pin;
use std::task::{Context, Poll};

use bytes::{Buf, Bytes};
use http_body_util::BodyExt;
use hyper::body::{Body, Frame, Incoming, SizeHint};
use hyper::{Method, StatusCode};
use scenery_wire::{
    CONTRACT_HEADER, Delivery, EVENTS_MEDIA_TYPE, IMPLEMENTATION_HEADER, INVOKE_PATH, Invocation,
    InvocationOutcome, MAX_METADATA, STREAM_MEDIA_TYPE, STREAM_SIZE_HEADER,
};
use serde_json::Value;
use tokio_util::sync::{CancellationToken, WaitForCancellationFutureOwned};

use crate::host::{Fault, ScopeGuard};
use crate::process::Worker;
use crate::transport::read;
use crate::values;

pub(crate) struct Outcome {
    pub kind: String,
    pub name: String,
    pub payload: Value,
    pub stream: Option<Stream>,
    pub events: Option<EventStream>,
    pub worker: Option<(u32, String)>,
}

impl Outcome {
    pub(crate) fn is_streamed(&self) -> bool {
        self.stream.is_some() || self.events.is_some()
    }

    pub(crate) fn json(&self) -> Value {
        let key = if self.kind == "result" {
            "value"
        } else {
            "problem"
        };
        serde_json::json!({"kind": self.kind, "name": self.name, key: self.payload})
    }
}

impl Worker {
    pub(crate) async fn invoke(
        &self,
        invitation: Invocation,
        guard: ScopeGuard,
    ) -> Result<Outcome, Fault> {
        let data = serde_json::to_vec(&invitation).map_err(Fault::internal)?;
        if data.len() > MAX_METADATA {
            return Err(Fault::unavailable(Delivery::NotSent));
        }
        let reply = self
            .client
            .exchange(Method::POST, INVOKE_PATH, data)
            .await
            .map_err(|(delivery, _)| Fault::unavailable(delivery))?;
        let identity = &self.launch.identity;
        for (name, expected) in [
            (CONTRACT_HEADER, &identity.contract_revision),
            (IMPLEMENTATION_HEADER, &identity.implementation_revision),
        ] {
            let mut values = reply.headers().get_all(name).iter();
            if !values
                .next()
                .is_some_and(|header| header.as_bytes() == expected.as_bytes())
                || values.next().is_some()
            {
                return Err(Fault::unavailable(Delivery::Unknown));
            }
        }
        match reply.status() {
            StatusCode::OK => {}
            StatusCode::CONFLICT => return Err(Fault::unavailable(Delivery::NotSent)),
            StatusCode::INTERNAL_SERVER_ERROR => {
                return Err(Fault::internal("worker implementation failed"));
            }
            _ => return Err(Fault::unavailable(Delivery::Unknown)),
        }
        let (parts, body) = reply.into_parts();
        let media = parts
            .headers
            .get("content-type")
            .and_then(|header| header.to_str().ok())
            .and_then(|value| value.split(';').next());
        let (data, stream, events) = match media {
            Some("application/json") => {
                let data = read(body, MAX_METADATA)
                    .await
                    .map_err(|_| Fault::unavailable(Delivery::Unknown))?;
                (data, None, None)
            }
            Some(STREAM_MEDIA_TYPE) => {
                let length = parts
                    .headers
                    .get(STREAM_SIZE_HEADER)
                    .and_then(|header| header.to_str().ok())
                    .and_then(|length| length.parse::<u64>().ok())
                    .ok_or_else(|| Fault::unavailable(Delivery::Unknown))?;
                let (data, body, pending) = stream_metadata(body)
                    .await
                    .map_err(|_| Fault::unavailable(Delivery::Unknown))?;
                let cancellation = guard.scope.cancel.clone().cancelled_owned();
                let deadline = Box::pin(tokio::time::sleep_until(guard.scope.call.deadline));
                let stream = Stream {
                    body,
                    pending,
                    remaining: length,
                    finished: false,
                    _scope: guard,
                    cancellation: Box::pin(cancellation),
                    retired: Box::pin(self.retired.clone().cancelled_owned()),
                    deadline,
                };
                (data, Some(stream), None)
            }
            Some(EVENTS_MEDIA_TYPE) => {
                let (data, body, pending) = stream_metadata(body)
                    .await
                    .map_err(|_| Fault::unavailable(Delivery::Unknown))?;
                let cancellation = guard.scope.cancel.clone().cancelled_owned();
                let deadline = Box::pin(tokio::time::sleep_until(guard.scope.call.deadline));
                let events = EventStream {
                    body,
                    pending,
                    buffer: Vec::new(),
                    finished: false,
                    _scope: guard,
                    cancellation: Box::pin(cancellation),
                    retired: Box::pin(self.retired.clone().cancelled_owned()),
                    deadline,
                };
                (data, None, Some(events))
            }
            _ => return Err(Fault::unavailable(Delivery::Unknown)),
        };
        let reply: InvocationOutcome =
            serde_json::from_slice(&data).map_err(|_| Fault::unavailable(Delivery::Unknown))?;
        let (kind, name, payload) = scenery_wire::decode_outcome(&reply.outcome)
            .map_err(|_| Fault::unavailable(Delivery::Unknown))?;
        let payload = values::parse(payload.get().as_bytes()).map_err(Fault::internal)?;
        Ok(Outcome {
            kind,
            name,
            payload,
            stream,
            events,
            worker: Some((self.pid(), identity.implementation_revision.clone())),
        })
    }
}

async fn stream_metadata(mut body: Incoming) -> anyhow::Result<(Bytes, Incoming, Bytes)> {
    async fn take(body: &mut Incoming, pending: &mut Bytes, size: usize) -> anyhow::Result<Bytes> {
        let mut result = Vec::with_capacity(size);
        while result.len() < size {
            if pending.is_empty() {
                let frame = body
                    .frame()
                    .await
                    .ok_or_else(|| anyhow::anyhow!("incomplete stream metadata"))??;
                *pending = frame.into_data().unwrap_or_default();
            }
            let count = (size - result.len()).min(pending.len());
            result.extend_from_slice(&pending[..count]);
            pending.advance(count);
        }
        Ok(result.into())
    }
    let mut pending = Bytes::new();
    let length = take(&mut body, &mut pending, 4).await?.get_u32() as usize;
    anyhow::ensure!(length <= MAX_METADATA, "stream metadata exceeds limit");
    let metadata = take(&mut body, &mut pending, length).await?;
    Ok((metadata, body, pending))
}

pub(crate) struct Stream {
    body: Incoming,
    pending: Bytes,
    remaining: u64,
    finished: bool,
    _scope: ScopeGuard,
    cancellation: Pin<Box<WaitForCancellationFutureOwned>>,
    retired: Pin<Box<WaitForCancellationFutureOwned>>,
    deadline: Pin<Box<tokio::time::Sleep>>,
}

impl Stream {
    pub(crate) fn length(&self) -> u64 {
        self.remaining
    }

    #[cfg(test)]
    pub(crate) fn for_test(body: Incoming, remaining: u64) -> Self {
        let guard = ScopeGuard::test();
        let cancellation = guard.scope.cancel.clone().cancelled_owned();
        let retired = CancellationToken::new().cancelled_owned();
        let deadline = Box::pin(tokio::time::sleep_until(guard.scope.call.deadline));
        Self {
            body,
            pending: Bytes::new(),
            remaining,
            finished: false,
            _scope: guard,
            cancellation: Box::pin(cancellation),
            retired: Box::pin(retired),
            deadline,
        }
    }
}

impl Body for Stream {
    type Data = Bytes;
    type Error = io::Error;

    fn poll_frame(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<io::Result<Frame<Bytes>>>> {
        if self.finished {
            return Poll::Ready(None);
        }
        if self.cancellation.as_mut().poll(cx).is_ready()
            || self.retired.as_mut().poll(cx).is_ready()
            || self.deadline.as_mut().poll(cx).is_ready()
        {
            self.finished = true;
            return Poll::Ready(Some(Err(io::Error::other("stream canceled"))));
        }
        let next = if self.pending.is_empty() {
            std::task::ready!(Pin::new(&mut self.body).poll_frame(cx))
        } else {
            Some(Ok(Frame::data(std::mem::take(&mut self.pending))))
        };
        match next {
            Some(Ok(frame)) => {
                if let Some(data) = frame.data_ref() {
                    let Some(remaining) = self.remaining.checked_sub(data.len() as u64) else {
                        self.finished = true;
                        return Poll::Ready(Some(Err(io::Error::other(
                            "worker stream exceeded its declared length",
                        ))));
                    };
                    self.remaining = remaining;
                }
                Poll::Ready(Some(Ok(frame)))
            }
            result => {
                self.finished = true;
                if let Some(Err(error)) = result {
                    return Poll::Ready(Some(Err(io::Error::other(error))));
                }
                if self.remaining != 0 {
                    return Poll::Ready(Some(Err(io::ErrorKind::UnexpectedEof.into())));
                }
                Poll::Ready(None)
            }
        }
    }

    fn size_hint(&self) -> SizeHint {
        SizeHint::with_exact(self.remaining)
    }
}

/// NDJSON event stream returned by a worker for a `delivery = "stream_out"` binding.
pub(crate) struct EventStream {
    body: Incoming,
    pending: Bytes,
    buffer: Vec<u8>,
    finished: bool,
    _scope: ScopeGuard,
    cancellation: Pin<Box<WaitForCancellationFutureOwned>>,
    retired: Pin<Box<WaitForCancellationFutureOwned>>,
    deadline: Pin<Box<tokio::time::Sleep>>,
}

impl EventStream {
    #[cfg(test)]
    pub(crate) fn for_test(body: Incoming) -> Self {
        let guard = ScopeGuard::test();
        let cancellation = guard.scope.cancel.clone().cancelled_owned();
        let retired = CancellationToken::new().cancelled_owned();
        let deadline = Box::pin(tokio::time::sleep_until(guard.scope.call.deadline));
        Self {
            body,
            pending: Bytes::new(),
            buffer: Vec::new(),
            finished: false,
            _scope: guard,
            cancellation: Box::pin(cancellation),
            retired: Box::pin(retired),
            deadline,
        }
    }

    /// Reads the next newline-delimited JSON frame from the worker, bounding each line by `max_bytes`.
    pub(crate) fn poll_line(
        &mut self,
        cx: &mut Context<'_>,
        max_bytes: usize,
    ) -> Poll<io::Result<Option<Vec<u8>>>> {
        loop {
            if self.finished {
                return Poll::Ready(Ok(None));
            }
            if !self.pending.is_empty() {
                if let Some(pos) = self.pending.iter().position(|&b| b == b'\n') {
                    let chunk = self.pending.split_to(pos + 1);
                    let line_bytes = &chunk[..pos];
                    if max_bytes > 0 && self.buffer.len() + line_bytes.len() > max_bytes {
                        self.finished = true;
                        return Poll::Ready(Err(io::Error::other(
                            "event stream item exceeds response body limit",
                        )));
                    }
                    self.buffer.extend_from_slice(line_bytes);
                    return Poll::Ready(Ok(Some(std::mem::take(&mut self.buffer))));
                }
                if max_bytes > 0 && self.buffer.len() + self.pending.len() > max_bytes {
                    self.finished = true;
                    return Poll::Ready(Err(io::Error::other(
                        "event stream item exceeds response body limit",
                    )));
                }
                self.buffer.extend_from_slice(&self.pending);
                self.pending.clear();
            }
            if self.cancellation.as_mut().poll(cx).is_ready()
                || self.retired.as_mut().poll(cx).is_ready()
                || self.deadline.as_mut().poll(cx).is_ready()
            {
                self.finished = true;
                return Poll::Ready(Err(io::Error::other("event stream canceled")));
            }
            match std::task::ready!(Pin::new(&mut self.body).poll_frame(cx)) {
                Some(Ok(frame)) => {
                    if let Some(data) = frame.into_data().ok().filter(|d| !d.is_empty()) {
                        self.pending = data;
                    }
                }
                Some(Err(error)) => {
                    self.finished = true;
                    return Poll::Ready(Err(io::Error::other(error)));
                }
                None => {
                    self.finished = true;
                    if !self.buffer.is_empty() {
                        return Poll::Ready(Err(io::ErrorKind::UnexpectedEof.into()));
                    }
                    return Poll::Ready(Ok(None));
                }
            }
        }
    }
}

pub(crate) async fn cancelled(token: CancellationToken, deadline: tokio::time::Instant) {
    tokio::select! {
        () = token.cancelled_owned() => {}
        () = tokio::time::sleep_until(deadline) => {}
    }
}
