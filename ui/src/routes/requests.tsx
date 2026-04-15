import { Link } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  createStoredRequest,
  deleteStoredRequest,
  fetchStoredRequests,
  updateStoredRequest,
} from "../lib/graphql";
import { useDashboard } from "../lib/dashboard-context";
import {
  closeExplorerTab,
  ensureExplorerTabs,
  loadPersistedTabs,
  makeTabFromEndpoint,
  makeTabFromStoredRequest,
  normalizeActiveTab,
  persistTabs,
  reconcileTabsWithEndpoints,
  type RequestTab,
} from "../lib/api-explorer";
import {
  cn,
  formatDurationNanos,
  formatTime,
  materializePath,
  parseJSONInput,
  processOutputText,
  renderMetadataPath,
  tryParseJSON,
} from "../lib/utils";
import type {
  ApiCallResponse,
  APIEncodingRPC,
  EndpointOption,
  StoredRequest,
  StoredRequestInput,
  ServiceRPC,
} from "../lib/types";

const REQUESTS_SIDEBAR_STORAGE_KEY = "pulse:requests-sidebar-collapsed";
const REQUESTS_SIDEBAR_WIDTH = 280;

export function RequestsPage() {
  const { appId, apiEncoding, callAPI, meta, outputs, refreshAll, rpc, status, traces } = useDashboard();
  const [items, setItems] = useState<StoredRequest[]>([]);
  const [requestError, setRequestError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [tabs, setTabs] = useState<RequestTab[]>([]);
  const [activeTabID, setActiveTabID] = useState<string | null>(null);
  const [traceServiceFilter, setTraceServiceFilter] = useState("");
  const [traceEndpointFilter, setTraceEndpointFilter] = useState("");
  const [traceStatusFilter, setTraceStatusFilter] = useState<"all" | "ok" | "error">("all");
  const [traceIDFilter, setTraceIDFilter] = useState("");
  const [showEndpointPicker, setShowEndpointPicker] = useState(false);
  const [showStoreModal, setShowStoreModal] = useState(false);
  const [editingRequest, setEditingRequest] = useState<StoredRequest | null>(null);
  const [deletingRequest, setDeletingRequest] = useState<StoredRequest | null>(null);
  const [menuRequestID, setMenuRequestID] = useState<string | null>(null);
  const [folderOpen, setFolderOpen] = useState<Record<string, boolean>>({
    my: true,
    shared: true,
  });
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [requestSeq, setRequestSeq] = useState(1);

  const endpointOptions = useMemo<EndpointOption[]>(() => {
    const combined = new Map<string, EndpointOption>();
    for (const svc of apiEncoding?.services ?? []) {
      for (const rpc of svc.rpcs) {
        const key = `${svc.name}.${rpc.name}`;
        combined.set(key, {
          key,
          svcName: svc.name,
          rpcName: rpc.name,
          method: rpc.methods?.[0] || "GET",
          path: rpc.path || `/${svc.name}.${rpc.name}`,
          accessType: rpc.access_type,
        });
      }
    }
    for (const svc of meta?.svcs ?? []) {
      for (const rpc of svc.rpcs) {
        const key = `${svc.name}.${rpc.name}`;
        const current = combined.get(key);
        combined.set(key, {
          key,
          svcName: svc.name,
          rpcName: rpc.name,
          method: current?.method || rpc.http_methods?.[0] || "GET",
          path: renderMetadataPath(rpc.path) || current?.path || `/${svc.name}.${rpc.name}`,
          accessType: current?.accessType || rpc.access_type,
        });
      }
    }
    const all = Array.from(combined.values());
    return all
      .slice()
      .sort((a, b) => a.svcName.localeCompare(b.svcName) || a.rpcName.localeCompare(b.rpcName));
  }, [apiEncoding, meta?.svcs]);

  const endpointMap = useMemo(
    () => new Map(endpointOptions.map((item) => [item.key, item])),
    [endpointOptions],
  );
  const endpointMetaMap = useMemo(() => {
    const entries = new Map<string, ServiceRPC>();
    for (const svc of meta?.svcs ?? []) {
      for (const rpc of svc.rpcs) {
        entries.set(`${svc.name}.${rpc.name}`, rpc);
      }
    }
    return entries;
  }, [meta?.svcs]);

  useEffect(() => {
    const persisted = loadPersistedTabs(appId);
    setTabs(persisted.tabs);
    setActiveTabID(persisted.activeTabID);
  }, [appId]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const raw = window.localStorage.getItem(REQUESTS_SIDEBAR_STORAGE_KEY);
    setSidebarCollapsed(raw === "1");
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    window.localStorage.setItem(REQUESTS_SIDEBAR_STORAGE_KEY, sidebarCollapsed ? "1" : "0");
  }, [sidebarCollapsed]);

  useEffect(() => {
    if (endpointOptions.length === 0) {
      return;
    }
    setTabs((current) => {
      const nextTabs = ensureExplorerTabs(
        reconcileTabsWithEndpoints(current, endpointMap),
        endpointOptions,
      );
      setActiveTabID((currentActiveTabID) => normalizeActiveTab(currentActiveTabID, nextTabs));
      return nextTabs;
    });
  }, [endpointMap, endpointOptions]);

  useEffect(() => {
    if (tabs.length === 0) {
      return;
    }
    const nextActive = normalizeActiveTab(activeTabID, tabs);
    if (nextActive !== activeTabID) {
      setActiveTabID(nextActive);
      return;
    }
    persistTabs(appId, nextActive, tabs);
  }, [activeTabID, appId, tabs]);

  const refreshRequests = async () => {
    setLoading(true);
    try {
      const next = await fetchStoredRequests(appId);
      setItems(next);
      setRequestError(null);
    } catch (err) {
      setRequestError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refreshRequests();
  }, [appId]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setShowEndpointPicker(true);
        return;
      }
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "b") {
        event.preventDefault();
        setSidebarCollapsed((current) => !current);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as HTMLElement | null;
      if (!target?.closest("[data-request-menu]")) {
        setMenuRequestID(null);
      }
    };
    window.addEventListener("pointerdown", onPointerDown);
    return () => window.removeEventListener("pointerdown", onPointerDown);
  }, []);

  const myRequests = items.filter((item) => !item.shared);
  const sharedRequests = items.filter((item) => item.shared);
  const activeTab = tabs.find((tab) => tab.id === activeTabID) || null;
  const activeEndpoint = useMemo(() => {
    if (!activeTab) {
      return null;
    }
    return endpointMap.get(`${activeTab.svcName}.${activeTab.rpcName}`) ?? null;
  }, [activeTab, endpointMap]);
  const recentLogLines = useMemo(() => {
    const correlationID = activeTab?.correlationID?.trim();
    if (!correlationID) {
      return [];
    }
    const scoped = outputs.flatMap((item) =>
      processOutputText(item)
        .split("\n")
        .map((line) => line.trim())
        .filter(Boolean)
        .filter((line) => lineMatchesField(line, "x_correlation_id", correlationID))
        .map((line) => ({
          created_at: item.created_at,
          pid: item.pid,
          stream: item.stream,
          line,
        })),
    );
    return scoped.slice(-250);
  }, [activeTab?.correlationID, outputs]);
  const activeEndpointMeta = useMemo<ServiceRPC | null>(() => {
    if (!activeTab) {
      return null;
    }
    return endpointMetaMap.get(`${activeTab.svcName}.${activeTab.rpcName}`) ?? null;
  }, [activeTab, endpointMetaMap]);
  const activeHasPathParams = useMemo(
    () => endpointHasPathParams(activeEndpointMeta, activeEndpoint?.path),
    [activeEndpoint?.path, activeEndpointMeta],
  );
  const activeHasRequestPayload = useMemo(
    () => endpointHasRequestPayload(activeEndpointMeta),
    [activeEndpointMeta],
  );
  const activeDefaultPayloadText = useMemo(
    () => defaultRequestPayloadText(activeEndpointMeta?.request_schema),
    [activeEndpointMeta?.request_schema],
  );
  const endpointEditorTarget = useMemo(() => {
    const loc = activeEndpointMeta?.loc;
    const root = status?.appRoot;
    if (!loc?.filename || !root) {
      return null;
    }
    const parts = [root];
    if (loc.pkg_path) {
      parts.push(loc.pkg_path);
    }
    parts.push(loc.filename);
    return {
      file: parts.join("/"),
      line: loc.src_line_start ?? 0,
      col: loc.src_col_start ?? 0,
      label: `${baseName(loc.filename)}${loc.src_line_start ? `:${loc.src_line_start}` : ""}`,
    };
  }, [activeEndpointMeta?.loc, status?.appRoot]);
  const traceServices = useMemo(
    () => Array.from(new Set(traces.map((trace) => trace.service_name).filter(Boolean))).sort(),
    [traces],
  );
  const traceEndpoints = useMemo(
    () =>
      Array.from(
        new Set(
          traces
            .filter((trace) => !traceServiceFilter || trace.service_name === traceServiceFilter)
            .map((trace) => trace.endpoint_name)
            .filter((endpoint): endpoint is string => typeof endpoint === "string" && endpoint.length > 0),
        ),
      ).sort(),
    [traceServiceFilter, traces],
  );
  const filteredTraces = useMemo(
    () =>
      traces.filter((trace) => {
        if (traceServiceFilter && trace.service_name !== traceServiceFilter) {
          return false;
        }
        if (traceEndpointFilter && trace.endpoint_name !== traceEndpointFilter) {
          return false;
        }
        if (traceStatusFilter === "ok" && trace.is_error) {
          return false;
        }
        if (traceStatusFilter === "error" && !trace.is_error) {
          return false;
        }
        if (traceIDFilter && !trace.trace_id.includes(traceIDFilter.trim())) {
          return false;
        }
        return true;
      }),
    [traceEndpointFilter, traceIDFilter, traceServiceFilter, traceStatusFilter, traces],
  );
  const activeTrace = useMemo(
    () =>
      activeTab?.response?.trace_id
        ? traces.find((trace) => trace.trace_id === activeTab.response?.trace_id) ?? null
        : null,
    [activeTab?.response?.trace_id, traces],
  );

  useEffect(() => {
    if (!activeTab || !activeHasRequestPayload || !activeDefaultPayloadText) {
      return;
    }
    if (!isPlaceholderPayload(activeTab.payloadText)) {
      return;
    }
    updateTab(activeTab.id, { payloadText: activeDefaultPayloadText });
  }, [activeDefaultPayloadText, activeHasRequestPayload, activeTab]);

  function updateTab(tabID: string, patch: Partial<RequestTab>) {
    setTabs((current) => current.map((tab) => (tab.id === tabID ? { ...tab, ...patch } : tab)));
  }

  function openStoredRequest(item: StoredRequest) {
    const endpoint = endpointMap.get(`${item.svcName}.${item.rpcName}`);
    const next = makeTabFromStoredRequest(item, endpoint);
    setTabs((current) => [...current, next]);
    setActiveTabID(next.id);
  }

  function applyEndpointToTab(tabID: string, endpoint: EndpointOption) {
    const endpointMeta = endpointMetaMap.get(`${endpoint.svcName}.${endpoint.rpcName}`) ?? null;
    setTabs((current) =>
      current.map((tab) =>
        tab.id === tabID
          ? {
              ...tab,
              title: tab.storedRequestID ? tab.title : `${endpoint.svcName}.${endpoint.rpcName}`,
              svcName: endpoint.svcName,
              rpcName: endpoint.rpcName,
              method: endpoint.method,
              path: endpoint.path,
              pathParamsText: "[]",
              payloadText: defaultRequestPayloadText(endpointMeta?.request_schema),
              response: null,
              responseError: null,
            }
          : tab,
      ),
    );
  }

  async function removeStoredRequest(item: StoredRequest) {
    await deleteStoredRequest(appId, item.id);
    setTabs((current) =>
      current.map((tab) =>
        tab.storedRequestID === item.id ? { ...tab, storedRequestID: undefined, shared: false } : tab,
      ),
    );
    await refreshRequests();
  }

  async function persistStoredRequest(
    tab: RequestTab,
    params:
      | { mode: "new"; title: string; shared: boolean }
      | { mode: "update"; storedRequestID: string },
  ) {
    const input: StoredRequestInput = {
      title: params.mode === "new" ? params.title : tab.title,
      rpcName: tab.rpcName,
      svcName: tab.svcName,
      shared: params.mode === "new" ? params.shared : tab.shared,
      data: {
        method: tab.method,
        pathParams: parseJSONInput(tab.pathParamsText),
        payload: parseJSONInput(tab.payloadText),
      },
    };

    let storedRequestID = tab.storedRequestID;
    let nextTitle = tab.title;
    let nextShared = tab.shared;
    if (params.mode === "update") {
      await updateStoredRequest(appId, params.storedRequestID, input);
      storedRequestID = params.storedRequestID;
    } else {
      storedRequestID = await createStoredRequest(appId, input);
      nextTitle = params.title;
      nextShared = params.shared;
    }

    updateTab(tab.id, {
      storedRequestID,
      title: nextTitle,
      shared: nextShared,
      responseError: null,
    });
    await refreshRequests();
  }

  async function updateStoredRequestMeta(item: StoredRequest, patch: { title: string; shared: boolean }) {
    await updateStoredRequest(appId, item.id, {
      title: patch.title,
      rpcName: item.rpcName,
      svcName: item.svcName,
      shared: patch.shared,
      data: item.data,
    });
    setTabs((current) =>
      current.map((tab) =>
        tab.storedRequestID === item.id ? { ...tab, title: patch.title, shared: patch.shared } : tab,
      ),
    );
    await refreshRequests();
  }

  return (
    <section className="w-full h-[calc(100vh-(var(--header-height)))] grid grid-cols-3">
      <div className="col-span-2 overflow-hidden border-border border-r min-w-0">
        <div className="relative flex max-w-full" style={{ height: "calc(100vh - var(--header-height))" }}>
          <div
            aria-hidden="true"
            className="relative shrink-0 bg-transparent transition-[width] duration-200 ease-linear"
            style={{ width: sidebarCollapsed ? 0 : REQUESTS_SIDEBAR_WIDTH }}
          />
          <aside
            className={cn(
              "absolute inset-y-0 left-0 z-10 overflow-auto border-border border-r bg-sidebar transition-[left] duration-200 ease-linear",
            )}
            style={{
              width: REQUESTS_SIDEBAR_WIDTH,
              left: sidebarCollapsed ? -REQUESTS_SIDEBAR_WIDTH : 0,
            }}
          >
            <div className="px-2 pt-4 pb-2">
              <input
                className="h-8 w-full rounded-md border border-border px-3 text-sm shadow-none"
                placeholder="Search"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
            </div>
            {requestError ? (
              <div className="px-4 py-3 text-sm text-red-500">{requestError}</div>
            ) : null}
            {loading ? (
              <div className="flex w-full pt-6 flex-1 items-start justify-center">
                <div className="h-6 w-6 animate-spin rounded-full border-2 border-border border-t-foreground" />
              </div>
            ) : (
              <div className="px-2 pb-4 space-y-4">
                <RequestFolder
                  items={myRequests}
                  label="My requests"
                  menuRequestID={menuRequestID}
                  open={folderOpen.my ?? true}
                  onDelete={(item) => setDeletingRequest(item)}
                  onEdit={(item) => setEditingRequest(item)}
                  onOpen={openStoredRequest}
                  onToggleMenu={(itemID) =>
                    setMenuRequestID((current) => (current === itemID ? null : itemID))
                  }
                  onToggleOpen={() =>
                    setFolderOpen((current) => ({ ...current, my: !(current.my ?? true) }))
                  }
                  searchQuery={search}
                />
                <RequestFolder
                  items={sharedRequests}
                  label="Shared requests"
                  menuRequestID={menuRequestID}
                  open={folderOpen.shared ?? true}
                  onDelete={(item) => setDeletingRequest(item)}
                  onEdit={(item) => setEditingRequest(item)}
                  onOpen={openStoredRequest}
                  onToggleMenu={(itemID) =>
                    setMenuRequestID((current) => (current === itemID ? null : itemID))
                  }
                  onToggleOpen={() =>
                    setFolderOpen((current) => ({ ...current, shared: !(current.shared ?? true) }))
                  }
                  searchQuery={search}
                />
              </div>
            )}
          </aside>

          <div className="flex flex-col flex-1 w-full overflow-hidden">
            <header className="flex h-12 shrink-0 items-center gap-2 bg-sidebar border-b border-border transition-[width,height] ease-linear">
              <div className="flex items-center gap-2 px-4">
                <button
                  type="button"
                  className="-ml-1 inline-flex h-8 w-8 items-center justify-center rounded-md transition-colors hover:bg-accent hover:text-accent-foreground"
                  onClick={() => setSidebarCollapsed((current) => !current)}
                >
                  <IconPanelLeft className="h-4 w-4" />
                </button>
                <div className="mr-2 h-4 w-px bg-border" />
                <div className="text-sm">API Explorer</div>
              </div>
            </header>

            <div className="w-full bg-muted transition-colors ease-linear border-t-0">
              <TabStrip
                activeTabID={activeTabID}
                tabs={tabs}
                onActivate={setActiveTabID}
                onClose={(tabID) => {
                  setTabs((current) => {
                    const next = closeExplorerTab(current, activeTabID, tabID, endpointOptions);
                    setActiveTabID(next.activeTabID);
                    return next.tabs;
                  });
                }}
                onNew={() => {
                  if (endpointOptions.length === 0) {
                    return;
                  }
                  const next = makeTabFromEndpoint(endpointOptions[0]);
                  setTabs((current) => [...current, next]);
                  setActiveTabID(next.id);
                }}
              />
            </div>

            <div className="flex-1 min-w-0 flex flex-col overflow-auto">
              {activeTab ? (
                <div className="p-4 w-full min-w-0 max-w-full">
                  <div className="space-y-5">
                    <div>
                      <EndpointSelector
                        currentKey={`${activeTab.svcName}.${activeTab.rpcName}`}
                        endpoints={endpointOptions}
                        invalidEndpoint={!activeEndpoint}
                        open={showEndpointPicker}
                        onClose={() => setShowEndpointPicker(false)}
                        onOpen={() => setShowEndpointPicker(true)}
                        onSelect={(endpoint) => applyEndpointToTab(activeTab.id, endpoint)}
                      />
                      {endpointEditorTarget ? (
                        <SourceLinkButton
                          label={endpointEditorTarget.label}
                          onClick={() =>
                            void openEditor(
                              appId,
                              rpc,
                              endpointEditorTarget.file,
                              endpointEditorTarget.line,
                              endpointEditorTarget.col,
                            )
                          }
                        />
                      ) : null}
                    </div>

                    <CompactRequestEditor
                      disabled={!status?.running}
                      hasAuth={!!meta?.auth_handler}
                      hasPathParams={activeHasPathParams}
                      hasRequestPayload={activeHasRequestPayload}
                      requestTab={activeTab}
                      onCall={() => void callCurrentTab(activeTab)}
                      onOpenStoreModal={() => setShowStoreModal(true)}
                      onResetPath={() => {
                        const endpoint = endpointMap.get(`${activeTab.svcName}.${activeTab.rpcName}`);
                        if (!endpoint) {
                          return;
                        }
                        updateTab(activeTab.id, {
                          method: endpoint.method,
                          path: endpoint.path,
                          pathParamsText: "[]",
                        });
                      }}
                      onUpdate={(patch) => updateTab(activeTab.id, patch)}
                      onUpdatePathParams={(value) => {
                        updateTab(activeTab.id, { pathParamsText: value });
                        const endpoint = endpointMap.get(`${activeTab.svcName}.${activeTab.rpcName}`);
                        if (!endpoint) {
                          return;
                        }
                        try {
                          updateTab(activeTab.id, {
                            path: materializePath(endpoint.path, parseJSONInput(value)),
                          });
                        } catch {
                          // Leave current path as-is while editing incomplete JSON.
                        }
                      }}
                    />

                    {activeTab.responseError ? (
                      <div className="rounded-md border border-red-500/30 bg-red-500/10 px-4 py-3 text-sm text-red-500">
                        {activeTab.responseError}
                      </div>
                    ) : null}

                    {activeTab.response ? (
                      <ResponsePanel
                        appId={appId}
                        response={activeTab.response}
                        traceDuration={activeTrace ? formatDurationNanos(activeTrace.duration_nanos) : ""}
                      />
                    ) : null}

                    {recentLogLines.length > 0 ? <RequestLogs lines={recentLogLines} /> : null}
                  </div>
                </div>
              ) : null}
            </div>
          </div>
        </div>
      </div>

      <div className="col-span-1 overflow-auto">
        <div className="overflow-y-auto overflow-x-hidden" style={{ height: "calc(100vh - var(--header-height))" }}>
          <section>
            <div className="flex items-center justify-between pt-4 px-4 bg-sidebar pb-2">
              <div className="flex items-center gap-2 -mt-2">
                <IconActivity className="h-4 w-4" />
                <p className="text-sm">Traces</p>
              </div>
              <button
                type="button"
                className="rounded-md border border-border px-2 py-1 text-xs transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground disabled:opacity-50"
                disabled={traces.length === 0}
                onClick={() => void rpc?.request("traces/clear", { app_id: appId }).then(() => refreshAll())}
              >
                Clear traces
              </button>
            </div>
            <div className="pb-3 bg-sidebar px-4 pt-0 border-b border-border">
              <div className="flex flex-col gap-1.5 mb-3">
                <div className="flex items-start gap-2.5">
                  <div className="flex-1 flex flex-col gap-0.5">
                    <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Type</span>
                    <select className="h-9 w-full rounded-md border border-border px-3 text-sm" value="api-calls" disabled>
                      <option value="api-calls">API Calls</option>
                    </select>
                  </div>
                </div>
              </div>
              <div className="space-y-3 devdash-trace-filters">
                <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Service</div>
                <select
                  className="h-9 w-full rounded-md border border-border px-3 text-sm"
                  value={traceServiceFilter}
                  onChange={(event) => {
                    setTraceServiceFilter(event.target.value);
                    setTraceEndpointFilter("");
                  }}
                >
                  <option value="">All services</option>
                  {traceServices.map((service) => (
                    <option key={service} value={service}>
                      {service}
                    </option>
                  ))}
                </select>

                <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Endpoint</div>
                <select
                  className="h-9 w-full rounded-md border border-border px-3 text-sm"
                  value={traceEndpointFilter}
                  onChange={(event) => setTraceEndpointFilter(event.target.value)}
                >
                  <option value="">All endpoints</option>
                  {traceEndpoints.map((endpoint) => (
                    <option key={endpoint} value={endpoint}>
                      {endpoint}
                    </option>
                  ))}
                </select>

                <div className="inline-flex gap-0.5 p-1 rounded-lg bg-sidebar-accent/50 self-start">
                  <button
                    type="button"
                    onClick={() => setTraceStatusFilter("all")}
                    className={cn(
                      "px-3 py-1.5 text-sm rounded-md transition-colors",
                      traceStatusFilter === "all"
                        ? "bg-background text-foreground shadow-sm"
                        : "text-muted-foreground hover:text-foreground",
                    )}
                  >
                    All
                  </button>
                  <button
                    type="button"
                    onClick={() => setTraceStatusFilter("error")}
                    className={cn(
                      "px-3 py-1.5 text-sm rounded-md transition-colors",
                      traceStatusFilter === "error"
                        ? "bg-background text-foreground shadow-sm"
                        : "text-muted-foreground hover:text-foreground",
                    )}
                  >
                    Errors
                  </button>
                </div>

                <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Trace ID</div>
                <input
                  className="h-9 w-full rounded-md border border-border px-3 text-sm"
                  placeholder="Trace ID"
                  value={traceIDFilter}
                  onChange={(event) => setTraceIDFilter(event.target.value)}
                />
              </div>
            </div>
            <div className="px-4 py-3">
              {filteredTraces.slice(0, 50).length === 0 ? (
                <p className="text-sm text-muted-foreground">No traces match the current filters.</p>
              ) : (
                <div className="w-full">
                  {filteredTraces.slice(0, 50).map((trace) => (
                    <Link
                      key={`${trace.trace_id}/${trace.span_id}`}
                      to="/$appId/envs/local/traces/$traceId"
                      params={{ appId, traceId: trace.trace_id }}
                      className="group/traceRow block border-b border-border text-sm transition-colors hover:bg-accent/50"
                    >
                      <div className="relative flex h-12 items-center justify-between px-2 py-2">
                        <div className="min-w-0 flex items-center h-full space-x-2">
                          <figure
                            className={cn(
                              "h-3 w-3 rounded-full",
                              trace.is_error ? "bg-red-500" : "bg-success",
                            )}
                          />
                          <div className="text-sm min-w-0 shrink flex items-start flex-col justify-start">
                            <div className="text-sm min-w-0 flex items-center">
                              <div className="flex-none w-5">
                                <IconTraceRequest className="h-4 w-4 inline-block mr-2" />
                              </div>
                              <div className="shrink truncate">
                                {trace.service_name || "unknown service"}.{trace.endpoint_name || trace.type}
                              </div>
                            </div>
                            <div className="mt-1 text-xs text-muted-foreground font-mono truncate">
                              {trace.trace_id}
                            </div>
                          </div>
                        </div>
                        <div className="min-w-0 flex flex-col text-right text-xs mt-1 text-muted-foreground">
                          <span>{formatDurationNanos(trace.duration_nanos)}</span>
                          <span>{formatTime(trace.started_at)}</span>
                        </div>
                      </div>
                    </Link>
                  ))}
                </div>
              )}
            </div>
          </section>
        </div>
      </div>
      {activeTab ? (
        <StoreRequestModal
          items={items}
          open={showStoreModal}
          requestTab={activeTab}
          onClose={() => setShowStoreModal(false)}
          onSave={(params) => persistStoredRequest(activeTab, params)}
        />
      ) : null}
      <EditStoredRequestModal
        item={editingRequest}
        items={items}
        onClose={() => setEditingRequest(null)}
        onSave={(item, patch) => updateStoredRequestMeta(item, patch)}
      />
      <DeleteStoredRequestModal
        item={deletingRequest}
        onClose={() => setDeletingRequest(null)}
        onDelete={removeStoredRequest}
      />
    </section>
  );

  async function callCurrentTab(tab: RequestTab) {
    const correlationID = `dash-call-${requestSeq}`;
    setRequestSeq((current) => current + 1);
    updateTab(tab.id, { correlationID, response: null, responseError: null });
    try {
      const result = await callAPI({
        service: tab.svcName,
        endpoint: tab.rpcName,
        path: tab.path,
        method: tab.method,
        payload: tryParseJSON(tab.payloadText),
        authToken: tab.authToken,
        correlationID,
      });
      updateTab(tab.id, { correlationID, response: result, responseError: null });
    } catch (err) {
      updateTab(tab.id, {
        correlationID,
        response: null,
        responseError: err instanceof Error ? err.message : String(err),
      });
    }
  }
}

function CompactRequestEditor({
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
    <div className="overflow-visible">
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
          <button
            type="button"
            data-testid="call-api-button"
            className="inline-flex h-9 w-20 items-center justify-center rounded-md bg-foreground px-4 py-2 text-sm font-medium text-background transition-colors hover:bg-foreground/90 disabled:pointer-events-none disabled:opacity-50"
            disabled={disabled}
            onClick={onCall}
          >
            Call
          </button>
        </div>
      </div>
    </div>
  );
}

function EditorSection({
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

function AutoSizeTextarea(
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

function SourceLinkButton({ label, onClick }: { label: string; onClick: () => void }) {
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

function ResponsePanel({
  appId,
  response,
  traceDuration,
}: {
  appId: string;
  response: ApiCallResponse;
  traceDuration: string;
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
            {hasTrace ? (
              <Link
                to="/$appId/envs/local/traces/$traceId"
                params={{ appId, traceId: response.trace_id! }}
                className="flex items-center text-sm underline"
              >
                View trace
              </Link>
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

function TabStrip({
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

function RequestFolder({
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

function MethodAndEndpointTag({ item }: { item: StoredRequest }) {
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

function RequestLogs({
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

function lineMatchesField(line: string, field: string, expectedValue: string): boolean {
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

function baseName(value: string): string {
  const normalized = value.replaceAll("\\", "/");
  const index = normalized.lastIndexOf("/");
  return index >= 0 ? normalized.slice(index + 1) : normalized;
}

function endpointHasPathParams(endpoint: ServiceRPC | null, fallbackPath?: string): boolean {
  if (endpoint?.path?.segments?.some((segment) => segment.type === "PARAM")) {
    return true;
  }
  return typeof fallbackPath === "string" && /:([^/]+)/.test(fallbackPath);
}

function endpointHasRequestPayload(endpoint: ServiceRPC | null): boolean {
  return endpoint?.request_schema != null;
}

function isPlaceholderPayload(value: string): boolean {
  const trimmed = value.trim();
  return trimmed === "" || trimmed === "{}" || trimmed === "null";
}

function defaultRequestPayloadText(schema: unknown): string {
  const value = schemaExampleValue(schema);
  if (value === undefined) {
    return "{}";
  }
  return JSON.stringify(value, null, 2);
}

function schemaExampleValue(schema: unknown): unknown {
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

function renderResponseBody(body: string): string {
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

function formatLogLine(line: string): string {
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

type FuzzyMatch = {
  score: number;
  ranges: Array<[number, number]>;
};

function fuzzyMatch(text: string, query: string): FuzzyMatch | null {
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

function highlightText(text: string, ranges: Array<[number, number]>): React.ReactNode {
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

async function openEditor(
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

function EndpointSelector({
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
    <div className="w-full" data-endpoint-selector="">
      <button
        type="button"
        className="flex h-10 w-full items-center justify-between rounded-md border border-border bg-background px-3 text-left text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground disabled:cursor-not-allowed"
        disabled={endpoints.length === 0}
        onClick={() => (open ? onClose() : onOpen())}
      >
        <span className="truncate">{currentKey || "No endpoint available"}</span>
        <div className="ml-3 flex shrink-0 items-center gap-2">
          <span className="text-xs text-muted-foreground">{shortcut}</span>
          <IconChevronsUpDown className="h-4 w-4 shrink-0 opacity-50" />
        </div>
      </button>
      {open ? (
        <div className="mt-2 w-full overflow-hidden rounded-md border border-border bg-popover text-popover-foreground shadow-lg">
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
              <div>
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
              href="https://pulse.dev/docs/ts/primitives/defining-apis"
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

function StoreRequestModal({
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

function EditStoredRequestModal({
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

function DeleteStoredRequestModal({
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

function SimpleModal({
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

function MethodAndEndpointPill({ method, label }: { method: string; label: string }) {
  return (
    <div className="font-mono text-xs text-muted-foreground">
      <p className="space-x-2 truncate">
        <span className="rounded border border-border px-1 text-[10px]">{method}</span>
        <span>{label}</span>
      </p>
    </div>
  );
}

function IconPanelLeft({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M9 4v16" />
    </svg>
  );
}

function IconChevronRight({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="m9 18 6-6-6-6" />
    </svg>
  );
}

function IconChevronsUpDown({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="m7 15 5 5 5-5" />
      <path d="m7 9 5-5 5 5" />
    </svg>
  );
}

function IconCheck({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="M20 6 9 17l-5-5" />
    </svg>
  );
}

function IconMoreHorizontal({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" className={className} aria-hidden="true">
      <circle cx="12" cy="12" r="1.5" />
      <circle cx="19" cy="12" r="1.5" />
      <circle cx="5" cy="12" r="1.5" />
    </svg>
  );
}

function IconPlus({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="M5 12h14" />
      <path d="M12 5v14" />
    </svg>
  );
}

function IconX({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="M18 6 6 18" />
      <path d="m6 6 12 12" />
    </svg>
  );
}

function IconInfo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 20 20" fill="currentColor" className={className} aria-hidden="true">
      <path fillRule="evenodd" d="M18 10A8 8 0 1 1 2 10a8 8 0 0 1 16 0Zm-7-4a1 1 0 1 1-2 0 1 1 0 0 1 2 0ZM9 9a1 1 0 0 0 0 2v3a1 1 0 0 0 1 1h1a1 1 0 1 0 0-2v-3a1 1 0 0 0-1-1H9Z" clipRule="evenodd" />
    </svg>
  );
}

function IconRefresh({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 256 256" fill="currentColor" className={className} aria-hidden="true">
      <path d="M228,128a100,100,0,0,1-98.66,100H128a99.39,99.39,0,0,1-68.62-27.29,12,12,0,0,1,16.48-17.45,76,76,0,1,0-1.57-109c-.13.13-.25.25-.39.37L54.89,92H72a12,12,0,0,1,0,24H24a12,12,0,0,1-12-12V56a12,12,0,0,1,24,0V76.72L57.48,57.06A100,100,0,0,1,228,128Z" />
    </svg>
  );
}

function IconSave({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="M15.2 3a2 2 0 0 1 1.4.6l3.8 3.8a2 2 0 0 1 .6 1.4V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z" />
      <path d="M17 21v-7a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v7" />
      <path d="M7 3v4a1 1 0 0 0 1 1h7" />
    </svg>
  );
}

function IconCopy({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <rect x="8" y="8" width="14" height="14" rx="2" ry="2" />
      <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
    </svg>
  );
}

function IconSource({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={className} aria-hidden="true">
      <path d="m9 18-6-6 6-6" />
      <path d="m15 6 6 6-6 6" />
    </svg>
  );
}

function IconActivity({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true">
      <path d="M22 12h-4l-3 9L9 3l-3 9H2" />
    </svg>
  );
}

function IconTraceRequest({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true">
      <path d="M17 8l4 4-4 4" />
      <path d="M3 12h18" />
    </svg>
  );
}
