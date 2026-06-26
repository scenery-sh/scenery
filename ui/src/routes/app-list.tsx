import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { DashboardRpcClient } from "../lib/rpc";
import {
  appSummarySessionState,
  isSelectableSession,
  sessionStateDotClass,
  sessionStateLabel,
} from "../lib/session-status";
import { useResolvedTheme } from "../lib/theme";
import type { AppSummary } from "../lib/types";
import { cn, dashboardWebsocketURL } from "../lib/utils";

export function AppListPage() {
  const navigate = useNavigate();
  const resolvedTheme = useResolvedTheme();
  const clientRef = useRef<DashboardRpcClient | null>(null);
  if (!clientRef.current) {
    clientRef.current = new DashboardRpcClient(dashboardWebsocketURL());
  }

  const [apps, setApps] = useState<AppSummary[]>();

  useEffect(() => {
    let canceled = false;
    const client = clientRef.current;
    if (!client) {
      return;
    }

    const refresh = async () => {
      try {
        const next = await client.request<AppSummary[]>("list-apps");
        if (!canceled) {
          setApps(next);
        }
      } catch {
        if (!canceled) {
          setApps([]);
        }
      }
    };

    void refresh();
    const timer = window.setInterval(() => {
      void refresh();
    }, 250);

    return () => {
      canceled = true;
      window.clearInterval(timer);
      client.dispose();
      clientRef.current = null;
    };
  }, []);

  const immediatelyUsefulApps = useMemo(
    () =>
      (apps ?? []).filter((app) =>
        isSelectableSession(appSummarySessionState(app)),
      ),
    [apps],
  );

  useEffect(() => {
    if (immediatelyUsefulApps.length === 1) {
      void navigate({
        to: "/$appId",
        params: { appId: immediatelyUsefulApps[0].id },
        replace: true,
      });
    }
  }, [navigate, immediatelyUsefulApps]);

  return (
    <div className="overflow-hidden min-h-screen flex flex-col w-full">
      <nav className="bg-contrast-5 min-w-fit mb-25">
        <div className="mx-auto px-6">
          <div className="flex items-center justify-between" style={{ minHeight: "var(--header-height)" }}>
            <img
              className="h-8"
              src={
                resolvedTheme === "dark"
                  ? "/assets/img/wordmark.svg"
                  : "/assets/branding/logo/logo-no-padding.svg"
              }
              alt="scenery Logo"
            />
          </div>
        </div>
      </nav>
      <div className="relative -mt-[100px] flex h-full w-full min-w-0 flex-col justify-center items-center">
        <h1 className="font-sans text-lg font-medium">Your applications</h1>
        <div className="my-8 flex w-full max-w-md flex-col gap-6">
          {apps === undefined ? (
            <div className="flex items-center justify-center">
              <div className="h-6 w-6 animate-spin rounded-full border-2 border-border border-t-foreground" />
            </div>
          ) : apps.length === 0 ? (
            <p>
              Create your first app by running <code>scenery app create</code> in your terminal, or by
              <br />
              starting an existing app using <code>scenery dev</code>.
            </p>
          ) : (
            apps.map((app) => {
              const state = appSummarySessionState(app);
              const muted = state === "stale" || state === "stopped";
              return (
                <Link
                  key={app.id}
                  to="/$appId"
                  params={{ appId: app.id }}
                  className={cn(
                    "flex items-center justify-between rounded-md border border-border px-4 py-3 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                    muted && "opacity-50",
                  )}
                >
                  <span className="flex items-center gap-2">
                    <figure
                      className={cn(
                        "h-2 w-2 rounded-full",
                        sessionStateDotClass(state),
                      )}
                    />
                    <span className="min-w-0">
                      <span className="block truncate font-medium">
                        {app.name}
                      </span>
                      {state !== "running" ? (
                        <span
                          className={cn(
                            "block truncate text-xs text-muted-foreground",
                            state === "compile-error" && "text-red-500",
                            state === "degraded" && "text-amber-400",
                          )}
                        >
                          {sessionStateLabel(state)}
                        </span>
                      ) : null}
                    </span>
                  </span>
                  <span aria-hidden="true">›</span>
                </Link>
              );
            })
          )}
        </div>
      </div>
    </div>
  );
}
