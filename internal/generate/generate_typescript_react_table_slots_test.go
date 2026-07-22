package generate

import (
	"strings"
	"testing"
)

func TestReactTableLiteralUnwrapsIntegerScalar(t *testing.T) {
	value := map[string]any{"$scalar": "int", "value": "500"}
	typeValue := map[string]any{"$expression": "optional(int32)"}
	if got := reactTableLiteral(value, typeValue); got != "500" {
		t.Fatalf("literal = %q, want 500", got)
	}
}

func TestRenderReactTablePageWiresResponseAwareSlotsAndRowAction(t *testing.T) {
	resources, binding := responseAwareTablePageResources(true)
	pages := selectedReactTablePages(resources, []Resource{binding})
	if len(pages) != 1 {
		t.Fatalf("selected pages = %#v", pages)
	}
	result := &Result{Root: "/app", Manifest: &Manifest{Resources: resources}}
	target := Resource{Address: "app/typescript_client/public_api", Module: "app", Name: "public_api", Kind: "scenery.typescript-client"}
	source, err := renderReactTablePage(result, target, "/app/generated/react", pages[0], []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`import { type ComponentType, useCallback, useMemo, useState } from "react";`,
		`TablePageResultContext`,
		`TablePageToolbarProps`,
		`const WorkOrdersToolbar: ComponentType<TablePageToolbarProps<WorkOrder>> = SceneryOverride`,
		`const WorkOrdersStatusFilterComponent: ComponentType<TablePageFilterProps<string, WorkOrder>> = SceneryOverride`,
		`function WorkOrdersStatusFilter(props: TablePageFilterProps<string, WorkOrder>)`,
		`context={props.context}`,
		`footer: SceneryOverride`,
		`rowAction: SceneryOverride`,
		`const [tableContext, setTableContext] = useState<TablePageResultContext<WorkOrder>>();`,
		`const onResultContextChange = useCallback((context: TablePageResultContext<WorkOrder>) => setTableContext(context), []);`,
		`function requiredTablePageSlot<T>(value: T | undefined, name: string): T`,
		`const WorkOrdersToolbarSlot = requiredTablePageSlot(slots.toolbar, "toolbar");`,
		`<WorkOrdersToolbarSlot context={tableContext} />`,
		`empty={slots.empty}`,
		`footer={slots.footer}`,
		`onResultContextChange={onResultContextChange}`,
		`rowAction={slots.rowAction}`,
		`const WorkOrdersOwnerFilterSlot = requiredTablePageSlot(slots.filters?.owner, "filters.owner");`,
		`{ field: "owner", label: "Owner", kind: "enum", options: [], component: WorkOrdersOwnerFilterSlot }`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated response-aware table page missing %q:\n%s", fragment, source)
		}
	}
	for _, module := range []string{"empty", "footer", "row-action", "status-filter", "toolbar"} {
		if !strings.Contains(source, `from "../../`+module+`.js"`) {
			t.Errorf("generated response-aware table page did not import %s component:\n%s", module, source)
		}
	}
}

func TestRenderReactTablePageWithoutToolbarKeepsMinimalReactImports(t *testing.T) {
	resources, binding := responseAwareTablePageResources(false)
	pages := selectedReactTablePages(resources, []Resource{binding})
	if len(pages) != 1 {
		t.Fatalf("selected pages = %#v", pages)
	}
	result := &Result{Root: "/app", Manifest: &Manifest{Resources: resources}}
	target := Resource{Address: "app/typescript_client/public_api", Module: "app", Name: "public_api", Kind: "scenery.typescript-client"}
	source, err := renderReactTablePage(result, target, "/app/generated/react", pages[0], []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`import { type ComponentType, useCallback, useMemo } from "react";`,
		`footer={slots.footer}`,
		`rowAction={slots.rowAction}`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated table page without toolbar missing %q:\n%s", fragment, source)
		}
	}
	for _, fragment := range []string{"useState", "TablePageResultContext", "tableContext", "onResultContextChange"} {
		if strings.Contains(source, fragment) {
			t.Errorf("generated table page without toolbar contains unnecessary %q:\n%s", fragment, source)
		}
	}
}

func TestRenderReactTablePagePlacesContentToolbarAboveTableAndHidesOwnedFilter(t *testing.T) {
	resources, binding := responseAwareTablePageResources(true)
	for index := range resources {
		if resources[index].Kind != "scenery.table-page" {
			continue
		}
		resources[index].Spec["toolbar"].(map[string]any)["placement"] = "content"
		resources[index].Spec["filter"].([]any)[0].(map[string]any)["hidden"] = true
	}
	pages := selectedReactTablePages(resources, []Resource{binding})
	if len(pages) != 1 {
		t.Fatalf("selected pages = %#v", pages)
	}
	result := &Result{Root: "/app", Manifest: &Manifest{Resources: resources}}
	target := Resource{Address: "app/typescript_client/public_api", Module: "app", Name: "public_api", Kind: "scenery.typescript-client"}
	source, err := renderReactTablePage(result, target, "/app/generated/react", pages[0], []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`<Page title={"Work Orders"} fill>`,
		"  <WorkOrdersToolbarSlot context={tableContext} />\n<QueryTable<WorkOrder>",
		`{ field: "status", label: "Status", kind: "enum", options: [{ value: "closed", label: "Closed" }, { value: "open", label: "Open" }], component: WorkOrdersStatusFilterSlot, hidden: true }`,
		`onResultContextChange={onResultContextChange}`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated content-toolbar table page missing %q:\n%s", fragment, source)
		}
	}
	if strings.Contains(source, `actions={<`) {
		t.Errorf("content toolbar was also rendered in Page actions:\n%s", source)
	}
}

func TestRenderReactTablePageProjectsTypedBindingMetadataIntoSlotContext(t *testing.T) {
	resources, binding := responseAwareTablePageResources(true)
	for index := range resources {
		if resources[index].Kind == "scenery.table-page" {
			resources[index].Spec["metadata"] = []any{"summary", "types", "manufacturers"}
		}
	}
	pages := selectedReactTablePages(resources, []Resource{binding})
	if len(pages) != 1 {
		t.Fatalf("selected pages = %#v", pages)
	}
	result := &Result{Root: "/app", Manifest: &Manifest{Resources: resources}}
	target := Resource{Address: "app/typescript_client/public_api", Module: "app", Name: "public_api", Kind: "scenery.typescript-client"}
	source, err := renderReactTablePage(result, target, "/app/generated/react", pages[0], []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`import type { WorkOrder, WorkOrderResults`,
		`type WorkOrdersMetadata = Pick<WorkOrderResults, "summary" | "types" | "manufacturers">;`,
		`TablePageToolbarProps<WorkOrder, WorkOrdersMetadata>`,
		`defineTablePageSlots<WorkOrder, never, { readonly "status": string; readonly "owner": string }, WorkOrdersMetadata>()`,
		`TablePageResultContext<WorkOrder, WorkOrdersMetadata>`,
		`TablePageResult<WorkOrder, WorkOrdersMetadata>`,
		`metadata: { summary: outcome.value.summary, types: outcome.value.types, manufacturers: outcome.value.manufacturers }`,
		`<QueryTable<WorkOrder, WorkOrdersMetadata>`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated metadata-aware table page missing %q:\n%s", fragment, source)
		}
	}
}

func TestRenderReactTablePageCanDelegateMappedSearchToToolbar(t *testing.T) {
	resources, binding := responseAwareTablePageResources(true)
	for index := range resources {
		if resources[index].Kind == "scenery.table-page" {
			resources[index].Spec["query"] = map[string]any{"search": "q", "search_hidden": true}
		}
	}
	pages := selectedReactTablePages(resources, []Resource{binding})
	result := &Result{Root: "/app", Manifest: &Manifest{Resources: resources}}
	target := Resource{Address: "app/typescript_client/public_api", Module: "app", Name: "public_api", Kind: "scenery.typescript-client"}
	source, err := renderReactTablePage(result, target, "/app/generated/react", pages[0], []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`q: query.search`, ` searchable hideSearch`} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated toolbar-owned search missing %q:\n%s", fragment, source)
		}
	}
}

func TestRenderReactTablePageDoesNotImportFilterOnlyStatusMap(t *testing.T) {
	resources, binding := responseAwareTablePageResources(false)
	resources = append(resources, Resource{Address: "app/status_map/filter_status", Module: "app", Name: "filter_status", Kind: "scenery.status-map", Spec: map[string]any{
		"status": map[string]any{"name": "open", "label": "Open", "variant": "neutral"},
	}})
	for index := range resources {
		if resources[index].Kind != "scenery.table-page" {
			continue
		}
		filters := resources[index].Spec["filter"].([]any)
		filters[0].(map[string]any)["status_map"] = map[string]any{"$ref": "status_map.filter_status"}
		filters[0].(map[string]any)["hidden"] = true
	}
	page := selectedReactTablePages(resources, []Resource{binding})[0]
	target := Resource{Address: "app/typescript_client/public_api", Module: "app", Name: "public_api", Kind: "scenery.typescript-client"}
	source, err := renderReactTablePage(&Result{Root: "/app", Manifest: &Manifest{Resources: resources}}, target, "/app/generated/react", page, []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(source, `from "./status-maps.generated.js"`) || strings.Contains(source, "AppFilterStatusStatusMap") {
		t.Fatalf("generated filter-only status map import is unused:\n%s", source)
	}
}

func responseAwareTablePageResources(withToolbar bool) ([]Resource, Resource) {
	status := Resource{Address: "app/enum/work_order_status", Module: "app", Name: "work_order_status", Kind: "scenery.enum", Spec: map[string]any{
		"value": []any{map[string]any{"name": "open"}, map[string]any{"name": "closed"}},
	}}
	query := Resource{Address: "app/record/work_order_query", Module: "app", Name: "work_order_query", Kind: "scenery.record", Spec: map[string]any{
		"field": []any{
			map[string]any{"name": "q", "type": map[string]any{"$expression": "optional(string)"}},
			map[string]any{"name": "status", "type": map[string]any{"$expression": "optional(list(enum.work_order_status))"}},
			map[string]any{"name": "owner", "type": map[string]any{"$expression": "optional(string)"}},
		},
	}}
	row := Resource{Address: "app/record/work_order", Module: "app", Name: "work_order", Kind: "scenery.record", Spec: map[string]any{
		"field": []any{
			map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}},
			map[string]any{"name": "status", "type": map[string]any{"$ref": "enum.work_order_status"}},
			map[string]any{"name": "owner", "type": map[string]any{"$ref": "string"}},
		},
	}}
	results := Resource{Address: "app/record/work_order_results", Module: "app", Name: "work_order_results", Kind: "scenery.record", Spec: map[string]any{
		"field": []any{
			map[string]any{"name": "items", "type": map[string]any{"$expression": "list(record.work_order)"}},
			map[string]any{"name": "summary", "type": map[string]any{"$expression": "string"}},
			map[string]any{"name": "types", "type": map[string]any{"$expression": "list(string)"}},
			map[string]any{"name": "manufacturers", "type": map[string]any{"$expression": "list(string)"}},
		},
	}}
	operation := Resource{Address: "app/operation/search_work_orders", Module: "app", Name: "search_work_orders", Kind: "scenery.operation", Spec: map[string]any{
		"input":  map[string]any{"$ref": query.Address},
		"result": map[string]any{"name": "success", "type": map[string]any{"$ref": results.Address}},
	}}
	binding := Resource{Address: "app/binding/search_work_orders_http", Module: "app", Name: "search_work_orders_http", Kind: "scenery.binding", Spec: map[string]any{
		"operation": map[string]any{"$ref": operation.Address}, "protocol": "http", "delivery": "call",
	}}
	components := []Resource{
		{Address: "app/react_component/empty", Module: "app", Name: "empty", Kind: "scenery.react-component", Spec: map[string]any{"module": "empty.tsx", "export": "Empty"}},
		{Address: "app/react_component/footer", Module: "app", Name: "footer", Kind: "scenery.react-component", Spec: map[string]any{"module": "footer.tsx", "export": "Footer"}},
		{Address: "app/react_component/row_action", Module: "app", Name: "row_action", Kind: "scenery.react-component", Spec: map[string]any{"module": "row-action.tsx", "export": "RowAction"}},
		{Address: "app/react_component/status_filter", Module: "app", Name: "status_filter", Kind: "scenery.react-component", Spec: map[string]any{"module": "status-filter.tsx", "export": "StatusFilter"}},
		{Address: "app/react_component/owner_filter", Module: "app", Name: "owner_filter", Kind: "scenery.react-component", Spec: map[string]any{"module": "owner-filter.tsx", "export": "OwnerFilter"}},
	}
	tableSpec := map[string]any{
		"path": "/work-orders", "source": map[string]any{"$ref": binding.Address}, "items": "items", "title": "Work Orders", "page_size": 50,
		"column": []any{map[string]any{"name": "id"}, map[string]any{"name": "status"}, map[string]any{"name": "owner"}},
		"filter": []any{
			map[string]any{"name": "status", "component": map[string]any{"$ref": "react_component.status_filter"}},
			map[string]any{"name": "owner", "component": map[string]any{"$ref": "react_component.owner_filter"}},
		},
		"empty":      map[string]any{"component": map[string]any{"$ref": "react_component.empty"}},
		"footer":     map[string]any{"component": map[string]any{"$ref": "react_component.footer"}},
		"row_action": map[string]any{"component": map[string]any{"$ref": "react_component.row_action"}},
	}
	if withToolbar {
		components = append(components, Resource{Address: "app/react_component/toolbar", Module: "app", Name: "toolbar", Kind: "scenery.react-component", Spec: map[string]any{"module": "toolbar.tsx", "export": "Toolbar"}})
		tableSpec["toolbar"] = map[string]any{"component": map[string]any{"$ref": "react_component.toolbar"}}
	}
	table := Resource{Address: "app/table_page/work_orders", Module: "app", Name: "work_orders", Kind: "scenery.table-page", Origin: Origin{Kind: "authored"}, Spec: tableSpec}
	resources := []Resource{status, query, row, results, operation, binding}
	resources = append(resources, components...)
	resources = append(resources, table)
	return resources, binding
}
