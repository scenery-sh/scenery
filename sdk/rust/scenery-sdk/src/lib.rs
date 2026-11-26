//! The service-side transport of Worker ABI v1.

use std::collections::BTreeMap;
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
use serde::{Deserialize, Serialize};
use serde_json::value::RawValue;
use tokio::io::AsyncRead;
use tokio::net::{UnixListener, UnixStream};
use tokio::signal::unix::{SignalKind, signal};
use tokio::sync::{Mutex, watch};
use tokio::task::JoinSet;
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
    #[serde(default)]
    pub parent_death_fd: Option<i32>,
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
            let _ = pipe.read(&mut buf);
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

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;
    use tokio::sync::{Notify, mpsc};

    #[test]
    fn shutdown_drains_an_admitted_invocation_and_its_telemetry() {
        run(async { tokio::time::timeout(Duration::from_secs(3), prove_shutdown_drain()).await? })
            .unwrap();
    }

    async fn prove_shutdown_drain() -> Result<()> {
        let directory = std::env::temp_dir().join(format!("sw-{}", std::process::id()));
        std::fs::create_dir(&directory)?;
        struct RemoveTestDirectory(PathBuf);
        impl Drop for RemoveTestDirectory {
            fn drop(&mut self) {
                let _ = std::fs::remove_dir_all(&self.0);
            }
        }
        let _cleanup = RemoveTestDirectory(directory.clone());
        let worker_socket = directory.join("worker");
        let host_socket = directory.join("host");
        let collector = UnixListener::bind(&host_socket)?;
        let (received, mut batch) = mpsc::unbounded_channel();
        let receiver = tokio::spawn(async move {
            let (socket, _) = collector.accept().await.unwrap();
            let handler = service_fn(move |request: Request<Incoming>| {
                let received = received.clone();
                async move {
                    assert_eq!(request.uri().path(), "/host/v1/telemetry");
                    assert_eq!(request.headers()["x-worker-scope"], "test-scope");
                    let body = request.into_body().collect().await?.to_bytes();
                    received.send(body).unwrap();
                    Ok::<_, hyper::Error>(json(br#"{"accepted":1}"#.to_vec()))
                }
            });
            let _ = hyper::server::conn::http2::Builder::new(TokioExecutor::new())
                .serve_connection(TokioIo::new(socket), handler)
                .await;
        });
        let stop = Arc::new(Notify::new());
        let stopping = Arc::clone(&stop);
        let entered = Arc::new(Notify::new());
        let worker_entered = Arc::clone(&entered);
        let release = Arc::new(Notify::new());
        let worker_release = Arc::clone(&release);
        let launch = Launch {
            worker_socket: worker_socket.clone(),
            host_socket,
            token: "test-token".into(),
            identity: Identity {
                abi: 1,
                service: "example/service/worker".into(),
                contract_revision: "contract".into(),
                implementation_revision: "implementation".into(),
            },
            parent_death_fd: None,
        };
        let worker = tokio::spawn(async move {
            serve_until(
                launch,
                move |_, context| {
                    let entered = Arc::clone(&worker_entered);
                    let release = Arc::clone(&worker_release);
                    Box::pin(async move {
                        entered.notify_one();
                        release.notified().await;
                        context
                            .telemetry(&[TelemetryRecord {
                                kind: "log",
                                name: "drain",
                                message: "handler completed",
                                elapsed_ns: 0,
                                attributes: BTreeMap::new(),
                            }])
                            .await?;
                        Ok(Output::json(RawValue::from_string("{}".into())?))
                    })
                },
                stopping.notified(),
            )
            .await
        });
        let socket = loop {
            if let Ok(socket) = UnixStream::connect(&worker_socket).await {
                break socket;
            }
            sleep(Duration::from_millis(1)).await;
        };
        let (mut client, connection) =
            hyper::client::conn::http2::handshake(TokioExecutor::new(), TokioIo::new(socket))
                .await?;
        let connection = tokio::spawn(connection);
        let request = Request::post("/worker/v1/invoke")
            .header(AUTHORIZATION, "Bearer test-token")
            .body(full(Bytes::from_static(
                br#"{"service":"example/service/worker","operation":"example/operation/test","mode":"direct","context":{"invocation_id":"test","scope":"test-scope","generation":1,"contract_revision":"contract"},"input":{}}"#,
            )))?;
        let response = tokio::spawn(async move { client.send_request(request).await });
        entered.notified().await;
        stop.notify_one();
        while UnixStream::connect(&worker_socket).await.is_ok() {
            sleep(Duration::from_millis(1)).await;
        }
        release.notify_one();
        let response = response.await??;
        assert_eq!(response.status(), StatusCode::OK);
        response.into_body().collect().await?;
        let batch: serde_json::Value =
            serde_json::from_slice(&batch.recv().await.ok_or("telemetry batch missing")?)?;
        assert_eq!(batch[0]["message"], "handler completed");
        worker.await??;
        receiver.abort();
        connection.abort();
        Ok(())
    }

    #[cfg(unix)]
    #[test]
    fn serve_stops_when_parent_death_pipe_closes() {
        use std::os::fd::IntoRawFd;

        run(async {
            tokio::time::timeout(Duration::from_secs(3), async {
                let directory =
                    std::env::temp_dir().join(format!("sw-parent-{}", std::process::id()));
                let _ = std::fs::remove_dir_all(&directory);
                std::fs::create_dir(&directory)?;
                struct RemoveTestDirectory(PathBuf);
                impl Drop for RemoveTestDirectory {
                    fn drop(&mut self) {
                        let _ = std::fs::remove_dir_all(&self.0);
                    }
                }
                let _cleanup = RemoveTestDirectory(directory.clone());
                let worker_socket = directory.join("worker");
                let (reader, writer) = std::io::pipe()?;
                let launch = Launch {
                    worker_socket: worker_socket.clone(),
                    host_socket: directory.join("host"),
                    token: "test-token".into(),
                    identity: Identity {
                        abi: 1,
                        service: "example/service/worker".into(),
                        contract_revision: "contract".into(),
                        implementation_revision: "implementation".into(),
                    },
                    parent_death_fd: Some(reader.into_raw_fd()),
                };
                let task = tokio::spawn(async move {
                    serve(launch, |_, _| {
                        Box::pin(async { Ok(Output::json(RawValue::from_string("{}".into())?)) })
                    })
                    .await
                });
                while UnixStream::connect(&worker_socket).await.is_err() {
                    sleep(Duration::from_millis(1)).await;
                }
                drop(writer);
                task.await??;
                Ok::<(), Error>(())
            })
            .await?
        })
        .unwrap();
    }
}
