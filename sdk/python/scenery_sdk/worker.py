"""Python Worker ABI v1 process runtime (launch, startup inputs, server loop)."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
from datetime import datetime, timezone
import hmac
import inspect
import json
import os
import signal
import struct
import sys
from types import MappingProxyType
from typing import Any, AsyncIterable, Awaitable, Callable, Generic, Iterable, Mapping, TypeVar

from ._h2c import ERR_INTERNAL_ERROR, H2Server, H2ServerStream
from .codec import parse_exact_json
from .host import Context, Host, MAX_METADATA

T = TypeVar("T")
E = TypeVar("E")


@dataclass(frozen=True, slots=True)
class StartupInputs:
    """Private startup inputs delivered by the Host over SCENERY_CONFIG_SNAPSHOT_FD."""

    config: Any
    databases: Mapping[str, str]


@dataclass(frozen=True, slots=True)
class Stream(Generic[T]):
    """Streaming HTTP operation outcome with declared byte length."""

    outcome: T
    body: AsyncIterable[bytes] | Iterable[bytes]
    size: int


def stream(outcome: T, body: AsyncIterable[bytes] | Iterable[bytes], size: int) -> Stream[T]:
    """Attach a bounded byte stream to a declared HTTP stream outcome."""
    if isinstance(size, bool) or not isinstance(size, int) or size < 0 or size > (1 << 63) - 1:
        raise ValueError("invalid worker stream size")
    return Stream(outcome=outcome, body=body, size=size)


@dataclass(frozen=True, slots=True)
class ServerEvent(Generic[E]):
    """Single typed server-sent event with optional SSE id and event name."""

    data: E
    id: str | None = None
    event: str | None = None


@dataclass(frozen=True, slots=True)
class EventStream(Generic[T, E]):
    """Streaming HTTP stream_out outcome carrying typed server-sent events."""

    outcome: T
    events: AsyncIterable[E | ServerEvent[E]] | Iterable[E | ServerEvent[E]]


@dataclass(frozen=True, slots=True)
class EncodedEventStream:
    """Internal transport wrapper carrying pre-encoded NDJSON event lines."""

    lines: AsyncIterable[str] | Iterable[str]


def sse_event(data: E, *, id: str | None = None, event: str | None = None) -> ServerEvent[E]:
    """Wrap an event payload with optional SSE id and event fields."""
    return ServerEvent(data=data, id=id, event=event)


def event_stream(
    outcome: T,
    events: AsyncIterable[E | ServerEvent[E]] | Iterable[E | ServerEvent[E]],
) -> EventStream[T, E]:
    """Attach a typed event stream to a declared HTTP stream_out outcome."""
    return EventStream(outcome=outcome, events=events)


def read_launch() -> dict[str, Any]:
    """Read and validate the Host-supplied launch configuration file from argv[1]."""
    if len(sys.argv) != 2:
        raise RuntimeError("expected one worker launch file")
    with open(sys.argv[1], "r", encoding="utf-8") as fh:
        launch = json.load(fh)
    identity = launch.get("identity") if isinstance(launch, dict) else None
    token = launch.get("token") if isinstance(launch, dict) else None
    if not isinstance(identity, dict) or identity.get("abi") != 1 or not isinstance(token, str) or len(token) < 32:
        raise RuntimeError("invalid worker launch")
    return launch


def startup_inputs() -> StartupInputs:
    """Read and close the private startup descriptor supplied by the Host."""
    raw_fd = os.environ.pop("SCENERY_CONFIG_SNAPSHOT_FD", None)
    if raw_fd is None:
        raise RuntimeError("missing private worker inputs")
    try:
        fd = int(raw_fd)
    except ValueError as exc:
        raise RuntimeError("invalid private worker descriptor") from exc
    if fd < 3:
        raise RuntimeError("missing private worker inputs")
    with os.fdopen(fd, "r", encoding="utf-8") as fh:
        parsed = parse_exact_json(fh.read())
    if not isinstance(parsed, dict):
        raise RuntimeError("invalid private worker inputs")
    databases = parsed.get("databases") or {}
    if not isinstance(databases, dict):
        raise RuntimeError("invalid worker database map")
    return StartupInputs(
        config=parsed.get("config"),
        databases=MappingProxyType(dict(databases)),
    )


def _parse_deadline_seconds(deadline: Any) -> float | None:
    if not isinstance(deadline, str) or not deadline:
        return None
    try:
        text = deadline[:-1] + "+00:00" if deadline.endswith("Z") else deadline
        dt = datetime.fromisoformat(text)
        if dt.tzinfo is None:
            dt = dt.replace(tzinfo=timezone.utc)
        return (dt - datetime.now(timezone.utc)).total_seconds()
    except ValueError:
        return None


async def serve(
    launch: dict[str, Any],
    dispatch: Callable[[str, Any, Context], Awaitable[tuple[str, Stream[Any] | EncodedEventStream | None]]],
) -> None:
    """Serve Worker ABI v1 requests on launch['worker_socket'] until drained."""
    host = Host(launch)
    identity = launch["identity"]
    expected_auth = "Bearer " + launch["token"]
    attestation = {
        "x-worker-contract": identity["contract_revision"],
        "x-worker-implementation": identity["implementation_revision"],
    }
    identity_bytes = json.dumps(identity, separators=(",", ":")).encode("utf-8")
    draining = False

    async def handle(req: H2ServerStream) -> None:
        auth = req.headers.get("authorization", "")
        if not hmac.compare_digest(auth, expected_auth):
            await req.respond(401, {})
            return

        async def respond_json(status: int, data: bytes) -> None:
            await req.respond(status, {**attestation, "content-type": "application/json"}, data)

        async def fault(status: int) -> None:
            await respond_json(status, b'{"error":"worker request failed"}')

        if draining:
            await fault(503)
            return

        path = req.headers.get(":path", "")
        method = req.headers.get(":method", "")
        if path == "/worker/v1/ready":
            if method != "GET":
                await fault(405)
                return
            await respond_json(200, identity_bytes)
            return
        if path != "/worker/v1/invoke":
            await fault(404)
            return
        if method != "POST":
            await fault(405)
            return

        try:
            raw_bytes = await req.body.read_all(MAX_METADATA)
            raw_text = raw_bytes.decode("utf-8")
            envelope = json.loads(raw_text)
            wire = parse_exact_json(raw_text)
            if (
                not isinstance(envelope, dict)
                or not isinstance(wire, dict)
                or not isinstance(envelope.get("context"), dict)
                or not isinstance(envelope["context"].get("scope"), str)
                or not isinstance(envelope.get("operation"), str)
                or "input" not in wire
            ):
                raise ValueError("invalid worker invocation")
            wire_input = wire["input"]
            if isinstance(wire.get("context"), dict) and "metadata" in wire["context"]:
                envelope["context"]["metadata"] = wire["context"]["metadata"]
        except Exception:
            if not req.closed:
                await fault(400)
            return

        if (
            envelope.get("service") != identity["service"]
            or envelope["context"].get("contract_revision") != identity["contract_revision"]
        ):
            await fault(409)
            return
        if envelope.get("mode") not in ("direct", "durable"):
            await fault(400)
            return

        loop = asyncio.get_running_loop()
        current_task = asyncio.current_task()
        timer_handle: asyncio.TimerHandle | None = None
        remaining_s = _parse_deadline_seconds(envelope["context"].get("deadline"))
        if remaining_s is not None:
            if remaining_s <= 0:
                req.cancel_event.set()
                return

            def _expire() -> None:
                req.cancel_event.set()
                if current_task is not None and not current_task.done():
                    current_task.cancel()

            timer_handle = loop.call_later(min(remaining_s, 2_147_483.0), _expire)

        try:
            if req.cancel_event.is_set():
                return
            ctx = host.context(envelope, req.cancel_event)
            outcome_json, stream_out = await dispatch(envelope["operation"], wire_input, ctx)
            if req.cancel_event.is_set() or req.closed:
                return
            metadata = ('{"outcome":' + outcome_json + "}").encode("utf-8")
            if len(metadata) > MAX_METADATA:
                await fault(500)
                return
            if stream_out is None:
                await respond_json(200, metadata)
                return
            prefix = struct.pack("!I", len(metadata))
            if isinstance(stream_out, EncodedEventStream):
                await req.respond(
                    200,
                    {
                        **attestation,
                        "content-type": "application/vnd.worker.events",
                    },
                    prefix + metadata,
                    end_stream=False,
                )
                lines = stream_out.lines
                if hasattr(lines, "__aiter__"):
                    async for line in lines:  # type: ignore[union-attr]
                        if req.cancel_event.is_set() or req.closed:
                            return
                        if not isinstance(line, str):
                            raise TypeError("worker event streams must yield encoded JSON lines")
                        await req.write_chunk((line + "\n").encode("utf-8"), end_stream=False)
                else:
                    for line in lines:  # type: ignore[union-attr]
                        if req.cancel_event.is_set() or req.closed:
                            return
                        if not isinstance(line, str):
                            raise TypeError("worker event streams must yield encoded JSON lines")
                        await req.write_chunk((line + "\n").encode("utf-8"), end_stream=False)
                await req.write_chunk(b"", end_stream=True)
                return
            await req.respond(
                200,
                {
                    **attestation,
                    "content-type": "application/vnd.worker.stream",
                    "x-worker-stream-size": str(stream_out.size),
                },
                prefix + metadata,
                end_stream=False,
            )
            remaining = stream_out.size
            body = stream_out.body
            if hasattr(body, "__aiter__"):
                async for chunk in body:  # type: ignore[union-attr]
                    if req.cancel_event.is_set() or req.closed:
                        return
                    if not isinstance(chunk, (bytes, bytearray, memoryview)):
                        raise TypeError("worker streams must yield bytes")
                    raw_chunk = bytes(chunk)
                    remaining -= len(raw_chunk)
                    if remaining < 0:
                        raise ValueError("worker stream exceeds declared size")
                    if raw_chunk:
                        await req.write_chunk(raw_chunk, end_stream=False)
            else:
                for chunk in body:  # type: ignore[union-attr]
                    if req.cancel_event.is_set() or req.closed:
                        return
                    if not isinstance(chunk, (bytes, bytearray, memoryview)):
                        raise TypeError("worker streams must yield bytes")
                    raw_chunk = bytes(chunk)
                    remaining -= len(raw_chunk)
                    if remaining < 0:
                        raise ValueError("worker stream exceeds declared size")
                    if raw_chunk:
                        await req.write_chunk(raw_chunk, end_stream=False)
            if remaining != 0:
                raise ValueError("worker stream ended before declared size")
            await req.write_chunk(b"", end_stream=True)
        except asyncio.CancelledError:
            return
        except Exception:
            if req.closed or req.cancel_event.is_set():
                return
            if req.headers_sent:
                await req.reset(ERR_INTERNAL_ERROR)
            else:
                await fault(500)
        finally:
            if timer_handle is not None:
                timer_handle.cancel()

    server = H2Server(launch["worker_socket"], handle)
    stop_event = asyncio.Event()
    loop = asyncio.get_running_loop()

    def _trigger_stop() -> None:
        nonlocal draining
        draining = True
        stop_event.set()

    for sig in (signal.SIGTERM, signal.SIGINT):
        try:
            loop.add_signal_handler(sig, _trigger_stop)
        except (NotImplementedError, RuntimeError):
            pass

    parent_fd = launch.get("parent_death_fd")
    watching_parent = isinstance(parent_fd, int) and parent_fd >= 3
    if watching_parent:
        def _on_parent_readable() -> None:
            try:
                data = os.read(parent_fd, 64)
            except OSError:
                data = b""
            if not data:
                loop.remove_reader(parent_fd)
                _trigger_stop()

        loop.add_reader(parent_fd, _on_parent_readable)

    try:
        await server.start()
        await stop_event.wait()
        await server.drain_and_close()
    finally:
        for sig in (signal.SIGTERM, signal.SIGINT):
            try:
                loop.remove_signal_handler(sig)
            except (NotImplementedError, RuntimeError):
                pass
        if watching_parent:
            try:
                loop.remove_reader(parent_fd)
            except Exception:
                pass
            try:
                os.close(parent_fd)
            except OSError:
                pass
        host.close()


async def maybe_await(value: Any) -> Any:
    """Await coroutines/awaitables while allowing synchronous Python handlers."""
    if inspect.isawaitable(value):
        return await value
    return value
