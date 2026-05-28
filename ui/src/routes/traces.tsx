import { Link } from "@tanstack/react-router";
import { useMemo } from "react";
import { isTemporalTrace, requestTracesURL, temporalTracesURL, traceDashboardURL } from "../lib/grafana";
import { cn, formatDurationNanos, formatTimestamp } from "../lib/utils";
import { useDashboard } from "../lib/dashboard-context";
import type { TraceSummary } from "../lib/types";

export function TracesPage({ traceId, spanId }: { traceId?: string; spanId?: string }) {
  return <TraceGrafanaHandoff traceId={traceId} spanId={spanId} />;
}

export function TracesListPage() {
  return <TraceGrafanaHandoff />;
}

function TraceGrafanaHandoff({ traceId, spanId }: { traceId?: string; spanId?: string }) {
  const { appId, status, traces } = useDashboard();
  const grafana = status?.grafana;
  const requestURL = requestTracesURL(grafana);
  const temporalURL = temporalTracesURL(grafana);
  const trace = useMemo(
    () => traces.find((item) => item.trace_id === traceId) ?? null,
    [traceId, traces],
  );
  const primaryURL = traceDashboardURL(grafana, trace);

  return (
    <div className="h-[calc(100vh-var(--header-height))] overflow-auto">
      <div className="px-8 py-6">
        <div className="max-w-5xl space-y-6">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div>
              <h1 className="text-lg font-medium">Traces</h1>
              <p className="mt-2 max-w-2xl text-sm text-muted-foreground">
                The dashboard trace viewer is deprecated. Grafana is the trace workbench for this dev session.
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
            <div className="flex flex-wrap items-center gap-2">
              <ExternalLink href={requestURL} label="Request traces" primary={!trace || !isTemporalTrace(trace)} />
              <ExternalLink href={temporalURL} label="Temporal traces" primary={isTemporalTrace(trace)} />
            </div>
            {!grafana?.available ? (
              <p className="mt-4 text-sm text-muted-foreground">
                Grafana is {grafana?.status || "unavailable"}{grafana?.message ? `: ${grafana.message}` : "."}
              </p>
            ) : null}
          </section>

          {traceId ? (
            <section className="rounded-md border border-border p-6">
              <div className="flex flex-wrap items-start justify-between gap-4">
                <div>
                  <h2 className="text-base font-medium">Selected Trace</h2>
                  <p className="mt-1 font-mono text-xs text-muted-foreground">{traceId}</p>
                </div>
                <ExternalLink href={primaryURL} label="Open in Grafana" primary />
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

function ExternalLink({
  href,
  label,
  primary = false,
}: {
  href: string;
  label: string;
  primary?: boolean;
}) {
  const disabled = !href;
  return (
    <a
      href={href || "#"}
      target="_blank"
      rel="noreferrer"
      onClick={(event) => {
        if (disabled) {
          event.preventDefault();
        }
      }}
      className={cn(
        "rounded-md border px-3 py-2 text-sm transition-colors",
        primary
          ? "border-primary bg-primary text-primary-foreground hover:bg-primary/90"
          : "border-border hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
        disabled && "pointer-events-none opacity-50",
      )}
    >
      {label}
    </a>
  );
}
