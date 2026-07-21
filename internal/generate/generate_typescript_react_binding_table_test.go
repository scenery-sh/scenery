package generate

import (
	"strings"
	"testing"
)

func TestBindingBackedReactTableLoadsCompleteTypedResultWithoutPagination(t *testing.T) {
	resources := []Resource{
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
		{Address: "house/table_page/work_orders", Module: "house", Name: "work_orders", Kind: "scenery.table-page", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"path": "/work-orders", "source": map[string]any{"$ref": "binding.search_work_orders_http"}, "items": "orders", "title": "Work Orders", "page_size": 50,
			"column": []any{
				map[string]any{"name": "id"},
				map[string]any{"name": "status", "appearance": "badge", "status_map": map[string]any{"$ref": "status_map.work_order_status"}},
				map[string]any{"name": "created_at", "hidden": true, "export": false},
			},
			"filter": map[string]any{"name": "status", "status_map": map[string]any{"$ref": "status_map.work_order_status"}},
			"sort":   map[string]any{"name": "created_at", "default": "desc"},
		}},
	}
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
		"paginated={false}",
		`{ field: "createdAt", label: "Created At", appearance: "auto", hidden: true, export: false }`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated binding table missing %q:\n%s", fragment, source)
		}
	}
	for _, fragment := range []string{"cursor: query.cursor", "limit: BigInt(query.limit)", "nextCursor:"} {
		if strings.Contains(source, fragment) {
			t.Errorf("generated binding table unexpectedly contains %q:\n%s", fragment, source)
		}
	}
}
