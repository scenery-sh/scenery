import { Link } from "@tanstack/react-router";
import { useDashboard } from "../lib/dashboard-context";
import type { ObservabilityBackendState } from "../lib/types";
import { cn, processOutputText } from "../lib/utils";

export function ObservabilityPage() {
  const { appId, outputs, status, traces } = useDashboard();
  const observability = status?.observability;
  const backends = [
    { label: "Metrics", state: observability?.metrics },
    { label: "Logs", state: observability?.logs },
    { label: "Traces", state: observability?.traces },
  ];
  const statusText = overallStatus(backends);

  return (
    <div data-scenery-ui="ObservabilityRoute" className="max-h-[calc(100vh-var(--header-height))] overflow-auto">
      <div className="min-h-0 grow px-8 pb-12 pt-6 leading-6">
        <div className="max-w-6xl space-y-8">
          <div>
            <h1 className="text-lg font-medium">Observability</h1>
            <p className="mt-2 text-sm text-muted-foreground">
              Native Scenery metrics, logs, and traces from the local Victoria backends.
            </p>
          </div>

          <section
            data-scenery-ui="ObservabilityBackends"
            data-scenery-state={statusText}
            className="grid gap-4 lg:grid-cols-3"
          >
            {backends.map((backend) => (
              <BackendCard key={backend.label} label={backend.label} state={backend.state} />
            ))}
          </section>

          <section className="rounded-md border border-border p-6">
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div>
                <h2 className="text-base font-medium">Session Scope</h2>
                <p className="mt-1 text-sm text-muted-foreground">
                  {observability?.message || "Queries are scoped to this local development session."}
                </p>
              </div>
              <StatusPill status={statusText} />
            </div>
            <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
              <InfoCell label="Backend" value={observability?.backend || "victoria"} />
              <InfoCell label="App" value={observability?.scope?.app_id || status?.appID || "local"} />
              <InfoCell label="Session" value={observability?.scope?.session_id || status?.sessionID || "n/a"} />
              <InfoCell label="Branch" value={observability?.scope?.branch || "n/a"} />
            </div>
          </section>

          <section className="grid gap-6 lg:grid-cols-2">
            <div className="rounded-md border border-border p-6">
              <div className="flex flex-wrap items-start justify-between gap-4">
                <h2 className="text-base font-medium">Recent Traces</h2>
                <LinkButton to="/$appId/envs/local/traces" params={{ appId }} label="Open traces" />
              </div>
              <div className="mt-4 divide-y divide-border">
                {traces.length === 0 ? (
                  <p className="py-3 text-sm text-muted-foreground">No local traces recorded yet.</p>
                ) : (
                  traces.slice(0, 8).map((trace) => (
                    <Link
                      key={`${trace.trace_id}-${trace.span_id}`}
                      to="/$appId/envs/local/traces/$traceId"
                      params={{ appId, traceId: trace.trace_id }}
                      className="block py-3 text-sm transition-colors hover:text-foreground"
                    >
                      <div className="flex items-center justify-between gap-4">
                        <span className="truncate font-medium">
                          {trace.service_name || "unknown"}.{trace.endpoint_name || trace.type}
                        </span>
                        <span className={trace.is_error ? "text-red-500" : "text-muted-foreground"}>
                          {trace.is_error ? "error" : "ok"}
                        </span>
                      </div>
                      <code className="mt-1 block truncate text-xs text-muted-foreground">{trace.trace_id}</code>
                    </Link>
                  ))
                )}
              </div>
            </div>

            <div className="rounded-md border border-border p-6">
              <h2 className="text-base font-medium">Recent Output</h2>
              <div className="mt-4 max-h-72 space-y-2 overflow-auto">
                {outputs.length === 0 ? (
                  <p className="text-sm text-muted-foreground">No process output recorded yet.</p>
                ) : (
                  outputs.slice(-12).map((item) => (
                    <div key={`${item.created_at}-${item.pid}-${item.stream}`} className="text-xs">
                      <span className="text-muted-foreground">{item.stream}</span>{" "}
                      <span className="font-mono">{processOutputText(item)}</span>
                    </div>
                  ))
                )}
              </div>
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}

function BackendCard({ label, state }: { label: string; state?: ObservabilityBackendState }) {
  const status = state?.status || "unavailable";
  return (
    <section data-scenery-ui="ObservabilityBackendCard" className="rounded-md border border-border p-5">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-base font-medium">{label}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{state?.message || state?.dialect || "Victoria backend"}</p>
        </div>
        <StatusPill status={status} />
      </div>
      <div className="mt-4 space-y-3">
        <InfoCell label="URL" value={state?.url || "not available"} />
        <InfoCell label="Query" value={state?.query_path || "n/a"} />
      </div>
    </section>
  );
}

function LinkButton({
  to,
  params,
  label,
}: {
  to: "/$appId/envs/local/traces" | "/$appId/requests" | "/$appId/cron";
  params: { appId: string };
  label: string;
}) {
  return (
    <Link
      to={to}
      params={params}
      className="rounded-md border border-border px-3 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
    >
      {label}
    </Link>
  );
}

function InfoCell({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 rounded-md border border-border px-4 py-3">
      <div className="text-xs uppercase text-muted-foreground">{label}</div>
      <div className="mt-2 truncate text-sm" title={value}>
        {value}
      </div>
    </div>
  );
}

function StatusPill({ status }: { status: string }) {
  const normalized = status.toLowerCase();
  return (
    <span
      className={cn(
        "rounded-full border px-2.5 py-1 text-xs font-medium capitalize",
        normalized === "ready"
          ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-300"
          : normalized === "starting" || normalized === "degraded"
            ? "border-amber-500/30 bg-amber-500/10 text-amber-300"
            : normalized === "disabled"
              ? "border-border bg-muted text-muted-foreground"
              : "border-red-500/30 bg-red-500/10 text-red-300",
      )}
    >
      {status}
    </span>
  );
}

function overallStatus(backends: Array<{ state?: ObservabilityBackendState }>): string {
  if (backends.every((backend) => backend.state?.status === "ready")) {
    return "ready";
  }
  if (backends.some((backend) => backend.state?.status === "ready")) {
    return "degraded";
  }
  return "unavailable";
}
