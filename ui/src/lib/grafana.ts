import type { GrafanaState } from "./types";

export function isGrafanaAvailable(grafana?: GrafanaState | null): boolean {
  return grafana?.available === true;
}

export function requestTracesURL(grafana?: GrafanaState | null): string {
  if (!isGrafanaAvailable(grafana)) {
    return "";
  }
  return grafana?.overview_url || grafana?.url || "";
}

export function traceDashboardURL(grafana?: GrafanaState | null): string {
  return requestTracesURL(grafana);
}
