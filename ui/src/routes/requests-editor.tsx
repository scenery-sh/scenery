import { forwardRef, useEffect, useMemo, useRef } from "react";
import { Button } from "@/components/primitives/Button";
import { cn, formatDurationNanos, formatTime, processOutputText, renderMetadataPath, tryParseJSON } from "../lib/utils";
import type { ApiCallResponse, APIEncodingRPC, ServiceRPC, StoredRequest } from "../lib/types";
import type { RequestTab } from "../lib/api-explorer";
import {
  IconChevronRight,
  IconCopy,
  IconInfo,
  IconMoreHorizontal,
  IconPlus,
  IconRefresh,
  IconSave,
  IconSource,
  IconX,
  type FuzzyMatch,
  fuzzyMatch,
  highlightText,
} from "./requests-modals";

export function CompactRequestEditor({
  disabled,
  hasAuth,
  hasPathParams,
  hasRequestPayload,
  requestTab,
  onCall,
  onOpenStoreModal,
  onResetPath,
  onUpdate,
  onUpdatePathParams,
}: {
  disabled: boolean;
  hasAuth: boolean;
  hasPathParams: boolean;
  hasRequestPayload: boolean;
  requestTab: RequestTab;
  onCall: () => void;
  onOpenStoreModal: () => void;
  onResetPath: () => void;
  onUpdate: (patch: Partial<RequestTab>) => void;
  onUpdatePathParams: (value: string) => void;
}) {
  return (
    <div data-onlava-ui="APIExplorerRequestForm" className="overflow-visible">
      <div className="space-y-4 rounded-md bg-secondary p-3 min-w-0 max-w-full">
        <EditorSection
          title={
            <div>
              <div className="mb-2">
                <span>API Caller</span>
              </div>
              <div className="flex items-center space-x-2">
                <span>Path</span>
                <button type="button" className="text-muted-foreground">
                  <IconInfo className="h-4 w-4" />
                </button>
              </div>
            </div>
          }
          actions={
            <button type="button" className="flex items-center opacity-70" onClick={onResetPath}>
              <IconRefresh className="h-4 w-4" />
            </button>
          }
        >
          <div className="bg-background rounded bg-opacity-10">
            <div className="flex items-center overflow-x-auto rounded py-1">
              <div className="px-2">
                <select
                  className="bg-transparent font-mono text-xs font-semibold outline-none"
                  value={requestTab.method}
                  onChange={(event) => onUpdate({ method: event.target.value.toUpperCase() })}
                >
                  <option value={requestTab.method}>{requestTab.method}</option>
                  {requestTab.method !== "GET" ? <option value="GET">GET</option> : null}
                  {requestTab.method !== "POST" ? <option value="POST">POST</option> : null}
                  {requestTab.method !== "PUT" ? <option value="PUT">PUT</option> : null}
                  {requestTab.method !== "PATCH" ? <option value="PATCH">PATCH</option> : null}
                  {requestTab.method !== "DELETE" ? <option value="DELETE">DELETE</option> : null}
                </select>
              </div>
              <div className="min-w-0 flex-1">
                <input
                  className="w-full bg-transparent py-1 font-mono text-xs outline-none"
                  value={requestTab.path}
                  onChange={(event) => onUpdate({ path: event.target.value })}
                />
              </div>
            </div>
          </div>
          {hasPathParams ? (
            <div className="mt-3 rounded bg-background/10 p-2">
              <textarea
                className="block min-h-[88px] w-full resize-y bg-transparent font-mono text-xs leading-6 outline-none"
                value={requestTab.pathParamsText}
                onChange={(event) => onUpdatePathParams(event.target.value)}
              />
            </div>
          ) : null}
        </EditorSection>

        {hasRequestPayload ? (
          <EditorSection
            title={
              <div className="flex items-center space-x-2">
                <span>Request editor</span>
              </div>
            }
          >
            <div className="rounded bg-background/10 p-2">
              <AutoSizeTextarea
                className="block min-h-[180px] w-full resize-y bg-transparent font-mono text-xs leading-6 outline-none"
                value={requestTab.payloadText}
                onChange={(event) => onUpdate({ payloadText: event.target.value })}
              />
            </div>
          </EditorSection>
        ) : null}

        {hasAuth ? (
          <EditorSection
            title={
              <div className="flex items-center space-x-2">
                <span>Authentication data</span>
                <button type="button" className="text-muted-foreground">
                  <IconInfo className="h-4 w-4" />
                </button>
              </div>
            }
            actions={
              <div className="flex items-center space-x-2">
                <button type="button" onClick={onOpenStoreModal}>
                  <IconSave className="h-4 w-4" />
                </button>
                <button type="button">
                  <IconRefresh className="h-4 w-4" />
                </button>
              </div>
            }
          >
            <div className="relative min-w-0 rounded">
              <input
                className="h-9 w-full rounded border-none bg-background bg-opacity-10 px-3 pb-0 pt-0 text-xs placeholder:text-xs outline-none"
                placeholder="Auth Token"
                value={requestTab.authToken}
                onChange={(event) => onUpdate({ authToken: event.target.value })}
              />
            </div>
          </EditorSection>
        ) : null}

        <div className="flex items-center justify-end space-x-5">
          <div className="flex space-x-3">
            <button type="button" className="inline-flex h-5 w-5 items-center justify-center">
              <IconCopy className="h-4 w-4" />
            </button>
            <button type="button" className="inline-flex h-5 w-5 items-center justify-center" onClick={onOpenStoreModal}>
              <IconSave className="h-4 w-4" />
            </button>
          </div>
          <Button
            data-testid="call-api-button"
            className="w-20"
            disabled={disabled}
            onClick={onCall}
          >
            Call
          </Button>
        </div>
      </div>
    </div>
  );
}

export function EditorSection({
  actions,
  children,
  title,
}: {
  actions?: React.ReactNode;
  children: React.ReactNode;
  title: React.ReactNode;
}) {
  return (
    <div className="flex min-w-0 shrink grow flex-col">
      <div className="flex shrink-0 grow-0 items-center justify-between pb-1.5">
        <span className="text-sm font-semibold">{title}</span>
        {actions}
      </div>
      <div className="flex min-w-0 shrink grow flex-col text-xs">{children}</div>
    </div>
  );
}

export function AutoSizeTextarea(
  props: React.TextareaHTMLAttributes<HTMLTextAreaElement>,
) {
  const ref = useRef<HTMLTextAreaElement | null>(null);
  const value = typeof props.value === "string" ? props.value : "";

  useEffect(() => {
    const element = ref.current;
    if (!element) {
      return;
    }
    element.style.height = "0px";
    element.style.height = `${element.scrollHeight}px`;
  }, [value]);

  return <textarea {...props} ref={ref} />;
}

export function SourceLinkButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <div className="mb-4 mt-2 flex items-center text-xs">
      <button type="button" className="mr-1 inline-flex items-center justify-center" onClick={onClick}>
        <IconSource className="h-4 w-4" />
      </button>
      <button type="button" className="underline flex items-center space-x-1 minimal" onClick={onClick}>
        <span>{label}</span>
      </button>
    </div>
  );
}

export function ResponsePanel({
  response,
  traceDuration,
  traceURL,
}: {
  response: ApiCallResponse;
  traceDuration: string;
  traceURL?: string;
}) {
  const hasError = response.status_code >= 400;
  const rawBody = typeof response.body === "string" ? response.body.trim() : "";
  const bodyText = renderResponseBody(response.body);
  const hasTrace = !!response.trace_id;

  return (
    <div className="w-full mt-5 min-w-0 max-w-full">
      <div className="bg-secondary p-3 rounded-md min-w-0 max-w-full">
        <div className="flex items-start justify-between gap-3">
          <div className="flex items-center space-x-3">
            <span className="text-sm font-semibold">Response</span>
            <div className="flex items-center space-x-1">
              <figure className={cn("w-[8px] h-[8px] rounded-full", hasError ? "bg-red-500" : "bg-success")} />
              <span className="font-mono text-sm">{response.status_code}</span>
            </div>
            {hasTrace && traceURL ? (
              <a
                href={traceURL}
                target="_blank"
                rel="noreferrer"
                className="flex items-center text-sm underline"
              >
                View in Grafana
              </a>
            ) : null}
          </div>

          {traceDuration ? (
            <div className="text-xs flex items-center space-x-1 mr-1 text-muted-foreground">
              <span>{traceDuration}</span>
            </div>
          ) : null}
        </div>

        {hasError ? (
          <div className="bg-background bg-opacity-10 p-2 mt-3 text-xs text-red-500 font-mono rounded min-w-0 max-w-full break-words whitespace-pre-wrap">
            {`HTTP ${response.status}: ${rawBody}`}
          </div>
        ) : (
          <pre className="bg-background text-foreground bg-opacity-10 rounded px-2 py-2 mt-3 text-xs min-w-0 max-w-full whitespace-pre-wrap break-words">
            {bodyText}
          </pre>
        )}
      </div>
    </div>
  );
}

export function TabStrip({
  activeTabID,
  tabs,
  onActivate,
  onClose,
  onNew,
}: {
  activeTabID: string | null;
  tabs: RequestTab[];
  onActivate: (tabID: string) => void;
  onClose: (tabID: string) => void;
  onNew: () => void;
}) {
  return (
    <div className="flex border-t-0" style={{ height: "40px" }}>
      {tabs.length > 0 ? (
        <>
          <div className="flex h-full bg-transparent rounded-none p-0 w-auto border-t-0 overflow-x-auto">
            {tabs.map((tab, index) => {
              const active = activeTabID === tab.id;
              const accentClass = active ? `brandient-${(index % 4) + 1}` : "";
              return (
                <button
                  key={tab.id}
                  type="button"
                  onClick={() => onActivate(tab.id)}
                  className={cn(
                    "flex items-center min-w-[180px] w-[180px] relative h-full px-3 group justify-between transition-colors border-b border-border bg-sidebar hover:bg-background/80",
                    active && "bg-background border-l border-r border-t border-border border-b-0",
                    index === 0 && active && "border-l-0",
                  )}
                >
                  {active ? <div className={`w-full top-0 left-0 right-0 bg-linear-to-r h-1 absolute ${accentClass}`} /> : null}
                  <span className={cn("text-xs truncate", active ? "font-medium" : "opacity-70")}>
                    {tab.title}
                  </span>
                  {tabs.length > 1 ? (
                    <span
                      className="group/div flex h-[24px] w-[24px] items-center justify-center rounded-full opacity-inactive hover:bg-accent"
                      onClick={(event) => {
                        event.stopPropagation();
                        onClose(tab.id);
                      }}
                    >
                      <IconX className="hidden h-3 w-3 group-hover:inline group-hover/div:opacity-100" />
                    </span>
                  ) : null}
                </button>
              );
            })}
          </div>
          <div className="flex items-center justify-center border-b border-border h-full pl-2 pr-4 bg-sidebar relative">
            <button
              type="button"
              className="flex items-center justify-center h-7 w-7 opacity-inactive cursor-pointer hover:opacity-100 rounded-full hover:bg-accent"
              onClick={onNew}
            >
              <IconPlus className="h-3.5 w-3.5" />
            </button>
          </div>
          <div className="flex-1 border-b border-border h-full bg-sidebar" />
        </>
      ) : null}
    </div>
  );
}

export function RequestFolder({
  items,
  label,
  menuRequestID,
  open,
  onEdit,
  onDelete,
  onOpen,
  onToggleOpen,
  onToggleMenu,
  searchQuery,
}: {
  items: StoredRequest[];
  label: string;
  menuRequestID: string | null;
  open: boolean;
  onEdit: (item: StoredRequest) => void;
  onDelete: (item: StoredRequest) => void;
  onOpen: (item: StoredRequest) => void;
  onToggleOpen: () => void;
  onToggleMenu: (itemID: string) => void;
  searchQuery: string;
}) {
  const filteredItems = useMemo(() => {
    const needle = searchQuery.trim();
    if (!needle) {
      return items.map((item) => ({ item, titleRanges: [] as Array<[number, number]>, score: 0 }));
    }
    return items
      .map((item) => {
        const titleMatch = fuzzyMatch(item.title, needle);
        const rpcMatch = fuzzyMatch(item.rpcName, needle);
        const svcMatch = fuzzyMatch(item.svcName, needle);
        const best =
          [titleMatch, rpcMatch, svcMatch]
            .filter((match): match is FuzzyMatch => match !== null)
            .sort((a, b) => b.score - a.score)[0] ?? null;
        return {
          item,
          titleRanges: titleMatch?.ranges ?? [],
          score: best?.score ?? Number.NEGATIVE_INFINITY,
        };
      })
      .filter((entry) => entry.score !== Number.NEGATIVE_INFINITY)
      .sort((a, b) => b.score - a.score || a.item.title.localeCompare(b.item.title));
  }, [items, searchQuery]);

  return (
    <div className="group/collapsible">
      <button
        type="button"
        className="flex h-8 w-full items-center rounded-md px-2 py-2 text-sm gap-2"
        onClick={onToggleOpen}
      >
        <span className="font-medium">{label}</span>
        <IconChevronRight className={cn("ml-auto h-4 w-4 transition-transform", open && "rotate-90")} />
      </button>
      {open ? <div className="flex flex-col gap-1 py-1">
        {filteredItems.length > 0 ? (
          filteredItems.map(({ item, titleRanges }) => (
            <div key={item.id} className="group/item flex items-center w-full h-full gap-2 pl-4" title={item.title}>
              <div className="flex flex-col flex-1 min-w-0 cursor-pointer" onClick={() => onOpen(item)}>
                <span className="block text-sm truncate">{highlightText(item.title, titleRanges)}</span>
                <MethodAndEndpointTag item={item} />
              </div>
              <div className="relative pr-2" data-request-menu="">
                <button
                  type="button"
                  className="h-4 w-4 cursor-pointer opacity-50 hover:opacity-100 shrink-0"
                  onClick={(event) => {
                    event.stopPropagation();
                    onToggleMenu(item.id);
                  }}
                >
                  <IconMoreHorizontal className="h-4 w-4" />
                </button>
                {menuRequestID === item.id ? (
                  <div className="absolute right-0 top-5 z-20 min-w-[110px] rounded-md border border-border bg-popover text-popover-foreground shadow-lg">
                    <button
                      type="button"
                      className="block w-full px-3 py-2 text-left text-sm transition-colors hover:bg-accent hover:text-accent-foreground"
                      onClick={(event) => {
                        event.stopPropagation();
                        onEdit(item);
                        onToggleMenu(item.id);
                      }}
                    >
                      Edit
                    </button>
                    <button
                      type="button"
                      className="block w-full px-3 py-2 text-left text-sm text-red-500 transition-colors hover:bg-accent"
                      onClick={(event) => {
                        event.stopPropagation();
                        onDelete(item);
                        onToggleMenu(item.id);
                      }}
                    >
                      Delete
                    </button>
                  </div>
                ) : null}
              </div>
            </div>
          ))
        ) : items.length > 0 && searchQuery ? (
          <p className="pl-4 text-sm opacity-50">No matches</p>
        ) : (
          <p className="pl-4 text-sm opacity-50">No stored requests</p>
        )}
      </div> : null}
    </div>
  );
}

export function MethodAndEndpointTag({ item }: { item: StoredRequest }) {
  return (
    <div className="font-mono text-xs text-muted-foreground">
      <p className="space-x-2 block truncate">
        <span className="px-1 text-[10px] font-mono rounded border border-border">{item.data.method}</span>
        <span>
          {item.svcName}.{item.rpcName}
        </span>
      </p>
    </div>
  );
}

export function RequestLogs({
  lines,
}: {
  lines: Array<{ pid: string; created_at: string; stream: string; line: string }>;
}) {
  const rendered = lines.map((item) => formatLogLine(item.line)).filter(Boolean).join("\n");
  return (
    <div className={cn("w-full max-w-full min-w-0 mt-5", !rendered && "invisible")}>
      {rendered ? (
        <div className="mt-5 rounded-md bg-secondary p-3 min-w-0 max-w-full">
          <div className="mb-3 flex items-center space-x-3 text-sm">
            <span>Request logs</span>
          </div>
          <pre className="block max-h-96 overflow-auto rounded bg-background px-2 py-2 text-xs leading-5 whitespace-pre-wrap">
            {rendered}
          </pre>
        </div>
      ) : null}
    </div>
  );
}

export function lineMatchesField(line: string, field: string, expectedValue: string): boolean {
  const trimmed = line.trim();
  if (!trimmed) {
    return false;
  }
  try {
    const parsed = JSON.parse(trimmed) as Record<string, unknown>;
    return String(parsed[field] ?? "") === expectedValue;
  } catch {
    return trimmed.includes(expectedValue);
  }
}

export function baseName(value: string): string {
  const normalized = value.replaceAll("\\", "/");
  const index = normalized.lastIndexOf("/");
  return index >= 0 ? normalized.slice(index + 1) : normalized;
}

export function endpointHasPathParams(endpoint: ServiceRPC | null, fallbackPath?: string): boolean {
  if (endpoint?.path?.segments?.some((segment) => segment.type === "PARAM")) {
    return true;
  }
  return typeof fallbackPath === "string" && /:([^/]+)/.test(fallbackPath);
}

export function endpointHasRequestPayload(endpoint: ServiceRPC | null): boolean {
  return endpoint?.request_schema != null;
}

export function isPlaceholderPayload(value: string): boolean {
  const trimmed = value.trim();
  return trimmed === "" || trimmed === "{}" || trimmed === "null";
}

export function defaultRequestPayloadText(schema: unknown): string {
  const value = schemaExampleValue(schema);
  if (value === undefined) {
    return "{}";
  }
  return JSON.stringify(value, null, 2);
}

export function schemaExampleValue(schema: unknown): unknown {
  if (!schema || typeof schema !== "object") {
    return undefined;
  }
  const record = schema as Record<string, unknown>;
  if (typeof record.builtin === "string") {
    switch (record.builtin) {
      case "BOOL":
        return false;
      case "INT":
      case "INT8":
      case "INT16":
      case "INT32":
      case "INT64":
      case "UINT":
      case "UINT8":
      case "UINT16":
      case "UINT32":
      case "UINT64":
      case "FLOAT32":
      case "FLOAT64":
        return 0;
      case "STRING":
      case "BYTES":
      case "TIME":
      case "UUID":
      case "DECIMAL":
      case "USER_ID":
        return "";
      case "JSON":
      case "ANY":
      default:
        return {};
    }
  }
  if (record.pointer && typeof record.pointer === "object") {
    return schemaExampleValue((record.pointer as Record<string, unknown>).base);
  }
  if (record.list && typeof record.list === "object") {
    return [];
  }
  if (record.map && typeof record.map === "object") {
    return {};
  }
  if (record.struct && typeof record.struct === "object") {
    const fields = (record.struct as Record<string, unknown>).fields;
    if (!Array.isArray(fields)) {
      return {};
    }
    const result: Record<string, unknown> = {};
    for (const field of fields) {
      if (!field || typeof field !== "object") {
        continue;
      }
      const fieldRecord = field as Record<string, unknown>;
      const rawName =
        typeof fieldRecord.json_name === "string" && fieldRecord.json_name
          ? fieldRecord.json_name
          : typeof fieldRecord.name === "string"
            ? fieldRecord.name
            : "";
      if (!rawName || rawName === "-") {
        continue;
      }
      result[rawName] = schemaExampleValue(fieldRecord.typ);
    }
    return result;
  }
  return {};
}

export function renderResponseBody(body: string): string {
  const text = typeof body === "string" ? body.trim() : "";
  if (!text) {
    return "";
  }
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

export function formatLogLine(line: string): string {
  const trimmed = line.trim();
  if (!trimmed) {
    return "";
  }
  try {
    const parsed = JSON.parse(trimmed) as Record<string, unknown>;
    const message = typeof parsed.message === "string" ? parsed.message : "";
    const level = typeof parsed.level === "string" ? parsed.level.toUpperCase() : "";
    delete parsed.message;
    delete parsed.level;
    delete parsed.trace_id;
    delete parsed.x_correlation_id;
    if (message === "request completed") {
      delete parsed.duration;
      delete parsed.duration_ms;
    }
    const rest = Object.entries(parsed)
      .filter(([, value]) => value !== undefined && value !== null && value !== "")
      .map(([key, value]) => `${key}=${typeof value === "string" ? value : JSON.stringify(value)}`)
      .join(" ");
    return [level, message, rest].filter(Boolean).join(" ");
  } catch {
    return trimmed;
  }
}
