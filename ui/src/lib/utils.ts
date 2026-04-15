import type { MetadataPath, ProcessOutput, TraceSummary } from "./types";

export function cn(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

export function formatDurationNanos(nanos?: number): string {
  if (!nanos) {
    return "0 ms";
  }
  const ms = nanos / 1_000_000;
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)} s`;
  }
  if (ms >= 1) {
    return `${ms.toFixed(2)} ms`;
  }
  return `${Math.round(nanos / 1000)} µs`;
}

export function formatTimestamp(value?: string): string {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

export function formatTime(value?: string): string {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleTimeString([], {
    hour: "numeric",
    minute: "2-digit",
  });
}

export function renderMetadataPath(path?: MetadataPath): string {
  if (!path || !Array.isArray(path.segments)) {
    return "/";
  }
  const rendered = path.segments.map((segment) =>
    segment.type === "PARAM" ? `:${segment.value}` : segment.value,
  );
  return `/${rendered.join("/")}`;
}

export function materializePath(template: string, params: unknown): string {
  if (!template) {
    return "/";
  }
  if (Array.isArray(params)) {
    let index = 0;
    return template.replace(/:([^/]+)/g, () => {
      const value = params[index];
      index += 1;
      return value === undefined ? "" : encodeURIComponent(String(value));
    });
  }
  if (!params || typeof params !== "object") {
    return template;
  }
  let next = template;
  for (const [key, value] of Object.entries(params as Record<string, unknown>)) {
    next = next.replace(`:${key}`, encodeURIComponent(String(value ?? "")));
  }
  return next;
}

export function decodeBase64Utf8(input: string): string {
  if (!input) {
    return "";
  }
  try {
    const binary = atob(input);
    const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
    return new TextDecoder().decode(bytes);
  } catch {
    return input;
  }
}

export function processOutputText(item: ProcessOutput): string {
  return decodeBase64Utf8(item.output).replace(/\r\n/g, "\n");
}

export function bodyTextFromApiCall(input: string): string {
  return decodeBase64Utf8(input);
}

export function tryParseJSON(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

export function formatJSON(value: unknown): string {
  if (value === undefined) {
    return "";
  }
  if (typeof value === "string") {
    return value;
  }
  return JSON.stringify(value, null, 2);
}

export function parseJSONInput(text: string): unknown {
  const trimmed = text.trim();
  if (!trimmed) {
    return {};
  }
  return JSON.parse(trimmed);
}

export function upsertTrace(list: TraceSummary[], next: TraceSummary): TraceSummary[] {
  const filtered = list.filter((item) => item.trace_id !== next.trace_id);
  return [next, ...filtered].sort((a, b) => b.started_at.localeCompare(a.started_at));
}

export function dashboardWebsocketURL(): string {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}/__pulse`;
}
