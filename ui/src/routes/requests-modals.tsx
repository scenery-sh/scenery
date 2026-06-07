import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/primitives/Button";
import { useDashboard } from "../lib/dashboard-context";
import { cn } from "../lib/utils";
import type { EndpointOption, ServiceRPC, StoredRequest, StoredRequestInput } from "../lib/types";
import type { RequestTab } from "../lib/api-explorer";

export type FuzzyMatch = {
  score: number;
  ranges: Array<[number, number]>;
};

export function fuzzyMatch(text: string, query: string): FuzzyMatch | null {
  const haystack = text.toLowerCase();
  const needle = query.trim().toLowerCase();
  if (!needle) {
    return { score: 0, ranges: [] };
  }
  let lastIndex = -1;
  const positions: number[] = [];
  for (const char of needle) {
    const nextIndex = haystack.indexOf(char, lastIndex + 1);
    if (nextIndex === -1) {
      return null;
    }
    positions.push(nextIndex);
    lastIndex = nextIndex;
  }
  let score = 100 - positions[0];
  for (let index = 1; index < positions.length; index += 1) {
    if (positions[index] === positions[index - 1] + 1) {
      score += 8;
    } else {
      score -= positions[index] - positions[index - 1] - 1;
    }
  }
  const ranges: Array<[number, number]> = [];
  for (const pos of positions) {
    const last = ranges[ranges.length - 1];
    if (last && last[1] === pos) {
      last[1] = pos + 1;
    } else {
      ranges.push([pos, pos + 1]);
    }
  }
  return { score, ranges };
}

export function highlightText(text: string, ranges: Array<[number, number]>): React.ReactNode {
  if (ranges.length === 0) {
    return text;
  }
  const nodes: React.ReactNode[] = [];
  let cursor = 0;
  ranges.forEach(([start, end], index) => {
    if (cursor < start) {
      nodes.push(text.slice(cursor, start));
    }
    nodes.push(<b key={`${start}-${end}-${index}`}>{text.slice(start, end)}</b>);
    cursor = end;
  });
  if (cursor < text.length) {
    nodes.push(text.slice(cursor));
  }
  return (
    <>{nodes}</>
  );
}

export async function openEditor(
  appId: string,
  rpc: ReturnType<typeof useDashboard>["rpc"],
  file: string,
  line: number,
  col: number,
) {
  if (!rpc) {
    return;
  }
  await rpc.request("editors/open", {
    app_id: appId,
    file,
    start_line: line,
    start_col: col,
  });
}

export function EndpointSelector({
  currentKey,
  endpoints,
  invalidEndpoint,
  open,
  onClose,
  onOpen,
  onSelect,
}: {
  currentKey: string;
  endpoints: EndpointOption[];
  invalidEndpoint: boolean;
  open: boolean;
  onClose: () => void;
  onOpen: () => void;
  onSelect: (endpoint: EndpointOption) => void;
}) {
  const [query, setQuery] = useState("");
  const shortcut = useMemo(
    () => (typeof navigator !== "undefined" && /Mac/.test(navigator.platform) ? "⌘K" : "Ctrl+K"),
    [],
  );

  useEffect(() => {
    if (!open) {
      setQuery("");
    }
  }, [open]);

  useEffect(() => {
    if (!open) {
      return;
    }
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as HTMLElement | null;
      if (!target?.closest("[data-endpoint-selector]")) {
        onClose();
      }
    };
    window.addEventListener("pointerdown", onPointerDown);
    return () => window.removeEventListener("pointerdown", onPointerDown);
  }, [onClose, open]);

  const filtered = useMemo(() => {
    const needle = query.trim();
    if (!needle) {
      return endpoints;
    }
    return endpoints
      .map((endpoint) => {
        const best =
          [
            fuzzyMatch(endpoint.key, needle),
            fuzzyMatch(endpoint.method, needle),
            fuzzyMatch(endpoint.path, needle),
          ]
            .filter((match): match is FuzzyMatch => match !== null)
            .sort((a, b) => b.score - a.score)[0] ?? null;
        return { endpoint, score: best?.score ?? Number.NEGATIVE_INFINITY };
      })
      .filter((entry) => entry.score !== Number.NEGATIVE_INFINITY)
      .sort((a, b) => b.score - a.score || a.endpoint.key.localeCompare(b.endpoint.key))
      .map((entry) => entry.endpoint);
  }, [endpoints, query]);

  return (
    <div className="w-full" data-endpoint-selector="" data-onlava-ui="APIExplorerEndpointSelector">
      <Button
        tone="secondary"
        size="lg"
        data-onlava-ui="APIExplorerEndpointSelectorButton"
        className="w-full justify-between px-3 text-left"
        disabled={endpoints.length === 0}
        onClick={() => (open ? onClose() : onOpen())}
      >
        <span className="truncate">{currentKey || "No endpoint available"}</span>
        <div className="ml-3 flex shrink-0 items-center gap-2">
          <span className="text-xs text-muted-foreground">{shortcut}</span>
          <IconChevronsUpDown className="h-4 w-4 shrink-0 opacity-50" />
        </div>
      </Button>
      {open ? (
        <div
          data-onlava-ui="APIExplorerEndpointSelectorMenu"
          className="mt-2 w-full overflow-hidden rounded-md border border-border bg-popover text-popover-foreground shadow-lg"
        >
          <input
            autoFocus
            className="h-11 w-full border-b border-border bg-transparent px-4 text-sm outline-none"
            placeholder="Search endpoint..."
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          <div className="max-h-[320px] overflow-auto">
            {filtered.length === 0 ? (
              <div className="px-4 py-6 text-sm text-muted-foreground">No endpoint found.</div>
            ) : (
              <div data-onlava-ui="APIExplorerEndpointSelectorOptions">
                {filtered.map((endpoint) => {
                  const selected = endpoint.key === currentKey;
                  return (
                    <button
                      key={endpoint.key}
                      type="button"
                      className={cn(
                        "flex w-full items-center justify-between gap-4 px-4 py-3 text-left transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                        selected && "bg-sidebar-accent/70",
                      )}
                      onClick={() => {
                        onSelect(endpoint);
                        onClose();
                      }}
                    >
                      <div className="min-w-0 flex items-start gap-3">
                        <IconCheck className={cn("mt-0.5 h-4 w-4", selected ? "opacity-100" : "opacity-0")} />
                        <div className="min-w-0">
                          <div className="truncate text-sm font-medium">{endpoint.key}</div>
                          <div className="mt-1 truncate font-mono text-xs text-muted-foreground">{endpoint.path}</div>
                        </div>
                      </div>
                      <span className="shrink-0 rounded border border-border px-1.5 py-0.5 font-mono text-[10px]">
                        {endpoint.method}
                      </span>
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      ) : null}
      {invalidEndpoint ? <p className="mt-1 text-sm font-medium text-red-500">Selected endpoint no longer exists</p> : null}
      {endpoints.length === 0 ? (
        <div className="mt-1 text-sm text-muted-foreground">
          <p>
            Define an endpoint to view it in the API Explorer.{" "}
            <a
              className="underline"
              href="https://onlava.com/docs/ts/primitives/defining-apis"
              rel="noreferrer"
              target="_blank"
            >
              Learn more
            </a>
          </p>
        </div>
      ) : null}
    </div>
  );
}

export function StoreRequestModal({
  items,
  open,
  requestTab,
  onClose,
  onSave,
}: {
  items: StoredRequest[];
  open: boolean;
  requestTab: RequestTab;
  onClose: () => void;
  onSave: (params: { mode: "new"; title: string; shared: boolean } | { mode: "update"; storedRequestID: string }) => Promise<void>;
}) {
  const [mode, setMode] = useState<"update" | "new">("new");
  const [selectedID, setSelectedID] = useState("");
  const [title, setTitle] = useState("");
  const [shared, setShared] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    const current = requestTab.storedRequestID ? items.find((item) => item.id === requestTab.storedRequestID) : null;
    setMode(current ? "update" : "new");
    setSelectedID(current?.id || "");
    setTitle(current?.title || requestTab.title || `${requestTab.svcName}.${requestTab.rpcName}`);
    setShared(current?.shared || false);
    setBusy(false);
    setError(null);
  }, [items, open, requestTab]);

  const hasRequests = items.length > 0;
  const duplicateTitle = useMemo(
    () => items.some((item) => item.title === title && item.id !== requestTab.storedRequestID),
    [items, requestTab.storedRequestID, title],
  );

  return (
    <SimpleModal open={open} title="Store request" onClose={onClose}>
      <div className="space-y-6">
        <MethodAndEndpointPill method={requestTab.method} label={`${requestTab.svcName}.${requestTab.rpcName}`} />
        <div className="space-y-4">
          <label className="flex items-start gap-3 text-sm">
            <input
              checked={mode === "update"}
              className="mt-1"
              disabled={!hasRequests}
              name="store-mode"
              type="radio"
              onChange={() => setMode("update")}
            />
            <span className="flex-1">
              <span className="block font-medium">Update stored request</span>
              {mode === "update" ? (
                <select
                  className="mt-3 h-10 w-full rounded-md border border-border px-3 text-sm"
                  value={selectedID}
                  onChange={(event) => {
                    const nextID = event.target.value;
                    setSelectedID(nextID);
                    const next = items.find((item) => item.id === nextID);
                    if (next) {
                      setTitle(next.title);
                      setShared(next.shared);
                    }
                  }}
                >
                  <option value="" disabled>
                    Select a request
                  </option>
                  {items.filter((item) => !item.shared).length > 0 ? (
                    <optgroup label="My requests">
                      {items.filter((item) => !item.shared).map((item) => (
                        <option key={item.id} value={item.id}>
                          {item.title}
                        </option>
                      ))}
                    </optgroup>
                  ) : null}
                  {items.filter((item) => item.shared).length > 0 ? (
                    <optgroup label="Shared requests">
                      {items.filter((item) => item.shared).map((item) => (
                        <option key={item.id} value={item.id}>
                          {item.title}
                        </option>
                      ))}
                    </optgroup>
                  ) : null}
                </select>
              ) : null}
            </span>
          </label>
          <label className="flex items-start gap-3 text-sm">
            <input
              checked={mode === "new"}
              className="mt-1"
              name="store-mode"
              type="radio"
              onChange={() => setMode("new")}
            />
            <span className="flex-1">
              <span className="block font-medium">Save a new request</span>
              {mode === "new" ? (
                <div className="mt-3 space-y-4">
                  <div className="space-y-2">
                    <label className="block text-sm font-medium">Request name</label>
                    <input
                      className="h-10 w-full rounded-md border border-border px-3 text-sm"
                      value={title}
                      onChange={(event) => setTitle(event.target.value)}
                    />
                    {duplicateTitle ? (
                      <p className="text-xs text-red-500">Request name already in use</p>
                    ) : null}
                  </div>
                  <label className="flex items-start gap-3 text-sm">
                    <input
                      checked={shared}
                      className="mt-1"
                      type="checkbox"
                      onChange={(event) => setShared(event.target.checked)}
                    />
                    <span>
                      <span className="block font-medium">Shared request</span>
                      <span className="block text-xs text-muted-foreground">Share this request with your team.</span>
                    </span>
                  </label>
                </div>
              ) : null}
            </span>
          </label>
        </div>
        <p className="text-xs text-muted-foreground">
          Authentication data is not stored as part of this request and is not shared with others.
        </p>
        <div className="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse">
          <span className="flex w-full sm:ml-3 sm:w-auto">
            <button
              type="button"
              className="w-full rounded-md border border-border bg-foreground px-3 py-2 text-sm text-background transition-opacity disabled:cursor-not-allowed disabled:opacity-50"
              disabled={busy || (mode === "update" ? !selectedID : !title.trim() || duplicateTitle)}
              onClick={async () => {
                try {
                  setBusy(true);
                  setError(null);
                  if (mode === "update") {
                    await onSave({ mode: "update", storedRequestID: selectedID });
                  } else {
                    await onSave({ mode: "new", title: title.trim(), shared });
                  }
                  onClose();
                } catch (err) {
                  setError(err instanceof Error ? err.message : String(err));
                } finally {
                  setBusy(false);
                }
              }}
            >
              {busy ? "Saving..." : "Save"}
            </button>
          </span>
          <span className="mt-3 flex w-full rounded-md shadow-sm sm:mt-0 sm:w-auto">
            <button
              type="button"
              className="w-full rounded-md border border-border px-3 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
              onClick={onClose}
            >
              Cancel
            </button>
          </span>
        </div>
        {error ? (
          <div className="mt-2 text-right text-sm text-red-500">
            <p>An error occurred. Try again. Code: {error}</p>
          </div>
        ) : null}
      </div>
    </SimpleModal>
  );
}

export function EditStoredRequestModal({
  item,
  items,
  onClose,
  onSave,
}: {
  item: StoredRequest | null;
  items: StoredRequest[];
  onClose: () => void;
  onSave: (item: StoredRequest, patch: { title: string; shared: boolean }) => Promise<void>;
}) {
  const [title, setTitle] = useState("");
  const [shared, setShared] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setTitle(item?.title || "");
    setShared(item?.shared || false);
    setBusy(false);
    setError(null);
  }, [item]);

  const duplicateTitle = useMemo(
    () => items.some((entry) => entry.id !== item?.id && entry.title === title),
    [item?.id, items, title],
  );

  return (
    <SimpleModal open={Boolean(item)} title="Edit stored request" onClose={onClose}>
      {item ? (
        <div className="space-y-6">
          <MethodAndEndpointPill method={item.data.method} label={`${item.svcName}.${item.rpcName}`} />
          <div className="space-y-4">
            <div className="space-y-2">
              <label className="block text-sm font-medium">Request name</label>
              <input
                className="h-10 w-full rounded-md border border-border px-3 text-sm"
                value={title}
                onChange={(event) => setTitle(event.target.value)}
              />
              {duplicateTitle ? <p className="text-xs text-red-500">Request name already in use</p> : null}
            </div>
            <label className="flex items-start gap-3 text-sm">
              <input
                checked={shared}
                className="mt-1"
                type="checkbox"
                onChange={(event) => setShared(event.target.checked)}
              />
              <span>
                <span className="block font-medium">Shared request</span>
                <span className="block text-xs text-muted-foreground">Share this request with your team.</span>
              </span>
            </label>
          </div>
          <p className="text-xs text-muted-foreground">
            Authentication data is not stored as part of this request and is not shared with others.
          </p>
          <div className="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse">
            <span className="flex w-full sm:ml-3 sm:w-auto">
              <button
                type="button"
                className="w-full rounded-md border border-border bg-foreground px-3 py-2 text-sm text-background transition-opacity disabled:cursor-not-allowed disabled:opacity-50"
                disabled={busy || !title.trim() || duplicateTitle}
                onClick={async () => {
                  try {
                    setBusy(true);
                    setError(null);
                    await onSave(item, { title: title.trim(), shared });
                    onClose();
                  } catch (err) {
                    setError(err instanceof Error ? err.message : String(err));
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                {busy ? "Saving..." : "Save"}
              </button>
            </span>
            <span className="mt-3 flex w-full rounded-md shadow-sm sm:mt-0 sm:w-auto">
              <button
                type="button"
                className="w-full rounded-md border border-border px-3 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                onClick={onClose}
              >
                Cancel
              </button>
            </span>
          </div>
          {error ? (
            <div className="mt-2 text-right text-sm text-red-500">
              <p>An error occurred. Try again. Code: {error}</p>
            </div>
          ) : null}
        </div>
      ) : null}
    </SimpleModal>
  );
}

export function DeleteStoredRequestModal({
  item,
  onClose,
  onDelete,
}: {
  item: StoredRequest | null;
  onClose: () => void;
  onDelete: (item: StoredRequest) => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setBusy(false);
    setError(null);
  }, [item]);

  return (
    <SimpleModal
      open={Boolean(item)}
      title="Delete request"
      onClose={onClose}
      widthClassName="max-w-none w-[calc(100vw-32px)]"
    >
      {item ? (
        <div className="space-y-6">
          <p className="text-sm text-muted-foreground">
            Are you sure you want to delete <span className="font-semibold text-foreground">{item.title}</span>?
            {item.shared ? (
              <span className="mt-1 block">Deleting a shared request removes it for everyone.</span>
            ) : null}
          </p>
          <div className="mt-5 sm:mt-4 sm:flex sm:flex-row-reverse w-full">
            <span className="flex w-full sm:ml-3 sm:w-auto">
              <button
                type="button"
                className="w-full rounded-md border border-border px-3 py-2 text-sm transition-opacity disabled:cursor-not-allowed disabled:opacity-50"
                disabled={busy}
                onClick={async () => {
                  try {
                    setBusy(true);
                    setError(null);
                    await onDelete(item);
                    onClose();
                  } catch (err) {
                    setError(err instanceof Error ? err.message : String(err));
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                {busy ? "Deleting..." : "Delete"}
              </button>
            </span>
            <span className="mt-3 flex w-full rounded-md shadow-sm sm:mt-0 sm:w-auto">
              <button
                type="button"
                className="w-full rounded-md border border-border px-3 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                onClick={onClose}
              >
                Cancel
              </button>
            </span>
          </div>
          {error ? (
            <div className="mt-2 text-right text-sm text-red-500">
              <p>An error occurred. Try again. Code: {error}</p>
            </div>
          ) : null}
        </div>
      ) : null}
    </SimpleModal>
  );
}

export function SimpleModal({
  children,
  open,
  title,
  onClose,
  widthClassName = "max-w-[550px]",
}: {
  children: React.ReactNode;
  open: boolean;
  title: string;
  onClose: () => void;
  widthClassName?: string;
}) {
  useEffect(() => {
    if (!open) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose, open]);

  if (!open) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/50 px-4" onClick={onClose}>
      <div
        className={cn("w-full rounded-xl border border-border bg-background p-6 shadow-2xl", widthClassName)}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="mb-6 flex items-center justify-between gap-4">
          <h2 className="text-lg font-medium">{title}</h2>
          <button
            type="button"
            className="flex h-8 w-8 items-center justify-center rounded-full transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
            onClick={onClose}
          >
            ×
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

export function MethodAndEndpointPill({ method, label }: { method: string; label: string }) {
  return (
    <div className="font-mono text-xs text-muted-foreground">
      <p className="space-x-2 truncate">
        <span className="rounded border border-border px-1 text-[10px]">{method}</span>
        <span>{label}</span>
      </p>
    </div>
  );
}

export function IconPanelLeft({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M9 4v16" />
    </svg>
  );
}

export function IconChevronRight({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="m9 18 6-6-6-6" />
    </svg>
  );
}

export function IconChevronsUpDown({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="m7 15 5 5 5-5" />
      <path d="m7 9 5-5 5 5" />
    </svg>
  );
}

export function IconCheck({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="M20 6 9 17l-5-5" />
    </svg>
  );
}

export function IconMoreHorizontal({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" className={className} aria-hidden="true">
      <circle cx="12" cy="12" r="1.5" />
      <circle cx="19" cy="12" r="1.5" />
      <circle cx="5" cy="12" r="1.5" />
    </svg>
  );
}

export function IconPlus({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="M5 12h14" />
      <path d="M12 5v14" />
    </svg>
  );
}

export function IconX({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="M18 6 6 18" />
      <path d="m6 6 12 12" />
    </svg>
  );
}

export function IconInfo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 20 20" fill="currentColor" className={className} aria-hidden="true">
      <path fillRule="evenodd" d="M18 10A8 8 0 1 1 2 10a8 8 0 0 1 16 0Zm-7-4a1 1 0 1 1-2 0 1 1 0 0 1 2 0ZM9 9a1 1 0 0 0 0 2v3a1 1 0 0 0 1 1h1a1 1 0 1 0 0-2v-3a1 1 0 0 0-1-1H9Z" clipRule="evenodd" />
    </svg>
  );
}

export function IconRefresh({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 256 256" fill="currentColor" className={className} aria-hidden="true">
      <path d="M228,128a100,100,0,0,1-98.66,100H128a99.39,99.39,0,0,1-68.62-27.29,12,12,0,0,1,16.48-17.45,76,76,0,1,0-1.57-109c-.13.13-.25.25-.39.37L54.89,92H72a12,12,0,0,1,0,24H24a12,12,0,0,1-12-12V56a12,12,0,0,1,24,0V76.72L57.48,57.06A100,100,0,0,1,228,128Z" />
    </svg>
  );
}

export function IconSave({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="M15.2 3a2 2 0 0 1 1.4.6l3.8 3.8a2 2 0 0 1 .6 1.4V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z" />
      <path d="M17 21v-7a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v7" />
      <path d="M7 3v4a1 1 0 0 0 1 1h7" />
    </svg>
  );
}

export function IconCopy({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <rect x="8" y="8" width="14" height="14" rx="2" ry="2" />
      <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
    </svg>
  );
}

export function IconSource({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="m9 18-6-6 6-6" />
      <path d="m15 6 6 6-6 6" />
    </svg>
  );
}

export function IconActivity({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true">
      <path d="M22 12h-4l-3 9L9 3l-3 9H2" />
    </svg>
  );
}

export function IconTraceRequest({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true">
      <path d="M17 8l4 4-4 4" />
      <path d="M3 12h18" />
    </svg>
  );
}
