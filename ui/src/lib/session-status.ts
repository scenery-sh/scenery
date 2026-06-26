import type { AppStatus, AppSummary } from "./types";

export type DashboardSessionState =
  | "compile-error"
  | "compiling"
  | "running"
  | "starting"
  | "degraded"
  | "stale"
  | "stopped"
  | "disconnected";

function normalizeSessionStatus(status?: string): string {
  return (status ?? "").trim().toLowerCase();
}

export function appSummarySessionState(app: AppSummary): DashboardSessionState {
  if (app.compileError) {
    return "compile-error";
  }
  const status = normalizeSessionStatus(app.sessionStatus);
  switch (status) {
    case "running":
      return app.offline ? "stopped" : "running";
    case "starting":
      return "starting";
    case "degraded":
      return "degraded";
    case "stale":
      return "stale";
    case "stopped":
    case "registered":
    case "exited":
      return "stopped";
    case "compile-error":
      return "compile-error";
    default:
      return app.offline ? "stopped" : "running";
  }
}

export function appStatusSessionState(
  status: AppStatus | null,
  connected: boolean,
): DashboardSessionState {
  if (!connected) {
    return "disconnected";
  }
  if (status?.compileError) {
    return "compile-error";
  }
  if (status?.compiling) {
    return "compiling";
  }
  const sessionStatus = normalizeSessionStatus(status?.sessionStatus);
  switch (sessionStatus) {
    case "running":
      return status?.running === false ? "stopped" : "running";
    case "starting":
      return "starting";
    case "degraded":
      return "degraded";
    case "stale":
      return "stale";
    case "stopped":
    case "registered":
    case "exited":
      return "stopped";
    case "compile-error":
      return "compile-error";
    default:
      return status?.running ? "running" : "stopped";
  }
}

export function isSelectableSession(state: DashboardSessionState): boolean {
  return state === "running" || state === "starting" || state === "compile-error";
}

export function isRunningSession(state: DashboardSessionState): boolean {
  return state === "running" || state === "starting";
}

export function sessionStateDotClass(state: DashboardSessionState): string {
  switch (state) {
    case "compile-error":
      return "bg-red-500";
    case "compiling":
      return "animate-spin border-2 border-sidebar-foreground border-t-transparent";
    case "running":
      return "bg-success";
    case "starting":
      return "animate-pulse bg-amber-400";
    case "degraded":
      return "bg-amber-400";
    case "stale":
    case "stopped":
    case "disconnected":
      return "bg-neutral-600 opacity-inactive";
  }
}

export function sessionStateLabel(state: DashboardSessionState): string {
  switch (state) {
    case "compile-error":
      return "Compile error";
    case "compiling":
      return "Compiling";
    case "running":
      return "Running";
    case "starting":
      return "Starting";
    case "degraded":
      return "Degraded";
    case "stale":
      return "Stale";
    case "stopped":
      return "Stopped";
    case "disconnected":
      return "Disconnected";
  }
}
