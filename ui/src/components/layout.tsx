import { Link, Outlet, useLocation } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useEffect, useMemo, useRef, useState } from "react";
import { DashboardProvider, useDashboard } from "../lib/dashboard-context";
import {
  appStatusSessionState,
  appSummarySessionState,
  isRunningSession,
  sessionStateDotClass,
  sessionStateLabel,
} from "../lib/session-status";
import type { AppStatus } from "../lib/types";
import { cn } from "../lib/utils";
import {
  AppShell,
  appShellAppMenuButtonClass,
  appShellIconButtonClass,
  appShellNavItemClass,
  appShellTopbarActionClass,
} from "./layouts/AppShell";

type DashboardNavIcon = ({ className }: { className?: string }) => ReactNode;

type DashboardNavItem =
  | { kind: "route"; to: string; label: string; icon: DashboardNavIcon }
  | { kind: "ghost"; label: string; icon: DashboardNavIcon };

const APP_NAV_ITEMS: readonly DashboardNavItem[] = [
  {
    kind: "route",
    to: "/$appId",
    label: "Home",
    icon: IconHome,
  },
  {
    kind: "route",
    to: "/$appId/requests",
    label: "API Explorer",
    icon: IconPanelStack,
  },
  {
    kind: "route",
    to: "/$appId/envs/local/api",
    label: "Service Catalog",
    icon: IconNodes,
  },
  { kind: "route", to: "/$appId/data", label: "Data", icon: IconDataObjects },
  { kind: "route", to: "/$appId/db", label: "DB Explorer", icon: IconDatabase },
  { kind: "route", to: "/$appId/observability", label: "Observability", icon: IconLayers },
];

export function DashboardRouteShell({ appId }: { appId: string }) {
  return (
    <DashboardProvider appId={appId}>
      <DashboardShell appId={appId} />
    </DashboardProvider>
  );
}

function DashboardShell({ appId }: { appId: string }) {
  const { apps, connected, status } = useDashboard();
  const { pathname } = useLocation();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const appSummary = apps.find((item) => item.id === appId);
  const appName = appSummary?.name || status?.appID || appId;
  const statusState = appStatusSessionState(status, connected);
  const runningApps = useMemo(
    () => apps.filter((item) => isRunningSession(appSummarySessionState(item))),
    [apps],
  );

  useEffect(() => {
    if (!menuOpen) {
      return;
    }
    const onPointerDown = (event: PointerEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) {
        setMenuOpen(false);
      }
    };
    window.addEventListener("pointerdown", onPointerDown);
    return () => {
      window.removeEventListener("pointerdown", onPointerDown);
    };
  }, [menuOpen]);

  useEffect(() => {
    const root = document.documentElement;
    root.classList.add("dark");
    root.style.colorScheme = "dark";
    document.body.classList.add("scenery-dark");
    return () => {
      document.body.classList.remove("scenery-dark");
    };
  }, []);

  return (
    <AppShell
      topbar={
        <div
          className="flex w-full items-center gap-3 px-3"
          style={{ height: "var(--header-height)" }}
        >
          <div className="flex min-w-0 items-center gap-4">
            <div className="relative flex min-w-0 text-left" ref={menuRef}>
              <button
                type="button"
                data-scenery-ui="AppStatus"
                data-scenery-state={statusState}
                className="flex h-8 w-6 items-center justify-center rounded-md transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                title={statusTooltip(status, connected)}
              >
                <figure
                  className={cn(
                    "h-3 w-3 shrink-0 rounded-full",
                    sessionStateDotClass(statusState),
                  )}
                />
              </button>
              <button
                type="button"
                data-scenery-ui="AppSelector"
                onClick={() => setMenuOpen((value) => !value)}
                className={appShellAppMenuButtonClass()}
                aria-haspopup="menu"
                aria-expanded={menuOpen}
                title={statusTooltip(status, connected)}
              >
                <span className="truncate text-sm font-medium">{appName}</span>
                <IconChevronDown
                  className={cn(
                    "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
                    menuOpen && "rotate-180",
                  )}
                />
              </button>
              {menuOpen ? (
                <div
                  data-scenery-ui="AppSelectorList"
                  className="absolute left-0 top-10 z-50 w-80 rounded-md border border-border bg-popover text-popover-foreground shadow-lg"
                >
                  <div className="border-b border-border px-3 py-2 text-xs font-medium uppercase text-muted-foreground">
                    Running apps
                  </div>
                  <div className="max-h-60 overflow-y-auto p-1">
                    {runningApps.length === 0 ? (
                      <div className="px-3 py-2 text-sm text-muted-foreground">
                        No running apps
                      </div>
                    ) : (
                      runningApps.map((item) => {
                        const state = appSummarySessionState(item);
                        return (
                          <Link
                            key={item.id}
                            to="/$appId"
                            params={{ appId: item.id }}
                            onClick={() => setMenuOpen(false)}
                            className={cn(
                              "flex w-full items-center gap-2 rounded-sm px-3 py-2 text-sm hover:bg-accent hover:text-accent-foreground",
                              item.id === appId &&
                                "bg-accent text-accent-foreground",
                            )}
                          >
                            <figure
                              className={cn(
                                "h-2 w-2 shrink-0 rounded-full",
                                sessionStateDotClass(state),
                              )}
                            />
                            <span className="min-w-0 flex-1">
                              <span className="block truncate font-medium">
                                {item.name}
                              </span>
                              <span className="block truncate text-xs text-muted-foreground">
                                {item.session_id || item.id}
                              </span>
                            </span>
                          </Link>
                        );
                      })
                    )}
                  </div>
                </div>
              ) : null}
            </div>
            <nav className="flex items-center gap-0.5">
              {APP_NAV_ITEMS.map((item) => {
                if ("kind" in item && item.kind === "ghost") {
                  const Icon = item.icon;
                  return (
                    <button
                      key={item.label}
                      type="button"
                      className={appShellNavItemClass(false, true)}
                      onClick={(event) => event.preventDefault()}
                    >
                      <Icon className="h-3.5 w-3.5 opacity-80" />
                      <span className="font-medium">{item.label}</span>
                    </button>
                  );
                }
                const targetPath = item.to.replace("/$appId", `/${appId}`);
                const active =
                  targetPath === `/${appId}`
                    ? pathname === targetPath || pathname === `${targetPath}/`
                    : pathname.startsWith(targetPath);
                const Icon = "icon" in item ? item.icon : null;
                return (
                  <Link
                    key={item.to}
                    to={item.to}
                    params={{ appId }}
                    className={appShellNavItemClass(active)}
                  >
                    {Icon ? <Icon className="h-3.5 w-3.5 opacity-80" /> : null}
                    <span className="font-medium">{item.label}</span>
                  </Link>
                );
              })}
            </nav>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <a
              href="#"
              onClick={(event) => event.preventDefault()}
              className={appShellTopbarActionClass()}
            >
              <IconCloud className="h-3.5 w-3.5 text-amber-400" />
              <span>Cloud Dashboard</span>
            </a>
            <HeaderIconButton
              label=""
              icon={<IconSparkles className="h-4 w-4 text-lime-300" />}
            />
            <HeaderIconButton
              label=""
              icon={<IconLink className="h-4 w-4" />}
            />
            <HeaderIconButton
              label="Toggle theme"
              icon={<IconSun className="h-4 w-4" />}
            />
            <HeaderIconButton
              label=""
              icon={<IconSidebar className="h-4 w-4" />}
            />
          </div>
        </div>
      }
      compileError={
        status?.compileError ? (
          <div className="border-b border-red-500/30 bg-red-500/10 px-4 py-3 text-sm text-red-500">
            <strong>Compile error</strong> {status.compileError}
          </div>
        ) : null
      }
    >
      <Outlet />
    </AppShell>
  );
}

function HeaderIconButton({ icon, label }: { icon: ReactNode; label: string }) {
  return (
    <button
      type="button"
      aria-label={label || undefined}
      title={label || undefined}
      className={appShellIconButtonClass()}
    >
      {icon}
      {label ? <span className="sr-only">{label}</span> : null}
    </button>
  );
}

function IconChevronDown({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <path
        d="m4 6 4 4 4-4"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function IconHome({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <path
        d="M2.5 7.2 8 2.8l5.5 4.4M4 6.8v6h3V9.5h2v3.3h3v-6"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function IconPanelStack({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <path
        d="M3 3.5h10M3 8h10M3 12.5h10"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
      />
    </svg>
  );
}

function IconNodes({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <circle cx="4" cy="4" r="1.5" stroke="currentColor" strokeWidth="1.2" />
      <circle cx="12" cy="4" r="1.5" stroke="currentColor" strokeWidth="1.2" />
      <circle cx="8" cy="12" r="1.5" stroke="currentColor" strokeWidth="1.2" />
      <path
        d="M5.2 4h5.6M4.8 5.1l2.3 5.3m4.1-5.3-2.3 5.3"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
      />
    </svg>
  );
}

function IconLayers({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <path
        d="m8 2.5 5 2.7-5 2.7-5-2.7 5-2.7Zm-5 6 5 2.7 5-2.7M3 11.3 8 14l5-2.7"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function IconDatabase({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <ellipse
        cx="8"
        cy="4"
        rx="4.5"
        ry="2"
        stroke="currentColor"
        strokeWidth="1.2"
      />
      <path
        d="M3.5 4v5c0 1.1 2 2 4.5 2s4.5-.9 4.5-2V4m-9 2.5c0 1.1 2 2 4.5 2s4.5-.9 4.5-2"
        stroke="currentColor"
        strokeWidth="1.2"
      />
    </svg>
  );
}

function IconDataObjects({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <rect
        x="2.5"
        y="3"
        width="4"
        height="4"
        rx="1"
        stroke="currentColor"
        strokeWidth="1.2"
      />
      <rect
        x="9.5"
        y="3"
        width="4"
        height="4"
        rx="1"
        stroke="currentColor"
        strokeWidth="1.2"
      />
      <rect
        x="6"
        y="10"
        width="4"
        height="3.5"
        rx="1"
        stroke="currentColor"
        strokeWidth="1.2"
      />
      <path
        d="M6.5 5h3M8 7v3"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
      />
    </svg>
  );
}

function IconSnippets({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <path
        d="M5 3.5h7v9H5zM3.5 5V12"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
      />
      <path
        d="M7 6.5h3M7 8.5h3M7 10.5h2"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
      />
    </svg>
  );
}

function IconCloud({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <path
        d="M5.5 12.5h5a2.5 2.5 0 0 0 .5-4.95A3.5 3.5 0 0 0 4.2 6.4 2.2 2.2 0 0 0 5.5 12.5Z"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function IconSparkles({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <path
        d="m8 2 1.1 2.9L12 6l-2.9 1.1L8 10 6.9 7.1 4 6l2.9-1.1L8 2Zm4 7 0.6 1.4L14 11l-1.4.6L12 13l-.6-1.4L10 11l1.4-.6L12 9ZM3.5 9l0.5 1.1L5 10.5l-1 .4L3.5 12l-.4-1.1-1.1-.4 1.1-.4L3.5 9Z"
        fill="currentColor"
      />
    </svg>
  );
}

function IconLink({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <path
        d="M6.2 9.8 4.8 11.2a2 2 0 1 1-2.8-2.8l1.4-1.4m6.6-0.6 1.4-1.4a2 2 0 1 1 2.8 2.8l-1.4 1.4M5.8 10.2l4.4-4.4"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
      />
    </svg>
  );
}

function IconSun({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <circle cx="8" cy="8" r="2.5" stroke="currentColor" strokeWidth="1.2" />
      <path
        d="M8 1.8v1.4M8 12.8v1.4M14.2 8h-1.4M3.2 8H1.8m11.1 4.2-1-1M4.1 4.1l-1-1m9.8 0-1 1M4.1 11.9l-1 1"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
      />
    </svg>
  );
}

function IconSidebar({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="none" className={className}>
      <rect
        x="2.5"
        y="3"
        width="11"
        height="10"
        rx="1.5"
        stroke="currentColor"
        strokeWidth="1.2"
      />
      <path d="M6 3v10" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function statusTooltip(status: AppStatus | null, connected: boolean): string {
  const state = appStatusSessionState(status, connected);
  if (status?.sessionStatusReason) {
    return `${sessionStateLabel(state)}: ${status.sessionStatusReason}`;
  }
  switch (state) {
    case "compile-error":
      return "Compile error.";
    case "compiling":
      return "Compiling...";
    case "running":
      return "App is running.";
    case "starting":
      return "App is starting.";
    case "degraded":
      return "Session is degraded.";
    case "stale":
      return "Session is stale.";
    case "stopped":
      return "App is not running.";
    case "disconnected":
      return "Disconnected from scenery. Attempting to reconnect.";
  }
}
