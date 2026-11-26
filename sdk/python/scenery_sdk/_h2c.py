"""Minimal asyncio HTTP/2 prior-knowledge (h2c) server and client over UDS."""

from __future__ import annotations

import asyncio
import os
import struct
from typing import AsyncIterable, AsyncIterator, Awaitable, Callable, Iterable

from ._hpack import HPACKDecoder, HPACKError, encode_headers

CLIENT_PREFACE = b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

FRAME_DATA = 0x0
FRAME_HEADERS = 0x1
FRAME_PRIORITY = 0x2
FRAME_RST_STREAM = 0x3
FRAME_SETTINGS = 0x4
FRAME_PING = 0x6
FRAME_GOAWAY = 0x7
FRAME_WINDOW_UPDATE = 0x8
FRAME_CONTINUATION = 0x9

FLAG_END_STREAM = 0x1
FLAG_ACK = 0x1
FLAG_END_HEADERS = 0x4
FLAG_PADDED = 0x8
FLAG_PRIORITY = 0x20

SETTING_HEADER_TABLE_SIZE = 0x1
SETTING_INITIAL_WINDOW_SIZE = 0x4
SETTING_MAX_FRAME_SIZE = 0x5

ERR_NO_ERROR = 0x0
ERR_PROTOCOL_ERROR = 0x1
ERR_INTERNAL_ERROR = 0x2
ERR_CANCEL = 0x8

DEFAULT_WINDOW = 65535
DEFAULT_MAX_FRAME = 16384
RECV_WINDOW = 1 << 20  # 1 MiB flow control window per stream/connection


class H2Error(Exception):
    """Transport or protocol error on an HTTP/2 connection."""


class H2StreamReset(H2Error):
    """Raised when the remote peer resets an active HTTP/2 stream."""

    def __init__(self, error_code: int) -> None:
        super().__init__(f"HTTP/2 stream reset ({error_code})")
        self.error_code = error_code


def _pack_frame(ftype: int, flags: int, stream_id: int, payload: bytes) -> bytes:
    n = len(payload)
    header = struct.pack(
        "!BBBI",
        (n >> 16) & 0xFF,
        (n >> 8) & 0xFF,
        n & 0xFF,
        ((ftype & 0xFF) << 24),
    )
    # Re-pack cleanly: 3-byte length, 1-byte type, 1-byte flags, 4-byte stream_id.
    header = bytes([(n >> 16) & 0xFF, (n >> 8) & 0xFF, n & 0xFF, ftype & 0xFF, flags & 0xFF]) + struct.pack(
        "!I", stream_id & 0x7FFFFFFF
    )
    return header + payload


def _pack_settings(pairs: Iterable[tuple[int, int]] = (), *, ack: bool = False) -> bytes:
    if ack:
        return _pack_frame(FRAME_SETTINGS, FLAG_ACK, 0, b"")
    payload = b"".join(struct.pack("!HI", k, v) for k, v in pairs)
    return _pack_frame(FRAME_SETTINGS, 0, 0, payload)


def _strip_padding(flags: int, payload: bytes) -> bytes:
    if not (flags & FLAG_PADDED):
        return payload
    if not payload:
        raise H2Error("padded frame missing pad length")
    pad = payload[0]
    if 1 + pad > len(payload):
        raise H2Error("frame padding exceeds payload")
    return payload[1 : len(payload) - pad]


def _strip_headers_payload(flags: int, payload: bytes) -> bytes:
    body = _strip_padding(flags, payload)
    if flags & FLAG_PRIORITY:
        if len(body) < 5:
            raise H2Error("HEADERS frame missing priority fields")
        body = body[5:]
    return body


class _StreamQueue:
    """Async chunk queue backing an incoming HTTP/2 stream body."""

    def __init__(self, on_consume: Callable[[int], None]) -> None:
        self._queue: asyncio.Queue[bytes | BaseException | None] = asyncio.Queue()
        self._closed = False
        self._on_consume = on_consume

    def feed(self, chunk: bytes) -> None:
        if not self._closed and chunk:
            self._queue.put_nowait(chunk)

    def end(self) -> None:
        if not self._closed:
            self._closed = True
            self._queue.put_nowait(None)

    def fail(self, error: BaseException) -> None:
        if not self._closed:
            self._closed = True
            self._queue.put_nowait(error)

    async def __aiter__(self) -> AsyncIterator[bytes]:
        while True:
            item = await self._queue.get()
            if item is None:
                return
            if isinstance(item, BaseException):
                raise item
            self._on_consume(len(item))
            yield item

    async def read_all(self, limit: int) -> bytes:
        parts: list[bytes] = []
        total = 0
        async for chunk in self:
            total += len(chunk)
            if total > limit:
                raise H2Error("stream body exceeded limit")
            parts.append(chunk)
        return b"".join(parts)


class _H2ConnectionBase:
    """Shared framing, flow-control, and write serialization for client & server."""

    def __init__(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        self._reader = reader
        self._writer = writer
        self._decoder = HPACKDecoder(4096)
        self._write_lock = asyncio.Lock()
        self._conn_send_window = DEFAULT_WINDOW
        self._peer_initial_window = DEFAULT_WINDOW
        self._peer_max_frame = DEFAULT_MAX_FRAME
        self._stream_send_windows: dict[int, int] = {}
        self._window_event = asyncio.Event()
        self._closed = False
        self._goaway_sent = False

    def _enqueue_raw(self, frame: bytes) -> None:
        if not self._closed and not self._writer.is_closing():
            self._writer.write(frame)

    async def _send_raw(self, frame: bytes) -> None:
        async with self._write_lock:
            if self._closed or self._writer.is_closing():
                raise H2Error("HTTP/2 connection closed")
            self._writer.write(frame)
            await self._writer.drain()

    def _send_window_update(self, stream_id: int, increment: int) -> None:
        if increment <= 0 or self._closed or self._writer.is_closing():
            return
        self._writer.write(_pack_frame(FRAME_WINDOW_UPDATE, 0, stream_id, struct.pack("!I", increment & 0x7FFFFFFF)))

    async def send_headers(
        self,
        stream_id: int,
        headers: list[tuple[str, str]],
        *,
        end_stream: bool = False,
    ) -> None:
        block = encode_headers(headers)
        max_frame = self._peer_max_frame
        async with self._write_lock:
            if self._closed or self._writer.is_closing():
                raise H2Error("HTTP/2 connection closed")
            offset = 0
            first = True
            while True:
                chunk = block[offset : offset + max_frame]
                offset += len(chunk)
                done = offset >= len(block)
                flags = (FLAG_END_HEADERS if done else 0) | (FLAG_END_STREAM if first and end_stream else 0)
                ftype = FRAME_HEADERS if first else FRAME_CONTINUATION
                self._writer.write(_pack_frame(ftype, flags, stream_id, chunk))
                first = False
                if done:
                    break
            await self._writer.drain()

    async def send_data(self, stream_id: int, data: bytes, *, end_stream: bool = False) -> None:
        view = memoryview(data)
        offset = 0
        if len(view) == 0:
            if end_stream:
                await self._send_raw(_pack_frame(FRAME_DATA, FLAG_END_STREAM, stream_id, b""))
            return
        while offset < len(view):
            while True:
                if self._closed:
                    raise H2Error("HTTP/2 connection closed")
                swin = self._stream_send_windows.get(stream_id)
                if swin is None:
                    raise H2StreamReset(ERR_CANCEL)
                avail = min(self._conn_send_window, swin, self._peer_max_frame)
                if avail > 0:
                    break
                self._window_event.clear()
                await self._window_event.wait()
            take = min(avail, len(view) - offset)
            chunk = bytes(view[offset : offset + take])
            offset += take
            self._conn_send_window -= take
            self._stream_send_windows[stream_id] -= take
            flags = FLAG_END_STREAM if (end_stream and offset >= len(view)) else 0
            await self._send_raw(_pack_frame(FRAME_DATA, flags, stream_id, chunk))

    async def send_rst_stream(self, stream_id: int, error_code: int = ERR_CANCEL) -> None:
        self._stream_send_windows.pop(stream_id, None)
        try:
            await self._send_raw(_pack_frame(FRAME_RST_STREAM, 0, stream_id, struct.pack("!I", error_code)))
        except Exception:
            pass

    async def send_goaway(self, last_stream_id: int = 0, error_code: int = ERR_NO_ERROR) -> None:
        if self._goaway_sent or self._closed:
            return
        self._goaway_sent = True
        try:
            await self._send_raw(
                _pack_frame(FRAME_GOAWAY, 0, 0, struct.pack("!II", last_stream_id & 0x7FFFFFFF, error_code))
            )
        except Exception:
            pass

    def _apply_peer_settings(self, payload: bytes) -> None:
        if len(payload) % 6 != 0:
            raise H2Error("invalid SETTINGS frame length")
        for i in range(0, len(payload), 6):
            ident, value = struct.unpack("!HI", payload[i : i + 6])
            if ident == SETTING_INITIAL_WINDOW_SIZE:
                delta = value - self._peer_initial_window
                self._peer_initial_window = value
                for sid in list(self._stream_send_windows):
                    self._stream_send_windows[sid] += delta
                self._window_event.set()
            elif ident == SETTING_MAX_FRAME_SIZE:
                if 16384 <= value <= 16777215:
                    self._peer_max_frame = value

    def _apply_window_update(self, stream_id: int, payload: bytes) -> None:
        if len(payload) != 4:
            raise H2Error("invalid WINDOW_UPDATE frame")
        (inc,) = struct.unpack("!I", payload)
        inc &= 0x7FFFFFFF
        if inc == 0:
            raise H2Error("zero WINDOW_UPDATE increment")
        if stream_id == 0:
            self._conn_send_window += inc
        elif stream_id in self._stream_send_windows:
            self._stream_send_windows[stream_id] += inc
        self._window_event.set()

    async def _read_frame(self) -> tuple[int, int, int, bytes] | None:
        try:
            hdr = await self._reader.readexactly(9)
        except asyncio.IncompleteReadError:
            return None
        length = (hdr[0] << 16) | (hdr[1] << 8) | hdr[2]
        ftype = hdr[3]
        flags = hdr[4]
        stream_id = struct.unpack("!I", hdr[5:9])[0] & 0x7FFFFFFF
        try:
            payload = await self._reader.readexactly(length) if length else b""
        except asyncio.IncompleteReadError:
            return None
        return ftype, flags, stream_id, payload

    async def _read_header_block(self, stream_id: int, flags: int, payload: bytes) -> tuple[list[tuple[str, str]], bool]:
        end_stream = bool(flags & FLAG_END_STREAM)
        parts = [_strip_headers_payload(flags, payload)]
        while not (flags & FLAG_END_HEADERS):
            nxt = await self._read_frame()
            if nxt is None:
                raise H2Error("EOF during CONTINUATION")
            ftype, flags, sid, cpayload = nxt
            if ftype != FRAME_CONTINUATION or sid != stream_id:
                raise H2Error("unexpected frame during CONTINUATION")
            parts.append(cpayload)
        try:
            headers = self._decoder.decode(b"".join(parts))
        except HPACKError as exc:
            raise H2Error(str(exc)) from exc
        return headers, end_stream

    def close(self) -> None:
        self._closed = True
        self._window_event.set()
        if not self._writer.is_closing():
            self._writer.close()


class H2ServerStream:
    """One inbound HTTP/2 request/response exchange on an H2Server."""

    def __init__(
        self,
        conn: _H2ServerConnection,
        stream_id: int,
        headers: dict[str, str],
        body: _StreamQueue,
    ) -> None:
        self._conn = conn
        self.stream_id = stream_id
        self.headers = headers
        self.body = body
        self.headers_sent = False
        self.closed = False
        self.cancel_event = asyncio.Event()

    async def respond(
        self,
        status: int,
        headers: dict[str, str],
        body: bytes = b"",
        *,
        end_stream: bool = True,
    ) -> None:
        if self.closed:
            return
        self.headers_sent = True
        hdr_list = [(":status", str(status))] + [(k.lower(), v) for k, v in headers.items()]
        if end_stream and not body:
            await self._conn.send_headers(self.stream_id, hdr_list, end_stream=True)
            self.closed = True
            self._conn.finish_stream(self.stream_id)
            return
        await self._conn.send_headers(self.stream_id, hdr_list, end_stream=False)
        if body:
            await self._conn.send_data(self.stream_id, body, end_stream=end_stream)
        elif end_stream:
            await self._conn.send_data(self.stream_id, b"", end_stream=True)
        if end_stream:
            self.closed = True
            self._conn.finish_stream(self.stream_id)

    async def write_chunk(self, chunk: bytes, *, end_stream: bool = False) -> None:
        if self.closed:
            return
        await self._conn.send_data(self.stream_id, chunk, end_stream=end_stream)
        if end_stream:
            self.closed = True
            self._conn.finish_stream(self.stream_id)

    async def reset(self, error_code: int = ERR_INTERNAL_ERROR) -> None:
        if self.closed:
            return
        self.closed = True
        self.cancel_event.set()
        await self._conn.send_rst_stream(self.stream_id, error_code)
        self._conn.finish_stream(self.stream_id)


class _H2ServerConnection(_H2ConnectionBase):
    def __init__(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
        handler: Callable[[H2ServerStream], Awaitable[None]],
    ) -> None:
        super().__init__(reader, writer)
        self._handler = handler
        self._streams: dict[int, H2ServerStream] = {}
        self._tasks: dict[int, asyncio.Task[None]] = {}
        self._last_stream_id = 0

    def finish_stream(self, stream_id: int) -> None:
        self._streams.pop(stream_id, None)
        self._stream_send_windows.pop(stream_id, None)

    async def start_drain(self) -> None:
        await self.send_goaway(self._last_stream_id, ERR_NO_ERROR)

    async def run(self) -> None:
        try:
            preface = await self._reader.readexactly(len(CLIENT_PREFACE))
            if preface != CLIENT_PREFACE:
                return
            await self._send_raw(
                _pack_settings([(SETTING_INITIAL_WINDOW_SIZE, RECV_WINDOW)])
                + _pack_frame(FRAME_WINDOW_UPDATE, 0, 0, struct.pack("!I", RECV_WINDOW - DEFAULT_WINDOW))
            )
            while not self._closed:
                frame = await self._read_frame()
                if frame is None:
                    break
                ftype, flags, sid, payload = frame
                if ftype == FRAME_SETTINGS:
                    if not (flags & FLAG_ACK):
                        self._apply_peer_settings(payload)
                        await self._send_raw(_pack_settings(ack=True))
                elif ftype == FRAME_PING:
                    if not (flags & FLAG_ACK) and len(payload) == 8:
                        await self._send_raw(_pack_frame(FRAME_PING, FLAG_ACK, 0, payload))
                elif ftype == FRAME_WINDOW_UPDATE:
                    self._apply_window_update(sid, payload)
                elif ftype == FRAME_PRIORITY:
                    continue
                elif ftype == FRAME_GOAWAY:
                    break
                elif ftype == FRAME_HEADERS:
                    if sid == 0 or sid % 2 == 0:
                        raise H2Error("invalid client stream id")
                    pairs, end_stream = await self._read_header_block(sid, flags, payload)
                    if sid in self._streams:
                        # Trailers close the request body.
                        if end_stream:
                            self._streams[sid].body.end()
                        continue
                    self._last_stream_id = max(self._last_stream_id, sid)
                    hdrs: dict[str, str] = {}
                    for k, v in pairs:
                        hdrs[k] = f"{hdrs[k]}, {v}" if k in hdrs and not k.startswith(":") else v

                    def _consume(n: int, stream_id: int = sid) -> None:
                        self._send_window_update(0, n)
                        if stream_id in self._streams:
                            self._send_window_update(stream_id, n)

                    body = _StreamQueue(_consume)
                    if end_stream:
                        body.end()
                    stream = H2ServerStream(self, sid, hdrs, body)
                    self._streams[sid] = stream
                    self._stream_send_windows[sid] = self._peer_initial_window
                    task = asyncio.create_task(self._run_stream(stream))
                    self._tasks[sid] = task
                elif ftype == FRAME_DATA:
                    data = _strip_padding(flags, payload)
                    stream = self._streams.get(sid)
                    if stream is not None:
                        if data:
                            stream.body.feed(data)
                        if flags & FLAG_END_STREAM:
                            stream.body.end()
                    elif data:
                        self._send_window_update(0, len(data))
                elif ftype == FRAME_RST_STREAM:
                    self._cancel_stream(sid)
        except Exception:
            pass
        finally:
            self.close()
            for sid in list(self._streams):
                self._cancel_stream(sid)
            if self._tasks:
                await asyncio.gather(*self._tasks.values(), return_exceptions=True)

    def _cancel_stream(self, sid: int) -> None:
        stream = self._streams.pop(sid, None)
        self._stream_send_windows.pop(sid, None)
        if stream is not None:
            stream.closed = True
            stream.cancel_event.set()
            stream.body.fail(H2StreamReset(ERR_CANCEL))
        task = self._tasks.get(sid)
        if task is not None and not task.done():
            task.cancel()

    async def _run_stream(self, stream: H2ServerStream) -> None:
        try:
            await self._handler(stream)
        except asyncio.CancelledError:
            pass
        except Exception:
            await stream.reset(ERR_INTERNAL_ERROR)
        finally:
            self._tasks.pop(stream.stream_id, None)
            self.finish_stream(stream.stream_id)


class H2Server:
    """Unix-domain HTTP/2 server supporting graceful draining."""

    def __init__(self, socket_path: str, handler: Callable[[H2ServerStream], Awaitable[None]]) -> None:
        self._socket_path = socket_path
        self._handler = handler
        self._server: asyncio.AbstractServer | None = None
        self._conns: set[_H2ServerConnection] = set()
        self._conn_tasks: set[asyncio.Task[None]] = set()
        self._draining = False

    async def start(self) -> None:
        try:
            os.unlink(self._socket_path)
        except FileNotFoundError:
            pass
        self._server = await asyncio.start_unix_server(self._accept, path=self._socket_path)

    def _accept(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        conn = _H2ServerConnection(reader, writer, self._handler)
        self._conns.add(conn)
        task = asyncio.create_task(self._serve_conn(conn))
        self._conn_tasks.add(task)
        task.add_done_callback(self._conn_tasks.discard)
        if self._draining:
            asyncio.create_task(conn.start_drain())

    async def _serve_conn(self, conn: _H2ServerConnection) -> None:
        try:
            await conn.run()
        finally:
            self._conns.discard(conn)

    async def drain_and_close(self) -> None:
        self._draining = True
        if self._server is not None:
            self._server.close()
        for conn in list(self._conns):
            await conn.start_drain()
        # Wait for in-flight handler tasks across all connections to finish.
        tasks: list[asyncio.Task[None]] = []
        for conn in list(self._conns):
            tasks.extend(conn._tasks.values())
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)
        for conn in list(self._conns):
            conn.close()
        if self._conn_tasks:
            await asyncio.gather(*self._conn_tasks, return_exceptions=True)
        if self._server is not None:
            await self._server.wait_closed()
        try:
            os.unlink(self._socket_path)
        except FileNotFoundError:
            pass


class H2Response:
    """Response headers and streaming body returned by H2Client."""

    def __init__(self, status: int, headers: dict[str, str], body: _StreamQueue) -> None:
        self.status = status
        self.headers = headers
        self.body = body


class _H2ClientConnection(_H2ConnectionBase):
    def __init__(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        super().__init__(reader, writer)
        self._next_stream_id = 1
        self._pending_headers: dict[int, asyncio.Future[H2Response]] = {}
        self._bodies: dict[int, _StreamQueue] = {}
        self._reader_task: asyncio.Task[None] | None = None

    @property
    def is_usable(self) -> bool:
        return not self._closed and not self._goaway_sent and not self._writer.is_closing()

    async def handshake(self) -> None:
        await self._send_raw(
            CLIENT_PREFACE
            + _pack_settings([(SETTING_INITIAL_WINDOW_SIZE, RECV_WINDOW)])
            + _pack_frame(FRAME_WINDOW_UPDATE, 0, 0, struct.pack("!I", RECV_WINDOW - DEFAULT_WINDOW))
        )
        self._reader_task = asyncio.create_task(self._read_loop())

    def allocate_stream(self) -> tuple[int, asyncio.Future[H2Response]]:
        sid = self._next_stream_id
        self._next_stream_id += 2
        self._stream_send_windows[sid] = self._peer_initial_window
        fut: asyncio.Future[H2Response] = asyncio.get_running_loop().create_future()
        self._pending_headers[sid] = fut
        return sid, fut

    async def _read_loop(self) -> None:
        error: BaseException = H2Error("host HTTP/2 connection closed")
        try:
            while not self._closed:
                frame = await self._read_frame()
                if frame is None:
                    break
                ftype, flags, sid, payload = frame
                if ftype == FRAME_SETTINGS:
                    if not (flags & FLAG_ACK):
                        self._apply_peer_settings(payload)
                        await self._send_raw(_pack_settings(ack=True))
                elif ftype == FRAME_PING:
                    if not (flags & FLAG_ACK) and len(payload) == 8:
                        await self._send_raw(_pack_frame(FRAME_PING, FLAG_ACK, 0, payload))
                elif ftype == FRAME_WINDOW_UPDATE:
                    self._apply_window_update(sid, payload)
                elif ftype == FRAME_PRIORITY:
                    continue
                elif ftype == FRAME_GOAWAY:
                    self._goaway_sent = True
                    if not self._pending_headers and not self._bodies:
                        break
                elif ftype == FRAME_HEADERS:
                    pairs, end_stream = await self._read_header_block(sid, flags, payload)
                    fut = self._pending_headers.pop(sid, None)
                    if fut is None:
                        body = self._bodies.get(sid)
                        if body is not None and end_stream:
                            body.end()
                            self._bodies.pop(sid, None)
                        continue
                    hdrs: dict[str, str] = {}
                    status = 0
                    for k, v in pairs:
                        if k == ":status":
                            status = int(v)
                        else:
                            hdrs[k] = f"{hdrs[k]}, {v}" if k in hdrs else v

                    def _consume(n: int, stream_id: int = sid) -> None:
                        self._send_window_update(0, n)
                        if stream_id in self._bodies:
                            self._send_window_update(stream_id, n)

                    body = _StreamQueue(_consume)
                    if end_stream:
                        body.end()
                    else:
                        self._bodies[sid] = body
                    if not fut.done():
                        fut.set_result(H2Response(status, hdrs, body))
                elif ftype == FRAME_DATA:
                    data = _strip_padding(flags, payload)
                    body = self._bodies.get(sid)
                    if body is not None:
                        if data:
                            body.feed(data)
                        if flags & FLAG_END_STREAM:
                            body.end()
                            self._bodies.pop(sid, None)
                    elif data:
                        self._send_window_update(0, len(data))
                elif ftype == FRAME_RST_STREAM:
                    code = struct.unpack("!I", payload)[0] if len(payload) == 4 else ERR_INTERNAL_ERROR
                    reset = H2StreamReset(code)
                    self._stream_send_windows.pop(sid, None)
                    fut = self._pending_headers.pop(sid, None)
                    if fut is not None and not fut.done():
                        fut.set_exception(reset)
                    body = self._bodies.pop(sid, None)
                    if body is not None:
                        body.fail(reset)
        except Exception as exc:
            error = exc
        finally:
            self.close()
            for fut in self._pending_headers.values():
                if not fut.done():
                    fut.set_exception(error)
            self._pending_headers.clear()
            for body in self._bodies.values():
                body.fail(error)
            self._bodies.clear()


class H2Client:
    """Pooled Unix-domain HTTP/2 client for Worker -> Host capability calls."""

    def __init__(self, socket_path: str) -> None:
        self._socket_path = socket_path
        self._conn: _H2ClientConnection | None = None
        self._connect_lock = asyncio.Lock()

    async def _connection(self) -> _H2ClientConnection:
        if self._conn is not None and self._conn.is_usable:
            return self._conn
        async with self._connect_lock:
            if self._conn is not None and self._conn.is_usable:
                return self._conn
            reader, writer = await asyncio.open_unix_connection(self._socket_path)
            conn = _H2ClientConnection(reader, writer)
            await conn.handshake()
            self._conn = conn
            return conn

    async def request(
        self,
        method: str,
        path: str,
        headers: dict[str, str],
        body: bytes | AsyncIterable[bytes] | Iterable[bytes] | None = None,
    ) -> H2Response:
        conn = await self._connection()
        sid, response_fut = conn.allocate_stream()
        req_headers = [
            (":method", method),
            (":scheme", "http"),
            (":authority", "host"),
            (":path", path),
        ] + [(k.lower(), v) for k, v in headers.items()]
        send_task: asyncio.Task[None] | None = None
        try:
            if body is None:
                await conn.send_headers(sid, req_headers, end_stream=True)
            elif isinstance(body, (bytes, bytearray, memoryview)):
                raw = bytes(body)
                await conn.send_headers(sid, req_headers, end_stream=len(raw) == 0)
                if raw:
                    await conn.send_data(sid, raw, end_stream=True)
            else:
                await conn.send_headers(sid, req_headers, end_stream=False)
                send_task = asyncio.create_task(_stream_request_body(conn, sid, body))
            response = await response_fut
            if send_task is not None and send_task.done() and not send_task.cancelled():
                exc = send_task.exception()
                if exc is not None:
                    raise exc
            return response
        except BaseException:
            if send_task is not None and not send_task.done():
                send_task.cancel()
            await conn.send_rst_stream(sid, ERR_CANCEL)
            raise

    def close(self) -> None:
        if self._conn is not None:
            self._conn.close()
            self._conn = None


async def _stream_request_body(
    conn: _H2ClientConnection,
    sid: int,
    body: AsyncIterable[bytes] | Iterable[bytes],
) -> None:
    if hasattr(body, "__aiter__"):
        async for chunk in body:  # type: ignore[union-attr]
            if not isinstance(chunk, (bytes, bytearray, memoryview)):
                raise TypeError("storage write stream must yield bytes")
            if chunk:
                await conn.send_data(sid, bytes(chunk), end_stream=False)
    else:
        for chunk in body:  # type: ignore[union-attr]
            if not isinstance(chunk, (bytes, bytearray, memoryview)):
                raise TypeError("storage write stream must yield bytes")
            if chunk:
                await conn.send_data(sid, bytes(chunk), end_stream=False)
    await conn.send_data(sid, b"", end_stream=True)
