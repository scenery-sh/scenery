import {
  closeExplorerTab,
  ensureExplorerTabs,
  makeTabFromEndpoint,
  makeTabFromStoredRequest,
  normalizeActiveTab,
  reconcileTabsWithEndpoints,
  stripTransientTabState,
} from "./api-explorer";
import type { EndpointOption, StoredRequest } from "./types";

const endpoint: EndpointOption = {
  key: "tenants.Config",
  svcName: "tenants",
  rpcName: "Config",
  method: "GET",
  path: "/tenants/config/:tenantId",
};

describe("api explorer state", () => {
  it("creates a default tab when endpoints exist", () => {
    const tabs = ensureExplorerTabs([], [endpoint], () => "tab-1");
    expect(tabs).toHaveLength(1);
    expect(tabs[0]?.id).toBe("tab-1");
    expect(tabs[0]?.title).toBe("tenants.Config");
    expect(tabs[0]?.pathParamsText).toBe("[]");
  });

  it("reconciles a tab path from path params and endpoint metadata", () => {
    const tab = {
      ...makeTabFromEndpoint(endpoint, undefined, () => "tab-1"),
      pathParamsText: '{"tenantId":"acme"}',
      path: "/",
    };
    const next = reconcileTabsWithEndpoints([tab], new Map([[endpoint.key, endpoint]]));
    expect(next[0]?.path).toBe("/tenants/config/acme");
  });

  it("reconciles a tab path from ordered path params", () => {
    const tab = {
      ...makeTabFromEndpoint(endpoint, undefined, () => "tab-1"),
      pathParamsText: '["acme"]',
      path: "/",
    };
    const next = reconcileTabsWithEndpoints([tab], new Map([[endpoint.key, endpoint]]));
    expect(next[0]?.path).toBe("/tenants/config/acme");
  });

  it("opens stored requests as tabs with stored request metadata", () => {
    const request: StoredRequest = {
      id: "stored-1",
      title: "Config request",
      rpcName: "Config",
      svcName: "tenants",
      shared: true,
      data: {
        method: "GET",
        pathParams: { tenantId: "acme" },
        payload: {},
      },
    };
    const tab = makeTabFromStoredRequest(request, endpoint, () => "tab-2");
    expect(tab.id).toBe("tab-2");
    expect(tab.storedRequestID).toBe("stored-1");
    expect(tab.shared).toBe(true);
    expect(tab.path).toBe("/tenants/config/acme");
  });

  it("falls back to an ordered path when opening stored requests without endpoint metadata", () => {
    const request: StoredRequest = {
      id: "stored-2",
      title: "List requests",
      rpcName: "ListRequests",
      svcName: "console",
      shared: false,
      data: {
        method: "GET",
        pathParams: ["console", "requests"],
        payload: null,
      },
    };
    const tab = makeTabFromStoredRequest(request, undefined, () => "tab-3");
    expect(tab.path).toBe("/console/requests");
  });

  it("closes the active tab and selects the nearest neighbor", () => {
    const first = makeTabFromEndpoint(endpoint, "First", () => "tab-1");
    const second = makeTabFromEndpoint(endpoint, "Second", () => "tab-2");
    const result = closeExplorerTab([first, second], "tab-2", "tab-2", [endpoint], () => "tab-3");
    expect(result.tabs).toHaveLength(1);
    expect(result.activeTabID).toBe("tab-1");
  });

  it("keeps at least one tab alive after closing the last tab", () => {
    const first = makeTabFromEndpoint(endpoint, "Only", () => "tab-1");
    const result = closeExplorerTab([first], "tab-1", "tab-1", [endpoint], () => "tab-2");
    expect(result.tabs).toHaveLength(1);
    expect(result.tabs[0]?.id).toBe("tab-2");
    expect(result.activeTabID).toBe("tab-2");
  });

  it("drops transient response data when persisting tabs", () => {
    const tab = {
      ...makeTabFromEndpoint(endpoint, undefined, () => "tab-1"),
      response: { status: "ok", status_code: 200, body: "{}", trace_id: "trace-1" },
      responseError: "boom",
    };
    const next = stripTransientTabState(tab);
    expect(next.response).toBeNull();
    expect(next.responseError).toBeNull();
  });

  it("normalizes the active tab against current tabs", () => {
    const tab = makeTabFromEndpoint(endpoint, undefined, () => "tab-1");
    expect(normalizeActiveTab("missing", [tab])).toBe("tab-1");
  });
});
