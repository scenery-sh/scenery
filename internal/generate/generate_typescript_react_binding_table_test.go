package generate

import (
	"strings"
	"testing"
)

func bindingTableResources() []Resource {
	return []Resource{
		{Address: "house/enum/work_order_status", Module: "house", Name: "work_order_status", Kind: "scenery.enum", Spec: map[string]any{
			"value": []any{map[string]any{"name": "open"}, map[string]any{"name": "complete"}},
		}},
		{Address: "house/enum/work_order_sort", Module: "house", Name: "work_order_sort", Kind: "scenery.enum", Spec: map[string]any{
			"value": map[string]any{"name": "created_at"},
		}},
		{Address: "house/enum/sort_direction", Module: "house", Name: "sort_direction", Kind: "scenery.enum", Spec: map[string]any{
			"value": []any{map[string]any{"name": "asc"}, map[string]any{"name": "desc"}},
		}},
		{Address: "house/record/work_order_query", Module: "house", Name: "work_order_query", Kind: "scenery.record", Spec: map[string]any{
			"field": []any{
				map[string]any{"name": "search", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "status", "type": map[string]any{"$expression": "optional(list(enum.work_order_status))"}},
				map[string]any{"name": "sort", "type": map[string]any{"$expression": "optional(enum.work_order_sort)"}},
				map[string]any{"name": "direction", "type": map[string]any{"$expression": "optional(enum.sort_direction)"}},
			},
		}},
		{Address: "house/record/work_order", Module: "house", Name: "work_order", Kind: "scenery.record", Spec: map[string]any{
			"field": []any{
				map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}},
				map[string]any{"name": "status", "type": map[string]any{"$ref": "string"}},
				map[string]any{"name": "created_at", "type": map[string]any{"$ref": "datetime"}},
			},
		}},
		{Address: "house/record/work_order_results", Module: "house", Name: "work_order_results", Kind: "scenery.record", Spec: map[string]any{
			"field": map[string]any{"name": "orders", "type": map[string]any{"$expression": "list(record.work_order)"}},
		}},
		{Address: "house/operation/search_work_orders", Module: "house", Name: "search_work_orders", Kind: "scenery.operation", Spec: map[string]any{
			"input": map[string]any{"$ref": "record.work_order_query"}, "result": map[string]any{"name": "success", "type": map[string]any{"$ref": "record.work_order_results"}},
		}},
		{Address: "house/binding/search_work_orders_http", Module: "house", Name: "search_work_orders_http", Kind: "scenery.binding", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.search_work_orders"}, "protocol": "http", "delivery": "call",
		}},
		{Address: "house/status_map/work_order_status", Module: "house", Name: "work_order_status", Kind: "scenery.status-map", Spec: map[string]any{
			"status": map[string]any{"name": "open", "label": "Open", "variant": "neutral"},
		}},
		{Address: "house/react_component/work_order_detail", Module: "house", Name: "work_order_detail", Kind: "scenery.react-component", Spec: map[string]any{
			"module": "work-order-detail.tsx", "export": "WorkOrderDetail",
		}},
		{Address: "house/table_page/work_orders", Module: "house", Name: "work_orders", Kind: "scenery.table-page", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"path": "/work-orders", "source": map[string]any{"$ref": "binding.search_work_orders_http"}, "items": "orders", "title": "Work Orders", "page_size": 50, "hide_header": true,
			"column": []any{
				map[string]any{"name": "id"},
				map[string]any{"name": "status", "appearance": "badge", "status_map": map[string]any{"$ref": "status_map.work_order_status"}},
				map[string]any{"name": "created_at", "hidden": true, "export": false},
			},
			"filter": map[string]any{"name": "status", "status_map": map[string]any{"$ref": "status_map.work_order_status"}},
			"sort":   map[string]any{"name": "created_at", "default": "desc"},
			"group":  map[string]any{"name": "status", "label": "Status", "order": []any{"open", "complete"}, "default": true},
			"row_detail": map[string]any{
				"component": map[string]any{"$ref": "react_component.work_order_detail"}, "presentation": "panel", "panel_width": 420,
			},
		}},
	}
}

func TestBindingBackedReactTableLoadsCompleteTypedResultWithoutPagination(t *testing.T) {
	resources := bindingTableResources()
	binding := resources[7]
	pages := selectedReactTablePages(resources, []Resource{binding})
	if len(pages) != 1 {
		t.Fatalf("selected pages = %#v", pages)
	}
	target := Resource{Address: "app/typescript_client/public_api", Module: "app", Name: "public_api", Kind: "scenery.typescript-client"}
	result := &Result{Manifest: &Manifest{Resources: resources}}
	source, err := renderReactTablePage(result, target, "react", pages[0], []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"client.searchWorkOrders({",
		"const load = useCallback(async (query: TablePageQuery, signal?: AbortSignal)",
		"}, { signal });",
		"search: query.search",
		"value is WorkOrderStatus",
		`sort: query.sort !== undefined && (query.sort === "created_at")`,
		`{ kind: "result", items: outcome.value.orders }`,
		"searchable",
		" hideHeader",
		`groups={[`,
		`{ field: "status", label: "Status", order: ["open", "complete"], default: true }`,
		`detailPanel: SceneryOverride1`,
		`detailPanel={slots.detailPanel}`,
		`detailPanelWidth={420}`,
		`{ field: "createdAt", label: "Created At", appearance: "auto", hidden: true, export: false }`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated binding table missing %q:\n%s", fragment, source)
		}
	}
	for _, fragment := range []string{"cursor: query.cursor", "limit: BigInt(query.limit)", "nextCursor:", "pagination="} {
		if strings.Contains(source, fragment) {
			t.Errorf("generated binding table unexpectedly contains %q:\n%s", fragment, source)
		}
	}
}

func TestBindingBackedReactTableMapsPagePaginationQueryFiltersAndPredicates(t *testing.T) {
	resources := bindingTableResources()
	for index := range resources {
		resources[index].Spec = cloneMapValue(resources[index].Spec)
		switch resources[index].Address {
		case "house/record/work_order_query":
			resources[index].Spec["field"] = []any{
				map[string]any{"name": "q", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "stage_filter", "type": map[string]any{"$expression": "optional(enum.work_order_status)"}},
				map[string]any{"name": "sort_field", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "sort_direction", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "page_number", "type": map[string]any{"$ref": "int32"}},
				map[string]any{"name": "per_page", "type": map[string]any{"$ref": "int64"}},
				map[string]any{"name": "tenant_id", "type": map[string]any{"$ref": "int64"}},
			}
		case "house/record/work_order_results":
			resources[index].Spec["field"] = []any{
				map[string]any{"name": "orders", "type": map[string]any{"$expression": "list(record.work_order)"}},
				map[string]any{"name": "total_count", "type": map[string]any{"$ref": "int64"}},
			}
		case "house/table_page/work_orders":
			resources[index].Spec["filter"] = map[string]any{"name": "status", "input": "stage_filter", "status_map": map[string]any{"$ref": "status_map.work_order_status"}}
			resources[index].Spec["query"] = map[string]any{"search": "q", "sort": "sort_field", "direction": "sort_direction"}
			resources[index].Spec["pagination"] = map[string]any{"page": "page_number", "page_size": "per_page", "total": "total_count"}
			resources[index].Spec["predicate"] = map[string]any{"name": "tenant_id", "value": 42}
			delete(resources[index].Spec, "group")
		}
	}

	binding := resources[7]
	page := selectedReactTablePages(resources, []Resource{binding})[0]
	target := Resource{Address: "app/typescript_client/public_api", Module: "app", Name: "public_api", Kind: "scenery.typescript-client"}
	source, err := renderReactTablePage(&Result{Manifest: &Manifest{Resources: resources}}, target, "react", page, []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`q: query.search`,
		`stageFilter: Array.isArray(query.filters["status"]) ? query.filters["status"].filter((value): value is WorkOrderStatus => value === "complete" || value === "open")[0] : undefined`,
		`sortField: query.sort !== undefined && (query.sort === "created_at") ? query.sort : undefined`,
		`sortDirection: query.direction`,
		`pageNumber: query.page`,
		`perPage: BigInt(query.limit)`,
		`tenantId: 42n`,
		`total: reactTableSafeTotal(outcome.value.totalCount)`,
		`function reactTableSafeTotal(value: bigint | number): number`,
		`pagination="page"`,
		`searchable`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated paginated binding table missing %q:\n%s", fragment, source)
		}
		if strings.Count(source, fragment) != 1 {
			t.Errorf("generated paginated binding table contains %q %d times, want once:\n%s", fragment, strings.Count(source, fragment), source)
		}
	}
	for _, obsolete := range []string{"paginated=", `pagination="cursor"`, "cursor: query.cursor", "nextCursor:"} {
		if strings.Contains(source, obsolete) {
			t.Errorf("generated paginated binding table unexpectedly contains %q:\n%s", obsolete, source)
		}
	}
}

func TestCRUDReactTableWrapsFixedPredicateAndUsesCursorPagination(t *testing.T) {
	resources := bindingTableResources()
	for index := range resources {
		resources[index].Spec = cloneMapValue(resources[index].Spec)
		switch resources[index].Address {
		case "house/record/work_order_query":
			resources[index].Spec["field"] = []any{
				map[string]any{"name": "search", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "status", "type": map[string]any{"$expression": "optional(list(enum.work_order_status))"}},
				map[string]any{"name": "sort", "type": map[string]any{"$expression": "optional(enum.work_order_sort)"}},
				map[string]any{"name": "direction", "type": map[string]any{"$expression": "optional(enum.sort_direction)"}},
				map[string]any{"name": "cursor", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "limit", "type": map[string]any{"$ref": "int64"}},
			}
		case "house/binding/search_work_orders_http":
			resources[index].Address = "house/binding/work_orders_list_http"
			resources[index].Name = "work_orders_list_http"
		case "house/table_page/work_orders":
			resources[index].Spec["source"] = map[string]any{"$ref": "crud.work_orders"}
			delete(resources[index].Spec, "items")
			delete(resources[index].Spec, "filter")
			delete(resources[index].Spec, "group")
			resources[index].Spec["predicate"] = map[string]any{"name": "status", "value": "open"}
		}
	}
	resources = append(resources,
		Resource{Address: "house/entity/work_order_entity", Module: "house", Name: "work_order_entity", Kind: "scenery.entity", Spec: map[string]any{"type": map[string]any{"$ref": "record.work_order"}}},
		Resource{Address: "house/crud/work_orders", Module: "house", Name: "work_orders", Kind: "scenery.crud", Spec: map[string]any{
			"entity": map[string]any{"$ref": "entity.work_order_entity"},
			"list":   map[string]any{"search": []any{"id"}, "filters": []any{"status"}, "sorts": []any{"created_at"}},
		}},
	)
	binding := resources[7]
	page := selectedReactTablePages(resources, []Resource{binding})[0]
	target := Resource{Address: "app/typescript_client/public_api", Module: "app", Name: "public_api", Kind: "scenery.typescript-client"}
	source, err := renderReactTablePage(&Result{Manifest: &Manifest{Resources: resources}}, target, "react", page, []Resource{binding})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`status: ["open"]`,
		`cursor: query.cursor`,
		`limit: BigInt(query.limit)`,
		`nextCursor: outcome.value.nextCursor`,
		`pagination="cursor"`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated CRUD table missing %q:\n%s", fragment, source)
		}
	}
	if !strings.Contains(source, "filters={[\n  ]}") {
		t.Errorf("fixed predicate unexpectedly generated a toolbar filter:\n%s", source)
	}
}
