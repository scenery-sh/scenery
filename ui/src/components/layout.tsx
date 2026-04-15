import { Link, Outlet, useLocation } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { DashboardProvider, useDashboard } from "../lib/dashboard-context";
import { useResolvedTheme } from "../lib/theme";
import { cn } from "../lib/utils";

const NAV_ITEMS = [
  { to: "/$appId/requests", label: "API Explorer" },
  { to: "/$appId/envs/local/api", label: "Service Catalog" },
  { to: "/$appId/envs/local/traces", label: "Traces" },
  { to: "/$appId/db", label: "DB Explorer" },
  { to: "/$appId/cron", label: "Cron" },
] as const;

const REQUESTS_NAV_ITEMS = [
  { kind: "route", to: "/$appId/requests", label: "API Explorer", icon: IconPanelStack },
  { kind: "route", to: "/$appId/envs/local/api", label: "Service Catalog", icon: IconNodes },
  { kind: "ghost", label: "Infra", icon: IconLayers },
  { kind: "ghost", label: "Flow", icon: IconFlow },
  { kind: "route", to: "/$appId/db", label: "DB Explorer", icon: IconDatabase },
  { kind: "ghost", label: "Snippets", icon: IconSnippets },
] as const;

export function DashboardRouteShell({ appId }: { appId: string }) {
  return (
    <DashboardProvider appId={appId}>
      <DashboardShell appId={appId} />
    </DashboardProvider>
  );
}

function DashboardShell({ appId }: { appId: string }) {
  const { apps, connected, status } = useDashboard();
  const resolvedTheme = useResolvedTheme();
  const { pathname } = useLocation();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const appSummary = apps.find((item) => item.id === appId);
  const appName = appSummary?.name || status?.appID || appId;
  const requestsMode = pathname.startsWith(`/${appId}/requests`);

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
    document.body.classList.add("encore-dark");
    return () => {
      document.body.classList.remove("encore-dark");
    };
  }, []);

  return (
    <div className={cn("[--header-height:65px] min-h-screen bg-background text-foreground", requestsMode && "[--header-height:52px]")}>
      <header className={cn("bg-sidebar text-sidebar-foreground border-sidebar-border fixed top-0 z-50 flex w-full items-center border-b", requestsMode && "bg-topnav text-topnav-foreground border-topnav-border")}>
        <div className={cn("flex h-(--header-height) w-full items-center gap-2 px-4", requestsMode && "gap-3 px-3")}>
          <div className="flex min-w-0 items-center gap-4">
            <Link
              to="/"
              className={cn(
                "flex items-center rounded-md px-2 py-2 h-8 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors",
                requestsMode && "hidden",
              )}
            >
              <img
                className={cn("h-6 w-auto", requestsMode && "h-4")}
                src={
                  resolvedTheme === "dark"
                    ? "/assets/img/wordmark.svg"
                    : "/assets/branding/logo/logo-no-padding.svg"
                }
                alt="Pulse"
              />
            </Link>
            <div className="relative flex min-w-0 text-left" ref={menuRef}>
              {requestsMode ? (
                <button
                  type="button"
                  className="flex h-8 w-6 items-center justify-center rounded-md hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors"
                  title={statusTooltip(status?.compileError, status?.compiling, status?.running, connected)}
                >
                  <figure
                    className={cn(
                      "h-3 w-3 rounded-full shrink-0",
                      status?.compileError
                        ? "bg-red-500"
                        : status?.compiling
                          ? "border-2 border-sidebar-foreground border-t-transparent animate-spin"
                          : status?.running
                            ? "bg-success"
                            : "bg-neutral-600 opacity-inactive",
                    )}
                  />
                </button>
              ) : null}
              <button
                type="button"
                onClick={() => setMenuOpen((value) => !value)}
                className={cn(
                  "flex items-center gap-2 text-left px-2 py-2 h-8 rounded-md hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus:outline-none cursor-pointer overflow-hidden transition-colors",
                  requestsMode && "gap-0 px-2",
                )}
                title={statusTooltip(status?.compileError, status?.compiling, status?.running, connected)}
              >
                {!requestsMode ? (
                  <figure
                    className={cn(
                      "h-3 w-3 rounded-full shrink-0",
                      status?.compileError
                        ? "bg-red-500"
                        : status?.compiling
                          ? "border-2 border-sidebar-foreground border-t-transparent animate-spin"
                          : status?.running
                            ? "bg-success"
                            : "bg-neutral-600 opacity-inactive",
                    )}
                  />
                ) : null}
                <span className="truncate text-sm font-medium">{appName}</span>
                {!requestsMode ? <span className="text-xs opacity-60">▾</span> : null}
              </button>
              {menuOpen ? (
                <div className="absolute left-0 top-10 z-50 w-64 rounded-md border border-border bg-popover text-popover-foreground shadow-lg">
                  <div className="max-h-60 overflow-y-auto p-1">
                    {apps.length === 0 ? (
                      <div className="px-3 py-2 text-sm text-muted-foreground">No apps found</div>
                    ) : (
                      apps.map((item) => (
                        <Link
                          key={item.id}
                          to="/$appId"
                          params={{ appId: item.id }}
                          onClick={() => setMenuOpen(false)}
                          className={cn(
                            "flex w-full items-center space-x-2 rounded-sm px-3 py-2 text-sm hover:bg-accent hover:text-accent-foreground",
                            item.offline && "opacity-50",
                            item.id === appId && "bg-accent text-accent-foreground",
                          )}
                        >
                          <figure
                            className={cn(
                              "h-2 w-2 rounded-full shrink-0",
                              item.offline ? "border border-border bg-transparent" : "bg-success",
                            )}
                          />
                          <span className="truncate font-medium">{item.name}</span>
                        </Link>
                      ))
                    )}
                  </div>
                </div>
              ) : null}
            </div>
            <nav className={cn("flex items-center gap-3", requestsMode && "gap-0.5")}>
              {(requestsMode ? REQUESTS_NAV_ITEMS : NAV_ITEMS).map((item) => {
                if ("kind" in item && item.kind === "ghost") {
                  const Icon = item.icon;
                  return (
                    <button
                      key={item.label}
                      type="button"
                      className="flex flex-row items-center rounded-md px-2 py-2 text-sm h-8 gap-2 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors opacity-90"
                      onClick={(event) => event.preventDefault()}
                    >
                      <Icon className="h-3.5 w-3.5 opacity-80" />
                      <span className="font-medium">{item.label}</span>
                    </button>
                  );
                }
                const active = pathname.startsWith(item.to.replace("/$appId", `/${appId}`));
                const Icon = "icon" in item ? item.icon : null;
                return (
                  <Link
                    key={item.to}
                    to={item.to}
                    params={{ appId }}
                    className={cn(
                      "flex flex-row items-center rounded-md px-2 py-2 text-sm h-8 gap-2 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors",
                      active && "bg-sidebar-accent text-sidebar-accent-foreground",
                    )}
                  >
                    {Icon ? <Icon className="h-3.5 w-3.5 opacity-80" /> : null}
                    <span className="font-medium">{item.label}</span>
                  </Link>
                );
              })}
            </nav>
          </div>
          <div className="ml-auto flex items-center gap-2">
            {requestsMode ? (
              <>
                <a
                  href="#"
                  onClick={(event) => event.preventDefault()}
                  className="rounded-md px-3 py-2 text-sm h-8 flex items-center gap-2 focus:outline-none hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors"
                >
                  <IconCloud className="h-3.5 w-3.5 text-amber-400" />
                  <span>Cloud Dashboard</span>
                </a>
                <HeaderIconButton label="" icon={<IconSparkles className="h-4 w-4 text-lime-300" />} />
                <HeaderIconButton label="" icon={<IconLink className="h-4 w-4" />} />
                <HeaderIconButton label="Toggle theme" icon={<IconSun className="h-4 w-4" />} />
                <HeaderIconButton label="" icon={<IconSidebar className="h-4 w-4" />} />
              </>
            ) : null}
            {!requestsMode && status?.addr ? (
              <code className="hidden xl:inline-block rounded-md border border-sidebar-border px-3 py-1.5 text-xs">
                {window.location.protocol === "https:" ? "https" : "http"}://{status.addr}
              </code>
            ) : null}
            {!requestsMode ? <span className="rounded-md border border-sidebar-border px-3 py-1.5 text-xs">
              {connected ? "Live WS" : "Reconnecting"}
            </span> : null}
            {!requestsMode && status?.pid ? (
              <span className="hidden xl:inline-block rounded-md border border-sidebar-border px-3 py-1.5 text-xs">
                pid {status.pid}
              </span>
            ) : null}
          </div>
        </div>
      </header>
      <div className="pt-[65px]">
        {status?.compileError ? (
          <div className="border-b border-red-500/30 bg-red-500/10 px-4 py-3 text-sm text-red-500">
            <strong>Compile error</strong> {status.compileError}
          </div>
        ) : null}
        <Outlet />
      </div>
    </div>
  );
}

function HeaderIconButton({ icon, label }: { icon: React.ReactNode; label: string }) {
  return (
    <button
      type="button"
      aria-label={label || undefined}
      title={label || undefined}
      className="inline-flex items-center justify-center rounded-md size-9 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors"
    >
      {icon}
      {label ? <span className="sr-only">{label}</span> : null}
    </button>
  );
}

function IconPanelStack({ className }: { className?: string }) {
  return <svg viewBox="0 0 16 16" fill="none" className={className}><path d="M3 3.5h10M3 8h10M3 12.5h10" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/></svg>;
}

function IconNodes({ className }: { className?: string }) {
  return <svg viewBox="0 0 16 16" fill="none" className={className}><circle cx="4" cy="4" r="1.5" stroke="currentColor" strokeWidth="1.2"/><circle cx="12" cy="4" r="1.5" stroke="currentColor" strokeWidth="1.2"/><circle cx="8" cy="12" r="1.5" stroke="currentColor" strokeWidth="1.2"/><path d="M5.2 4h5.6M4.8 5.1l2.3 5.3m4.1-5.3-2.3 5.3" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/></svg>;
}

function IconLayers({ className }: { className?: string }) {
  return <svg viewBox="0 0 16 16" fill="none" className={className}><path d="m8 2.5 5 2.7-5 2.7-5-2.7 5-2.7Zm-5 6 5 2.7 5-2.7M3 11.3 8 14l5-2.7" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"/></svg>;
}

function IconFlow({ className }: { className?: string }) {
  return <svg viewBox="0 0 16 16" fill="none" className={className}><path d="M5 4h5a2 2 0 1 1 0 4H6a2 2 0 1 0 0 4h5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/><circle cx="4" cy="4" r="1.5" fill="currentColor"/><circle cx="12" cy="12" r="1.5" fill="currentColor"/></svg>;
}

function IconDatabase({ className }: { className?: string }) {
  return <svg viewBox="0 0 16 16" fill="none" className={className}><ellipse cx="8" cy="4" rx="4.5" ry="2" stroke="currentColor" strokeWidth="1.2"/><path d="M3.5 4v5c0 1.1 2 2 4.5 2s4.5-.9 4.5-2V4m-9 2.5c0 1.1 2 2 4.5 2s4.5-.9 4.5-2" stroke="currentColor" strokeWidth="1.2"/></svg>;
}

function IconSnippets({ className }: { className?: string }) {
  return <svg viewBox="0 0 16 16" fill="none" className={className}><path d="M5 3.5h7v9H5zM3.5 5V12" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/><path d="M7 6.5h3M7 8.5h3M7 10.5h2" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/></svg>;
}

function IconCloud({ className }: { className?: string }) {
  return <svg viewBox="0 0 16 16" fill="none" className={className}><path d="M5.5 12.5h5a2.5 2.5 0 0 0 .5-4.95A3.5 3.5 0 0 0 4.2 6.4 2.2 2.2 0 0 0 5.5 12.5Z" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"/></svg>;
}

function IconSparkles({ className }: { className?: string }) {
  return <svg viewBox="0 0 16 16" fill="none" className={className}><path d="m8 2 1.1 2.9L12 6l-2.9 1.1L8 10 6.9 7.1 4 6l2.9-1.1L8 2Zm4 7 0.6 1.4L14 11l-1.4.6L12 13l-.6-1.4L10 11l1.4-.6L12 9ZM3.5 9l0.5 1.1L5 10.5l-1 .4L3.5 12l-.4-1.1-1.1-.4 1.1-.4L3.5 9Z" fill="currentColor"/></svg>;
}

function IconLink({ className }: { className?: string }) {
  return <svg viewBox="0 0 16 16" fill="none" className={className}><path d="M6.2 9.8 4.8 11.2a2 2 0 1 1-2.8-2.8l1.4-1.4m6.6-0.6 1.4-1.4a2 2 0 1 1 2.8 2.8l-1.4 1.4M5.8 10.2l4.4-4.4" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/></svg>;
}

function IconSun({ className }: { className?: string }) {
  return <svg viewBox="0 0 16 16" fill="none" className={className}><circle cx="8" cy="8" r="2.5" stroke="currentColor" strokeWidth="1.2"/><path d="M8 1.8v1.4M8 12.8v1.4M14.2 8h-1.4M3.2 8H1.8m11.1 4.2-1-1M4.1 4.1l-1-1m9.8 0-1 1M4.1 11.9l-1 1" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/></svg>;
}

function IconSidebar({ className }: { className?: string }) {
  return <svg viewBox="0 0 16 16" fill="none" className={className}><rect x="2.5" y="3" width="11" height="10" rx="1.5" stroke="currentColor" strokeWidth="1.2"/><path d="M6 3v10" stroke="currentColor" strokeWidth="1.2"/></svg>;
}

function statusTooltip(
  compileError: string | undefined,
  compiling: boolean | undefined,
  running: boolean | undefined,
  connected: boolean,
): string {
  if (!connected) {
    return "Disconnected from Pulse. Attempting to reconnect.";
  }
  if (compiling) {
    return "Compiling...";
  }
  if (compileError) {
    return "Compile error.";
  }
  if (running) {
    return "App is running.";
  }
  return "App is not running.";
}
