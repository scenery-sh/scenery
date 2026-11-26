import asyncio
from typing import Any

from scenerycontract import (
    MISSING,
    Clients,
    ConstructorInput,
    Context,
    MissingType,
    Problem,
    PyDownloadInput,
    PyDownloadOk,
    PyDownloadOutcome,
    PyQuoteInput,
    PyQuoteNegativeAmount,
    PyQuoteOk,
    PyQuoteOutcome,
    PyQuoteResult,
    PySettleInput,
    PySettleOk,
    PySettleOutcome,
    RustClientInput,
    RustClientNegativeAmount,
    RustClientOk,
    Stream,
    stream,
)


class PythonService:
    def __init__(self, clients: Clients, config: Any, database_url: str) -> None:
        self._clients = clients
        self._config = config
        self._database_url = database_url

    async def stop(self) -> None:
        pass

    async def quote(self, ctx: Context, input: PyQuoteInput) -> PyQuoteOutcome:
        if input.amount < 0:
            return PyQuoteNegativeAmount(
                problem=Problem(
                    code="negative_amount",
                    message="The quote amount must be non-negative.",
                )
            )
        if not isinstance(input.delay_ms, MissingType):
            await asyncio.sleep(max(0, input.delay_ms) / 1000.0)
        if input.note == "rust":
            rust_reply = await self._clients.rust.invoke(ctx, RustClientInput(amount=input.amount))
            if isinstance(rust_reply, RustClientOk):
                val = rust_reply.value
                return PyQuoteOk(
                    value=PyQuoteResult(
                        vendor=val.vendor,
                        amount=val.amount,
                        note=val.note,
                        principal=val.principal,
                        tenant=val.tenant,
                        trace_id=val.trace_id,
                        deadline=val.deadline,
                        currency=val.currency,
                    )
                )
            if isinstance(rust_reply, RustClientNegativeAmount):
                return PyQuoteNegativeAmount(problem=rust_reply.problem)
            raise RuntimeError(f"unexpected rust reply: {rust_reply!r}")

        native = await self._clients.native.invoke(ctx, {})
        if native.kind != "result":
            raise RuntimeError("native label failed")
        val = native.value
        if ctx.invocation.principal is not None:
            trace_id = (ctx.invocation.traceparent or "").split("-")[1] if ctx.invocation.traceparent else None
            if (
                val.principal != ctx.invocation.principal
                or val.tenant != ctx.invocation.tenant_id
                or val.trace_id != trace_id
                or isinstance(val.deadline, MissingType)
            ):
                raise RuntimeError("invocation context was lost")
        currency: str | MissingType = MISSING
        if isinstance(self._config, dict):
            raw_currency = self._config.get("values", {}).get("python.currency")
            if isinstance(raw_currency, str):
                currency = raw_currency
        return PyQuoteOk(
            value=PyQuoteResult(
                vendor="python:" + val.vendor,
                amount=input.amount,
                note=input.note,
                principal=val.principal,
                tenant=val.tenant,
                trace_id=val.trace_id,
                deadline=val.deadline,
                currency=currency,
            )
        )

    def download(self, ctx: Context, input: PyDownloadInput) -> Stream[PyDownloadOutcome]:
        payload = b"P" * (64 * 1024)
        return stream(PyDownloadOk(value=b""), [payload], len(payload))

    async def settle(self, ctx: Context, input: PySettleInput) -> PySettleOutcome:
        if ctx.mode != "durable" or ctx.invocation.execution_id is None:
            raise RuntimeError("missing durable execution identity")
        if not self._database_url:
            raise RuntimeError("missing native database URL")
        return PySettleOk(value={})


def new_service(input: ConstructorInput) -> PythonService:
    database_url = input.databases.get("database", "")
    if not database_url:
        raise RuntimeError("missing native database URL")
    return PythonService(input.clients, input.config, database_url)
