import type { GrafanaState, TraceSummary } from "./types";

export function isGrafanaAvailable(grafana?: GrafanaState | null): boolean {
  return grafana?.available === true;
}

export function requestTracesURL(grafana?: GrafanaState | null): string {
  if (!isGrafanaAvailable(grafana)) {
    return "";
  }
  return grafana?.overview_url || grafana?.url || "";
}

export function temporalTracesURL(grafana?: GrafanaState | null): string {
  if (!isGrafanaAvailable(grafana)) {
    return "";
  }
  return grafana?.temporal_url || grafana?.overview_url || grafana?.url || "";
}

export function traceDashboardURL(grafana?: GrafanaState | null, trace?: Pick<TraceSummary, "type"> | null): string {
  return isTemporalTrace(trace) ? temporalTracesURL(grafana) : requestTracesURL(grafana);
}

export function isTemporalTrace(trace?: Pick<TraceSummary, "type"> | null): boolean {
  return (trace?.type ?? "").startsWith("TEMPORAL_");
}
