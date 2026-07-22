package compiler

import "testing"

func TestWorkspacePageValidatesAndExpands(t *testing.T) {
	resources := workspacePageFixture()
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	workspace := byAddress["house/workspace_page/operations"]
	if diagnostics := validateWorkspacePage(byAddress, workspace); len(diagnostics) != 0 {
		t.Fatalf("validate workspace page: %#v", diagnostics)
	}
	expanded, diagnostics := expandWorkspacePageResources(resources)
	if len(diagnostics) != 0 {
		t.Fatalf("expand workspace page: %#v", diagnostics)
	}
	byAddress = resourcesByAddress(&Manifest{Resources: expanded})
	if byAddress["house/page/operations"].Kind != "scenery.page" || stringValue(byAddress["house/renderer/operations_web"].Spec["module"]) != workspacePageRendererModule {
		t.Fatalf("missing expanded workspace resources: %#v", byAddress)
	}
}

func TestWorkspacePageRejectsInvalidTabsAndTypedStatsFields(t *testing.T) {
	resources := workspacePageFixture()
	for index := range resources {
		if resources[index].Address != "house/workspace_page/operations" {
			continue
		}
		resources[index].Spec["tab"] = []any{
			map[string]any{"name": "orders", "page": map[string]any{"$ref": "table_page.orders"}, "label": "Orders", "count": "orders_available", "available": "orders_total"},
			map[string]any{"name": "orders", "page": map[string]any{"$ref": "table_page.orders"}, "label": "Duplicate"},
		}
	}
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	diagnostics := validateWorkspacePage(byAddress, byAddress["house/workspace_page/operations"])
	if !hasDiagnostic(diagnostics, "SCN2626") || !hasDiagnostic(diagnostics, "SCN2627") {
		t.Fatalf("invalid workspace diagnostics: %#v", diagnostics)
	}
}

func TestWorkspacePageRequiresStatsForCountedTabs(t *testing.T) {
	resources := workspacePageFixture()
	for index := range resources {
		if resources[index].Address == "house/workspace_page/operations" {
			delete(resources[index].Spec, "stats")
		}
	}
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	if diagnostics := validateWorkspacePage(byAddress, byAddress["house/workspace_page/operations"]); !hasDiagnostic(diagnostics, "SCN2627") {
		t.Fatalf("missing stats diagnostics: %#v", diagnostics)
	}
}

func TestWorkspacePageAcceptsSidebarDestinationsAndDisabledNavigationEntries(t *testing.T) {
	resources := workspacePageFixture()
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	if diagnostics := validateWorkspacePage(byAddress, byAddress["house/workspace_page/operations"]); len(diagnostics) != 0 {
		t.Fatalf("validate workspace sidebar navigation: %#v", diagnostics)
	}
}

func TestWorkspacePageRejectsAmbiguousOrUnexplainedNavigationEntries(t *testing.T) {
	for _, test := range []struct {
		index  int
		mutate func(map[string]any)
	}{
		{2, func(tab map[string]any) { tab["page"] = map[string]any{"$ref": "table_page.orders"} }},
		{3, func(tab map[string]any) { delete(tab, "available") }},
		{3, func(tab map[string]any) { delete(tab, "unavailable_reason") }},
	} {
		resources := workspacePageFixture()
		for index := range resources {
			if resources[index].Address != "house/workspace_page/operations" {
				continue
			}
			tabs := resources[index].Spec["tab"].([]any)
			test.mutate(tabs[test.index].(map[string]any))
		}
		byAddress := resourcesByAddress(&Manifest{Resources: resources})
		if diagnostics := validateWorkspacePage(byAddress, byAddress["house/workspace_page/operations"]); !hasDiagnostic(diagnostics, "SCN2626") {
			t.Fatalf("missing navigation diagnostic: %#v", diagnostics)
		}
	}
}

func workspacePageFixture() []Resource {
	return []Resource{
		{Address: "house/record/workspace_stats", Module: "house", Name: "workspace_stats", Kind: "scenery.record", Spec: map[string]any{"field": []any{
			map[string]any{"name": "orders_total", "type": map[string]any{"$expression": "int64"}},
			map[string]any{"name": "orders_available", "type": map[string]any{"$expression": "bool"}},
		}}},
		{Address: "house/operation/workspace_stats", Module: "house", Name: "workspace_stats", Kind: "scenery.operation", Spec: map[string]any{"input": map[string]any{"$ref": "std.type.unit"}, "result": []any{map[string]any{"name": "success", "type": map[string]any{"$ref": "record.workspace_stats"}}}}},
		{Address: "house/binding/workspace_stats_http", Module: "house", Name: "workspace_stats_http", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.workspace_stats"}, "protocol": "http", "delivery": "call"}},
		{Address: "house/table_page/orders", Module: "house", Name: "orders", Kind: "scenery.table-page", Origin: Origin{Kind: "authored"}},
		{Address: "house/content_page/summary", Module: "house", Name: "summary", Kind: "scenery.content-page", Origin: Origin{Kind: "authored"}},
		{Address: "house/react_component/actions", Module: "house", Name: "actions", Kind: "scenery.react-component"},
		{Address: "house/workspace_page/operations", Module: "house", Name: "operations", Kind: "scenery.workspace-page", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"path": "/operations", "title": "Operations", "presentation": "sidebar",
			"tab": []any{
				map[string]any{"name": "orders", "page": map[string]any{"$ref": "table_page.orders"}, "label": "Orders", "description": "Open work", "group": "Work", "count": "orders_total", "available": "orders_available", "unavailable_reason": "Orders are not projected"},
				map[string]any{"name": "summary", "page": map[string]any{"$ref": "content_page.summary"}, "label": "Summary"},
				map[string]any{"name": "vendors", "destination": "/vendors", "label": "Vendors", "group": "Related", "available": "orders_available", "unavailable_reason": "Vendors live in their own workspace"},
				map[string]any{"name": "rules", "label": "Business rules", "group": "Unavailable", "available": "orders_available", "unavailable_reason": "No projected records"},
			},
			"stats":   map[string]any{"source": map[string]any{"$ref": "binding.workspace_stats_http"}, "tile": []any{map[string]any{"name": "orders_total", "label": "Orders"}}},
			"actions": map[string]any{"component": map[string]any{"$ref": "react_component.actions"}},
		}},
	}
}
