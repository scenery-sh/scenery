package generate

import (
	"strings"
	"testing"
)

func TestRenderReactWorkspacePageKeepsTabsAliveAndUsesTypedStats(t *testing.T) {
	statsRecord := Resource{Address: "work/record/workspace_stats", Module: "work", Name: "workspace_stats", Kind: "scenery.record"}
	statsOperation := Resource{Address: "work/operation/workspace_stats", Module: "work", Name: "workspace_stats", Kind: "scenery.operation", Spec: map[string]any{"result": []any{map[string]any{"name": "success", "type": map[string]any{"$ref": statsRecord.Address}}}}}
	statsBinding := Resource{Address: "work/binding/workspace_stats_http", Module: "work", Name: "workspace_stats_http", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": statsOperation.Address}}}
	table := Resource{Address: "work/table_page/orders", Module: "work", Name: "orders", Kind: "scenery.table-page", Spec: map[string]any{"path": "/orders", "title": "Orders"}}
	content := Resource{Address: "work/content_page/summary", Module: "work", Name: "summary", Kind: "scenery.content-page", Spec: map[string]any{"path": "/summary", "title": "Summary"}}
	actions := Resource{Address: "work/react_component/actions", Module: "work", Name: "actions", Kind: "scenery.react-component", Spec: map[string]any{"module": "slots.tsx", "export": "Actions"}}
	workspace := Resource{Address: "work/workspace_page/operations", Module: "work", Name: "operations", Kind: "scenery.workspace-page", Spec: map[string]any{
		"path": "/operations", "title": "Operations", "presentation": "sidebar",
		"tab": []any{
			map[string]any{"name": "orders", "page": map[string]any{"$ref": table.Address}, "label": "Orders", "description": "Open work", "group": "Work", "count": "orders_total", "available": "orders_available", "unavailable_reason": "Orders are not projected"},
			map[string]any{"name": "summary", "page": map[string]any{"$ref": content.Address}, "label": "Summary"},
			map[string]any{"name": "vendors", "destination": "/vendors", "label": "Vendors", "available": "orders_available", "unavailable_reason": "Use the vendor workspace"},
			map[string]any{"name": "rules", "label": "Business rules", "available": "orders_available", "unavailable_reason": "No projected records"},
		},
		"stats":   map[string]any{"source": map[string]any{"$ref": statsBinding.Address}, "tile": []any{map[string]any{"name": "orders_total", "label": "Orders"}}},
		"actions": map[string]any{"component": map[string]any{"$ref": actions.Address}},
	}}
	resources := []Resource{statsRecord, statsOperation, statsBinding, table, content, actions, workspace}
	result := &Result{Root: "/app", Manifest: &Manifest{Resources: resources}}
	page := reactWorkspacePage{workspace: workspace, statsBinding: statsBinding, statsOperation: statsOperation, statsRecord: statsRecord}
	source, err := renderReactWorkspacePage(result, Resource{Name: "public_api"}, "/app/generated/react", page, []Resource{statsBinding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`presentation={"sidebar"}`,
		`new URLSearchParams(globalThis.location.search).get("tab")`,
		`globalThis.addEventListener("popstate", sync)`,
		`next.searchParams.set("tab", name)`,
		`const tabNames = useMemo(() => ["orders", "summary"] as const, [])`,
		`count: statsState.kind === "result" ? statsState.value.ordersTotal : undefined`,
		`available: statsState.kind === "result" ? statsState.value.ordersAvailable : undefined`,
		`description: "Open work"`,
		`unavailableReason: "Orders are not projected"`,
		`destination: "/vendors"`,
		`name: "rules", label: "Business rules", available: statsState.kind === "result" ? statsState.value.ordersAvailable : undefined, unavailableReason: "No projected records" },`,
		`content: <SceneryWorkspaceTab1 client={client} />`,
		`content: <SceneryWorkspaceTab2 />`,
		`actions={<SceneryWorkspaceActions />}`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated workspace page missing %q:\n%s", fragment, source)
		}
	}
}

func TestRenderReactWorkspacePageWithoutStatTilesKeepsQueryStateTyped(t *testing.T) {
	statsRecord := Resource{Address: "work/record/workspace_stats", Module: "work", Name: "workspace_stats", Kind: "scenery.record"}
	statsOperation := Resource{Address: "work/operation/workspace_stats", Module: "work", Name: "workspace_stats", Kind: "scenery.operation", Spec: map[string]any{"result": []any{map[string]any{"name": "success", "type": map[string]any{"$ref": statsRecord.Address}}}}}
	statsBinding := Resource{Address: "work/binding/workspace_stats_http", Module: "work", Name: "workspace_stats_http", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": statsOperation.Address}}}
	content := Resource{Address: "work/content_page/crews", Module: "work", Name: "crews", Kind: "scenery.content-page", Spec: map[string]any{"path": "/crews", "title": "Crews"}}
	workspace := Resource{Address: "work/workspace_page/governance", Module: "work", Name: "governance", Kind: "scenery.workspace-page", Spec: map[string]any{
		"path": "/governance", "title": "Governance", "presentation": "sidebar",
		"tab":   []any{map[string]any{"name": "crews", "page": map[string]any{"$ref": content.Address}, "label": "Crews", "count": "crew_count"}},
		"stats": map[string]any{"source": map[string]any{"$ref": statsBinding.Address}},
	}}
	resources := []Resource{statsRecord, statsOperation, statsBinding, content, workspace}
	result := &Result{Root: "/app", Manifest: &Manifest{Resources: resources}}
	page := reactWorkspacePage{workspace: workspace, statsBinding: statsBinding, statsOperation: statsOperation, statsRecord: statsRecord}
	source, err := renderReactWorkspacePage(result, Resource{Name: "public_api"}, "/app/generated/react", page, []Resource{statsBinding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`content: <SceneryWorkspaceTab1 />`,
		`stats={<QueryState {...queryStateProps(statsState, "statistics")} retry={() => void statsQuery.refetch()}>{null}</QueryState>}`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated workspace page missing %q:\n%s", fragment, source)
		}
	}
	for _, fragment := range []string{"StatGrid", "StatTile", `<SceneryWorkspaceTab1 client={client} />`} {
		if strings.Contains(source, fragment) {
			t.Errorf("generated workspace page contains unused or invalid fragment %q:\n%s", fragment, source)
		}
	}
}

func TestWorkspaceStatsBindingIsIncludedByReactClient(t *testing.T) {
	operation := Resource{Address: "work/operation/workspace_stats", Module: "work", Name: "workspace_stats", Kind: "scenery.operation"}
	binding := Resource{Address: "work/binding/workspace_stats_http", Module: "work", Name: "workspace_stats_http", Kind: "scenery.binding", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "operation": map[string]any{"$ref": operation.Address}, "protocol": "http", "http": map[string]any{"method": "GET"},
	}}
	workspace := Resource{Address: "work/workspace_page/operations", Module: "work", Name: "operations", Kind: "scenery.workspace-page", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"stats": map[string]any{"source": map[string]any{"$ref": binding.Address}},
	}}
	target := Resource{Kind: "scenery.typescript-client", Spec: map[string]any{
		"gateways": []any{map[string]any{"$ref": "app/http_gateway/public"}}, "react": map[string]any{},
	}}
	selected := publicHTTPBindings([]Resource{operation, binding, workspace}, target)
	if len(selected) != 1 || selected[0].Address != binding.Address {
		t.Fatalf("workspace stats bindings = %#v", resourceAddresses(selected))
	}
}

func TestReactWorkspaceRouteOwnsEmbeddedPageRoutes(t *testing.T) {
	table := reactTablePage{table: Resource{Address: "work/table_page/orders", Module: "work", Name: "orders", Kind: "scenery.table-page", Spec: map[string]any{"path": "/orders"}}}
	content := reactContentPage{content: Resource{Address: "work/content_page/summary", Module: "work", Name: "summary", Kind: "scenery.content-page", Spec: map[string]any{"path": "/summary"}}}
	workspace := reactWorkspacePage{workspace: Resource{Address: "work/workspace_page/operations", Module: "work", Name: "operations", Kind: "scenery.workspace-page", Spec: map[string]any{
		"path": "/operations", "tab": []any{
			map[string]any{"name": "orders", "page": map[string]any{"$ref": table.table.Address}},
			map[string]any{"name": "summary", "page": map[string]any{"$ref": content.content.Address}},
		},
	}}}
	routes := reactRoutePages([]reactTablePage{table}, nil, []reactContentPage{content}, workspace)
	if len(routes) != 1 || routes[0].resource.Address != workspace.workspace.Address {
		t.Fatalf("workspace routes = %#v", routes)
	}
}
