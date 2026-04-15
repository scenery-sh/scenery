import type { TraceSummary } from "./types";

export type TraceCompatID = string | { high?: string; low?: string };

export interface TraceCompatEvent {
  trace_id?: TraceCompatID;
  span_id?: string;
  event_id?: string;
  event_time?: string;
  span_start?: Record<string, unknown>;
  span_end?: Record<string, unknown>;
  span_event?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface TraceSpanBoundary {
  at: string;
  kind: string;
  payload: Record<string, unknown>;
}

export interface TraceSpanEventItem {
  id: string;
  at: string;
  kind: string;
  title: string;
  payload: Record<string, unknown>;
}

export interface TraceSpanModel {
  id: string;
  rawID: string;
  traceID: string;
  parentID?: string;
  parentRawID?: string;
  serviceName: string;
  endpointName: string;
  kind: string;
  title: string;
  startedAt?: string;
  endedAt?: string;
  durationNanos?: number;
  statusCode?: string;
  httpStatusCode?: number;
  userID?: string;
  isError: boolean;
  isRoot: boolean;
  depth: number;
  start?: TraceSpanBoundary;
  end?: TraceSpanBoundary;
  events: TraceSpanEventItem[];
  summary?: TraceSummary;
}

export interface TraceModel {
  traceID: string;
  spans: TraceSpanModel[];
  rootSpan?: TraceSpanModel;
  userID?: string;
}

interface MutableTraceSpanModel extends TraceSpanModel {
  childIDs: string[];
}

export function normalizeTraceID(value: unknown): string {
  if (typeof value === "string") {
    const parsed = parseBigInt(value, "hex-first");
    if (parsed === null) {
      return value;
    }
    return parsed.toString(16).padStart(32, "0");
  }
  if (!value || typeof value !== "object") {
    return "";
  }
  const record = value as { high?: unknown; low?: unknown };
  const high = parseBigInt(String(record.high ?? "0"), "decimal-first") ?? 0n;
  const low = parseBigInt(String(record.low ?? "0"), "decimal-first") ?? 0n;
  return ((high << 64n) | low).toString(16).padStart(32, "0");
}

export function normalizeSpanID(value: string | null | undefined): string {
  if (!value) {
    return "";
  }
  const parsed = parseBigInt(value, "hex-first");
  return parsed === null ? value : parsed.toString(10);
}

export function buildTraceModel(
  traceID: string,
  summaries: TraceSummary[],
  events: TraceCompatEvent[],
): TraceModel {
  const summaryByNormID = new Map<string, TraceSummary>();
  for (const summary of summaries) {
    summaryByNormID.set(normalizeSpanID(summary.span_id), summary);
  }

  const spanMap = new Map<string, MutableTraceSpanModel>();

  const ensureSpan = (normID: string): MutableTraceSpanModel => {
    const existing = spanMap.get(normID);
    if (existing) {
      return existing;
    }
    const summary = summaryByNormID.get(normID);
    const span: MutableTraceSpanModel = {
      id: normID,
      rawID: summary?.span_id || normID,
      traceID,
      serviceName: summary?.service_name || "",
      endpointName: summary?.endpoint_name || "",
      kind: summaryKind(summary),
      title: summaryTitle(summary),
      startedAt: summary?.started_at,
      durationNanos: summary?.duration_nanos,
      isError: Boolean(summary?.is_error),
      isRoot: Boolean(summary?.is_root),
      depth: 0,
      events: [],
      summary,
      childIDs: [],
    };
    if (summary?.parent_span_id) {
      span.parentID = normalizeSpanID(summary.parent_span_id);
      span.parentRawID = summary.parent_span_id;
    }
    spanMap.set(normID, span);
    return span;
  };

  for (const summary of summaries) {
    ensureSpan(normalizeSpanID(summary.span_id));
  }

  for (const event of events) {
    const normID = normalizeSpanID(typeof event.span_id === "string" ? event.span_id : "");
    if (!normID) {
      continue;
    }
    const span = ensureSpan(normID);
    const eventID = typeof event.event_id === "string" ? event.event_id : "";
    const at = typeof event.event_time === "string" ? event.event_time : "";

    if (isRecord(event.span_start)) {
      const entry = firstEntry(event.span_start);
      if (entry) {
        const [kind, rawPayload] = entry;
        const payload = asRecord(rawPayload);
        span.start = { at, kind, payload };
        span.kind = resolveSpanKind(kind, span.kind);
        span.serviceName = stringField(payload.service_name) || span.serviceName;
        span.endpointName =
          kind === "db"
            ? stringField(payload.operation) || span.endpointName
            : stringField(payload.endpoint_name) || span.endpointName;
        span.userID = stringField(payload.uid) || span.userID;
        span.startedAt = at || span.startedAt;
        const parent = normalizeSpanID(stringField(payload.parent_span_id));
        if (parent) {
          span.parentID = parent;
          span.parentRawID = span.parentRawID || stringField(payload.parent_span_id);
        }
        span.title = traceSpanTitle(span);
      }
      continue;
    }

    if (isRecord(event.span_end)) {
      const entry = firstEntry(event.span_end);
      if (entry) {
        const [kind, rawPayload] = entry;
        const payload = asRecord(rawPayload);
        span.end = { at, kind, payload };
        span.kind = resolveSpanKind(kind, span.kind);
        span.endedAt = at || span.endedAt;
        span.durationNanos = numericField(event.span_end.duration_nanos) ?? span.durationNanos;
        span.statusCode = stringField(event.span_end.status_code) || span.statusCode;
        span.isError = Boolean(span.isError || payload.error || event.span_end.error);
        span.httpStatusCode = numericField(payload.http_status_code) ?? span.httpStatusCode;
        span.userID = stringField(payload.uid) || span.userID;
        span.serviceName = stringField(payload.service_name) || span.serviceName;
        span.endpointName =
          kind === "db"
            ? stringField(payload.operation) || span.endpointName
            : stringField(payload.endpoint_name) || span.endpointName;
        span.title = traceSpanTitle(span);
      }
      continue;
    }

    if (isRecord(event.span_event)) {
      const entry = firstEntry(event.span_event);
      if (!entry) {
        continue;
      }
      const [kind, rawPayload] = entry;
      span.events.push({
        id: eventID,
        at,
        kind,
        title: humanizeTraceKind(kind),
        payload: asRecord(rawPayload),
      });
    }
  }

  for (const span of spanMap.values()) {
    if (!span.title) {
      span.title = traceSpanTitle(span);
    }
  }

  for (const span of spanMap.values()) {
    if (!span.parentID) {
      continue;
    }
    const parent = spanMap.get(span.parentID);
    if (!parent) {
      continue;
    }
    if (!parent.childIDs.includes(span.id)) {
      parent.childIDs.push(span.id);
    }
  }

  const childSort = (a: string, b: string) => compareTime(spanMap.get(a)?.startedAt, spanMap.get(b)?.startedAt);
  for (const span of spanMap.values()) {
    span.childIDs.sort(childSort);
    span.events.sort((a, b) => compareEvent(a, b));
  }

  const root =
    [...spanMap.values()].find((span) => span.isRoot) ||
    [...spanMap.values()].find((span) => !span.parentID) ||
    [...spanMap.values()][0];

  const ordered: MutableTraceSpanModel[] = [];
  const visit = (span: MutableTraceSpanModel | undefined, depth: number) => {
    if (!span) {
      return;
    }
    span.depth = depth;
    ordered.push(span);
    for (const childID of span.childIDs) {
      visit(spanMap.get(childID), depth + 1);
    }
  };
  visit(root, 0);

  const remaining = [...spanMap.values()]
    .filter((span) => !ordered.includes(span))
    .sort((a, b) => compareTime(a.startedAt, b.startedAt));
  for (const span of remaining) {
    visit(span, 0);
  }

  return {
    traceID,
    spans: ordered.map(stripChildren),
    rootSpan: root ? stripChildren(root) : undefined,
    userID: ordered.map((span) => span.userID).find(Boolean),
  };
}

function stripChildren(span: MutableTraceSpanModel): TraceSpanModel {
  const { childIDs: _childIDs, ...rest } = span;
  return rest;
}

function compareEvent(a: TraceSpanEventItem, b: TraceSpanEventItem): number {
  const timeCmp = compareTime(a.at, b.at);
  if (timeCmp !== 0) {
    return timeCmp;
  }
  return (numericField(a.id) ?? 0) - (numericField(b.id) ?? 0);
}

function compareTime(a?: string, b?: string): number {
  const left = a ? Date.parse(a) : 0;
  const right = b ? Date.parse(b) : 0;
  return left - right;
}

function parseBigInt(value: string, mode: "hex-first" | "decimal-first"): bigint | null {
  const trimmed = value.trim().replace(/^0[xX]/, "");
  if (!trimmed) {
    return null;
  }
  const candidates = mode === "hex-first" ? [16, 10] : [10, 16];
  for (const base of candidates) {
    try {
      return BigInt(base === 16 ? `0x${trimmed}` : trimmed);
    } catch {
      continue;
    }
  }
  return null;
}

function summaryKind(summary?: TraceSummary): string {
  switch (summary?.type) {
    case "AUTH":
      return "auth";
    case "DB":
      return "db";
    default:
      return "request";
  }
}

function summaryTitle(summary?: TraceSummary): string {
  if (!summary) {
    return "";
  }
  if (summary.type === "DB") {
    return `DB ${summary.endpoint_name || "query"}`;
  }
  if (summary.type === "AUTH") {
    return summary.service_name ? `${summary.service_name}.AuthHandler` : "Auth Handler";
  }
  if (summary.service_name && summary.endpoint_name) {
    return `${summary.service_name}.${summary.endpoint_name}`;
  }
  return summary.service_name || summary.endpoint_name || "Trace span";
}

function resolveSpanKind(kind: string, fallback: string): string {
  switch (kind) {
    case "request":
    case "auth":
    case "db":
      return kind;
    default:
      return fallback || "request";
  }
}

function traceSpanTitle(span: Pick<TraceSpanModel, "kind" | "serviceName" | "endpointName">): string {
  if (span.kind === "db") {
    return `DB ${span.endpointName || "query"}`;
  }
  if (span.kind === "auth") {
    return span.serviceName ? `${span.serviceName}.AuthHandler` : "Auth Handler";
  }
  if (span.serviceName && span.endpointName) {
    return `${span.serviceName}.${span.endpointName}`;
  }
  return span.serviceName || span.endpointName || humanizeTraceKind(span.kind || "request");
}

function humanizeTraceKind(value: string): string {
  return value
    .replace(/_/g, " ")
    .replace(/\b\w/g, (char) => char.toUpperCase());
}

function firstEntry(record: Record<string, unknown>): [string, unknown] | null {
  const entry = Object.entries(record)[0];
  return entry ?? null;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function asRecord(value: unknown): Record<string, unknown> {
  return isRecord(value) ? value : {};
}

function stringField(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function numericField(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim()) {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return undefined;
}
