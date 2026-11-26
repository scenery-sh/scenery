from datetime import datetime, timezone
from decimal import Decimal, ROUND_HALF_UP
import json

from scenerycontract import (
    MISSING,
    CapabilityError,
    Clients,
    ConstructorInput,
    Context,
    GetStatsInput,
    GetStatsOk,
    GetStatsOutcome,
    LinksSnapshotClientOk,
    MissingType,
    RecordClickInput,
    RecordClickOk,
    RecordClickOutcome,
    RollupStatsInput,
    RollupStatsOk,
    RollupStatsOutcome,
    StatsSummary,
)

ROLLUP_KEY = "analytics/rollup.json"


class AnalyticsService:
    """Computes link analytics, records click events, and publishes scheduled rollups."""

    def __init__(self, clients: Clients) -> None:
        self._clients = clients

    async def record_click(self, ctx: Context, input: RecordClickInput) -> RecordClickOutcome:
        key = f"analytics/clicks/{input.slug}.json"
        existing = await _read_json(ctx, key)
        count = int(existing.get("count", 0)) + 1 if isinstance(existing, dict) else 1
        payload = {
            "slug": input.slug,
            "count": count,
            "last_click_id": input.click_id,
            "last_clicked_at": input.clicked_at,
        }
        await ctx.storage_write(
            "assets",
            key,
            json.dumps(payload, sort_keys=True).encode("utf-8"),
            content_type="application/json",
        )
        return RecordClickOk(value=input.click_id)

    async def rollup_stats(self, ctx: Context, _input: RollupStatsInput) -> RollupStatsOutcome:
        now = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        summary = await self._compute_summary(ctx, last_rollup_at=now)
        report = {
            "total_links": summary.total_links,
            "total_clicks": summary.total_clicks,
            "avg_clicks_per_link": str(summary.avg_clicks_per_link),
            "top_slug": None if isinstance(summary.top_slug, MissingType) else summary.top_slug,
            "last_rollup_at": now,
        }
        await ctx.storage_write(
            "assets",
            ROLLUP_KEY,
            json.dumps(report, sort_keys=True).encode("utf-8"),
            content_type="application/json",
        )
        return RollupStatsOk(value=summary)

    async def get_stats(self, ctx: Context, _input: GetStatsInput) -> GetStatsOutcome:
        cached = await _read_json(ctx, ROLLUP_KEY)
        last_rollup: str | MissingType = MISSING
        if isinstance(cached, dict) and isinstance(cached.get("last_rollup_at"), str):
            last_rollup = cached["last_rollup_at"]
        summary = await self._compute_summary(ctx, last_rollup_at=last_rollup)
        return GetStatsOk(value=summary)

    async def _compute_summary(
        self,
        ctx: Context,
        *,
        last_rollup_at: str | MissingType,
    ) -> StatsSummary:
        reply = await self._clients.links_snapshot.invoke(ctx, {})
        if not isinstance(reply, LinksSnapshotClientOk):
            raise RuntimeError(f"unexpected links snapshot reply: {reply!r}")
        items = reply.value.items
        total_links = len(items)
        total_clicks = sum(item.clicks for item in items)
        if total_links > 0:
            avg = (Decimal(total_clicks) / Decimal(total_links)).quantize(
                Decimal("0.01"), rounding=ROUND_HALF_UP
            )
            top = max(items, key=lambda item: item.clicks)
            top_slug: str | MissingType = top.slug
        else:
            avg = Decimal("0.00")
            top_slug = MISSING
        return StatsSummary(
            total_links=total_links,
            total_clicks=total_clicks,
            avg_clicks_per_link=_canonical_decimal(avg),
            top_slug=top_slug,
            last_rollup_at=last_rollup_at,
        )


def _canonical_decimal(value: Decimal) -> str:
    normalized = value.normalize()
    if normalized == 0:
        return "0"
    text = format(normalized, "f")
    if "." in text:
        text = text.rstrip("0").rstrip(".")
    return text


async def _read_json(ctx: Context, key: str) -> object | None:
    try:
        resp = await ctx.storage_read("assets", key)
    except CapabilityError:
        return None
    raw = await resp.read_all()
    if not raw:
        return None
    try:
        return json.loads(raw.decode("utf-8"))
    except ValueError:
        return None


def new_service(input: ConstructorInput) -> AnalyticsService:
    return AnalyticsService(input.clients)
