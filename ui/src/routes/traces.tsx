import { Link } from "@tanstack/react-router";
import { useMemo } from "react";
import { cn, formatDurationNanos, formatTimestamp } from "../lib/utils";
import { useDashboard } from "../lib/dashboard-context";
import type { TraceSummary } from "../lib/types";

export function TracesPage({ traceId, spanId }: { traceId?: string; spanId?: string }) {
  return <TraceWorkbench traceId={traceId} spanId={spanId} />;
}

export function TracesListPage() {
  return <TraceWorkbench />;
}

function TraceWorkbench({ traceId, spanId }: { traceId?: string; spanId?: string }) {
  const { appId, status, traces } = useDashboard();
  const trace = useMemo(
    () => traces.find((item) => item.trace_id === traceId) ?? null,
    [traceId, traces],
  );
  const tracesBackend = status?.observability?.traces;

  return (
    <div
      data-scenery-ui="TracesRoute"
      data-scenery-trace-count={traces.length}
      className="h-[calc(100vh-var(--header-height))] overflow-auto"
    >
      <div className="px-8 py-6">
        <div className="max-w-5xl space-y-6">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div>
              <h1 className="text-lg font-medium">Traces</h1>
              <p className="mt-2 max-w-2xl text-sm text-muted-foreground">
                Native Scenery trace summaries exported by this dev session.
              </p>
            </div>
            <Link
              to="/$appId/observability"
              params={{ appId }}
              className="rounded-md border border-border px-3 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
            >
              Observability
            </Link>
          </div>

          <section className="rounded-md border border-border p-6">
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div>
                <h2 className="text-base font-medium">Trace Backend</h2>
                <p className="mt-1 text-sm text-muted-foreground">
                  {tracesBackend?.message || tracesBackend?.dialect || "VictoriaTraces"}
                </p>
              </div>
              <span className="rounded-full border border-border px-2.5 py-1 text-xs font-medium capitalize">
                {tracesBackend?.status || "unavailable"}
              </span>
            </div>
            {tracesBackend?.url ? (
              <code className="mt-4 block truncate text-xs text-muted-foreground">{tracesBackend.url}</code>
            ) : null}
          </section>

          {!traceId ? (
            <section className="rounded-md border border-border p-6">
              <h2 className="text-base font-medium">Recent traces</h2>
              {traces.length === 0 ? (
                <p
                  data-scenery-ui="TraceEmptyState"
                  data-scenery-state="intentional-empty"
                  className="mt-4 text-sm text-muted-foreground"
                >
                  No local traces recorded yet.
                </p>
              ) : (
                <div data-scenery-ui="TraceTable" className="mt-4 divide-y divide-border">
                  {traces.slice(0, 25).map((item) => (
                    <Link
                      key={`${item.trace_id}-${item.span_id}`}
                      data-scenery-ui="TraceTableRow"
                      to="/$appId/envs/local/traces/$traceId"
                      params={{ appId, traceId: item.trace_id }}
                      className="grid grid-cols-[minmax(180px,1fr)_120px_120px] gap-4 py-3 text-sm transition-colors hover:text-foreground"
                    >
                      <span className="min-w-0">
                        <span className="block truncate font-medium">
                          {item.service_name || "unknown"}.{item.endpoint_name || item.type}
                        </span>
                        <span className="mt-1 block truncate font-mono text-xs text-muted-foreground">
                          {item.trace_id}
                        </span>
                      </span>
                      <span className={item.is_error ? "text-red-500" : "text-muted-foreground"}>
                        {item.is_error ? "error" : "ok"}
                      </span>
                      <span className="text-muted-foreground">{formatDurationNanos(item.duration_nanos)}</span>
                    </Link>
                  ))}
                </div>
              )}
            </section>
          ) : null}

          {traceId ? (
            <section data-scenery-ui="TraceDetail" className="rounded-md border border-border p-6">
              <div className="flex flex-wrap items-start justify-between gap-4">
                <div>
                  <h2 className="text-base font-medium">Selected Trace</h2>
                  <p className="mt-1 font-mono text-xs text-muted-foreground">{traceId}</p>
                </div>
                <Link
                  to="/$appId/envs/local/traces"
                  params={{ appId }}
                  className="rounded-md border border-border px-3 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                >
                  All traces
                </Link>
              </div>
              <TraceSummaryGrid trace={trace} traceId={traceId} spanId={spanId} />
            </section>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function TraceSummaryGrid({
  trace,
  traceId,
  spanId,
}: {
  trace: TraceSummary | null;
  traceId: string;
  spanId?: string;
}) {
  const fields = [
    { label: "Trace ID", value: traceId, mono: true },
    { label: "Span ID", value: spanId || trace?.span_id || "n/a", mono: true },
    { label: "Kind", value: trace?.type || "n/a" },
    {
      label: "Name",
      value: trace ? `${trace.service_name || "unknown"}.${trace.endpoint_name || trace.type}` : "n/a",
    },
    { label: "Status", value: trace ? (trace.is_error ? "error" : "ok") : "n/a" },
    { label: "Recorded", value: trace ? formatTimestamp(trace.started_at) : "n/a" },
    { label: "Duration", value: trace ? formatDurationNanos(trace.duration_nanos) : "n/a" },
  ];
  return (
    <div className="mt-6 grid gap-3 md:grid-cols-2">
      {fields.map((field) => (
        <div key={field.label} className="min-w-0 rounded-md border border-border px-4 py-3">
          <div className="text-xs uppercase tracking-wide text-muted-foreground">{field.label}</div>
          <div
            className={cn("mt-2 truncate text-sm", field.mono && "font-mono text-xs")}
            title={field.value}
          >
            {field.value}
          </div>
        </div>
      ))}
    </div>
  );
}
