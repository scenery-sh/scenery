import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { JSONView } from "../components/json-view";
import { useDashboard } from "../lib/dashboard-context";
import {
  loadPersistedTabs,
  makeTabFromEndpoint,
  persistTabs,
  type RequestTab,
} from "../lib/api-explorer";
import {
  buildTraceModel,
  normalizeTraceID,
  normalizeSpanID,
  type TraceCompatEvent,
  type TraceSpanEventItem,
  type TraceSpanModel,
} from "../lib/traces";
import {
  cn,
  decodeBase64Utf8,
  formatDurationNanos,
  formatTime,
  formatTimestamp,
  renderMetadataPath,
  tryParseJSON,
} from "../lib/utils";
import type { ApiCallResponse, EndpointOption, TraceSummary } from "../lib/types";

type ReplayState = {
  method: string;
  path: string;
  payloadText: string;
};

export function TracesPage({ traceId, spanId }: { traceId?: string; spanId?: string }) {
  const navigate = useNavigate();
  const { appId, callAPI, meta, rpc, traces } = useDashboard();
  const [events, setEvents] = useState<TraceCompatEvent[]>([]);
  const [summaries, setSummaries] = useState<TraceSummary[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [replayOpen, setReplayOpen] = useState(false);
  const [replayState, setReplayState] = useState<ReplayState | null>(null);
  const [replayResponse, setReplayResponse] = useState<ApiCallResponse | null>(null);
  const [replayError, setReplayError] = useState<string | null>(null);
  const [replayLoading, setReplayLoading] = useState(false);

  const endpointOptions = useMemo<EndpointOption[]>(
    () =>
      (meta?.svcs ?? []).flatMap((svc) =>
        svc.rpcs.map((rpcMeta) => ({
          key: `${svc.name}.${rpcMeta.name}`,
          svcName: svc.name,
          rpcName: rpcMeta.name,
          method: rpcMeta.http_methods?.[0] || "GET",
          path: renderMetadataPath(rpcMeta.path),
        })),
      ),
    [meta],
  );

  const endpointMap = useMemo(
    () => new Map(endpointOptions.map((item) => [item.key, item])),
    [endpointOptions],
  );

  useEffect(() => {
    if (!rpc || !traceId) {
      setEvents([]);
      setSummaries([]);
      setError(null);
      return;
    }
    setLoading(true);
    setError(null);
    Promise.all([
      rpc.request<TraceCompatEvent[]>("traces/get", { app_id: appId, trace_id: traceId }),
      rpc.request<TraceSummary[]>("traces/spans/summaries/list", { app_id: appId, trace_id: traceId }),
    ])
      .then(([nextEvents, nextSummaries]) => {
        setEvents(nextEvents ?? []);
        setSummaries(nextSummaries ?? []);
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false));
  }, [appId, rpc, traceId]);

  const model = useMemo(
    () => (traceId ? buildTraceModel(traceId, summaries, events) : null),
    [events, summaries, traceId],
  );

  const childMap = useMemo(() => {
    const map = new Map<string, TraceSpanModel[]>();
    for (const span of model?.spans ?? []) {
      if (!span.parentID) {
        continue;
      }
      const bucket = map.get(span.parentID) ?? [];
      bucket.push(span);
      map.set(span.parentID, bucket);
    }
    for (const bucket of map.values()) {
      bucket.sort((a, b) => compareDateString(a.startedAt, b.startedAt));
    }
    return map;
  }, [model]);

  const selectedSpan = useMemo(() => {
    if (!model) {
      return null;
    }
    const normalized = normalizeSpanID(spanId || "");
    return (
      model.spans.find((item) => item.id === normalized || item.rawID === spanId) ||
      model.rootSpan ||
      model.spans[0] ||
      null
    );
  }, [model, spanId]);

  const selectedTraceSummary = traces.find((item) => item.trace_id === traceId) || model?.rootSpan?.summary;
  const selectedCounts = useMemo(
    () => (selectedSpan ? countSpanActivity(selectedSpan, childMap) : null),
    [childMap, selectedSpan],
  );
  const lanes = useMemo(() => (model ? buildTraceLanes(model.spans) : []), [model]);

  useEffect(() => {
    if (!selectedSpan) {
      setReplayState(null);
      setReplayResponse(null);
      setReplayError(null);
      setReplayOpen(false);
      return;
    }
    const request = requestStartPayload(selectedSpan);
    if (!request) {
      setReplayState(null);
      setReplayResponse(null);
      setReplayError(null);
      setReplayOpen(false);
      return;
    }
    setReplayState({
      method: stringField(request.http_method) || "GET",
      path: stringField(request.path) || "/",
      payloadText: requestPayloadText(request.request_payload),
    });
    setReplayResponse(null);
    setReplayError(null);
  }, [selectedSpan]);

  if (!traceId) {
    return null;
  }

  return (
    <section className="h-[calc(100vh-var(--header-height))]">
      {loading ? (
        <div className="h-full flex items-center justify-center py-4">
          <div className="h-8 w-8 animate-spin rounded-full border-2 border-border border-t-foreground" />
        </div>
      ) : error ? (
        <div className="p-8">
          <div className="rounded-md border border-red-500/30 bg-red-500/10 px-4 py-3 text-sm text-red-500">
            {error}
          </div>
        </div>
      ) : !model || !selectedSpan ? (
        <div className="p-8 text-sm text-muted-foreground">Trace not found.</div>
      ) : (
        <div className="h-full flex flex-col min-w-0 overflow-hidden">
          <section className="flex md:flex-row flex-col items-stretch flex-1 min-h-0 overflow-auto md:overflow-hidden min-w-0">
            <div
              className={cn(
                "flex flex-col relative min-w-0 overflow-hidden w-full md:min-h-0 min-h-[50vh]",
                replayOpen ? "md:w-4/12" : "md:w-1/2",
              )}
            >
              <div className="flex flex-col px-4 pt-4 pb-0 shrink-0">
                <div className="shrink-0 md:mr-4">
                  <h1 className="text-lg md:text-xl font-medium leading-none flex flex-wrap items-center mb-2 gap-2">
                    <BackButton appId={appId} />
                    <span>Trace Details</span>
                  </h1>
                  <div className="overflow-x-auto">
                    <table className="text-xs">
                      <tbody>
                        <tr>
                          <th className="text-left text-xs text-foreground font-semibold pr-4 py-1">Duration</th>
                          <td>{formatDurationNanos(totalTraceDuration(model, selectedTraceSummary))}</td>
                        </tr>
                        <tr>
                          <th className="text-left text-xs text-foreground font-semibold pr-4 py-1">Recorded</th>
                          <td>{formatTimestamp(selectedTraceSummary?.started_at || model.rootSpan?.startedAt)}</td>
                        </tr>
                        <tr>
                          <th className="text-left text-xs text-foreground font-semibold pr-4 py-1">Trace ID</th>
                          <td className="font-mono text-[11px]">{traceId}</td>
                        </tr>
                        {model.userID ? (
                          <tr>
                            <th className="text-left text-xs text-foreground font-semibold pr-4 py-1">User ID</th>
                            <td>{model.userID}</td>
                          </tr>
                        ) : null}
                      </tbody>
                    </table>
                  </div>
                </div>
                <div className="mt-6 w-full flex items-end self-end">
                  <TraceTimeline lanes={lanes} selectedSpanID={selectedSpan.id} />
                </div>
              </div>

              <div className="flex flex-col w-full flex-1 min-h-0 min-w-0 overflow-auto">
                <TraceSpanTree
                  appId={appId}
                  traceId={model.traceID}
                  spans={model.spans}
                  selectedSpanID={selectedSpan.id}
                  childMap={childMap}
                />
                <div className="shrink-0 h-20" />
              </div>
            </div>

            <div
              className={cn(
                "relative min-w-0 w-full md:min-h-0 min-h-[50vh] md:border-l-0 border-t md:border-t-0 flex flex-col overflow-hidden",
                replayOpen ? "md:w-5/12" : "md:w-1/2",
              )}
            >
              <PanelDividerLine />
              <SpanDetail
                appId={appId}
                span={selectedSpan}
                counts={selectedCounts}
                onOpenExplorer={() => void openInAPIExplorer(selectedSpan)}
                onReplay={() => setReplayOpen((value) => !value)}
                replayOpen={replayOpen}
              />
            </div>

            {replayOpen ? (
              <div className="overflow-hidden h-full md:h-full h-auto md:min-h-0 min-h-[50vh] md:w-3/12 w-full">
                <div className="p-4 relative w-full md:min-w-[350px] h-full overflow-auto">
                  <PanelDividerLine />
                  {replayState ? (
                    <ReplayPanel
                      appId={appId}
                      state={replayState}
                      onChange={setReplayState}
                      onClose={() => setReplayOpen(false)}
                      loading={replayLoading}
                      response={replayResponse}
                      error={replayError}
                      onSubmit={async () => {
                        if (!replayState) {
                          return;
                        }
                        await replaySelectedSpan(selectedSpan, replayState);
                      }}
                    />
                  ) : null}
                </div>
              </div>
            ) : null}
          </section>
        </div>
      )}
    </section>
  );

  async function replaySelectedSpan(span: TraceSpanModel, state: ReplayState) {
    const request = requestStartPayload(span);
    if (!request) {
      return;
    }
    setReplayLoading(true);
    setReplayError(null);
    setReplayResponse(null);
    try {
      const result = await callAPI({
        service: stringField(request.service_name) || span.serviceName,
        endpoint: stringField(request.endpoint_name) || span.endpointName,
        path: state.path,
        method: state.method,
        payload: tryParseJSON(state.payloadText),
      });
      setReplayResponse(result);
    } catch (err) {
      setReplayError(err instanceof Error ? err.message : String(err));
    } finally {
      setReplayLoading(false);
    }
  }

  async function openInAPIExplorer(span: TraceSpanModel) {
    const request = requestStartPayload(span);
    if (!request) {
      return;
    }

    const serviceName = stringField(request.service_name) || span.serviceName;
    const endpointName = stringField(request.endpoint_name) || span.endpointName;
    if (!serviceName || !endpointName) {
      return;
    }

    const key = `${serviceName}.${endpointName}`;
    const endpoint =
      endpointMap.get(key) ||
      ({
        key,
        svcName: serviceName,
        rpcName: endpointName,
        method: stringField(request.http_method) || "GET",
        path: stringField(request.path) || "/",
      } satisfies EndpointOption);

    const next = makeTabFromEndpoint(endpoint, key);
    next.method = stringField(request.http_method) || endpoint.method;
    next.path = stringField(request.path) || endpoint.path;
    next.pathParamsText = JSON.stringify(pathParamsObject(endpoint.path, request.path_params), null, 2);
    next.payloadText = requestPayloadText(request.request_payload);

    const persisted = loadPersistedTabs(appId);
    const tabs: RequestTab[] = [...persisted.tabs, next];
    persistTabs(appId, next.id, tabs);
    await navigate({ to: "/$appId/requests", params: { appId } });
  }
}

export function TracesListPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const { appId, rpc, traces, refreshAll } = useDashboard();
  const searchParams = useMemo(() => new URLSearchParams(location.search), [location.search]);
  const [traceServiceFilter, setTraceServiceFilter] = useState(searchParams.get("service") ?? "");
  const [traceEndpointFilter, setTraceEndpointFilter] = useState(searchParams.get("endpoint") ?? "");
  const [traceStatusFilter, setTraceStatusFilter] = useState<"all" | "error">(
    searchParams.get("error") === "true" ? "error" : "all",
  );
  const [traceIDFilter, setTraceIDFilter] = useState(searchParams.get("trace_id") ?? "");

  useEffect(() => {
    setTraceServiceFilter(searchParams.get("service") ?? "");
    setTraceEndpointFilter(searchParams.get("endpoint") ?? "");
    setTraceStatusFilter(searchParams.get("error") === "true" ? "error" : "all");
    setTraceIDFilter(searchParams.get("trace_id") ?? "");
  }, [searchParams]);

  const traceServices = useMemo(
    () => Array.from(new Set(traces.map((trace) => trace.service_name).filter(Boolean))).sort(),
    [traces],
  );
  const traceEndpoints = useMemo(
    () =>
      Array.from(
        new Set(
          traces
            .filter((trace) => !traceServiceFilter || trace.service_name === traceServiceFilter)
            .map((trace) => trace.endpoint_name)
            .filter((endpoint): endpoint is string => typeof endpoint === "string" && endpoint.length > 0),
        ),
      ).sort(),
    [traceServiceFilter, traces],
  );
  const filteredTraces = useMemo(
    () =>
      traces.filter((trace) => {
        if (traceServiceFilter && trace.service_name !== traceServiceFilter) {
          return false;
        }
        if (traceEndpointFilter && trace.endpoint_name !== traceEndpointFilter) {
          return false;
        }
        if (traceStatusFilter === "error" && !trace.is_error) {
          return false;
        }
        if (traceIDFilter && !trace.trace_id.includes(traceIDFilter.trim())) {
          return false;
        }
        return true;
      }),
    [traceEndpointFilter, traceIDFilter, traceServiceFilter, traceStatusFilter, traces],
  );

  useEffect(() => {
    const next = new URLSearchParams();
    if (traceServiceFilter) {
      next.set("service", traceServiceFilter);
    }
    if (traceEndpointFilter) {
      next.set("endpoint", traceEndpointFilter);
    }
    if (traceStatusFilter === "error") {
      next.set("error", "true");
    }
    if (traceIDFilter) {
      next.set("trace_id", traceIDFilter);
    }
    const nextSearch = next.toString();
    const currentSearch =
      typeof window !== "undefined" ? window.location.search.replace(/^\?/, "") : "";
    if (nextSearch !== currentSearch) {
      void navigate({
        to: "/$appId/envs/local/traces",
        params: { appId },
        search: nextSearch ? `?${nextSearch}` : "",
        replace: true,
      });
    }
  }, [appId, location.search, navigate, traceEndpointFilter, traceIDFilter, traceServiceFilter, traceStatusFilter]);

  return (
    <div className="h-[calc(100vh-var(--header-height))] overflow-auto">
      <div className="px-8 py-6">
        <div className="flex items-center justify-between gap-4">
          <h1 className="text-lg font-medium">Traces</h1>
          <button
            type="button"
            className="rounded-md border border-border px-3 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground disabled:opacity-50"
            disabled={traces.length === 0}
            onClick={() => void rpc?.request("traces/clear", { app_id: appId }).then(() => refreshAll())}
          >
            Clear traces
          </button>
        </div>

        <div className="mt-6 rounded-md border border-border p-4 space-y-4 devdash-trace-filters">
          <div className="grid grid-cols-[240px_minmax(300px,1fr)_auto] gap-6 items-end">
            <div className="space-y-2">
              <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Service</div>
              <select
                className="h-9 w-full rounded-md border border-border px-3 text-sm"
                value={traceServiceFilter}
                onChange={(event) => {
                  setTraceServiceFilter(event.target.value);
                  setTraceEndpointFilter("");
                }}
              >
                <option value="">All services</option>
                {traceServices.map((service) => (
                  <option key={service} value={service}>
                    {service}
                  </option>
                ))}
              </select>
            </div>

            <div className="space-y-2">
              <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Endpoint</div>
              <select
                className="h-9 w-full rounded-md border border-border px-3 text-sm"
                value={traceEndpointFilter}
                onChange={(event) => setTraceEndpointFilter(event.target.value)}
              >
                <option value="">All endpoints</option>
                {traceEndpoints.map((endpoint) => (
                  <option key={endpoint} value={endpoint}>
                    {endpoint}
                  </option>
                ))}
              </select>
            </div>

            <div className="inline-flex gap-0.5 p-1 rounded-lg bg-sidebar-accent/50 self-start">
              <button
                type="button"
                onClick={() => setTraceStatusFilter("all")}
                className={cn(
                  "px-3 py-1.5 text-sm rounded-md transition-colors",
                  traceStatusFilter === "all"
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                All
              </button>
              <button
                type="button"
                onClick={() => setTraceStatusFilter("error")}
                className={cn(
                  "px-3 py-1.5 text-sm rounded-md transition-colors",
                  traceStatusFilter === "error"
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                Errors
              </button>
            </div>
          </div>

          <div className="space-y-2 max-w-[320px]">
            <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Trace ID</div>
            <input
              className="h-9 w-full rounded-md border border-border px-3 text-sm"
              placeholder="Trace ID"
              value={traceIDFilter}
              onChange={(event) => setTraceIDFilter(event.target.value)}
            />
          </div>
        </div>

        <div className="mt-6">
          {filteredTraces.length === 0 ? (
            <div className="rounded-md border border-border p-6 text-sm text-muted-foreground">
              No traces match the current filters.
            </div>
          ) : (
            <div className="overflow-auto rounded-md border border-border">
              <table className="min-w-full text-sm">
                <thead className="bg-muted/50">
                  <tr>
                    <th className="px-4 py-3 text-left">Request</th>
                    <th className="px-4 py-3 text-left">Status</th>
                    <th className="px-4 py-3 text-left">Recorded</th>
                    <th className="px-4 py-3 text-left">Duration</th>
                    <th className="px-4 py-3 text-left" />
                  </tr>
                </thead>
                <tbody>
                  {filteredTraces.map((trace) => {
                    const hasEndpointFilterShortcut =
                      Boolean(trace.service_name) &&
                      Boolean(trace.endpoint_name) &&
                      !(traceServiceFilter === trace.service_name && traceEndpointFilter === trace.endpoint_name);
                    return (
                      <tr
                        key={`${trace.trace_id}/${trace.span_id}`}
                        className="group cursor-pointer border-t border-border hover:bg-sidebar-accent/50"
                        onClick={(event) => {
                          const target = event.target as HTMLElement;
                          if (target.closest("a") || target.closest("button")) {
                            return;
                          }
                          void navigate({
                            to: "/$appId/envs/local/traces/$traceId",
                            params: { appId, traceId: trace.trace_id },
                          });
                        }}
                      >
                        <td className="px-4 py-3 w-1/2">
                          <Link
                            to="/$appId/envs/local/traces/$traceId"
                            params={{ appId, traceId: trace.trace_id }}
                            className="flex items-start h-full no-underline text-foreground"
                          >
                            <div className="flex flex-col min-w-0">
                              <span className="text-sm truncate group-hover:underline">
                                {trace.service_name || "unknown service"}.{trace.endpoint_name || trace.type}
                              </span>
                              <span className="text-xs text-muted-foreground font-mono truncate">
                                {trace.trace_id}
                              </span>
                            </div>
                          </Link>
                        </td>
                        <td className="px-4 py-3">
                          <span className={cn(
                            "inline-flex rounded-md px-2 py-1 text-xs",
                            trace.is_error ? "bg-red-500/10 text-red-500" : "bg-green-500/10 text-green-600",
                          )}>
                            {trace.is_error ? "Error" : "Success"}
                          </span>
                        </td>
                        <td className="px-4 py-3">
                          <span>{formatTime(trace.started_at)}</span>
                          <span className="ml-2 text-xs text-muted-foreground hidden sm:inline">
                            {formatTimestamp(trace.started_at)}
                          </span>
                        </td>
                        <td className="px-4 py-3">{formatDurationNanos(trace.duration_nanos)}</td>
                        <td className="px-4 py-3">
                          {hasEndpointFilterShortcut ? (
                            <button
                              type="button"
                              className="text-xs underline"
                              onClick={() => {
                                setTraceServiceFilter(trace.service_name || "");
                                setTraceEndpointFilter(trace.endpoint_name || "");
                              }}
                            >
                              Filter
                            </button>
                          ) : null}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function BackButton({ appId }: { appId: string }) {
  return (
    <Link
      to="/$appId/requests"
      params={{ appId }}
      className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
      title="Back"
    >
      ←
    </Link>
  );
}

function TraceTimeline({
  lanes,
  selectedSpanID,
}: {
  lanes: TraceSpanModel[][];
  selectedSpanID: string;
}) {
  const root = lanes[0]?.[0];
  const total = traceDurationFromSpan(root);
  const marks = buildTimelineMarks(total);

  return (
    <div className="w-full relative flex flex-col items-center">
      <div className="w-full relative mt-4 pb-2">
        <div className="relative ml-3 mr-2 sm:mr-6 pt-5">
          <div className="bg-border h-px absolute bottom-0 left-0 right-0" />
          <div className="text-[10px] sm:text-xs absolute left-0 bottom-0 flex flex-col items-center w-[40px] sm:w-[60px] -ml-[20px] sm:-ml-[30px]">
            <span className="whitespace-nowrap mb-0.5">0ms</span>
            <figure className="w-px bg-border h-2" />
          </div>
          {marks.map((mark) => (
            <div
              key={mark.percent}
              className="text-[10px] sm:text-xs absolute bottom-0 flex flex-col items-center w-[40px] sm:w-[60px] -ml-[20px] sm:-ml-[30px]"
              style={{ left: `${mark.percent}%` }}
            >
              <span className="whitespace-nowrap mb-0.5">{mark.label}</span>
              <figure className="w-px bg-border h-2" />
            </div>
          ))}
          <div className="text-[10px] sm:text-xs absolute right-0 bottom-0 flex flex-col items-center w-[40px] sm:w-[60px] -mr-[20px] sm:-mr-[30px]">
            <span className="whitespace-nowrap mb-0.5">{formatDurationNanos(total)}</span>
            <figure className="w-px bg-border h-2" />
          </div>
        </div>
      </div>

      <div className="w-full mt-4 space-y-2">
        {lanes.map((lane, index) => (
          <div key={index} className="relative h-4">
            {lane.map((span) => {
              const left = percentageOffset(root, span.startedAt);
              const width = percentageWidth(root, span);
              return (
                <div
                  key={span.id}
                  className={cn(
                    "absolute top-0 h-4 rounded-sm",
                    selectedSpanID === span.id
                      ? "ring-2 ring-background"
                      : "",
                    span.isError
                      ? "bg-red-500"
                      : span.kind === "db"
                        ? "bg-yellow-500"
                        : span.kind === "auth"
                          ? "bg-green-500"
                          : "bg-sky-500",
                  )}
                  style={{ left: `${left}%`, width: `${width}%`, minWidth: "2px" }}
                  title={span.title}
                />
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}

function TraceSpanTree({
  appId,
  traceId,
  spans,
  selectedSpanID,
  childMap,
}: {
  appId: string;
  traceId: string;
  spans: TraceSpanModel[];
  selectedSpanID: string;
  childMap: Map<string, TraceSpanModel[]>;
}) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  useEffect(() => {
    setCollapsed({});
  }, [traceId]);

  const roots = useMemo(
    () => spans.filter((span) => !span.parentID || !spans.some((item) => item.id === span.parentID)),
    [spans],
  );

  return (
    <div>
      {roots.map((span) => (
        <TraceSpanTreeItem
          key={span.id}
          appId={appId}
          traceId={traceId}
          span={span}
          depth={0}
          selectedSpanID={selectedSpanID}
          childMap={childMap}
          collapsed={collapsed}
          onToggle={(id) => setCollapsed((current) => ({ ...current, [id]: !current[id] }))}
        />
      ))}
    </div>
  );
}

function TraceSpanTreeItem({
  appId,
  traceId,
  span,
  depth,
  selectedSpanID,
  childMap,
  collapsed,
  onToggle,
}: {
  appId: string;
  traceId: string;
  span: TraceSpanModel;
  depth: number;
  selectedSpanID: string;
  childMap: Map<string, TraceSpanModel[]>;
  collapsed: Record<string, boolean>;
  onToggle: (id: string) => void;
}) {
  const children = childMap.get(span.id) ?? [];
  const isCollapsed = Boolean(collapsed[span.id]);

  return (
    <div>
      <div
        className={cn(
          "flex items-stretch p-2 sm:p-4 pr-0 select-none",
          selectedSpanID === span.id ? "bg-foreground/10 font-medium" : "",
        )}
      >
        <div className="flex-shrink-0" style={{ width: `${depth * 14 + 20}px` }} />
        {children.length > 0 ? (
          <button
            type="button"
            className="bg-background z-40 h-3 w-3 -ml-[15px] mr-[3px] mt-[2.5px] flex-shrink-0 text-xs"
            onClick={() => onToggle(span.id)}
          >
            {isCollapsed ? "+" : "−"}
          </button>
        ) : (
          <div className="w-3 mr-[3px]" />
        )}
        <Link
          to="/$appId/envs/local/traces/$traceId/$spanId"
          params={{ appId, traceId, spanId: span.rawID || span.id }}
          className="flex grow flex-col ml-1 min-w-0"
        >
          <div className={cn("text-xs truncate mb-1", span.isError ? "text-destructive" : "text-foreground")}>
            {span.title}
          </div>
          <div className="mr-2 sm:mr-6 ml-3 w-full self-end mt-1">
            <div className="relative" style={{ height: "8px" }}>
              <div className="absolute inset-x-0 bg-border" style={{ height: "1px", top: "3px" }} />
              <div
                className={cn(
                  "absolute inset-y-0 rounded-sm",
                  span.isError
                    ? "bg-red-500"
                    : span.kind === "db"
                      ? "bg-yellow-500"
                      : span.kind === "auth"
                        ? "bg-green-500"
                        : "bg-sky-500",
                )}
                style={{ left: "0%", width: `${Math.max(4, Math.min(100, Math.round((traceDurationFromSpan(span) / 10_000_000) * 100))) / 2}%` }}
              />
            </div>
          </div>
          <div className="mt-1 text-[11px] text-muted-foreground">
            {formatDurationNanos(span.durationNanos)} • {formatTime(span.startedAt)}
          </div>
        </Link>
      </div>
      {!isCollapsed
        ? children.map((child) => (
            <TraceSpanTreeItem
              key={child.id}
              appId={appId}
              traceId={traceId}
              span={child}
              depth={depth + 1}
              selectedSpanID={selectedSpanID}
              childMap={childMap}
              collapsed={collapsed}
              onToggle={onToggle}
            />
          ))
        : null}
    </div>
  );
}

function SpanDetail({
  appId,
  span,
  counts,
  onOpenExplorer,
  onReplay,
  replayOpen,
}: {
  appId: string;
  span: TraceSpanModel;
  counts: ReturnType<typeof countSpanActivity> | null;
  onOpenExplorer: () => void;
  onReplay: () => void;
  replayOpen: boolean;
}) {
  const logs = span.events.filter((event) => event.kind === "log_message");
  const request = requestStartPayload(span);
  const parentTraceID = parentTraceLinkID(span);
  const requestMethod = stringField(request?.http_method);
  const requestPath = stringField(request?.path);

  return (
    <div className="flex flex-col flex-1 min-h-0 min-w-0 overflow-hidden">
      <div className="p-4 pb-0 min-w-0">
        <div className="flex flex-col sm:flex-row justify-between items-start gap-2">
          <div className="flex items-center flex-wrap">
            <h2 className="text-lg sm:text-xl font-medium break-words">{span.title}</h2>
            {request ? (
              <button
                type="button"
                onClick={onReplay}
                className="ml-3 rounded-md border border-border px-3 py-1.5 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
              >
                {replayOpen ? "Hide Replay" : "Replay"}
              </button>
            ) : null}
            <button
              type="button"
              onClick={onOpenExplorer}
              className="ml-2 rounded-md border border-border px-3 py-1.5 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
            >
              Open in API Explorer
            </button>
            <Link
              to="/$appId/envs/local/api/$serviceSlug/$rpcSlug"
              params={{ appId, serviceSlug: span.serviceName || "_", rpcSlug: span.endpointName || "_" }}
              className="ml-2 text-xs underline text-muted-foreground"
            >
              Service Catalog
            </Link>
          </div>
        </div>
        <SummaryRow span={span} counts={counts} />
      </div>

      <div className="px-4 min-w-0 min-h-0 flex-1 overflow-auto">
        <div className="mt-6 min-w-0">
          <SpanEventTimeline span={span} />
        </div>
        <div className="mt-6 grid grid-cols-2 gap-4">
          <InfoCard label="Service" value={span.serviceName || "n/a"} />
          <InfoCard label="Endpoint" value={span.endpointName || "n/a"} />
          <InfoCard label="Kind" value={span.kind} />
          <InfoCard label="Duration" value={formatDurationNanos(span.durationNanos)} />
          <InfoCard label="Started" value={formatTimestamp(span.startedAt)} />
          <InfoCard label="Ended" value={formatTimestamp(span.endedAt)} />
          <InfoCard label="Status code" value={span.statusCode || "n/a"} />
          <InfoCard label="HTTP status" value={span.httpStatusCode ? String(span.httpStatusCode) : "n/a"} />
          <InfoCard label="Method" value={requestMethod || "n/a"} />
          <InfoCard label="Path" value={requestPath || "n/a"} />
          <InfoCard label="User ID" value={span.userID || "n/a"} />
          <InfoCard label="Span ID" value={span.rawID || span.id} mono />
        </div>
        {parentTraceID ? (
          <div className="mt-4">
            <Link
              to="/$appId/envs/local/traces/$traceId"
              params={{ appId, traceId: parentTraceID }}
              className="text-sm underline"
            >
              Open parent trace
            </Link>
          </div>
        ) : null}
        <div className="mt-6 space-y-4">
          {span.start ? <JSONView title={`${span.start.kind} start`} value={span.start.payload} /> : null}
          {span.end ? <JSONView title={`${span.end.kind} end`} value={span.end.payload} /> : null}
        </div>
        {logs.length > 0 ? (
          <section className="mt-6">
            <h3 className="text-sm font-medium">Logs</h3>
            <div className="mt-4 space-y-3">
              {logs.map((event) => (
                <EventCard key={`${event.id}-${event.kind}`} event={event} />
              ))}
            </div>
          </section>
        ) : null}
        {span.events.filter((event) => event.kind !== "log_message").length > 0 ? (
          <section className="mt-6">
            <h3 className="text-sm font-medium">Events</h3>
            <div className="mt-4 space-y-3">
              {span.events
                .filter((event) => event.kind !== "log_message")
                .map((event) => (
                  <EventCard key={`${event.id}-${event.kind}`} event={event} />
                ))}
            </div>
          </section>
        ) : null}
        <div className="shrink-0 h-20" />
      </div>
    </div>
  );
}

function SummaryRow({
  span,
  counts,
}: {
  span: TraceSpanModel;
  counts: ReturnType<typeof countSpanActivity> | null;
}) {
  return (
    <div className="text-xs flex flex-wrap mt-0.5 gap-y-1">
      <Metric label="Duration" value={formatDurationNanos(span.durationNanos)} />
      <Metric label="API Calls" value={String(counts?.requests ?? 0)} />
      <Metric label="DB Queries" value={String(counts?.db ?? 0)} />
      <Metric label="HTTP Calls" value={String(counts?.httpCalls ?? 0)} />
      <Metric label="Log Lines" value={String(counts?.logs ?? 0)} />
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="items-center inline-flex pr-4 sm:pr-6 pt-2">
      <span className="font-semibold mr-1">{value}</span>
      {label}
    </div>
  );
}

function SpanEventTimeline({ span }: { span: TraceSpanModel }) {
  const total = traceDurationFromSpan(span);
  const base = parseTime(span.startedAt) ?? 0;
  const items = span.events.filter((event) => event.kind !== "log_message");
  if (!items.length || total <= 0) {
    return null;
  }
  return (
    <div className="rounded-md border border-border p-4">
      <div className="text-sm font-medium">Event timeline</div>
      <div className="mt-4 relative h-8">
        <div className="absolute inset-x-0 top-1/2 h-px -translate-y-1/2 bg-border" />
        {items.map((event) => {
          const offset = ((parseTime(event.at) ?? base) - base) / total;
          return (
            <div
              key={event.id}
              className="absolute top-1/2 h-3 w-3 -translate-x-1/2 -translate-y-1/2 rounded-full bg-sky-500"
              style={{ left: `${Math.max(0, Math.min(100, offset * 100))}%` }}
              title={`${event.title} · ${formatTime(event.at)}`}
            />
          );
        })}
      </div>
    </div>
  );
}

function ReplayPanel({
  appId,
  state,
  onChange,
  onClose,
  loading,
  response,
  error,
  onSubmit,
}: {
  appId: string;
  state: ReplayState;
  onChange: (next: ReplayState) => void;
  onClose: () => void;
  loading: boolean;
  response: ApiCallResponse | null;
  error: string | null;
  onSubmit: () => Promise<void>;
}) {
  return (
    <div>
      <div className="flex items-center mb-4 justify-between">
        <h2 className="text-xl font-medium">Replay request</h2>
        <button
          type="button"
          className="rounded-md border border-border px-2 py-1 text-sm"
          onClick={onClose}
        >
          Close
        </button>
      </div>
      <div className="space-y-4">
        <Field
          label="Method"
          value={state.method}
          onChange={(value) => onChange({ ...state, method: value.toUpperCase() })}
        />
        <Field
          label="Path"
          value={state.path}
          onChange={(value) => onChange({ ...state, path: value })}
        />
        <TextAreaField
          label="Payload JSON"
          value={state.payloadText}
          onChange={(value) => onChange({ ...state, payloadText: value })}
        />
        <button
          type="button"
          className="rounded-md border border-border px-3 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
          onClick={() => void onSubmit()}
          disabled={loading}
        >
          {loading ? "Calling..." : "Send request"}
        </button>
        {error ? (
          <div className="rounded-md border border-red-500/30 bg-red-500/10 px-4 py-3 text-sm text-red-500">
            {error}
          </div>
        ) : null}
        {response ? (
          <div className="space-y-4">
            <div className="grid grid-cols-3 gap-4">
              <InfoCard label="Status" value={response.status} />
              <InfoCard label="Code" value={String(response.status_code)} />
              <InfoCard label="Trace" value={response.trace_id || "n/a"} mono />
            </div>
            {response.trace_id ? (
              <Link
                to="/$appId/envs/local/traces/$traceId"
                params={{ appId, traceId: response.trace_id }}
                className="inline-flex text-sm underline"
              >
                Open trace
              </Link>
            ) : null}
            <JSONView title="Response body" value={tryParseJSON(response.body)} />
          </div>
        ) : null}
      </div>
    </div>
  );
}

function EventCard({ event }: { event: TraceSpanEventItem }) {
  return (
    <div className="rounded-md border border-border px-4 py-3">
      <div className="flex items-center justify-between gap-4">
        <strong className="text-sm">{event.title}</strong>
        <span className="text-xs text-muted-foreground">{formatTime(event.at)}</span>
      </div>
      <pre className="mt-3 overflow-auto whitespace-pre-wrap text-xs leading-6">
        {JSON.stringify(event.payload, null, 2)}
      </pre>
    </div>
  );
}

function InfoCard({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="rounded-md border border-border p-4">
      <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className={cn("mt-2 text-sm", mono && "font-mono break-all")}>{value || "n/a"}</div>
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="space-y-2">
      <label className="text-sm font-medium">{label}</label>
      <input
        className="h-10 w-full rounded-md border border-border px-3 text-sm"
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </div>
  );
}

function TextAreaField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="space-y-2">
      <label className="text-sm font-medium">{label}</label>
      <textarea
        className="w-full rounded-md border border-border px-3 py-2 text-sm"
        style={{ minHeight: 180 }}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </div>
  );
}

function PanelDividerLine() {
  return <div className="hidden md:block w-px bg-border h-[calc(100%-40px)] absolute left-0 top-[20px]" />;
}

function requestStartPayload(span: TraceSpanModel): Record<string, unknown> | null {
  return span.start?.kind === "request" ? span.start.payload : null;
}

function parentTraceLinkID(span: TraceSpanModel): string {
  const request = requestStartPayload(span);
  if (!request) {
    return "";
  }
  const raw = request.parent_trace_id;
  const normalized = normalizeTraceID(raw);
  return normalized && normalized !== span.traceID ? normalized : "";
}

function countSpanActivity(
  span: TraceSpanModel,
  childMap: Map<string, TraceSpanModel[]>,
): {
  requests: number;
  db: number;
  httpCalls: number;
  logs: number;
} {
  let requests = 0;
  let db = 0;
  let httpCalls = 0;
  let logs = 0;

  const walk = (node: TraceSpanModel) => {
    if (node !== span) {
      if (node.kind === "request") {
        requests += 1;
      }
      if (node.kind === "db") {
        db += 1;
      }
    }
    for (const event of node.events) {
      if (event.kind === "http_call_start") {
        httpCalls += 1;
      }
      if (event.kind === "log_message") {
        logs += 1;
      }
    }
    for (const child of childMap.get(node.id) ?? []) {
      walk(child);
    }
  };

  walk(span);
  return { requests, db, httpCalls, logs };
}

function buildTraceLanes(spans: TraceSpanModel[]): TraceSpanModel[][] {
  const sorted = [...spans].sort((a, b) => compareDateString(a.startedAt, b.startedAt));
  const lanes: TraceSpanModel[][] = [];

  for (const span of sorted) {
    const start = parseTime(span.startedAt) ?? 0;
    const end = endTime(span);
    let placed = false;
    for (const lane of lanes) {
      const last = lane[lane.length - 1];
      if (!last) {
        lane.push(span);
        placed = true;
        break;
      }
      if ((endTime(last) ?? 0) <= start) {
        lane.push(span);
        placed = true;
        break;
      }
    }
    if (!placed) {
      lanes.push([span]);
    }
  }

  return lanes;
}

function buildTimelineMarks(totalNanos: number): Array<{ percent: number; label: string }> {
  if (totalNanos <= 0) {
    return [];
  }
  const totalMs = totalNanos / 1_000_000;
  const step = totalMs / 5;
  const marks: Array<{ percent: number; label: string }> = [];
  for (let index = 1; index < 5; index += 1) {
    const value = step * index;
    marks.push({
      percent: (value / totalMs) * 100,
      label: value >= 1000 ? `${(value / 1000).toFixed(value >= 10_000 ? 0 : 1)}s` : `${Math.round(value)}ms`,
    });
  }
  return marks;
}

function totalTraceDuration(model: ReturnType<typeof buildTraceModel>, summary?: TraceSummary): number {
  return summary?.duration_nanos || traceDurationFromSpan(model.rootSpan) || 0;
}

function traceDurationFromSpan(span: TraceSpanModel | undefined): number {
  if (!span) {
    return 0;
  }
  if (span.durationNanos) {
    return span.durationNanos;
  }
  const start = parseTime(span.startedAt);
  const end = endTime(span);
  return start !== null && end !== null ? Math.max(0, (end - start) * 1_000_000) : 0;
}

function percentageOffset(root: TraceSpanModel | undefined, startedAt?: string): number {
  const rootStart = parseTime(root?.startedAt);
  const current = parseTime(startedAt);
  const totalMs = (traceDurationFromSpan(root) || 1) / 1_000_000;
  if (rootStart === null || current === null) {
    return 0;
  }
  return Math.max(0, Math.min(100, ((current - rootStart) / totalMs) * 100));
}

function percentageWidth(root: TraceSpanModel | undefined, span: TraceSpanModel): number {
  const total = traceDurationFromSpan(root) || traceDurationFromSpan(span) || 1;
  const current = traceDurationFromSpan(span);
  return Math.max(2, Math.min(100, (current / total) * 100));
}

function endTime(span: TraceSpanModel | undefined): number | null {
  const explicit = parseTime(span?.endedAt);
  if (explicit !== null) {
    return explicit;
  }
  const start = parseTime(span?.startedAt);
  if (start === null || !span?.durationNanos) {
    return null;
  }
  return start + span.durationNanos / 1_000_000;
}

function parseTime(value?: string): number | null {
  if (!value) {
    return null;
  }
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function compareDateString(a?: string, b?: string): number {
  return (parseTime(a) ?? 0) - (parseTime(b) ?? 0);
}

function pathParamsObject(pathTemplate: string, rawPathParams: unknown): Record<string, string> {
  const values = Array.isArray(rawPathParams) ? rawPathParams.map((item) => String(item ?? "")) : [];
  const keys = pathTemplate
    .split("/")
    .filter((segment) => segment.startsWith(":"))
    .map((segment) => segment.slice(1));
  const out: Record<string, string> = {};
  for (const [index, key] of keys.entries()) {
    out[key] = values[index] || "";
  }
  return out;
}

function requestPayloadText(value: unknown): string {
  if (typeof value !== "string" || !value) {
    return "{}";
  }
  const decoded = decodeBase64Utf8(value);
  const parsed = tryParseJSON(decoded);
  return typeof parsed === "string" ? decoded : JSON.stringify(parsed, null, 2);
}

function stringField(value: unknown): string {
  return typeof value === "string" ? value : "";
}
