"""Worker -> Host scoped capability client over HTTP/2 on UDS."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass, field
import json
from types import MappingProxyType
from typing import Any, AsyncIterable, AsyncIterator, Iterable, Mapping
from urllib.parse import urlencode

from ._h2c import H2Client, H2Response
from .codec import parse_exact_json

MAX_METADATA = 32 << 20


class CapabilityError(Exception):
    """Raised when a Host capability call fails or is interrupted."""

    def __init__(self, message: str, delivery: str | None = None, details: dict[str, Any] | None = None) -> None:
        super().__init__(message)
        self.delivery = delivery
        self.details = details or {}


@dataclass(frozen=True, slots=True)
class InvocationContext:
    """Authenticated invocation metadata supplied by the Host."""

    invocation_id: str
    generation: int
    contract_revision: str
    principal: str | None = None
    tenant_id: str | None = None
    traceparent: str | None = None
    deadline: str | None = None
    locale: str | None = None
    execution_id: str | None = None
    metadata: Mapping[str, Any] = field(default_factory=lambda: MappingProxyType({}))


class StorageReadResponse:
    """Streaming object body and metadata headers returned by storage_read."""

    def __init__(self, headers: Mapping[str, str], response: H2Response) -> None:
        self.headers = MappingProxyType(dict(headers))
        self._response = response

    def __aiter__(self) -> AsyncIterator[bytes]:
        return self._response.body.__aiter__()

    async def read_all(self, limit: int = 256 << 20) -> bytes:
        return await self._response.body.read_all(limit)


class Context:
    """Per-invocation context exposing invocation identity and Host capabilities."""

    def __init__(
        self,
        host: "Host",
        scope: str,
        invocation: InvocationContext,
        mode: str,
        binding: str | None,
        cancel_event: asyncio.Event,
    ) -> None:
        self._host = host
        self._scope = scope
        self.invocation = invocation
        self.mode = mode
        self.binding = binding
        self.cancel_event = cancel_event

    @classmethod
    def standalone(
        cls,
        principal: str | None = None,
        tenant_id: str | None = None,
        invocation_id: str = "standalone",
        mode: str = "direct",
        binding: str | None = None,
        execution_id: str | None = None,
    ) -> "Context":
        """Construct an offline Context for unit and regression tests."""
        resolved_exec_id = execution_id if execution_id is not None else (invocation_id if mode == "durable" else None)
        return cls(
            Host({"token": "", "host_socket": ""}),
            "",
            InvocationContext(
                invocation_id=invocation_id,
                generation=1,
                contract_revision="",
                principal=principal,
                tenant_id=tenant_id,
                execution_id=resolved_exec_id,
            ),
            mode,
            binding,
            asyncio.Event(),
        )

    def is_cancelled(self) -> bool:
        return self.cancel_event.is_set()

    async def binding_request(self, client: str, input_json: str, *, enqueue: bool = False) -> Any:
        payload = ('{"client":' + json.dumps(client) + ',"input":' + input_json + "}").encode("utf-8")
        if len(payload) > MAX_METADATA:
            raise CapabilityError("binding metadata exceeds the limit", "not_sent")
        path = "durable/enqueue" if enqueue else "binding/invoke"
        raw = await self._read("POST", path, 200, payload, {"content-type": "application/json"})
        try:
            reply = parse_exact_json(raw)
        except Exception as exc:
            raise CapabilityError("invalid host binding reply", "unknown") from exc
        if isinstance(reply, dict) and "error" in reply and "output" not in reply:
            error = json.loads(raw)["error"]
            delivery = None
            detail = error
            while isinstance(detail, dict):
                meta = detail.get("meta") if isinstance(detail.get("meta"), dict) else {}
                val = meta.get("delivery") or detail.get("outcome")
                if val in ("not_sent", "unknown"):
                    delivery = val
                    break
                detail = detail.get("cause")
            raise CapabilityError(error.get("message", "host binding failed"), delivery, error)
        if not isinstance(reply, dict) or "output" not in reply or len(reply) != 1:
            raise CapabilityError("invalid host binding reply", "unknown")
        return reply["output"]

    async def config_snapshot(self) -> Any:
        return parse_exact_json(await self._read("GET", "config/snapshot", 200))

    async def connection_token(self, provider: str, scopes: list[str] | tuple[str, ...]) -> Any:
        data = await self._post_json("connections/token", {"provider": provider, "scopes": list(scopes)})
        return data["token"]

    async def telemetry(self, records: list[dict[str, Any]] | tuple[dict[str, Any], ...]) -> None:
        if len(records) > 64:
            raise ValueError("telemetry batch exceeds 64 records")
        await self._post_json("telemetry", list(records))

    async def storage_read(self, store: str, key: str, *, if_match: str | None = None) -> StorageReadResponse:
        resp = await self._request(
            "GET",
            _storage_path("read", store, {"key": key}),
            200,
            headers=_storage_conditions(if_match=if_match),
        )
        return StorageReadResponse(resp.headers, resp)

    async def storage_head(self, store: str, key: str) -> Mapping[str, str]:
        resp = await self._request("HEAD", _storage_path("read", store, {"key": key}), 200)
        return MappingProxyType(dict(resp.headers))

    async def storage_write(
        self,
        store: str,
        key: str,
        body: bytes | AsyncIterable[bytes] | Iterable[bytes],
        *,
        content_type: str | None = None,
        if_match: str | None = None,
        if_none_match: bool | str = False,
    ) -> dict[str, Any]:
        headers = _storage_conditions(if_match=if_match, if_none_match=if_none_match, allow_if_none_match=True)
        if content_type is not None:
            headers["content-type"] = content_type
        return json.loads(await self._read("PUT", _storage_path("write", store, {"key": key}), 201, body, headers))

    async def storage_delete(self, store: str, key: str, *, if_match: str | None = None) -> None:
        await self._request(
            "DELETE",
            _storage_path("delete", store, {"key": key}),
            204,
            headers=_storage_conditions(if_match=if_match),
        )

    async def storage_delete_prefix(self, store: str, prefix: str = "") -> None:
        params: dict[str, Any] = {"recursive": "1"}
        if prefix:
            params["key"] = prefix
        await self._request("DELETE", _storage_path("delete", store, params), 204)

    async def storage_list(
        self,
        store: str,
        *,
        prefix: str | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> dict[str, Any]:
        params = {"prefix": prefix, "cursor": cursor, "limit": limit}
        return json.loads(await self._read("GET", _storage_path("list", store, params), 200))

    async def _post_json(self, path: str, value: Any) -> Any:
        body = json.dumps(value, separators=(",", ":")).encode("utf-8")
        return json.loads(await self._read("POST", path, 200, body, {"content-type": "application/json"}))

    async def _read(
        self,
        method: str,
        path: str,
        expected: int,
        body: bytes | AsyncIterable[bytes] | Iterable[bytes] | None = None,
        headers: dict[str, str] | None = None,
    ) -> str:
        resp = await self._request(method, path, expected, body=body, headers=headers)
        try:
            raw = await resp.body.read_all(MAX_METADATA)
            return raw.decode("utf-8")
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            raise CapabilityError("invalid or interrupted host capability response", "unknown") from exc

    async def _request(
        self,
        method: str,
        path: str,
        expected: int,
        *,
        body: bytes | AsyncIterable[bytes] | Iterable[bytes] | None = None,
        headers: dict[str, str] | None = None,
    ) -> H2Response:
        if self.cancel_event.is_set():
            raise asyncio.CancelledError("invocation canceled")
        req_headers = dict(headers or {})
        req_headers["authorization"] = "Bearer " + self._host.token
        req_headers["x-worker-scope"] = self._scope
        try:
            resp = await self._host.client.request(method, "/host/v1/" + path, req_headers, body=body)
        except asyncio.CancelledError:
            raise
        except OSError as exc:
            raise CapabilityError("host capability unavailable", "not_sent") from exc
        except Exception as exc:
            raise CapabilityError("host capability interrupted", "unknown") from exc
        if resp.status != expected:
            details = await _read_capability_error(resp)
            delivery = "not_sent" if 400 <= resp.status < 500 else "unknown"
            raise CapabilityError(details.get("message", "host rejected capability"), delivery, details)
        return resp


class Host:
    """Shared Host capability transport for one Python worker process."""

    def __init__(self, launch: dict[str, Any]) -> None:
        self.token: str = launch["token"]
        self.client = H2Client(launch["host_socket"])

    def context(self, envelope: dict[str, Any], cancel_event: asyncio.Event) -> Context:
        raw_ctx = envelope["context"]
        metadata = raw_ctx.get("metadata")
        invocation = InvocationContext(
            invocation_id=raw_ctx["invocation_id"],
            generation=raw_ctx["generation"],
            contract_revision=raw_ctx["contract_revision"],
            principal=raw_ctx.get("principal"),
            tenant_id=raw_ctx.get("tenant_id"),
            traceparent=raw_ctx.get("traceparent"),
            deadline=raw_ctx.get("deadline"),
            locale=raw_ctx.get("locale"),
            execution_id=raw_ctx.get("execution_id"),
            metadata=MappingProxyType(metadata) if isinstance(metadata, dict) else MappingProxyType({}),
        )
        return Context(
            self,
            raw_ctx["scope"],
            invocation,
            envelope["mode"],
            envelope.get("binding"),
            cancel_event,
        )

    def close(self) -> None:
        self.client.close()


def _storage_path(operation: str, store: str, params: dict[str, Any]) -> str:
    query = [("store", store)]
    for k, v in params.items():
        if v is not None and k != "store":
            query.append((k, str(v)))
    return f"storage/{operation}?{urlencode(query)}"


def _storage_conditions(
    *,
    if_match: str | None = None,
    if_none_match: bool | str = False,
    allow_if_none_match: bool = False,
) -> dict[str, str]:
    headers: dict[str, str] = {}
    if if_match is not None:
        headers["if-match"] = if_match
    if if_none_match is not False and if_none_match is not None:
        if not allow_if_none_match or (if_none_match is not True and if_none_match != "*"):
            raise ValueError("invalid storage if_none_match condition")
        headers["if-none-match"] = "*"
    return headers


async def _read_capability_error(resp: H2Response) -> dict[str, Any]:
    details: dict[str, Any] = {"status": resp.status}
    try:
        raw = await resp.body.read_all(64 << 10)
        parsed = json.loads(raw.decode("utf-8"))
        if isinstance(parsed, dict):
            if isinstance(parsed.get("code"), str):
                details["code"] = parsed["code"]
            if isinstance(parsed.get("message"), str):
                details["message"] = parsed["message"]
    except Exception:
        pass
    return details
