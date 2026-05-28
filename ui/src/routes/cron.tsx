import { useMemo } from "react";
import { useDashboard } from "../lib/dashboard-context";
import { requestTracesURL } from "../lib/grafana";
import { formatDurationNanos, formatTime } from "../lib/utils";

export function CronPage() {
  const { meta, status, traces } = useDashboard();
  const jobs = meta?.cron_jobs ?? [];
  const traceURL = requestTracesURL(status?.grafana);

  const items = useMemo(
    () =>
      jobs.map((job) => {
        const matchingTraces = traces
          .filter(
            (trace) =>
              trace.service_name === job.endpoint?.service_name &&
              trace.endpoint_name === job.endpoint?.rpc_name,
          )
          .slice(0, 5);

        return {
          job,
          recent: matchingTraces,
          last: matchingTraces[0] ?? null,
        };
      }),
    [jobs, traces],
  );

  return (
    <div className="max-h-[calc(100vh-var(--header-height))] overflow-auto">
      <div className="min-h-0 grow px-8 pt-6 pb-12 leading-6">
        <div className="max-w-6xl space-y-8">
          <div>
            <h1 className="text-lg font-medium">Cron</h1>
            <p className="mt-2 text-sm text-muted-foreground">
              Scheduled onlava jobs discovered from the current app graph, with recent local executions matched from traces.
            </p>
          </div>

          <div className="grid grid-cols-3 gap-4">
            <StatCard label="Jobs" value={String(jobs.length)} />
            <StatCard
              label="Jobs with recent runs"
              value={String(items.filter((item) => item.recent.length > 0).length)}
            />
            <StatCard
              label="Recent executions"
              value={String(items.reduce((count, item) => count + item.recent.length, 0))}
            />
          </div>

          {items.length === 0 ? (
            <div className="rounded-md border border-border p-6 text-sm text-muted-foreground">
              No cron jobs discovered in this app.
            </div>
          ) : (
            <div className="space-y-6">
              {items.map(({ job, last, recent }) => (
                <section key={job.id} className="rounded-md border border-border p-6">
                  <div className="flex items-start justify-between gap-4">
                    <div>
                      <h2 className="text-base font-medium">{job.title || job.id}</h2>
                      <p className="mt-1 text-sm text-muted-foreground">{job.id}</p>
                    </div>
                    <code className="text-xs">
                      {job.endpoint?.service_name}.{job.endpoint?.rpc_name}
                    </code>
                  </div>

                  <div className="mt-4 grid grid-cols-3 gap-4">
                    <StatCard label="Schedule" value={job.schedule || job.every || "unspecified"} />
                    <StatCard
                      label="Endpoint"
                      value={`${job.endpoint?.service_name || "?"}.${job.endpoint?.rpc_name || "?"}`}
                    />
                    <StatCard
                      label="Last local run"
                      value={last ? `${formatTime(last.started_at)} · ${last.is_error ? "error" : "ok"}` : "none"}
                    />
                  </div>

                  <div className="mt-6">
                    <div className="text-sm font-medium">Recent local executions</div>
                    <div className="mt-3 space-y-3">
                      {recent.length === 0 ? (
                        <p className="text-sm text-muted-foreground">
                          No matching local traces recorded yet for this job.
                        </p>
                      ) : (
                        recent.map((trace) => (
                          <a
                            key={trace.trace_id}
                            href={traceURL || "#"}
                            target="_blank"
                            rel="noreferrer"
                            onClick={(event) => {
                              if (!traceURL) {
                                event.preventDefault();
                              }
                            }}
                            className="block rounded-md border border-border px-4 py-3 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                          >
                            <div className="flex items-center justify-between gap-3">
                              <strong>{trace.endpoint_name || trace.type}</strong>
                              <span className={trace.is_error ? "text-red-500" : "text-muted-foreground"}>
                                {trace.is_error ? "error" : "ok"}
                              </span>
                            </div>
                            <div className="mt-2 flex items-center justify-between gap-3 text-xs text-muted-foreground">
                              <span>{formatDurationNanos(trace.duration_nanos)}</span>
                              <span>{formatTime(trace.started_at)}</span>
                            </div>
                          </a>
                        ))
                      )}
                    </div>
                  </div>
                </section>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border border-border p-4">
      <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className="mt-2 text-sm">{value}</div>
    </div>
  );
}
