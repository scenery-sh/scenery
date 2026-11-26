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
        let _ = public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await;
    });

    client_io
        .write_all(
            b"POST /auth/login/email HTTP/1.1\r\nHost: localhost\r\nContent-Length: 512\r\n\r\n",
        )
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
        let _ = public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await;
    });

    client_io
        .write_all(b"POST /api/upload HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\n\r\n")
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
        let _ = public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await;
    });

    client_io
        .write_all(b"GET /api/download HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    let mut head = [0u8; 32];
    let _ = client_io.read(&mut head).await.unwrap();
    assert!(
        tokio::time::timeout(Duration::from_millis(600), server)
            .await
            .is_ok(),
        "server connection must close when response write timeout expires"
    );
}

#[tokio::test]
async fn buffered_large_body_with_guard_enforces_write_deadline_and_slow_live_download() {
    // Non-reading client must time out at write_timeout even though ResponseBodyGuard drains Full<Bytes>.
    let timeouts = HttpTimeouts {
        header_read: Duration::from_secs(2),
        read: Duration::from_secs(2),
        write: Duration::from_millis(100),
        idle: Duration::from_secs(600),
    };
    let (server_io, mut client_io) = tokio::io::duplex(1024);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let server = tokio::spawn(async move {
        let service = service_fn(move |_request: Request<Incoming>| {
            let control = control.clone();
            control.begin_request(timeouts.read, timeouts.write);
            async move {
                control.request_body_finished();
                let payload = Bytes::from(vec![b'x'; 256 * 1024]);
                let response = Response::new(Full::new(payload));
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, Infallible>(response.map(|body| ResponseBodyGuard::new(body, control)))
            }
        });
        let _ = public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await;
    });
    client_io
        .write_all(b"GET /large HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    let mut head = [0u8; 32];
    let _ = client_io.read(&mut head).await.unwrap();
    assert!(
        tokio::time::timeout(Duration::from_millis(600), server)
            .await
            .is_ok(),
        "server did not enforce write timeout for buffered body behind ResponseBodyGuard"
    );

    // Slow live download with ResponseBodyGuard keeps resetting idle timeout until flushed.
    let live_timeouts = HttpTimeouts {
        header_read: Duration::from_secs(2),
        read: Duration::from_secs(2),
        write: Duration::from_secs(2),
        idle: Duration::from_millis(300),
    };
    let (server_io, mut client_io) = tokio::io::duplex(1024);
    let (stream, control) = DeadlineStream::new(server_io, live_timeouts);
    let server = tokio::spawn(async move {
        let service = service_fn(move |_request: Request<Incoming>| {
            let control = control.clone();
            control.begin_request(live_timeouts.read, live_timeouts.write);
            async move {
                control.request_body_finished();
                let payload = Bytes::from(vec![b'y'; 256 * 1024]);
                let response = Response::new(Full::new(payload));
                control.begin_response(response.status(), response.headers(), live_timeouts.idle);
                Ok::<_, Infallible>(response.map(|body| ResponseBodyGuard::new(body, control)))
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });
    client_io
        .write_all(b"GET /large HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
        .await
        .unwrap();
    let mut reply = Vec::new();
    let mut chunk = [0u8; 4096];
    loop {
        tokio::time::sleep(Duration::from_millis(15)).await;
        let read = client_io.read(&mut chunk).await.unwrap();
        if read == 0 {
            break;
        }
        reply.extend_from_slice(&chunk[..read]);
    }
    let body_offset = reply
        .windows(4)
        .position(|window| window == b"\r\n\r\n")
        .expect("HTTP response separator")
        + 4;
    assert_eq!(reply.len() - body_offset, 256 * 1024);
    assert!(server.await.unwrap().is_ok());
}

#[tokio::test]
async fn handler_outlives_the_read_timeout_once_the_body_is_read() {
    let timeouts = HttpTimeouts {
        header_read: Duration::from_secs(2),
        read: Duration::from_millis(80),
        write: Duration::from_secs(2),
        idle: Duration::from_secs(5),
    };
    let (server_io, mut client_io) = tokio::io::duplex(1024);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let server = tokio::spawn(async move {
        let service = service_fn(move |mut request: Request<Incoming>| {
            let control = control.clone();
            control.begin_request(timeouts.read, timeouts.write);
            request.extensions_mut().insert(control.clone());
            async move {
                let body = crate::transport::read_request(request, 64).await.unwrap();
                tokio::time::sleep(Duration::from_millis(400)).await;
                let response = Response::new(Full::new(body));
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, Infallible>(response)
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });
    client_io
        .write_all(b"POST /slow HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: 2\r\n\r\nok")
        .await
        .unwrap();
    let mut reply = Vec::new();
    client_io.read_to_end(&mut reply).await.unwrap();
    let reply = String::from_utf8(reply).unwrap();
    assert!(reply.starts_with("HTTP/1.1 200 OK"), "{reply}");
    assert!(reply.ends_with("ok"), "{reply}");
    assert!(server.await.unwrap().is_ok());
}

#[tokio::test]
async fn slow_but_live_download_outlasts_the_write_and_idle_timeouts() {
    let timeouts = HttpTimeouts {
        header_read: Duration::from_secs(2),
        read: Duration::from_secs(2),
        write: Duration::from_millis(120),
        idle: Duration::from_millis(120),
    };
    let (server_io, mut client_io) = tokio::io::duplex(256);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let server = tokio::spawn(async move {
        let service = service_fn(move |_request: Request<Incoming>| {
            let control = control.clone();
            control.begin_request(timeouts.read, timeouts.write);
            async move {
                control.request_body_finished();
                let response = Response::new(Full::new(Bytes::from(vec![b'x'; 4096])));
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, Infallible>(response)
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });
    client_io
        .write_all(b"GET /download HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
        .await
        .unwrap();
    let mut received = 0;
    let mut chunk = [0u8; 256];
    loop {
        tokio::time::sleep(Duration::from_millis(30)).await;
        let read = client_io.read(&mut chunk).await.unwrap();
        if read == 0 {
            break;
        }
        received += read;
    }
    assert!(received > 4096, "download was cut after {received} bytes");
    assert!(server.await.unwrap().is_ok());
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
    assert!(control.write_deadline().is_some());
    assert!(control.read_deadline().is_none());
    control.on_response_flushed();
    assert!(control.write_deadline().is_none());
    assert!(control.read_deadline().is_none());

    let mut ndjson_headers = HeaderMap::new();
    ndjson_headers.insert(
        "content-type",
        HeaderValue::from_static("application/x-ndjson; charset=utf-8"),
    );
    control.begin_request(timeouts.read, timeouts.write);
    control.begin_response(StatusCode::OK, &ndjson_headers, timeouts.idle);
    assert!(control.write_deadline().is_some());
    assert!(control.read_deadline().is_none());
    control.on_response_flushed();
    assert!(control.write_deadline().is_none());
    assert!(control.read_deadline().is_none());

    control.begin_request(timeouts.read, timeouts.write);
    assert!(control.read_deadline().is_some());
    control.request_body_finished();
    assert!(control.read_deadline().is_none());
    control.begin_response(StatusCode::OK, &HeaderMap::new(), timeouts.idle);
    assert!(control.write_deadline().is_some());
    assert!(control.read_deadline().is_some());

    control.begin_request(timeouts.read, timeouts.write);
    control.begin_response(
        StatusCode::SWITCHING_PROTOCOLS,
        &HeaderMap::new(),
        timeouts.idle,
    );
    assert!(control.write_deadline().is_some());
    assert!(control.read_deadline().is_none());
    control.on_response_flushed();
    assert!(control.write_deadline().is_none());
    assert!(control.read_deadline().is_none());
}

#[tokio::test]
async fn stalled_handler_exceeding_write_timeout_fails_before_writing_headers() {
    let timeouts = HttpTimeouts {
        header_read: Duration::from_secs(2),
        read: Duration::from_secs(2),
        write: Duration::from_millis(80),
        idle: Duration::from_secs(2),
    };
    let (server_io, mut client_io) = tokio::io::duplex(4096);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let server = tokio::spawn(async move {
        let service = service_fn(move |_request: Request<Incoming>| {
            let control = control.clone();
            control.begin_request(timeouts.read, timeouts.write);
            async move {
                control.request_body_finished();
                tokio::select! {
                    () = tokio::time::sleep(Duration::from_millis(200)) => {}
                    () = control.wait_write_expired() => {
                        return Err(io::Error::new(io::ErrorKind::TimedOut, "http write timeout"));
                    }
                }
                let mut response = Response::new(Full::new(Bytes::from_static(b"data: ok\n\n")));
                response.headers_mut().insert(
                    "content-type",
                    HeaderValue::from_static("text/event-stream"),
                );
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, io::Error>(response)
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });

    client_io
        .write_all(b"GET /stalled-sse HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    let mut reply = Vec::new();
    let outcome = tokio::time::timeout(
        Duration::from_millis(500),
        client_io.read_to_end(&mut reply),
    )
    .await
    .expect("stalled handler did not fail within write timeout");
    assert!(outcome.is_err() || reply.is_empty());
    assert!(!reply.windows(6).any(|window| window == b"200 OK"));
    assert!(server.await.unwrap().is_err());

    // Even if a late SSE response reaches begin_response directly after expiry,
    // its expired write_deadline stays intact.
    let expired = DeadlineControl::new(timeouts);
    expired.begin_request(timeouts.read, Duration::from_millis(10));
    tokio::time::sleep(Duration::from_millis(25)).await;
    let mut sse_headers = HeaderMap::new();
    sse_headers.insert(
        "content-type",
        HeaderValue::from_static("text/event-stream"),
    );
    expired.begin_response(StatusCode::OK, &sse_headers, timeouts.idle);
    assert!(expired.write_deadline().is_some());
}

#[tokio::test]
async fn sse_response_completion_rearms_idle_read_deadline_on_keep_alive() {
    let timeouts = HttpTimeouts {
        header_read: Duration::from_secs(2),
        read: Duration::from_secs(2),
        write: Duration::from_secs(2),
        idle: Duration::from_millis(80),
    };
    let (server_io, mut client_io) = tokio::io::duplex(4096);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let server = tokio::spawn(async move {
        let service = service_fn(move |_request: Request<Incoming>| {
            let control = control.clone();
            control.begin_request(timeouts.read, timeouts.write);
            async move {
                let mut response = Response::new(Full::new(Bytes::from_static(b"data: done\n\n")));
                response.headers_mut().insert(
                    "content-type",
                    HeaderValue::from_static("text/event-stream"),
                );
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, Infallible>(response.map(|body| ResponseBodyGuard::new(body, control)))
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });

    client_io
        .write_all(b"GET /events HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    let mut buffer = [0u8; 256];
    let mut received = Vec::new();
    while !received
        .windows(12)
        .any(|window| window == b"data: done\n\n")
    {
        let n = client_io.read(&mut buffer).await.unwrap();
        assert!(n > 0, "unexpected EOF before SSE payload arrived");
        received.extend_from_slice(&buffer[..n]);
    }
    assert!(
        tokio::time::timeout(Duration::from_millis(500), server)
            .await
            .is_ok(),
        "idle keep-alive connection after SSE did not time out"
    );
}

#[tokio::test]
async fn keep_alive_idle_outlives_header_read_then_enforces_header_read_on_next_request() {
    let timeouts = HttpTimeouts {
        header_read: Duration::from_millis(80),
        read: Duration::from_secs(2),
        write: Duration::from_secs(2),
        idle: Duration::from_millis(450),
    };
    let (server_io, mut client_io) = tokio::io::duplex(4096);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let server = tokio::spawn(async move {
        let service = service_fn(move |_request: Request<Incoming>| {
            let control = control.clone();
            control.begin_request(timeouts.read, timeouts.write);
            async move {
                let mut response = Response::new(Full::new(Bytes::from_static(b"data: ok\n\n")));
                response.headers_mut().insert(
                    "content-type",
                    HeaderValue::from_static("text/event-stream"),
                );
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, Infallible>(response.map(|body| ResponseBodyGuard::new(body, control)))
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });

    client_io
        .write_all(b"GET /events HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    let mut buffer = [0u8; 512];
    let mut received = Vec::new();
    while !received.windows(10).any(|window| window == b"data: ok\n\n") {
        let n = client_io.read(&mut buffer).await.unwrap();
        assert!(n > 0);
        received.extend_from_slice(&buffer[..n]);
    }

    // Pause longer than header_read (80ms) but shorter than idle (450ms).
    tokio::time::sleep(Duration::from_millis(180)).await;
    client_io
        .write_all(b"GET /events HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    received.clear();
    while !received.windows(10).any(|window| window == b"data: ok\n\n") {
        let n = client_io.read(&mut buffer).await.unwrap();
        assert!(n > 0, "keep-alive closed prematurely after header_read");
        received.extend_from_slice(&buffer[..n]);
    }

    // Start a partial third request after idle pause: header_read (80ms) must cut it before 450ms idle.
    tokio::time::sleep(Duration::from_millis(120)).await;
    let partial_started = Instant::now();
    client_io
        .write_all(b"GET /stall HTTP/1.1\r\nHost: loc")
        .await
        .unwrap();
    let mut tail = Vec::new();
    let eof = tokio::time::timeout(Duration::from_millis(280), client_io.read_to_end(&mut tail))
        .await
        .expect("partial second request headers should time out at header_read");
    assert!(eof.is_ok());
    assert!(partial_started.elapsed() < Duration::from_millis(280));
    let _ = server.await;
}

#[tokio::test]
async fn sse_stalled_headers_time_out_while_quiet_stream_stays_open() {
    // Client stops reading before large SSE response headers flush -> write_timeout aborts.
    let timeouts = HttpTimeouts {
        header_read: Duration::from_secs(2),
        read: Duration::from_secs(2),
        write: Duration::from_millis(80),
        idle: Duration::from_secs(5),
    };
    let (server_io, client_io) = tokio::io::duplex(64);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let server = tokio::spawn(async move {
        let service = service_fn(move |_request: Request<Incoming>| {
            let control = control.clone();
            control.begin_request(timeouts.read, timeouts.write);
            async move {
                let mut response = Response::new(Full::new(Bytes::from_static(b"data: 1\n\n")));
                response.headers_mut().insert(
                    "content-type",
                    HeaderValue::from_static("text/event-stream"),
                );
                response
                    .headers_mut()
                    .insert("x-pad", HeaderValue::from_str(&"a".repeat(512)).unwrap());
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, Infallible>(response.map(|body| ResponseBodyGuard::new(body, control)))
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });
    let mut client_io = client_io;
    client_io
        .write_all(b"GET /sse HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    assert!(
        tokio::time::timeout(Duration::from_millis(500), server)
            .await
            .is_ok(),
        "stalled SSE header write did not time out"
    );
    drop(client_io);

    // Quiet pause between two flushed SSE events outlives write_timeout (80ms).
    let timeouts = HttpTimeouts {
        header_read: Duration::from_secs(2),
        read: Duration::from_secs(2),
        write: Duration::from_millis(80),
        idle: Duration::from_millis(80),
    };
    let (server_io, mut client_io) = tokio::io::duplex(4096);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let (tx, rx) =
        tokio::sync::mpsc::unbounded_channel::<Result<hyper::body::Frame<Bytes>, io::Error>>();
    let server = tokio::spawn(async move {
        let rx = std::sync::Mutex::new(Some(rx));
        let service = service_fn(move |_request: Request<Incoming>| {
            let control = control.clone();
            let mut rx = rx.lock().unwrap().take().unwrap();
            control.begin_request(timeouts.read, timeouts.write);
            async move {
                let body =
                    http_body_util::StreamBody::new(futures_util::stream::poll_fn(move |cx| {
                        rx.poll_recv(cx)
                    }));
                let mut response = Response::new(body);
                response.headers_mut().insert(
                    "content-type",
                    HeaderValue::from_static("text/event-stream"),
                );
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, Infallible>(response.map(|body| ResponseBodyGuard::new(body, control)))
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });

    client_io
        .write_all(b"GET /sse HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
        .await
        .unwrap();
    tx.send(Ok(hyper::body::Frame::data(Bytes::from_static(
        b"data: one\n\n",
    ))))
    .unwrap();
    let mut buf = [0u8; 512];
    let mut seen = Vec::new();
    while !seen.windows(11).any(|w| w == b"data: one\n\n") {
        let n = client_io.read(&mut buf).await.unwrap();
        assert!(n > 0);
        seen.extend_from_slice(&buf[..n]);
    }
    tokio::time::sleep(Duration::from_millis(200)).await;
    tx.send(Ok(hyper::body::Frame::data(Bytes::from_static(
        b"data: two\n\n",
    ))))
    .unwrap();
    drop(tx);
    client_io.read_to_end(&mut seen).await.unwrap();
    let text = String::from_utf8_lossy(&seen);
    assert!(text.contains("data: two\n\n"), "{text}");
    assert!(server.await.unwrap().is_ok());
}

#[tokio::test(start_paused = true)]
async fn keep_alive_idle_outlives_30s_hyper_default_up_to_120s() {
    let timeouts = HttpTimeouts {
        header_read: Duration::from_secs(10),
        read: Duration::from_secs(30),
        write: Duration::from_secs(30),
        idle: Duration::from_secs(120),
    };
    let (server_io, mut client_io) = tokio::io::duplex(4096);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let server = tokio::spawn(async move {
        let service = service_fn(move |_request: Request<Incoming>| {
            let control = control.clone();
            control.begin_request(timeouts.read, timeouts.write);
            control.request_body_finished();
            async move {
                let response = Response::new(Full::new(Bytes::from_static(b"ok")));
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, Infallible>(response.map(|body| ResponseBodyGuard::new(body, control)))
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });

    client_io
        .write_all(b"GET /one HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    let mut buf = [0u8; 256];
    let mut reply = Vec::new();
    while !reply.ends_with(b"ok") {
        let n = client_io.read(&mut buf).await.unwrap();
        assert!(n > 0, "first response truncated");
        reply.extend_from_slice(&buf[..n]);
    }
    assert!(reply.starts_with(b"HTTP/1.1 200 OK"));

    // Exceed Hyper's 30s default header_read_timeout while remaining under 120s idle.
    tokio::time::sleep(Duration::from_secs(100)).await;
    client_io
        .write_all(b"GET /two HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    reply.clear();
    while !reply.ends_with(b"ok") {
        let n = client_io.read(&mut buf).await.unwrap();
        assert!(n > 0, "idle keep-alive closed before 120s deadline");
        reply.extend_from_slice(&buf[..n]);
    }
    assert!(reply.starts_with(b"HTTP/1.1 200 OK"));

    // Exhaust the full 120s connection_idle timeout after the second response.
    tokio::time::sleep(Duration::from_secs(125)).await;
    let mut tail = Vec::new();
    let eof = tokio::time::timeout(Duration::from_secs(1), client_io.read_to_end(&mut tail))
        .await
        .expect("idle keep-alive should close after 120s");
    assert!(eof.is_ok());
    assert!(tail.is_empty());
    let _ = server.await;
}

#[tokio::test]
async fn pipelined_partial_second_request_times_out_at_header_read() {
    let timeouts = HttpTimeouts {
        header_read: Duration::from_millis(80),
        read: Duration::from_millis(500),
        write: Duration::from_millis(500),
        idle: Duration::from_millis(600),
    };
    let (server_io, mut client_io) = tokio::io::duplex(4096);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let server = tokio::spawn(async move {
        let service = service_fn(move |_request: Request<Incoming>| {
            let control = control.clone();
            control.begin_request(timeouts.read, timeouts.write);
            control.request_body_finished();
            async move {
                let response = Response::new(Full::new(Bytes::from_static(b"done")));
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, Infallible>(response.map(|body| ResponseBodyGuard::new(body, control)))
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });

    let started = Instant::now();
    client_io
        .write_all(b"GET /1 HTTP/1.1\r\nHost: localhost\r\n\r\nGET /2 HTTP/1.1\r\nHost: par")
        .await
        .unwrap();
    let mut reply = Vec::new();
    let eof = tokio::time::timeout(
        Duration::from_millis(300),
        client_io.read_to_end(&mut reply),
    )
    .await
    .expect("pipelined partial request should time out at header_read (< idle)");
    assert!(eof.is_ok());
    assert!(started.elapsed() < Duration::from_millis(300));
    let text = String::from_utf8_lossy(&reply);
    assert!(
        text.contains("HTTP/1.1 200 OK") && text.contains("done"),
        "{text}"
    );
    let _ = server.await;
}

#[tokio::test]
async fn chunked_empty_body_on_bodyless_route_does_not_kill_long_handler() {
    let timeouts = HttpTimeouts {
        header_read: Duration::from_secs(2),
        read: Duration::from_millis(80),
        write: Duration::from_secs(2),
        idle: Duration::from_secs(5),
    };
    let (server_io, mut client_io) = tokio::io::duplex(4096);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let request_plan: crate::plan::RequestPlan = serde_json::from_value(json!({
        "Mappings": [], "ContextMappings": [], "Body": null
    }))
    .unwrap();
    let request_plan = std::sync::Arc::new(request_plan);
    let generation = std::sync::Arc::new(crate::supervisor::Generation {
        number: 1,
        identity: crate::process::BuildIdentity {
            contract_revision: "c".into(),
            implementation_revision: String::new(),
            build_input_digest: String::new(),
            go_target: String::new(),
        },
        workers: Default::default(),
        retired: tokio_util::sync::CancellationToken::new(),
    });
    let call = crate::host::Call::new(
        generation,
        &HeaderMap::new(),
        "GET",
        "/slow",
        Duration::from_secs(5),
    )
    .unwrap();
    let server = tokio::spawn(async move {
        let request_plan = request_plan.clone();
        let service = service_fn(move |mut request: Request<Incoming>| {
            let control = control.clone();
            let request_plan = request_plan.clone();
            let mut call = call.clone();
            control.begin_request(timeouts.read, timeouts.write);
            request.extensions_mut().insert(control.clone());
            async move {
                let decoded = crate::http_codec::decode_request(
                    request,
                    &std::collections::BTreeMap::new(),
                    &request_plan,
                    &mut call,
                )
                .await
                .unwrap_or_else(|fault| panic!("decode_request failed: {}", fault.code));
                assert_eq!(decoded, json!({}));
                tokio::time::sleep(Duration::from_millis(180)).await;
                let response = Response::new(Full::new(Bytes::from_static(b"ok")));
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, Infallible>(response)
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });

    client_io
        .write_all(
            b"GET /slow HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n",
        )
        .await
        .unwrap();
    let mut reply = Vec::new();
    client_io.read_to_end(&mut reply).await.unwrap();
    let text = String::from_utf8(reply).unwrap();
    assert!(text.starts_with("HTTP/1.1 200 OK"), "{text}");
    assert!(text.ends_with("ok"), "{text}");
    assert!(server.await.unwrap().is_ok());
}

#[tokio::test]
async fn unread_chunked_body_preserves_full_idle_timeout_on_keep_alive() {
    let timeouts = HttpTimeouts {
        header_read: Duration::from_millis(80),
        read: Duration::from_millis(40),
        write: Duration::from_millis(500),
        idle: Duration::from_millis(450),
    };
    let (server_io, mut client_io) = tokio::io::duplex(4096);
    let (stream, control) = DeadlineStream::new(server_io, timeouts);
    let server = tokio::spawn(async move {
        let service = service_fn(move |request: Request<Incoming>| {
            let control = control.clone();
            control.begin_request(timeouts.read, timeouts.write);
            let had_body = !hyper::body::Body::is_end_stream(request.body());
            if !had_body {
                control.request_body_finished();
            }
            let slow = request.uri().path() == "/slow-unauth";
            async move {
                let status = if had_body {
                    if slow {
                        // Simulate async auth outliving `read` before rejecting unread body.
                        tokio::time::sleep(Duration::from_millis(70)).await;
                    }
                    drop(request);
                    StatusCode::UNAUTHORIZED
                } else {
                    StatusCode::OK
                };
                control.finish_unread_request(had_body).await;
                let mut response = Response::new(Full::new(Bytes::from_static(b"ok")));
                *response.status_mut() = status;
                control.begin_response(response.status(), response.headers(), timeouts.idle);
                Ok::<_, Infallible>(response.map(|body| ResponseBodyGuard::new(body, control)))
            }
        });
        public_http1_builder()
            .serve_connection(TokioIo::new(stream), service)
            .await
    });

    for path in ["/unauth", "/slow-unauth"] {
        let req = format!(
            "POST {path} HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n"
        );
        client_io.write_all(req.as_bytes()).await.unwrap();
        let mut buf = [0u8; 256];
        let mut reply = Vec::new();
        while !reply.ends_with(b"ok") {
            let n = client_io.read(&mut buf).await.unwrap();
            assert!(n > 0, "unexpected EOF on {path} 401 reply");
            reply.extend_from_slice(&buf[..n]);
        }
        assert!(reply.starts_with(b"HTTP/1.1 401 Unauthorized"), "{path}");
        // Pause longer than header_read (80ms) but shorter than idle (450ms).
        tokio::time::sleep(Duration::from_millis(180)).await;
    }

    client_io
        .write_all(b"GET /next HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
        .await
        .unwrap();
    let mut reply = Vec::new();
    client_io.read_to_end(&mut reply).await.unwrap();
    let text = String::from_utf8(reply).unwrap();
    assert!(
        text.starts_with("HTTP/1.1 200 OK") && text.ends_with("ok"),
        "keep-alive connection closed prematurely after unread chunked body: {text}"
    );
    assert!(server.await.unwrap().is_ok());
}
