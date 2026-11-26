use std::io;
use std::pin::Pin;
use std::sync::Arc;
use std::task::{Context, Poll};

use bytes::Bytes;
use http_body_util::BodyExt;
use hyper::body::{Body, Frame};
use hyper::{HeaderMap, StatusCode};
use scenery_wire::RawEvent;
use tokio_util::sync::WaitForCancellationFutureOwned;

use crate::abi::EventStream;
use crate::host::{Fault, Host, Reply, empty, header};
use crate::http_codec::{negotiate_media, not_acceptable};
use crate::plan::ResponsePlan;
use crate::values::{self, Types};

/// Encodes a `delivery = "stream_out"` worker event stream as a typed `text/event-stream` HTTP response.
pub(crate) fn encode_sse_response(
    host: &Arc<Host>,
    plan: &ResponsePlan,
    event_type: String,
    events: EventStream,
    request: &HeaderMap,
) -> Result<Reply, Fault> {
    let status = StatusCode::from_u16(plan.status).map_err(Fault::internal)?;
    let media = negotiate_media(request, &plan.media).ok_or_else(not_acceptable)?;
    let mut response = empty(status);
    let content_type = if media.contains(';') {
        media.to_owned()
    } else {
        format!("{media}; charset=utf-8")
    };
    header(response.headers_mut(), "content-type", &content_type);
    header(response.headers_mut(), "cache-control", "no-store");
    header(response.headers_mut(), "x-accel-buffering", "no");
    let max_bytes = usize::try_from(plan.options.max_bytes).unwrap_or(usize::MAX);
    let body = SseBody {
        host: host.clone(),
        event_type,
        events,
        max_bytes,
        stop: Box::pin(host.stop.clone().cancelled_owned()),
        finished: false,
    };
    *response.body_mut() = body.boxed_unsync();
    Ok(response)
}

struct SseBody {
    host: Arc<Host>,
    event_type: String,
    events: EventStream,
    max_bytes: usize,
    stop: Pin<Box<WaitForCancellationFutureOwned>>,
    finished: bool,
}

impl Body for SseBody {
    type Data = Bytes;
    type Error = io::Error;

    fn poll_frame(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<io::Result<Frame<Bytes>>>> {
        if self.finished {
            return Poll::Ready(None);
        }
        if self.stop.as_mut().poll(cx).is_ready() {
            self.finished = true;
            return Poll::Ready(None);
        }
        let max_bytes = self.max_bytes;
        match std::task::ready!(self.events.poll_line(cx, max_bytes)) {
            Ok(Some(line)) => {
                match format_sse_frame(&self.host.bundle.types, &self.event_type, &line, max_bytes) {
                    Ok(frame) => Poll::Ready(Some(Ok(Frame::data(frame)))),
                    Err(error) => {
                        self.finished = true;
                        let _ = self.host.telemetry.failure(&error.to_string());
                        Poll::Ready(Some(Err(io::Error::other("invalid event stream frame"))))
                    }
                }
            }
            Ok(None) => {
                self.finished = true;
                Poll::Ready(None)
            }
            Err(error) => {
                self.finished = true;
                Poll::Ready(Some(Err(error)))
            }
        }
    }
}

/// Validates one NDJSON worker event frame against the declared contract type and formats an SSE message.
pub(crate) fn format_sse_frame(
    types: &Types,
    event_type: &str,
    line: &[u8],
    max_bytes: usize,
) -> anyhow::Result<Bytes> {
    let raw: RawEvent = serde_json::from_slice(line)?;
    if let Some(id) = &raw.id {
        anyhow::ensure!(
            !id.contains(['\r', '\n', '\0']),
            "event stream id contains forbidden control character"
        );
    }
    if let Some(event) = &raw.event {
        anyhow::ensure!(
            !event.is_empty() && !event.contains(['\r', '\n', '\0']),
            "event stream event name is invalid"
        );
    }
    let parsed = values::parse(raw.data.get().as_bytes())?;
    let normalized = values::normalize(types, event_type, parsed)?;
    let encoded = values::encode(&normalized);
    let mut out = String::with_capacity(encoded.len() + 32);
    if let Some(id) = &raw.id {
        out.push_str("id: ");
        out.push_str(id);
        out.push('\n');
    }
    if let Some(event) = &raw.event {
        out.push_str("event: ");
        out.push_str(event);
        out.push('\n');
    }
    out.push_str("data: ");
    out.push_str(&encoded);
    out.push_str("\n\n");
    if max_bytes > 0 && out.len() > max_bytes {
        anyhow::bail!("encoded SSE frame exceeds response body limit");
    }
    Ok(Bytes::from(out))
}

#[cfg(test)]
mod tests {
    use super::*;
    use http_body_util::Full;
    use hyper::body::Incoming;
    use hyper_util::rt::TokioIo;
    use serde_json::json;

    async fn incoming_bytes(data: Bytes) -> anyhow::Result<Incoming> {
        let (client, server) = tokio::io::duplex(64 * 1024);
        let serve = tokio::spawn(async move {
            let service = hyper::service::service_fn(move |_| {
                let data = data.clone();
                async move {
                    Ok::<_, std::convert::Infallible>(hyper::Response::new(Full::new(data)))
                }
            });
            let _ = hyper::server::conn::http1::Builder::new()
                .serve_connection(TokioIo::new(server), service)
                .await;
        });
        let (mut sender, conn) =
            hyper::client::conn::http1::handshake(TokioIo::new(client)).await?;
        let drive = tokio::spawn(conn);
        let response = sender
            .send_request(hyper::Request::new(Full::new(Bytes::new())))
            .await?;
        drop(sender);
        drop(serve);
        drop(drive);
        Ok(response.into_body())
    }

    #[test]
    fn formats_valid_sse_frame_and_rejects_bad_metadata_or_type() {
        let types: Types = serde_json::from_value(json!({
            "app/record/tick": {
                "Kind": "record",
                "Fields": [
                    {"Name": "symbol", "Type": "string", "Constraints": {"Pattern": "", "Format": "", "UniqueItems": false}},
                    {"Name": "seq", "Type": "int64", "Constraints": {"Pattern": "", "Format": "", "UniqueItems": false}}
                ],
                "UnknownFields": "reject",
                "Open": false,
                "Values": [],
                "Discriminator": "",
                "Variants": {}
            }
        }))
        .expect("types");

        let frame = format_sse_frame(
            &types,
            "app/record/tick",
            br#"{"data":{"seq":"42","symbol":"BTC"},"id":"evt-1","event":"tick"}"#,
            4096,
        )
        .expect("valid frame");
        assert_eq!(
            frame.as_ref(),
            b"id: evt-1\nevent: tick\ndata: {\"seq\":\"42\",\"symbol\":\"BTC\"}\n\n"
        );

        let minimal = format_sse_frame(
            &types,
            "app/record/tick",
            br#"{"data":{"symbol":"ETH","seq":"1"}}"#,
            4096,
        )
        .expect("minimal frame");
        assert_eq!(
            minimal.as_ref(),
            b"data: {\"seq\":\"1\",\"symbol\":\"ETH\"}\n\n"
        );

        assert!(
            format_sse_frame(
                &types,
                "app/record/tick",
                br#"{"data":{"symbol":"ETH","seq":"1"},"id":"bad\nid"}"#,
                4096,
            )
            .is_err()
        );
        assert!(
            format_sse_frame(
                &types,
                "app/record/tick",
                br#"{"data":{"symbol":"ETH","seq":"1"},"event":""}"#,
                4096,
            )
            .is_err()
        );
        assert!(
            format_sse_frame(
                &types,
                "app/record/tick",
                br#"{"data":{"symbol":"ETH","seq":"not-an-int"}}"#,
                4096,
            )
            .is_err()
        );
        assert!(
            format_sse_frame(
                &types,
                "app/record/tick",
                br#"{"data":{"symbol":"ETH","seq":"1"},"extra":true}"#,
                4096,
            )
            .is_err()
        );
    }

    #[tokio::test]
    async fn event_stream_splits_lines_and_rejects_truncated_tail() -> anyhow::Result<()> {
        let payload = Bytes::from_static(b"{\"data\":1}\n{\"data\":2}\n");
        let mut stream = EventStream::for_test(incoming_bytes(payload).await?);
        let first = std::future::poll_fn(|cx| stream.poll_line(cx, 1024)).await?;
        let second = std::future::poll_fn(|cx| stream.poll_line(cx, 1024)).await?;
        let end = std::future::poll_fn(|cx| stream.poll_line(cx, 1024)).await?;
        assert_eq!(first.as_deref(), Some(b"{\"data\":1}".as_slice()));
        assert_eq!(second.as_deref(), Some(b"{\"data\":2}".as_slice()));
        assert!(end.is_none());

        let truncated = Bytes::from_static(b"{\"data\":1}\n{\"data\":2}");
        let mut bad = EventStream::for_test(incoming_bytes(truncated).await?);
        assert!(
            std::future::poll_fn(|cx| bad.poll_line(cx, 1024))
                .await?
                .is_some()
        );
        assert!(
            std::future::poll_fn(|cx| bad.poll_line(cx, 1024))
                .await
                .is_err()
        );
        Ok(())
    }
}
