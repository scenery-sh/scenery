//! The service-side transport of Worker ABI v1.

use std::convert::Infallible;
use std::future::{Future, poll_fn};
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::path::PathBuf;
use std::pin::{Pin, pin};
use std::sync::Arc;
use std::task::Poll;

use bytes::{BufMut, Bytes, BytesMut};
use http_body_util::{BodyExt, Full, Limited, StreamBody, combinators::UnsyncBoxBody};
use hyper::body::{Frame, Incoming};
use hyper::header::{AUTHORIZATION, CONTENT_TYPE, HeaderValue};
use hyper::service::service_fn;
use hyper::{Method, Request, Response, StatusCode};
use hyper_util::rt::{TokioExecutor, TokioIo};
use scenery_wire::{
    BindingCallRef, BindingReply, CAPABILITY_PREFIX, CONTRACT_HEADER, ConnectionTokenReply,
    ConnectionTokenRequest, Delivery, EVENTS_MEDIA_TYPE, EventEmitCallRef, IMPLEMENTATION_HEADER,
    INVOKE_PATH, InvocationOutcome, MAX_METADATA, READY_PATH, RawEvent, SCOPE_HEADER,
    STREAM_MEDIA_TYPE, STREAM_SIZE_HEADER,
};
use serde::Serialize;
use serde_json::value::RawValue;
use tokio::io::AsyncRead;
use tokio::net::{UnixListener, UnixStream};
use tokio::signal::unix::{SignalKind, signal};
use tokio::sync::{Mutex, watch};
use tokio::task::JoinSet;
use tokio_util::io::ReaderStream;

mod storage;
pub use storage::{StorageRead, StorageWriteOptions};

pub use scenery_wire::{
    BindingFailure, ConfigSnapshot, ContractBytes, Decimal, DispatchReceipt, Duration, Error,
    EventReceipt, Float32, Float64, Identity, Int64, Invocation, InvocationContext, Launch, Result,
    TelemetryRecord, Timestamp, UInt64, Unit, decode_outcome, encode_outcome, present, put_field,
    record_object, serde, serde_json, take_field, take_optional_field, unknown_fields,
};
pub use tokio::time::sleep;

pub type HandlerFuture = Pin<Box<dyn Future<Output = Result<Output>> + Send>>;
type Body = UnsyncBoxBody<Bytes, Error>;
pub struct Stream {
    pub reader: Pin<Box<dyn AsyncRead + Send>>,
    pub size: u64,
}

/// One typed server-sent event item emitted by a `delivery = "stream_out"` handler.
#[derive(Clone, Debug)]
pub struct Event<T> {
    pub data: T,
    pub id: Option<String>,
    pub event: Option<String>,
}

impl<T> Event<T> {
    pub fn new(data: T) -> Self {
        Self {
            data,
            id: None,
            event: None,
        }
    }

    pub fn id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }

    pub fn event(mut self, event: impl Into<String>) -> Self {
        self.event = Some(event.into());
        self
    }
}

/// Async sender paired with an [`EventStream`] via [`event_channel`].
#[derive(Clone)]
pub struct EventSender<T> {
    tx: tokio::sync::mpsc::Sender<Result<Event<T>>>,
}

impl<T> EventSender<T> {
    pub async fn send(&self, event: Event<T>) -> Result<()> {
        self.tx
            .send(Ok(event))
            .await
            .map_err(|_| Error::from("event stream closed"))
    }

    pub async fn send_data(&self, data: T) -> Result<()> {
        self.send(Event::new(data)).await
    }

    pub async fn fail(&self, error: Error) -> Result<()> {
        self.tx
            .send(Err(error))
            .await
            .map_err(|_| Error::from("event stream closed"))
    }

    pub async fn closed(&self) {
        self.tx.closed().await;
    }

    pub fn is_closed(&self) -> bool {
        self.tx.is_closed()
    }
}

/// Typed outbound event stream returned by `delivery = "stream_out"` Rust handlers.
pub struct EventStream<T> {
    rx: tokio::sync::mpsc::Receiver<Result<Event<T>>>,
}

impl<T: Serialize + Send + 'static> EventStream<T> {
    pub fn into_raw(mut self) -> RawEventStream {
        RawEventStream {
            next: Box::new(move |cx| match self.rx.poll_recv(cx) {
                Poll::Pending => Poll::Pending,
                Poll::Ready(None) => Poll::Ready(None),
                Poll::Ready(Some(Err(err))) => Poll::Ready(Some(Err(err))),
                Poll::Ready(Some(Ok(ev))) => {
                    let encoded = serde_json::value::to_raw_value(&ev.data)
                        .map_err(Error::from)
                        .map(|data| RawEvent {
                            data,
                            id: ev.id,
                            event: ev.event,
                        });
                    Poll::Ready(Some(encoded))
                }
            }),
        }
    }
}

/// Creates a bounded channel pairing an [`EventSender`] with an [`EventStream`].
pub fn event_channel<T>(capacity: usize) -> (EventSender<T>, EventStream<T>) {
    let (tx, rx) = tokio::sync::mpsc::channel(capacity.max(1));
    (EventSender { tx }, EventStream { rx })
}

type RawEventPollFn =
    Box<dyn FnMut(&mut std::task::Context<'_>) -> Poll<Option<Result<RawEvent>>> + Send>;

/// Type-erased event stream consumed by the worker runtime transport.
pub struct RawEventStream {
    next: RawEventPollFn,
}

pub struct Output {
    pub outcome: Box<RawValue>,
    pub stream: Option<Stream>,
    pub events: Option<RawEventStream>,
}

impl Output {
    pub fn json(outcome: Box<RawValue>) -> Self {
        Self {
            outcome,
            stream: None,
            events: None,
        }
    }

    pub fn stream(outcome: Box<RawValue>, stream: Option<Stream>) -> Self {
        Self {
            outcome,
            stream,
            events: None,
        }
    }

    pub fn events(outcome: Box<RawValue>, events: Option<RawEventStream>) -> Self {
        Self {
            outcome,
            stream: None,
            events,
        }
    }
}

#[derive(Clone)]
pub struct Context {
    pub invocation: InvocationContext,
    host: Host,
    canceled: watch::Receiver<()>,
    _keep_canceled_open: Option<Arc<watch::Sender<()>>>,
}

impl Context {
    pub fn standalone() -> Self {
        Self::with_principal(None, None)
    }

    pub fn with_principal(principal: Option<&str>, tenant_id: Option<&str>) -> Self {
        let (tx, canceled) = watch::channel(());
        Self {
            invocation: InvocationContext {
                invocation_id: "standalone".into(),
                scope: String::new(),
                generation: 1,
                contract_revision: String::new(),
                principal: principal.map(str::to_owned),
                tenant_id: tenant_id.map(str::to_owned),
                traceparent: None,
                deadline: None,
                locale: None,
                execution_id: None,
                metadata: None,
            },
            host: Host {
                socket: Arc::new(PathBuf::new()),
                token: Arc::from(""),
                sender: Arc::new(Mutex::new(None)),
            },
            canceled,
            _keep_canceled_open: Some(Arc::new(tx)),
        }
    }

    pub fn with_execution_id(mut self, execution_id: impl Into<String>) -> Self {
        self.invocation.execution_id = Some(execution_id.into());
        self
    }

    pub async fn canceled(&mut self) {
        let _ = self.canceled.changed().await;
    }

    pub async fn config_snapshot(&self) -> Result<ConfigSnapshot> {
        let response = self.worker_request(Method::GET, "config/snapshot", full(Bytes::new()))?;
        let raw = self.read_capability(response).await?;
        Ok(serde_json::from_slice(&raw)?)
    }

    pub async fn connection_token(&self, provider: &str, scopes: &[&str]) -> Result<String> {
        let input = serde_json::to_vec(&ConnectionTokenRequest {
            provider,
            scopes: scopes.to_vec(),
        })?;
        let reply: ConnectionTokenReply =
            serde_json::from_slice(&self.capability("connections/token", input).await?)?;
        Ok(reply.token)
    }

    pub async fn telemetry(&self, records: &[TelemetryRecord<'_>]) -> Result<()> {
        self.capability("telemetry", serde_json::to_vec(records)?)
            .await?;
        Ok(())
    }

    pub async fn invoke<T: Serialize + ?Sized>(
        &self,
        client: &str,
        input: &T,
    ) -> Result<Box<RawValue>> {
        self.call_binding("binding/invoke", client, input).await
    }

    pub async fn enqueue<T: Serialize + ?Sized>(
        &self,
        client: &str,
        input: &T,
    ) -> Result<DispatchReceipt> {
        let reply = self.call_binding("durable/enqueue", client, input).await?;
        Ok(serde_json::from_str(reply.get())?)
    }

    pub async fn emit_event<T: Serialize + ?Sized>(
        &self,
        publisher: &str,
        payload: &T,
    ) -> Result<EventReceipt> {
        let input = serde_json::to_vec(&EventEmitCallRef { publisher, payload })?;
        if input.len() > MAX_METADATA {
            return Err(unavailable_failure(
                Delivery::NotSent,
                "capability exceeds metadata limit",
            )
            .into());
        }
        let mut request = self.worker_request(Method::POST, "events/emit", full(input))?;
        request
            .headers_mut()
            .insert(CONTENT_TYPE, HeaderValue::from_static("application/json"));
        let response = self
            .host
            .send_classified(request)
            .await
            .map_err(|(delivery, err)| unavailable_failure(delivery, err.to_string()))?;
        let status = response.status();
        let raw = Limited::new(response.into_body(), MAX_METADATA)
            .collect()
            .await
            .map_err(|err| unavailable_failure(Delivery::Unknown, err.to_string()))?
            .to_bytes();
        if status != StatusCode::OK {
            return Err(event_emit_failure(status, &raw).into());
        }
        let reply: BindingReply = serde_json::from_slice(&raw).map_err(|_| {
            unavailable_failure(Delivery::Unknown, "invalid event capability response")
        })?;
        match (reply.output, reply.error) {
            (Some(output), None) => serde_json::from_str(output.get()).map_err(|_| {
                unavailable_failure(Delivery::Unknown, "invalid event receipt").into()
            }),
            (None, Some(error)) => Err(error.into()),
            _ => Err(
                unavailable_failure(Delivery::Unknown, "invalid event capability response").into(),
            ),
        }
    }

    async fn call_binding<T: Serialize + ?Sized>(
        &self,
        path: &str,
        client: &str,
        input: &T,
    ) -> Result<Box<RawValue>> {
        let raw = self
            .capability(path, serde_json::to_vec(&BindingCallRef { client, input })?)
            .await?;
        binding_output(&raw)
    }

    pub async fn capability(&self, path: &str, input: Vec<u8>) -> Result<Bytes> {
        let mut request = self.worker_request(Method::POST, path, full(input))?;
        request
            .headers_mut()
            .insert(CONTENT_TYPE, HeaderValue::from_static("application/json"));
        self.read_capability(request).await
    }

    fn worker_request(&self, method: Method, path: &str, body: Body) -> Result<Request<Body>> {
        Ok(Request::builder()
            .method(method)
            .uri(format!("http://host{CAPABILITY_PREFIX}{path}"))
            .header(AUTHORIZATION, format!("Bearer {}", self.host.token))
            .header(SCOPE_HEADER, &self.invocation.scope)
            .body(body)?)
    }

    async fn read_capability(&self, request: Request<Body>) -> Result<Bytes> {
        let response = self.host.send(request).await?;
        let status = response.status();
        if status != StatusCode::OK {
            if let Ok(collected) = Limited::new(response.into_body(), MAX_METADATA)
                .collect()
                .await
                && let Ok(parsed) =
                    serde_json::from_slice::<serde_json::Value>(&collected.to_bytes())
                && let (Some(code), Some(message)) =
                    (parsed["code"].as_str(), parsed["message"].as_str())
            {
                return Err(format!("{code}: {message}").into());
            }
            return Err(format!("capability returned HTTP {}", status.as_u16()).into());
        }
        Ok(Limited::new(response.into_body(), MAX_METADATA)
            .collect()
            .await?
            .to_bytes())
    }
}

fn binding_output(raw: &[u8]) -> Result<Box<RawValue>> {
    let reply: BindingReply = serde_json::from_slice(raw)?;
    match (reply.output, reply.error) {
        (Some(output), None) => Ok(output),
        (None, Some(error)) => Err(error.into()),
        _ => Err("invalid binding response".into()),
    }
}

type Sender = hyper::client::conn::http2::SendRequest<Body>;

#[derive(Clone)]
struct Host {
    socket: Arc<PathBuf>,
    token: Arc<str>,
    sender: Arc<Mutex<Option<Sender>>>,
}

impl Host {
    async fn send(&self, request: Request<Body>) -> Result<Response<Incoming>> {
        self.send_classified(request)
            .await
            .map_err(|(_, error)| error)
    }

    async fn send_classified(
        &self,
        request: Request<Body>,
    ) -> std::result::Result<Response<Incoming>, (Delivery, Error)> {
        let mut guard = self.sender.lock().await;
        if guard.as_ref().is_none_or(Sender::is_closed) {
            let socket = UnixStream::connect(&*self.socket)
                .await
                .map_err(|err| (Delivery::NotSent, err.into()))?;
            let (sender, connection) =
                hyper::client::conn::http2::handshake(TokioExecutor::new(), TokioIo::new(socket))
                    .await
                    .map_err(|err| (Delivery::NotSent, err.into()))?;
            tokio::spawn(connection);
            *guard = Some(sender);
        }
        let mut sender = guard
            .as_ref()
            .ok_or_else(|| (Delivery::NotSent, "host connection is unavailable".into()))?
            .clone();
        drop(guard);
        sender
            .ready()
            .await
            .map_err(|err| (Delivery::NotSent, err.into()))?;
        sender
            .send_request(request)
            .await
            .map_err(|err| (Delivery::Unknown, err.into()))
    }
}

fn unavailable_failure(delivery: Delivery, message: impl Into<String>) -> BindingFailure {
    BindingFailure {
        code: Some("unavailable".into()),
        message: message.into(),
        meta: Some(serde_json::json!({ "delivery": delivery })),
    }
}

pub fn run(future: impl Future<Output = Result<()>>) -> Result<()> {
    tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()?
        .block_on(future)
}

pub async fn serve(
    launch: Launch,
    handler: impl Fn(Invocation, Context) -> HandlerFuture + Send + Sync + 'static,
) -> Result<()> {
    let mut terminate = signal(SignalKind::terminate())?;
    let mut interrupt = signal(SignalKind::interrupt())?;
    let (mut parent_rx, _keep_parent_tx) = watch_parent_death_fd(launch.parent_death_fd);
    let mut parent_gone = pin!(parent_rx.wait_for(|&gone| gone));
    let stop = poll_fn(|cx| {
        if terminate.poll_recv(cx).is_ready()
            || interrupt.poll_recv(cx).is_ready()
            || parent_gone.as_mut().poll(cx).is_ready()
        {
            Poll::Ready(())
        } else {
            Poll::Pending
        }
    });
    serve_until(launch, handler, stop).await
}

fn watch_parent_death_fd(fd: Option<i32>) -> (watch::Receiver<bool>, Option<watch::Sender<bool>>) {
    let (parent_tx, parent_rx) = watch::channel(false);
    #[cfg(unix)]
    if let Some(fd) = fd.filter(|&fd| fd >= 3) {
        use std::io::Read;
        use std::os::fd::FromRawFd;
        std::thread::spawn(move || {
            // SAFETY: Host passes an inherited read pipe descriptor solely to signal parent exit.
            let mut pipe = unsafe { std::fs::File::from_raw_fd(fd) };
            let mut buf = [0_u8; 1];
            loop {
                match pipe.read(&mut buf) {
                    Ok(0) => break,
                    Ok(_) => continue,
                    Err(err) if err.kind() == std::io::ErrorKind::Interrupted => continue,
                    Err(_) => break,
                }
            }
            let _ = parent_tx.send(true);
        });
        return (parent_rx, None);
    }
    (parent_rx, Some(parent_tx))
}

async fn serve_until(
    launch: Launch,
    handler: impl Fn(Invocation, Context) -> HandlerFuture + Send + Sync + 'static,
    stop: impl Future<Output = ()>,
) -> Result<()> {
    let listener = UnixListener::bind(&launch.worker_socket)?;
    let identity = Arc::new(launch.identity);
    let handler = Arc::new(handler);
    let host = Host {
        socket: Arc::new(launch.host_socket),
        token: Arc::from(launch.token),
        sender: Arc::new(Mutex::new(None)),
    };
    let (shutdown, stopped) = watch::channel(());
    let mut connections = JoinSet::new();
    let mut stop = pin!(stop);
    loop {
        let accepted = poll_fn(|cx| {
            if stop.as_mut().poll(cx).is_ready() {
                return Poll::Ready(Ok(None));
            }
            while matches!(connections.poll_join_next(cx), Poll::Ready(Some(_))) {}
            listener.poll_accept(cx).map(|result| result.map(Some))
        })
        .await?;
        let Some((socket, _)) = accepted else {
            break;
        };
        let identity = Arc::clone(&identity);
        let handler = Arc::clone(&handler);
        let host = host.clone();
        let mut stopped = stopped.clone();
        connections.spawn(async move {
            let service = service_fn(move |request| {
                handle(
                    request,
                    Arc::clone(&identity),
                    host.clone(),
                    Arc::clone(&handler),
                )
            });
            let builder = hyper::server::conn::http2::Builder::new(TokioExecutor::new());
            let mut connection = pin!(builder.serve_connection(TokioIo::new(socket), service));
            let mut stop = pin!(stopped.changed());
            let finished = poll_fn(|cx| {
                if stop.as_mut().poll(cx).is_ready() {
                    Poll::Ready(false)
                } else {
                    connection.as_mut().poll(cx).map(|_| true)
                }
            })
            .await;
            if !finished {
                connection.as_mut().graceful_shutdown();
                let _ = connection.await;
            }
        });
    }
    drop(listener);
    drop(shutdown);
    // The supervisor grants five seconds before forcing termination.
    let _ = tokio::time::timeout(std::time::Duration::from_secs(4), async {
        while connections.join_next().await.is_some() {}
    })
    .await;
    Ok(())
}

async fn handle(
    request: Request<Incoming>,
    identity: Arc<Identity>,
    host: Host,
    handler: Arc<impl Fn(Invocation, Context) -> HandlerFuture>,
) -> std::result::Result<Response<Body>, Infallible> {
    let token = request
        .headers()
        .get(AUTHORIZATION)
        .and_then(|value| value.to_str().ok());
    if token.and_then(|value| value.strip_prefix("Bearer ")) != Some(&host.token) {
        return Ok(fault(StatusCode::UNAUTHORIZED));
    }
    let mut response = match (request.method(), request.uri().path()) {
        (&Method::GET, READY_PATH) => match serde_json::to_vec(&*identity) {
            Ok(body) => json(body),
            Err(_) => fault(StatusCode::INTERNAL_SERVER_ERROR),
        },
        (&Method::POST, INVOKE_PATH) => match invoke(request, &identity, host, handler).await {
            Ok(response) => response,
            Err(status) => fault(status),
        },
        _ => fault(StatusCode::NOT_FOUND),
    };
    let headers = response.headers_mut();
    if let Ok(value) = identity.contract_revision.parse() {
        headers.insert(CONTRACT_HEADER, value);
    }
    if let Ok(value) = identity.implementation_revision.parse() {
        headers.insert(IMPLEMENTATION_HEADER, value);
    }
    Ok(response)
}

async fn invoke(
    request: Request<Incoming>,
    identity: &Identity,
    host: Host,
    handler: Arc<impl Fn(Invocation, Context) -> HandlerFuture>,
) -> std::result::Result<Response<Body>, StatusCode> {
    let raw = Limited::new(request.into_body(), MAX_METADATA)
        .collect()
        .await
        .map_err(|_| StatusCode::BAD_REQUEST)?
        .to_bytes();
    let invocation: Invocation =
        serde_json::from_slice(&raw).map_err(|_| StatusCode::BAD_REQUEST)?;
    if invocation.service != identity.service
        || invocation.context.contract_revision != identity.contract_revision
    {
        return Err(StatusCode::CONFLICT);
    }
    if invocation.mode != "direct" && invocation.mode != "durable" {
        return Err(StatusCode::BAD_REQUEST);
    }
    let (cancel_on_drop, canceled) = watch::channel(());
    let context = Context {
        invocation: invocation.context.clone(),
        host,
        canceled,
        _keep_canceled_open: None,
    };
    let fail = StatusCode::INTERNAL_SERVER_ERROR;
    let mut future =
        catch_unwind(AssertUnwindSafe(|| handler(invocation, context))).map_err(|_| fail)?;
    let output = poll_fn(
        |cx| match catch_unwind(AssertUnwindSafe(|| future.as_mut().poll(cx))) {
            Ok(result) => result.map(|value| value.map_err(|_| fail)),
            Err(_) => Poll::Ready(Err(fail)),
        },
    )
    .await?;
    let metadata = serde_json::to_vec(&InvocationOutcome {
        outcome: output.outcome,
    })
    .map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?;
    if metadata.len() > MAX_METADATA {
        return Err(StatusCode::INTERNAL_SERVER_ERROR);
    }
    if let Some(stream) = output.stream {
        let mut prefix = BytesMut::with_capacity(4 + metadata.len());
        prefix.put_u32(metadata.len() as u32);
        prefix.extend_from_slice(&metadata);
        let size = stream.size;
        let stream = tokio_util::io::ReaderStream::new(stream.reader);
        let stream = FramedStream {
            prefix: Some(prefix.freeze()),
            stream,
            _cancel_on_drop: Some(cancel_on_drop),
        };
        let body = StreamBody::new(stream).map_err(Error::from).boxed_unsync();
        let mut response = Response::new(body);
        response.headers_mut().insert(
            CONTENT_TYPE,
            STREAM_MEDIA_TYPE.parse().expect("static media type"),
        );
        response.headers_mut().insert(
            STREAM_SIZE_HEADER,
            size.to_string().parse().expect("decimal size"),
        );
        Ok(response)
    } else if let Some(events) = output.events {
        let mut prefix = BytesMut::with_capacity(4 + metadata.len());
        prefix.put_u32(metadata.len() as u32);
        prefix.extend_from_slice(&metadata);
        let stream = FramedEventStream {
            prefix: Some(prefix.freeze()),
            events,
            _cancel_on_drop: Some(cancel_on_drop),
        };
        let body = StreamBody::new(stream).boxed_unsync();
        let mut response = Response::new(body);
        response.headers_mut().insert(
            CONTENT_TYPE,
            EVENTS_MEDIA_TYPE.parse().expect("static media type"),
        );
        Ok(response)
    } else {
        Ok(json(metadata))
    }
}

fn full(data: impl Into<Bytes>) -> Body {
    Full::new(data.into())
        .map_err(|never| match never {})
        .boxed_unsync()
}

fn json(data: Vec<u8>) -> Response<Body> {
    let mut response = Response::new(full(data));
    response.headers_mut().insert(
        CONTENT_TYPE,
        "application/json".parse().expect("static media type"),
    );
    response
}

fn fault(status: StatusCode) -> Response<Body> {
    let mut response = json(br#"{"error":"worker request failed"}"#.to_vec());
    *response.status_mut() = status;
    response
}

struct FramedStream {
    prefix: Option<Bytes>,
    stream: ReaderStream<Pin<Box<dyn AsyncRead + Send>>>,
    _cancel_on_drop: Option<watch::Sender<()>>,
}

impl futures_core::Stream for FramedStream {
    type Item = std::io::Result<Frame<Bytes>>;

    fn poll_next(
        mut self: Pin<&mut Self>,
        cx: &mut std::task::Context<'_>,
    ) -> std::task::Poll<Option<Self::Item>> {
        if let Some(prefix) = self.prefix.take() {
            return std::task::Poll::Ready(Some(Ok(Frame::data(prefix))));
        }
        Pin::new(&mut self.stream)
            .poll_next(cx)
            .map(|item| item.map(|chunk| chunk.map(Frame::data)))
    }
}

struct FramedEventStream {
    prefix: Option<Bytes>,
    events: RawEventStream,
    _cancel_on_drop: Option<watch::Sender<()>>,
}

impl futures_core::Stream for FramedEventStream {
    type Item = Result<Frame<Bytes>>;

    fn poll_next(
        mut self: Pin<&mut Self>,
        cx: &mut std::task::Context<'_>,
    ) -> std::task::Poll<Option<Self::Item>> {
        if let Some(prefix) = self.prefix.take() {
            return Poll::Ready(Some(Ok(Frame::data(prefix))));
        }
        match (self.events.next)(cx) {
            Poll::Pending => Poll::Pending,
            Poll::Ready(None) => Poll::Ready(None),
            Poll::Ready(Some(Err(err))) => Poll::Ready(Some(Err(err))),
            Poll::Ready(Some(Ok(raw))) => {
                let mut line = match serde_json::to_vec(&raw) {
                    Ok(line) => line,
                    Err(err) => return Poll::Ready(Some(Err(err.into()))),
                };
                line.push(b'\n');
                Poll::Ready(Some(Ok(Frame::data(Bytes::from(line)))))
            }
        }
    }
}

fn event_emit_failure(status: StatusCode, raw: &[u8]) -> BindingFailure {
    let delivery = if status.is_client_error() {
        Delivery::NotSent
    } else {
        Delivery::Unknown
    };
    let mut failure = match serde_json::from_slice::<BindingFailure>(raw) {
        Ok(mut error) => {
            if status.is_server_error() && error.code.as_deref() != Some("deadline_exceeded") {
                error.code = Some("unavailable".into());
            }
            error
        }
        Err(_) => BindingFailure {
            code: Some("unavailable".into()),
            message: format!("capability returned HTTP {}", status.as_u16()),
            meta: None,
        },
    };
    let meta = failure
        .meta
        .get_or_insert_with(|| serde_json::Value::Object(Default::default()));
    if let Some(object) = meta.as_object_mut()
        && !object.contains_key("delivery")
    {
        object.insert(
            "delivery".into(),
            serde_json::to_value(delivery).unwrap_or(serde_json::Value::Null),
        );
    }
    failure
}

#[cfg(test)]
#[path = "lib_test.rs"]
mod tests;
