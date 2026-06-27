import { useMemo } from "react";
import { useDashboard } from "../lib/dashboard-context";

type ServiceCard = {
  key: string;
  label: string;
  url: string;
  kind: string;
};

const SERVICE_ORDER = [
  "api",
  "dashboard",
  "grafana",
];

export function HomePage() {
  const { status } = useDashboard();
  const cards = useMemo(() => buildServiceCards(status?.routes ?? {}), [status?.routes]);

  return (
    <main
      data-scenery-ui="DashboardHome"
      data-scenery-service-count={cards.length}
      className="h-[calc(100vh-var(--header-height))] overflow-auto"
    >
      <div className="mx-auto w-full max-w-7xl px-8 py-8">
        <header className="mb-6 flex flex-wrap items-end justify-between gap-4">
          <div>
            <h1 className="text-xl font-semibold tracking-normal">Home</h1>
            <div className="mt-1 text-sm text-muted-foreground">
              {status?.sessionID || status?.appID || "local"}
            </div>
          </div>
          <div className="rounded-md border border-border px-3 py-2 text-sm text-muted-foreground">
            {cards.length} service{cards.length === 1 ? "" : "s"}
          </div>
        </header>

        {cards.length > 0 ? (
          <section
            data-scenery-ui="DashboardHomeServiceRoutes"
            className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3"
          >
            {cards.map((card) => (
              <a
                key={card.key}
                href={card.url}
                target="_blank"
                rel="noreferrer"
                className="group flex min-h-[132px] flex-col rounded-md border border-border bg-card px-5 py-4 text-card-foreground transition-colors hover:border-primary/50 hover:bg-accent/40"
              >
                <div className="flex items-start justify-between gap-4">
                  <div className="min-w-0">
                    <div className="truncate text-base font-medium">
                      {card.label}
                    </div>
                    <div className="mt-1 text-xs uppercase text-muted-foreground">
                      {card.kind}
                    </div>
                  </div>
                  <IconExternal className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground" />
                </div>
                <div className="mt-auto min-w-0 rounded-md border border-border bg-background/60 px-3 py-2 font-mono text-xs text-muted-foreground">
                  <span className="block truncate">{card.url}</span>
                </div>
              </a>
            ))}
          </section>
        ) : (
          <section
            data-scenery-ui="DashboardHomeNoServiceRoutes"
            data-scenery-state="intentional-empty"
            className="rounded-md border border-border px-5 py-6 text-sm text-muted-foreground"
          >
            No public service URLs are available.
          </section>
        )}
      </div>
    </main>
  );
}

function buildServiceCards(routes: Record<string, string>): ServiceCard[] {
  return Object.entries(routes)
    .filter(([name, url]) => {
      const key = name.trim().toLowerCase();
      return key !== "" && !key.startsWith("victoria") && url.trim() !== "";
    })
    .map(([name, url]) => {
      const key = name.trim();
      return {
        key,
        label: serviceLabel(key),
        url: url.trim(),
        kind: serviceKind(key),
      };
    })
    .sort((a, b) => serviceRank(a.key) - serviceRank(b.key) || a.label.localeCompare(b.label));
}

function serviceRank(name: string): number {
  const index = SERVICE_ORDER.indexOf(name.toLowerCase());
  return index === -1 ? SERVICE_ORDER.length : index;
}

function serviceLabel(name: string): string {
  const key = name.toLowerCase();
  if (key === "api") {
    return "API";
  }
  return name
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function serviceKind(name: string): string {
  const key = name.toLowerCase();
  if (key === "api") {
    return "Runtime";
  }
  if (key === "dashboard") {
    return "Dashboard";
  }
  if (key === "grafana") {
    return "Observability";
  }
  return "Frontend";
}

function IconExternal({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <path
        d="M6 4.5H4.5a2 2 0 0 0-2 2v5a2 2 0 0 0 2 2h5a2 2 0 0 0 2-2V10M8.5 2.5h5v5M8 8l5.2-5.2"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
