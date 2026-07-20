package compiler

import "testing"

func TestTablePageValidatesAndExpandsToExistingPageRendererContract(t *testing.T) {
	resources := tablePageFixtureResources()
	expanded, diagnostics := expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	expanded, diagnostics = expandTablePageResources(expanded)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	if diagnostics := validateUISemantics("", expanded); hasErrors(diagnostics) {
		t.Fatalf("table page diagnostics = %#v", diagnostics)
	}
	byAddress := resourcesByAddress(&Manifest{Resources: expanded})
	page, renderer := byAddress["house/page/scenes"], byAddress["house/renderer/scenes_web"]
	if refString(page.Spec["load"]) != "house/binding/scene_api_list_internal" || renderer.Spec["module"] != tablePageRendererModule || renderer.Origin.ExpansionLineage[0].Generator != "house/table_page/scenes" {
		t.Fatalf("page=%#v renderer=%#v", page, renderer)
	}

	for _, table := range expanded {
		if table.Address == "house/table_page/scenes" {
			table.Spec["page_size"] = 101
			if diagnostics := validateTablePage(byAddress, table); !hasDiagnostic(diagnostics, "SCN2613") {
				t.Fatalf("page size diagnostics = %#v", diagnostics)
			}
			break
		}
	}
}

func TestTablePageValidationRejectsInvalidAuthoredContracts(t *testing.T) {
	resources := tablePageFixtureResources()
	expanded, diagnostics := expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	byAddress := resourcesByAddress(&Manifest{Resources: expanded})
	base := byAddress["house/table_page/scenes"]
	tests := []struct {
		name string
		code string
		edit func(map[string]any)
	}{
		{"missing source", "SCN2608", func(spec map[string]any) { spec["source"] = map[string]any{"$ref": "crud.missing"} }},
		{"unknown column", "SCN2609", func(spec map[string]any) { spec["column"] = map[string]any{"name": "missing"} }},
		{"all columns hidden", "SCN2609", func(spec map[string]any) { spec["column"] = map[string]any{"name": "name", "hidden": true} }},
		{"filter outside allowlist", "SCN2610", func(spec map[string]any) { spec["filter"] = map[string]any{"name": "name"} }},
		{"missing override", "SCN2611", func(spec map[string]any) {
			spec["column"] = map[string]any{"name": "name", "component": map[string]any{"$ref": "react_component.missing"}}
		}},
		{"unknown row link field", "SCN2612", func(spec map[string]any) { spec["row_link"] = "/scenes/{missing}" }},
		{"page size exceeds CRUD limit", "SCN2613", func(spec map[string]any) { spec["page_size"] = 101 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := cloneResourceView([]Resource{base})[0]
			test.edit(table.Spec)
			if got := validateTablePage(byAddress, table); !hasDiagnostic(got, test.code) {
				t.Fatalf("diagnostics = %#v, want %s", got, test.code)
			}
		})
	}

	badComponent := Resource{Address: "house/react_component/bad", Kind: "scenery.react-component", Spec: map[string]any{"module": "", "export": "not-valid!"}}
	if got := validateReactComponent("", byAddress, badComponent); !hasDiagnostic(got, "SCN2607") {
		t.Fatalf("react component diagnostics = %#v", got)
	}

	resources = append(resources, Resource{Address: "house/page/scenes", Module: "house", Name: "scenes", Kind: "scenery.page"})
	expanded, diagnostics = expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	_, diagnostics = expandTablePageResources(expanded)
	if !hasDiagnostic(diagnostics, "SCN2614") {
		t.Fatalf("collision diagnostics = %#v", diagnostics)
	}
}

func TestTablePageBindingSourceExpandsAndValidatesCompleteTypedList(t *testing.T) {
	resources := append(tablePageFixtureResources(),
		Resource{Address: "house/enum/scene_sort", Module: "house", Name: "scene_sort", Kind: "scenery.enum", Spec: map[string]any{
			"value": map[string]any{"name": "name"},
		}},
		Resource{Address: "house/enum/sort_direction", Module: "house", Name: "sort_direction", Kind: "scenery.enum", Spec: map[string]any{
			"value": []any{map[string]any{"name": "asc"}, map[string]any{"name": "desc"}},
		}},
		Resource{Address: "house/record/scene_query", Module: "house", Name: "scene_query", Kind: "scenery.record", Spec: map[string]any{
			"field": []any{
				map[string]any{"name": "search", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "name", "type": map[string]any{"$expression": "optional(list(string))"}},
				map[string]any{"name": "sort", "type": map[string]any{"$expression": "optional(enum.scene_sort)"}},
				map[string]any{"name": "direction", "type": map[string]any{"$expression": "optional(enum.sort_direction)"}},
			},
		}},
		Resource{Address: "house/record/scene_results", Module: "house", Name: "scene_results", Kind: "scenery.record", Spec: map[string]any{
			"field": map[string]any{"name": "rows", "type": map[string]any{"$expression": "list(record.scene_row)"}},
		}},
		Resource{Address: "house/operation/search_scenes", Module: "house", Name: "search_scenes", Kind: "scenery.operation", Spec: map[string]any{
			"input": map[string]any{"$ref": "record.scene_query"}, "result": map[string]any{"name": "success", "type": map[string]any{"$ref": "record.scene_results"}},
		}},
		Resource{Address: "house/binding/search_scenes_http", Module: "house", Name: "search_scenes_http", Kind: "scenery.binding", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.search_scenes"}, "protocol": "http", "delivery": "call",
		}},
		Resource{Address: "house/status_map/scene_name", Module: "house", Name: "scene_name", Kind: "scenery.status-map", Spec: map[string]any{
			"status": map[string]any{"name": "example", "label": "Example", "variant": "neutral"},
		}},
	)
	for index := range resources {
		if resources[index].Address != "house/table_page/scenes" {
			continue
		}
		resources[index].Spec = cloneMapValue(resources[index].Spec)
		resources[index].Spec["source"] = map[string]any{"$ref": "binding.search_scenes_http"}
		resources[index].Spec["items"] = "rows"
		resources[index].Spec["filter"] = map[string]any{
			"name": "name", "status_map": map[string]any{"$ref": "status_map.scene_name"},
		}
		break
	}
	expanded, diagnostics := expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	expanded, diagnostics = expandTablePageResources(expanded)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	if diagnostics := validateUISemantics("", expanded); hasErrors(diagnostics) {
		t.Fatalf("table page diagnostics = %#v", diagnostics)
	}
	page := resourcesByAddress(&Manifest{Resources: expanded})["house/page/scenes"]
	if got := refString(page.Spec["load"]); got != "house/binding/scenes_load_internal" {
		t.Fatalf("page load = %q", got)
	}
}

func TestTablePageValidatesWorkbenchContracts(t *testing.T) {
	resources := tablePageFixtureResources()
	for index := range resources {
		if resources[index].Address == "house/crud/scene_api" {
			resources[index].Spec["list"].(map[string]any)["filters"] = []any{"name"}
		}
	}
	resources = append(resources,
		Resource{Address: "house/status_map/state", Module: "house", Name: "state", Kind: "scenery.status-map", Spec: map[string]any{
			"status": map[string]any{"name": "open", "label": "Open", "variant": "neutral"},
		}},
		Resource{Address: "house/react_component/detail", Module: "house", Name: "detail", Kind: "scenery.react-component", Spec: map[string]any{"module": "detail.tsx", "export": "Detail"}},
		Resource{Address: "house/record/metrics", Module: "house", Name: "metrics", Kind: "scenery.record", Spec: map[string]any{
			"field": map[string]any{"name": "total", "type": map[string]any{"$expression": "int32"}},
		}},
		Resource{Address: "house/operation/metrics", Module: "house", Name: "metrics", Kind: "scenery.operation", Spec: map[string]any{
			"input": map[string]any{"$ref": "std.type.unit"}, "result": map[string]any{"name": "success", "type": map[string]any{"$ref": "record.metrics"}},
		}},
		Resource{Address: "house/binding/metrics_http", Module: "house", Name: "metrics_http", Kind: "scenery.binding", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.metrics"}, "protocol": "http", "delivery": "call", "http": map[string]any{"method": "GET"},
		}},
		Resource{Address: "house/record/create_input", Module: "house", Name: "create_input", Kind: "scenery.record", Spec: map[string]any{
			"field": map[string]any{"name": "name", "type": map[string]any{"$ref": "string"}},
		}},
		Resource{Address: "house/operation/create", Module: "house", Name: "create", Kind: "scenery.operation", Spec: map[string]any{
			"input": map[string]any{"$ref": "record.create_input"}, "result": map[string]any{"name": "success", "type": map[string]any{"$ref": "record.create_input"}},
		}},
		Resource{Address: "house/binding/create_http", Module: "house", Name: "create_http", Kind: "scenery.binding", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.create"}, "protocol": "http", "delivery": "call", "http": map[string]any{"method": "POST"},
		}},
		Resource{Address: "house/form_dialog/create", Module: "house", Name: "create", Kind: "scenery.form-dialog", Spec: map[string]any{"source": map[string]any{"$ref": "binding.create_http"}, "title": "Create"}},
	)
	expanded, diagnostics := expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	byAddress := resourcesByAddress(&Manifest{Resources: expanded})
	table := byAddress["house/table_page/scenes"]
	table.Spec = cloneMapValue(table.Spec)
	table.Spec["stats"] = map[string]any{
		"source": map[string]any{"$ref": "binding.metrics_http"},
		"tile":   map[string]any{"name": "total", "label": "Total"},
	}
	table.Spec["row_detail"] = map[string]any{"component": map[string]any{"$ref": "react_component.detail"}, "dialog": map[string]any{"$ref": "form_dialog.create"}}
	table.Spec["action"] = map[string]any{"name": "create", "label": "Create", "dialog": map[string]any{"$ref": "form_dialog.create"}}
	table.Spec["filter"] = map[string]any{
		"name":       "name",
		"label":      "State",
		"pinned":     true,
		"status_map": map[string]any{"$ref": "status_map.state"},
	}
	if diagnostics := validateTablePage(byAddress, table); hasErrors(diagnostics) {
		t.Fatalf("workbench diagnostics = %#v", diagnostics)
	}

	invalidPinned := table
	invalidPinned.Spec = cloneMapValue(table.Spec)
	invalidPinned.Spec["filter"].(map[string]any)["component"] = map[string]any{"$ref": "react_component.detail"}
	if diagnostics := validateTablePage(byAddress, invalidPinned); !hasDiagnostic(diagnostics, "SCN2622") {
		t.Fatalf("pinned custom filter diagnostics = %#v", diagnostics)
	}
}

func tablePageFixtureResources() []Resource {
	resources := dataProfileFixtureResources()
	crud := &resources[4]
	crud.Spec["actions"] = []any{"list"}
	crud.Spec["list"] = map[string]any{"filters": []any{}, "sorts": []any{"name"}, "default_sort": map[string]any{"field": "name", "direction": "desc"}, "max_page_size": 100}
	return append(resources,
		Resource{Address: "house/react_component/name_cell", Module: "house", Name: "name_cell", Kind: "scenery.react-component", Spec: map[string]any{"module": "components/name-cell.tsx", "export": "NameCell"}},
		Resource{Address: "house/table_page/scenes", Module: "house", Name: "scenes", Kind: "scenery.table-page", Origin: Origin{Kind: "authored", SourceID: "src_house"}, Spec: map[string]any{
			"path": "/scenes", "source": map[string]any{"$ref": "crud.scene_api"}, "title": "Scenes", "page_size": 50, "row_link": "/scenes/{id}",
			"column": []any{map[string]any{"name": "id", "appearance": "text"}, map[string]any{"name": "name", "component": map[string]any{"$ref": "react_component.name_cell"}}},
			"sort":   map[string]any{"name": "name", "default": "desc"},
		}},
	)
}
