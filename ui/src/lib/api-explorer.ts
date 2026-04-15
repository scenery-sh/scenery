import type { ApiCallResponse, EndpointOption, StoredRequest } from "./types";
import { materializePath, parseJSONInput } from "./utils";

export interface RequestTab {
  id: string;
  title: string;
  svcName: string;
  rpcName: string;
  method: string;
  path: string;
  authToken: string;
  correlationID?: string;
  storedRequestID?: string;
  shared: boolean;
  pathParamsText: string;
  payloadText: string;
  response?: ApiCallResponse | null;
  responseError?: string | null;
}

export interface PersistedTabsState {
  activeTabID: string | null;
  tabs: RequestTab[];
}

function defaultTabID(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `tab-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export function makeTabFromEndpoint(
  endpoint: EndpointOption,
  title?: string,
  makeID: () => string = defaultTabID,
): RequestTab {
  return {
    id: makeID(),
    title: title || `${endpoint.svcName}.${endpoint.rpcName}`,
    svcName: endpoint.svcName,
    rpcName: endpoint.rpcName,
    method: endpoint.method,
    path: endpoint.path,
    authToken: "",
    shared: false,
    pathParamsText: "[]",
    payloadText: "{}",
    response: null,
    responseError: null,
  };
}

export function makeTabFromStoredRequest(
  request: StoredRequest,
  endpoint?: EndpointOption,
  makeID: () => string = defaultTabID,
): RequestTab {
  const pathParamsText = JSON.stringify(request.data.pathParams ?? {}, null, 2);
  const resolvedPath = endpoint
    ? materializePath(endpoint.path, request.data.pathParams)
    : resolveStoredRequestPath(request.data.pathParams);
  return {
    id: makeID(),
    title: request.title || `${request.svcName}.${request.rpcName}`,
    svcName: request.svcName,
    rpcName: request.rpcName,
    method: request.data.method || endpoint?.method || "GET",
    path: resolvedPath,
    authToken: "",
    storedRequestID: request.id,
    shared: request.shared,
    pathParamsText,
    payloadText: JSON.stringify(request.data.payload ?? {}, null, 2),
    response: null,
    responseError: null,
  };
}

function resolveStoredRequestPath(pathParams: unknown): string {
  if (Array.isArray(pathParams) && pathParams.every((value) => typeof value === "string" && value.length > 0)) {
    return `/${pathParams.join("/")}`;
  }
  return "/";
}

export function storageKey(appId: string): string {
  return `pulse:api-explorer:${appId}`;
}

export function loadPersistedTabs(appId: string): PersistedTabsState {
  try {
    const raw = window.sessionStorage.getItem(storageKey(appId));
    if (!raw) {
      return { activeTabID: null, tabs: [] };
    }
    const parsed = JSON.parse(raw) as PersistedTabsState;
    if (!Array.isArray(parsed.tabs)) {
      return { activeTabID: null, tabs: [] };
    }
    return {
      activeTabID: typeof parsed.activeTabID === "string" ? parsed.activeTabID : null,
      tabs: parsed.tabs.map(stripTransientTabState),
    };
  } catch {
    return { activeTabID: null, tabs: [] };
  }
}

export function persistTabs(appId: string, activeTabID: string | null, tabs: RequestTab[]) {
  const next: PersistedTabsState = {
    activeTabID,
    tabs: tabs.map(stripTransientTabState),
  };
  window.sessionStorage.setItem(storageKey(appId), JSON.stringify(next));
}

export function reconcileTabsWithEndpoints(
  tabs: RequestTab[],
  endpointMap: Map<string, EndpointOption>,
): RequestTab[] {
  return tabs.map((tab) => {
    const endpoint = endpointMap.get(`${tab.svcName}.${tab.rpcName}`);
    if (!endpoint) {
      return tab;
    }
    let nextPath = tab.path;
    try {
      nextPath = materializePath(endpoint.path, parseJSONInput(tab.pathParamsText));
    } catch {
      nextPath = endpoint.path;
    }
    return {
      ...tab,
      method: tab.method || endpoint.method,
      path: nextPath,
    };
  });
}

export function ensureExplorerTabs(
  tabs: RequestTab[],
  endpointOptions: EndpointOption[],
  makeID?: () => string,
): RequestTab[] {
  if (tabs.length > 0 || endpointOptions.length === 0) {
    return tabs;
  }
  return [makeTabFromEndpoint(endpointOptions[0], undefined, makeID)];
}

export function normalizeActiveTab(activeTabID: string | null, tabs: RequestTab[]): string | null {
  if (tabs.length === 0) {
    return null;
  }
  const activeExists = activeTabID && tabs.some((tab) => tab.id === activeTabID);
  return activeExists ? activeTabID : tabs[0]?.id || null;
}

export function closeExplorerTab(
  tabs: RequestTab[],
  activeTabID: string | null,
  tabID: string,
  endpointOptions: EndpointOption[],
  makeID?: () => string,
): PersistedTabsState {
  const index = tabs.findIndex((tab) => tab.id === tabID);
  if (index === -1) {
    return { activeTabID, tabs };
  }

  let nextTabs = [...tabs.slice(0, index), ...tabs.slice(index + 1)];
  if (nextTabs.length === 0) {
    nextTabs = ensureExplorerTabs(nextTabs, endpointOptions, makeID);
  }

  const nextActiveTabID =
    activeTabID === tabID
      ? nextTabs[Math.min(index, nextTabs.length - 1)]?.id || null
      : normalizeActiveTab(activeTabID, nextTabs);

  return {
    activeTabID: nextActiveTabID,
    tabs: nextTabs,
  };
}

export function stripTransientTabState(tab: RequestTab): RequestTab {
  return {
    ...tab,
    correlationID: undefined,
    response: null,
    responseError: null,
  };
}
