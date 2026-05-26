import { useDashboard } from "../lib/dashboard-context";
import type { GrafanaDashboard } from "../lib/types";
import { cn } from "../lib/utils";

export function ObservabilityPage() {
  const { status } = useDashboard();
  const grafana = status?.grafana;
  const grafanaAvailable = grafana?.available === true;
  const datasourceEntries = Object.entries(grafana?.datasources ?? {});
  const dashboards = grafanaAvailable ? (grafana?.dashboards ?? []) : [];

  return (
    <div className="max-h-[calc(100vh-var(--header-height))] overflow-auto">
      <div className="min-h-0 grow px-8 pb-12 pt-6 leading-6">
        <div className="max-w-6xl space-y-8">
          <div>
            <h1 className="text-lg font-medium">Observability</h1>
            <p className="mt-2 text-sm text-muted-foreground">
              Local Victoria sidecars and Grafana workbench status for this dev session.
            </p>
          </div>

          <section className="rounded-md border border-border p-6">
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div>
                <h2 className="text-base font-medium">Grafana</h2>
                <p className="mt-1 text-sm text-muted-foreground">
                  {grafana?.message || grafanaStatusCopy(grafana?.status)}
                </p>
              </div>
              <StatusPill status={grafana?.status || "unavailable"} />
            </div>

            <div className="mt-6 grid gap-4 md:grid-cols-3">
              <InfoCell label="URL" value={grafana?.url || "not available"} />
              <InfoCell label="Config" value={grafana?.config_path || "not generated"} />
              <InfoCell label="Dashboards" value={grafana?.dashboards_path || "not generated"} />
            </div>

            <div className="mt-6 flex flex-wrap gap-2">
              <GrafanaLink href={grafanaAvailable ? grafana?.url : undefined} label="Open Grafana" primary />
              <GrafanaLink href={grafanaAvailable ? grafana?.overview_url : undefined} label="Overview" />
              <GrafanaLink href={grafanaAvailable ? grafana?.logs_url : undefined} label="Logs" />
              <GrafanaLink href={grafanaAvailable ? grafana?.endpoint_url : undefined} label="Endpoint Debugger" />
            </div>
          </section>

          <section className="grid gap-6 lg:grid-cols-2">
            <div className="rounded-md border border-border p-6">
              <h2 className="text-base font-medium">Datasources</h2>
              <div className="mt-4 divide-y divide-border">
                {datasourceEntries.length === 0 ? (
                  <p className="py-3 text-sm text-muted-foreground">
                    No Grafana datasources are provisioned for this session.
                  </p>
                ) : (
                  datasourceEntries.map(([name, uid]) => (
                    <div key={name} className="flex items-center justify-between gap-4 py-3 text-sm">
                      <div>
                        <div className="font-medium capitalize">{name}</div>
                        <code className="text-xs text-muted-foreground">{uid}</code>
                      </div>
                      <StatusPill status={grafana?.datasource_status?.[name] || grafana?.status || "unknown"} />
                    </div>
                  ))
                )}
              </div>
            </div>

            <div className="rounded-md border border-border p-6">
              <h2 className="text-base font-medium">Dashboards</h2>
              <div className="mt-4 divide-y divide-border">
                {dashboards.length === 0 ? (
                  <p className="py-3 text-sm text-muted-foreground">
                    No Grafana dashboards are available.
                  </p>
                ) : (
                  dashboards.map((dashboard) => (
                    <DashboardRow key={dashboard.uid} dashboard={dashboard} />
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

function DashboardRow({ dashboard }: { dashboard: GrafanaDashboard }) {
  if (!dashboard.url) {
    return (
      <div className="flex items-center justify-between gap-4 py-3 text-sm text-muted-foreground">
        <div>
          <div className="font-medium">{dashboard.title}</div>
          <code className="text-xs">{dashboard.uid}</code>
        </div>
        <span className="text-xs">Unavailable</span>
      </div>
    );
  }
  return (
    <a
      href={dashboard.url}
      target="_blank"
      rel="noreferrer"
      className="flex items-center justify-between gap-4 py-3 text-sm transition-colors hover:text-foreground"
    >
      <div>
        <div className="font-medium">{dashboard.title}</div>
        <code className="text-xs text-muted-foreground">{dashboard.uid}</code>
      </div>
      <span className="text-xs text-muted-foreground">Open</span>
    </a>
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

function GrafanaLink({
  href,
  label,
  primary = false,
}: {
  href?: string;
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

function StatusPill({ status }: { status: string }) {
  const normalized = status.toLowerCase();
  return (
    <span
      className={cn(
        "rounded-full border px-2.5 py-1 text-xs font-medium capitalize",
        normalized === "ready" || normalized === "external"
          ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-300"
          : normalized === "starting"
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

function grafanaStatusCopy(status?: string): string {
  switch (status) {
    case "ready":
      return "Grafana is ready with onlava datasources and dashboards.";
    case "external":
      return "A verified external Grafana instance has onlava datasources and dashboards.";
    case "starting":
      return "Grafana is starting.";
    case "disabled":
      return "Grafana is disabled for this dev session.";
    case "degraded":
      return "Grafana is partially available. Check the dev process output for details.";
    default:
      return "Grafana status is not available yet.";
  }
}
