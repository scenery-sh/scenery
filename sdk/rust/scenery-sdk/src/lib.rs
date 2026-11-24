//! The service-side transport of Worker ABI v1.

use std::collections::BTreeMap;
use std::convert::Infallible;
use std::future::{Future, poll_fn};
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::path::PathBuf;
use std::pin::Pin;
use std::sync::Arc;
use std::task::Poll;

use bytes::{BufMut, Bytes, BytesMut};
use http_body_util::{BodyExt, Full, Limited, StreamBody, combinators::UnsyncBoxBody};
use hyper::body::{Frame, Incoming};
use hyper::header::{AUTHORIZATION, CONTENT_TYPE, HeaderValue};
use hyper::service::service_fn;
use hyper::{Method, Request, Response, StatusCode};
use hyper_util::rt::{TokioExecutor, TokioIo};
use serde::{Deserialize, Serialize};
use serde_json::value::RawValue;
use tokio::io::AsyncRead;
use tokio::net::{UnixListener, UnixStream};
use tokio::signal::unix::{SignalKind, signal};
use tokio::sync::{Mutex, watch};
use tokio_util::io::ReaderStream;

mod scalars;
mod storage;
pub use scalars::*;
pub use storage::{StorageRead, StorageWriteOptions};

pub use serde;
pub use serde_json;
pub use tokio::time::sleep;

pub type Error = Box<dyn std::error::Error + Send + Sync>;
pub type Result<T> = std::result::Result<T, Error>;
pub type HandlerFuture = Pin<Box<dyn Future<Output = Result<Output>> + Send>>;
type Body = UnsyncBoxBody<Bytes, Error>;
const MAX_METADATA: usize = 32 << 20;

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Identity {
    pub abi: u32,
    pub service: String,
    pub contract_revision: String,
    pub implementation_revision: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Launch {
    pub worker_socket: PathBuf,
    pub host_socket: PathBuf,
    pub token: String,
    pub identity: Identity,
}

impl Launch {
    pub fn from_args() -> Result<Self> {
        let mut args = std::env::args_os().skip(1);
        let arg = args.next().ok_or("expected a launch file")?;
        if args.next().is_some() {
            return Err("expected one launch file".into());
        }
        let launch: Self = serde_json::from_slice(&std::fs::read(arg)?)?;
        if launch.identity.abi != 1 || launch.token.len() < 32 {
            return Err("invalid worker identity or token".into());
        }
        Ok(launch)
    }
}

#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct InvocationContext {
    pub invocation_id: String,
    pub scope: String,
    pub generation: u64,
    pub contract_revision: String,
    pub principal: Option<String>,
    pub tenant_id: Option<String>,
    pub traceparent: Option<String>,
    pub deadline: Option<String>,
    pub locale: Option<String>,
    pub execution_id: Option<String>,
    pub metadata: Option<Box<RawValue>>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Invocation {
    pub service: String,
    pub operation: String,
    pub binding: Option<String>,
    pub mode: String,
    pub context: InvocationContext,
    pub input: Box<RawValue>,
}

pub struct Stream {
    pub reader: Pin<Box<dyn AsyncRead + Send>>,
    pub size: u64,
}

pub struct Output {
    pub outcome: Box<RawValue>,
    pub stream: Option<Stream>,
}

impl Output {
    pub fn json(outcome: Box<RawValue>) -> Self {
        Self {
            outcome,
            stream: None,
        }
    }
}

#[derive(Clone)]
pub struct Context {
    pub invocation: InvocationContext,
    host: Host,
    canceled: watch::Receiver<()>,
}

#[derive(Debug, Deserialize)]
pub struct BindingFailure {
    pub code: Option<String>,
    pub message: String,
    pub meta: Option<serde_json::Value>,
}

impl std::fmt::Display for BindingFailure {
    fn fmt(&self, output: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        output.write_str(&self.message)
    }
}

impl std::error::Error for BindingFailure {}

#[derive(Clone, Debug, Deserialize)]
pub struct DispatchReceipt {
    pub durable_identity: String,
    pub execution_id: String,
    pub accepted_revision: String,
}

#[derive(Deserialize)]
pub struct ConfigSnapshot {
    pub revision: String,
    pub values: BTreeMap<String, Box<RawValue>>,
    pub secrets: BTreeMap<String, String>,
}

#[derive(Serialize)]
pub struct TelemetryRecord<'a> {
    pub kind: &'a str,
    pub name: &'a str,
    pub message: &'a str,
    pub elapsed_ns: u64,
    pub attributes: BTreeMap<String, serde_json::Value>,
}

impl Context {
    pub async fn canceled(&mut self) {
        let _ = self.canceled.changed().await;
    }

    pub async fn config_snapshot(&self) -> Result<ConfigSnapshot> {
        let response = self.worker_request(Method::GET, "config/snapshot", full(Bytes::new()))?;
        let raw = self.read_capability(response).await?;
        Ok(serde_json::from_slice(&raw)?)
    }

    pub async fn connection_token(&self, provider: &str, scopes: &[&str]) -> Result<String> {
        #[derive(Deserialize)]
        struct Reply {
            token: String,
        }

        let input =
            serde_json::to_vec(&serde_json::json!({ "provider": provider, "scopes": scopes }))?;
        let reply: Reply =
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

    async fn call_binding<T: Serialize + ?Sized>(
        &self,
        path: &str,
        client: &str,
        input: &T,
    ) -> Result<Box<RawValue>> {
        #[derive(Serialize)]
        struct Call<'a, T: ?Sized> {
            client: &'a str,
            input: &'a T,
        }
        #[derive(Deserialize)]
        struct Reply {
            output: Option<Box<RawValue>>,
            error: Option<BindingFailure>,
        }
        let raw = self
            .capability(path, serde_json::to_vec(&Call { client, input })?)
            .await?;
        let reply: Reply = serde_json::from_slice(&raw)?;
        match (reply.output, reply.error) {
            (Some(output), None) => Ok(output),
            (None, Some(error)) => Err(error.into()),
            _ => Err("invalid binding response".into()),
        }
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
            .uri(format!("http://host/host/v1/{path}"))
            .header(AUTHORIZATION, format!("Bearer {}", self.host.token))
            .header("X-Worker-Scope", &self.invocation.scope)
            .body(body)?)
    }

    async fn read_capability(&self, request: Request<Body>) -> Result<Bytes> {
        let response = self.host.send(request).await?;
        if response.status() != StatusCode::OK {
            return Err(format!("capability returned HTTP {}", response.status().as_u16()).into());
        }
        Ok(Limited::new(response.into_body(), MAX_METADATA)
            .collect()
            .await?
            .to_bytes())
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
        let mut guard = self.sender.lock().await;
        if guard.as_ref().is_none_or(Sender::is_closed) {
            let socket = UnixStream::connect(&*self.socket).await?;
            let (sender, connection) =
                hyper::client::conn::http2::handshake(TokioExecutor::new(), TokioIo::new(socket))
                    .await?;
            tokio::spawn(connection);
            *guard = Some(sender);
        }
        let mut sender = guard
            .as_ref()
            .ok_or("host connection is unavailable")?
            .clone();
        drop(guard);
        sender.ready().await?;
        Ok(sender.send_request(request).await?)
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
    let listener = UnixListener::bind(&launch.worker_socket)?;
    let identity = Arc::new(launch.identity);
    let handler = Arc::new(handler);
    let host = Host {
        socket: Arc::new(launch.host_socket),
        token: Arc::from(launch.token),
        sender: Arc::new(Mutex::new(None)),
    };
    loop {
        let accepted = poll_fn(|cx| {
            if terminate.poll_recv(cx).is_ready() {
                return Poll::Ready(Ok(None));
            }
            listener.poll_accept(cx).map(|result| result.map(Some))
        })
        .await?;
        let Some((socket, _)) = accepted else {
            return Ok(());
        };
        let identity = Arc::clone(&identity);
        let handler = Arc::clone(&handler);
        let host = host.clone();
        tokio::spawn(async move {
            let service = service_fn(move |request| {
                handle(
                    request,
                    Arc::clone(&identity),
                    host.clone(),
                    Arc::clone(&handler),
                )
            });
            let _ = hyper::server::conn::http2::Builder::new(TokioExecutor::new())
                .serve_connection(TokioIo::new(socket), service)
                .await;
        });
    }
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
        (&Method::GET, "/worker/v1/ready") => match serde_json::to_vec(&*identity) {
            Ok(body) => json(body),
            Err(_) => fault(StatusCode::INTERNAL_SERVER_ERROR),
        },
        (&Method::POST, "/worker/v1/invoke") => {
            match invoke(request, &identity, host, handler).await {
                Ok(response) => response,
                Err(status) => fault(status),
            }
        }
        _ => fault(StatusCode::NOT_FOUND),
    };
    let headers = response.headers_mut();
    if let Ok(value) = identity.contract_revision.parse() {
        headers.insert("X-Worker-Contract", value);
    }
    if let Ok(value) = identity.implementation_revision.parse() {
        headers.insert("X-Worker-Implementation", value);
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
    #[derive(Serialize)]
    struct ResponseMetadata {
        outcome: Box<RawValue>,
    }
    let metadata = serde_json::to_vec(&ResponseMetadata {
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
            "application/vnd.worker.stream"
                .parse()
                .expect("static media type"),
        );
        response.headers_mut().insert(
            "X-Worker-Stream-Size",
            size.to_string().parse().expect("decimal size"),
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

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Unit {}

pub fn encode_outcome(value: &impl Serialize, kind: &str, name: &str) -> Result<Box<RawValue>> {
    #[derive(Serialize)]
    struct Envelope<'a, T> {
        kind: &'a str,
        name: &'a str,
        #[serde(skip_serializing_if = "Option::is_none")]
        value: Option<&'a T>,
        #[serde(skip_serializing_if = "Option::is_none")]
        problem: Option<&'a T>,
    }
    let (value, problem) = match kind {
        "result" => (Some(value), None),
        "error" => (None, Some(value)),
        _ => return Err("invalid outcome kind".into()),
    };
    Ok(serde_json::value::to_raw_value(&Envelope {
        kind,
        name,
        value,
        problem,
    })?)
}

pub fn decode_outcome(raw: &RawValue) -> Result<(String, String, Box<RawValue>)> {
    #[derive(Deserialize)]
    #[serde(deny_unknown_fields)]
    struct Envelope {
        kind: String,
        name: String,
        #[serde(default, deserialize_with = "present")]
        value: Option<Box<RawValue>>,
        #[serde(default, deserialize_with = "present")]
        problem: Option<Box<RawValue>>,
    }
    let envelope: Envelope = serde_json::from_str(raw.get())?;
    let payload = match (envelope.kind.as_str(), envelope.value, envelope.problem) {
        ("result", Some(value), None) | ("error", None, Some(value)) => value,
        _ => return Err("invalid outcome envelope".into()),
    };
    Ok((envelope.kind, envelope.name, payload))
}
