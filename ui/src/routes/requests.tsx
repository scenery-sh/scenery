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
import { requestTracesURL, temporalTracesURL } from "../lib/grafana";
import {
  cn,
  formatDurationNanos,
  materializePath,
  parseJSONInput,
  processOutputText,
  renderMetadataPath,
  tryParseJSON,
} from "../lib/utils";
import {
  CompactRequestEditor,
  RequestFolder,
  RequestLogs,
  ResponsePanel,
  SourceLinkButton,
  TabStrip,
  baseName,
  defaultRequestPayloadText,
  endpointHasPathParams,
  endpointHasRequestPayload,
  isPlaceholderPayload,
  lineMatchesField,
  renderResponseBody,
} from "./requests-editor";
import {
  DeleteStoredRequestModal,
  EditStoredRequestModal,
  EndpointSelector,
  IconPanelLeft,
  StoreRequestModal,
  openEditor,
} from "./requests-modals";
import type {
  ApiCallResponse,
  APIEncodingRPC,
  EndpointOption,
  StoredRequest,
  StoredRequestInput,
  ServiceRPC,
} from "../lib/types";

const REQUESTS_SIDEBAR_STORAGE_KEY = "onlava:requests-sidebar-collapsed";
const REQUESTS_SIDEBAR_WIDTH = 280;

export function RequestsPage() {
  const { appId, apiEncoding, callAPI, meta, outputs, rpc, status, traces } = useDashboard();
  const [items, setItems] = useState<StoredRequest[]>([]);
  const [requestError, setRequestError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [tabs, setTabs] = useState<RequestTab[]>([]);
  const [activeTabID, setActiveTabID] = useState<string | null>(null);
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
  const requestTraceURL = requestTracesURL(status?.grafana);
  const temporalTraceURL = temporalTracesURL(status?.grafana);
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
                        response={activeTab.response}
                        traceDuration={activeTrace ? formatDurationNanos(activeTrace.duration_nanos) : ""}
                        traceURL={requestTraceURL}
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

      <div className="col-span-1 overflow-auto bg-sidebar">
        <div className="overflow-y-auto overflow-x-hidden" style={{ height: "calc(100vh - var(--header-height))" }}>
          <section className="px-4 py-4">
            <div>
              <p className="text-sm font-medium">Grafana</p>
              <p className="mt-1 text-xs text-muted-foreground">
                Request and Temporal traces now live in Grafana for this dev session.
              </p>
            </div>
            <div className="mt-4 flex flex-wrap gap-2">
              <GrafanaPanelLink href={requestTraceURL} label="Request traces" primary />
              <GrafanaPanelLink href={temporalTraceURL} label="Temporal traces" />
            </div>

            {activeTab?.response?.trace_id ? (
              <div className="mt-6 rounded-md border border-border bg-background/30 p-4">
                <div className="text-xs uppercase tracking-wide text-muted-foreground">Last response trace</div>
                <div className="mt-2 break-all font-mono text-xs">{activeTab.response.trace_id}</div>
                <div className="mt-3 flex items-center justify-between gap-3 text-xs text-muted-foreground">
                  <span>{activeTrace ? formatDurationNanos(activeTrace.duration_nanos) : "duration pending"}</span>
                  {requestTraceURL ? (
                    <a href={requestTraceURL} target="_blank" rel="noreferrer" className="underline">
                      Open in Grafana
                    </a>
                  ) : null}
                </div>
              </div>
            ) : null}

            {!status?.grafana?.available ? (
              <p className="mt-6 text-sm text-muted-foreground">
                Grafana is {status?.grafana?.status || "unavailable"}.
              </p>
            ) : null}
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

function GrafanaPanelLink({
  href,
  label,
  primary = false,
}: {
  href: string;
  label: string;
  primary?: boolean;
}) {
  const disabled = !href;
  return (
    <a
      href={href || "#"}
      target="_blank"
      rel="noreferrer"
      onClick={(event) => {
        if (disabled) {
          event.preventDefault();
        }
      }}
      className={cn(
        "rounded-md border px-3 py-2 text-sm transition-colors",
        primary
          ? "border-primary bg-primary text-primary-foreground hover:bg-primary/90"
          : "border-border hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
        disabled && "pointer-events-none opacity-50",
      )}
    >
      {label}
    </a>
  );
}
