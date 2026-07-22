package compiler

import (
	"strings"
	"testing"
)

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
		{"missing footer component", "SCN2611", func(spec map[string]any) {
			spec["footer"] = map[string]any{"component": map[string]any{"$ref": "react_component.missing"}}
		}},
		{"missing row action component", "SCN2611", func(spec map[string]any) {
			spec["row_action"] = map[string]any{"component": map[string]any{"$ref": "react_component.missing"}}
		}},
		{"invalid toolbar placement", "SCN2622", func(spec map[string]any) {
			spec["toolbar"] = map[string]any{"component": map[string]any{"$ref": "react_component.name_cell"}, "placement": "sidebar"}
		}},
		{"invalid export format", "SCN2609", func(spec map[string]any) {
			spec["column"] = map[string]any{"name": "name", "export_format": "locale"}
		}},
		{"unknown filename token", "SCN2622", func(spec map[string]any) {
			spec["export"] = map[string]any{"file_name": "scenes-{timestamp}.csv"}
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

func TestTablePageValidatesGroupingAndDetailPresentation(t *testing.T) {
	resources := tablePageFixtureResources()
	expanded, diagnostics := expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	byAddress := resourcesByAddress(&Manifest{Resources: expanded})
	base := byAddress["house/table_page/scenes"]
	byAddress["house/form_dialog/edit"] = Resource{
		Address: "house/form_dialog/edit",
		Module:  "house",
		Name:    "edit",
		Kind:    "scenery.form-dialog",
		Spec:    map[string]any{},
	}
	tests := []struct {
		name string
		want string
		edit func(map[string]any)
	}{
		{
			name: "paginated grouping",
			want: "complete-list data source",
			edit: func(spec map[string]any) {
				spec["group"] = map[string]any{"name": "name", "label": "Name"}
			},
		},
		{
			name: "duplicate group field",
			want: "groups require unique row fields",
			edit: func(spec map[string]any) {
				spec["group"] = []any{
					map[string]any{"name": "name", "label": "Name"},
					map[string]any{"name": "name", "label": "Name again"},
				}
			},
		},
		{
			name: "invalid presentation",
			want: "presentation must be inline or panel",
			edit: func(spec map[string]any) {
				spec["row_detail"] = map[string]any{
					"component":    map[string]any{"$ref": "react_component.name_cell"},
					"presentation": "drawer",
				}
			},
		},
		{
			name: "panel width on inline detail",
			want: "panel_width requires panel presentation",
			edit: func(spec map[string]any) {
				spec["row_detail"] = map[string]any{
					"component":   map[string]any{"$ref": "react_component.name_cell"},
					"panel_width": 360,
				}
			},
		},
		{
			name: "dialog on panel detail",
			want: "dialog is available only with inline presentation",
			edit: func(spec map[string]any) {
				spec["row_detail"] = map[string]any{
					"component":    map[string]any{"$ref": "react_component.name_cell"},
					"dialog":       map[string]any{"$ref": "form_dialog.edit"},
					"presentation": "panel",
				}
			},
		},
		{
			name: "row action with row detail",
			want: "row_action and row_detail are mutually exclusive",
			edit: func(spec map[string]any) {
				spec["row_detail"] = map[string]any{
					"component": map[string]any{"$ref": "react_component.name_cell"},
				}
				spec["row_action"] = map[string]any{
					"component": map[string]any{"$ref": "react_component.name_cell"},
				}
			},
		},
		{
			name: "prefetch on inline detail",
			want: "requires panel presentation",
			edit: func(spec map[string]any) {
				spec["row_detail"] = map[string]any{
					"component":       map[string]any{"$ref": "react_component.name_cell"},
					"prefetch_export": "prefetchName",
				}
			},
		},
		{
			name: "invalid row action prefetch export",
			want: "valid module export",
			edit: func(spec map[string]any) {
				spec["row_action"] = map[string]any{
					"component":       map[string]any{"$ref": "react_component.name_cell"},
					"prefetch_export": "not-valid!",
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := cloneResourceView([]Resource{base})[0]
			test.edit(table.Spec)
			got := validateTablePage(byAddress, table)
			found := false
			for _, diagnostic := range got {
				found = found || diagnostic.Code == "SCN2623" && strings.Contains(diagnostic.Message, test.want)
			}
			if !found {
				t.Fatalf("diagnostics = %#v, want SCN2623 containing %q", got, test.want)
			}
		})
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
		resources[index].Spec["group"] = map[string]any{
			"name": "name", "label": "Name", "default": true,
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
			"field": []any{
				map[string]any{"name": "total", "type": map[string]any{"$expression": "int32"}},
				map[string]any{"name": "matching", "type": map[string]any{"$expression": "int32"}},
			},
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
		"tile": map[string]any{
			"name": "total", "label": "Total", "appearance": "money", "sub": "matching", "sub_appearance": "count", "sub_label": "projects", "icon": "chartBar", "filter": "name", "value": "wall",
		},
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
	clearTile := table
	clearTile.Spec = cloneMapValue(table.Spec)
	clearStats := cloneMapValue(clearTile.Spec["stats"])
	clearStatsTile := cloneMapValue(clearStats["tile"])
	clearStats["tile"] = clearStatsTile
	clearTile.Spec["stats"] = clearStats
	delete(clearStatsTile, "value")
	clearStatsTile["clear"] = true
	if diagnostics := validateTablePage(byAddress, clearTile); hasErrors(diagnostics) {
		t.Fatalf("clear stats tile diagnostics = %#v", diagnostics)
	}

	customStringFilter := table
	customStringFilter.Spec = cloneMapValue(table.Spec)
	customStringFilter.Spec["filter"] = map[string]any{
		"name":      "name",
		"label":     "Owner",
		"component": map[string]any{"$ref": "react_component.detail"},
	}
	if diagnostics := validateTablePage(byAddress, customStringFilter); hasErrors(diagnostics) {
		t.Fatalf("custom string filter diagnostics = %#v", diagnostics)
	}

	hiddenStringFilter := table
	hiddenStringFilter.Spec = cloneMapValue(table.Spec)
	hiddenStringFilter.Spec["filter"] = map[string]any{
		"name": "name", "label": "State", "hidden": true,
	}
	if diagnostics := validateTablePage(byAddress, hiddenStringFilter); hasErrors(diagnostics) {
		t.Fatalf("hidden string filter diagnostics = %#v", diagnostics)
	}

	invalidPinned := table
	invalidPinned.Spec = cloneMapValue(table.Spec)
	invalidPinned.Spec["filter"].(map[string]any)["component"] = map[string]any{"$ref": "react_component.detail"}
	if diagnostics := validateTablePage(byAddress, invalidPinned); !hasDiagnostic(diagnostics, "SCN2622") {
		t.Fatalf("pinned custom filter diagnostics = %#v", diagnostics)
	}

	hiddenPinned := table
	hiddenPinned.Spec = cloneMapValue(table.Spec)
	hiddenPinned.Spec["filter"].(map[string]any)["hidden"] = true
	if diagnostics := validateTablePage(byAddress, hiddenPinned); !hasDiagnostic(diagnostics, "SCN2622") {
		t.Fatalf("hidden pinned filter diagnostics = %#v", diagnostics)
	}

	for name, edit := range map[string]func(map[string]any){
		"appearance":  func(tile map[string]any) { tile["appearance"] = "sparkle" },
		"sub":         func(tile map[string]any) { tile["sub"] = "missing" },
		"target":      func(tile map[string]any) { tile["filter"] = "missing" },
		"value":       func(tile map[string]any) { tile["value"] = 42 },
		"pair":        func(tile map[string]any) { delete(tile, "value") },
		"clear value": func(tile map[string]any) { tile["clear"] = true },
	} {
		t.Run("stats "+name, func(t *testing.T) {
			invalid := table
			invalid.Spec = cloneMapValue(table.Spec)
			stats := cloneMapValue(invalid.Spec["stats"])
			tile := cloneMapValue(stats["tile"])
			stats["tile"] = tile
			invalid.Spec["stats"] = stats
			edit(tile)
			if diagnostics := validateTablePage(byAddress, invalid); !hasDiagnostic(diagnostics, "SCN2634") {
				t.Fatalf("stats tile diagnostics = %#v", diagnostics)
			}
		})
	}

	for index := range resources {
		if resources[index].Address == "house/crud/scene_api" {
			resources[index].Spec["list"].(map[string]any)["filters"] = []any{"name", "created_at"}
		}
		if resources[index].Address == "house/record/scene_row" {
			fields := resources[index].Spec["field"].([]any)
			resources[index].Spec["field"] = append(fields, map[string]any{"name": "created_at", "type": map[string]any{"$expression": "datetime"}})
		}
	}
	expanded, diagnostics = expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	byAddress = resourcesByAddress(&Manifest{Resources: expanded})
	presetTable := byAddress["house/table_page/scenes"]
	presetTable.Spec = cloneMapValue(presetTable.Spec)
	presetTable.Spec["filter"] = map[string]any{
		"name": "created_at",
		"preset": []any{
			map[string]any{"name": "today", "label": "Today", "range": "today"},
			map[string]any{"name": "week", "label": "Last 7 days", "range": "last_7_days"},
		},
	}
	if diagnostics := validateTablePage(byAddress, presetTable); hasErrors(diagnostics) {
		t.Fatalf("valid date presets diagnostics = %#v", diagnostics)
	}
	invalidPreset := presetTable
	invalidPreset.Spec = cloneMapValue(presetTable.Spec)
	invalidPreset.Spec["filter"].(map[string]any)["preset"].([]any)[1].(map[string]any)["name"] = "today"
	if diagnostics := validateTablePage(byAddress, invalidPreset); !hasDiagnostic(diagnostics, "SCN2635") {
		t.Fatalf("duplicate preset diagnostics = %#v", diagnostics)
	}
}

func TestTablePageBindingPaginationQueryAndPredicates(t *testing.T) {
	resources := tablePageBindingPaginationFixtureResources()
	expanded, diagnostics := expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	byAddress := resourcesByAddress(&Manifest{Resources: expanded})
	table := byAddress["house/table_page/scenes"]
	contract, diagnostics := resolveTablePageSource(byAddress, table)
	if hasErrors(diagnostics) {
		t.Fatalf("source diagnostics = %#v", diagnostics)
	}
	if !contract.paginated || contract.maxPageSize != 0 {
		t.Fatalf("source contract = %#v, want unbounded page pagination", contract)
	}
	if diagnostics := validateTablePage(byAddress, table); hasErrors(diagnostics) {
		t.Fatalf("table page diagnostics = %#v", diagnostics)
	}
	hiddenSearch := table
	hiddenSearch.Spec = cloneMapValue(table.Spec)
	hiddenSearch.Spec["query"].(map[string]any)["search_hidden"] = true
	if diagnostics := validateTablePage(byAddress, hiddenSearch); hasErrors(diagnostics) {
		t.Fatalf("hidden mapped search diagnostics = %#v", diagnostics)
	}
	missingSearch := hiddenSearch
	missingSearch.Spec = cloneMapValue(hiddenSearch.Spec)
	missingSearch.Spec["query"].(map[string]any)["search"] = "missing"
	if diagnostics := validateTablePage(byAddress, missingSearch); !hasDiagnostic(diagnostics, "SCN2625") {
		t.Fatalf("hidden missing search diagnostics = %#v", diagnostics)
	}

	grouped := table
	grouped.Spec = cloneMapValue(table.Spec)
	grouped.Spec["group"] = map[string]any{"name": "name"}
	if diagnostics := validateTablePage(byAddress, grouped); !hasDiagnostic(diagnostics, "SCN2623") {
		t.Fatalf("paginated grouping diagnostics = %#v", diagnostics)
	}
}

func TestTablePageBindingMetadataRequiresDistinctAuxiliaryResultFields(t *testing.T) {
	resources := tablePageBindingPaginationFixtureResources()
	for index := range resources {
		if resources[index].Address == "house/table_page/scenes" {
			resources[index].Spec["metadata"] = []any{"summary", "stages"}
		}
	}
	expanded, diagnostics := expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	byAddress := resourcesByAddress(&Manifest{Resources: expanded})
	base := byAddress["house/table_page/scenes"]
	if diagnostics := validateTablePage(byAddress, base); hasErrors(diagnostics) {
		t.Fatalf("valid metadata diagnostics = %#v", diagnostics)
	}
	for name, metadata := range map[string][]any{
		"missing":   {"unknown"},
		"items":     {"rows"},
		"total":     {"total_count"},
		"duplicate": {"summary", "summary"},
	} {
		t.Run(name, func(t *testing.T) {
			table := base
			table.Spec = cloneMapValue(base.Spec)
			table.Spec["metadata"] = metadata
			if diagnostics := validateTablePage(byAddress, table); !hasDiagnostic(diagnostics, "SCN2622") {
				t.Fatalf("metadata diagnostics = %#v", diagnostics)
			}
		})
	}

	crud := byAddress["house/table_page/scenes"]
	crud.Spec = cloneMapValue(crud.Spec)
	crud.Spec["source"] = map[string]any{"$ref": "crud.scene_api"}
	delete(crud.Spec, "items")
	delete(crud.Spec, "pagination")
	delete(crud.Spec, "query")
	delete(crud.Spec, "predicate")
	crud.Spec["metadata"] = []any{"summary"}
	if diagnostics := validateTablePage(byAddress, crud); !hasDiagnostic(diagnostics, "SCN2622") {
		t.Fatalf("CRUD metadata diagnostics = %#v", diagnostics)
	}
}

func TestTablePageBindingMappingsRejectInvalidFieldsAndValues(t *testing.T) {
	resources := tablePageBindingPaginationFixtureResources()
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
		{
			name: "non-integer page input",
			code: "SCN2624",
			edit: func(spec map[string]any) {
				spec["pagination"].(map[string]any)["page"] = "q"
			},
		},
		{
			name: "missing total field",
			code: "SCN2624",
			edit: func(spec map[string]any) {
				spec["pagination"].(map[string]any)["total"] = "missing"
			},
		},
		{
			name: "invalid query direction mapping",
			code: "SCN2625",
			edit: func(spec map[string]any) {
				spec["query"].(map[string]any)["direction"] = "q"
			},
		},
		{
			name: "missing filter input",
			code: "SCN2625",
			edit: func(spec map[string]any) {
				spec["filter"].(map[string]any)["input"] = "missing"
			},
		},
		{
			name: "wrong predicate value type",
			code: "SCN2625",
			edit: func(spec map[string]any) {
				spec["predicate"].(map[string]any)["value"] = "not-an-integer"
			},
		},
		{
			name: "predicate conflicts with pagination",
			code: "SCN2625",
			edit: func(spec map[string]any) {
				spec["predicate"].(map[string]any)["name"] = "page_number"
				spec["predicate"].(map[string]any)["value"] = 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := base
			table.Spec = cloneMapValue(base.Spec)
			test.edit(table.Spec)
			if diagnostics := validateTablePage(byAddress, table); !hasDiagnostic(diagnostics, test.code) {
				t.Fatalf("diagnostics = %#v, want %s", diagnostics, test.code)
			}
		})
	}
}

func TestTablePageBindingFilterAcceptsEnumInputForStringRowField(t *testing.T) {
	resources := tablePageBindingPaginationFixtureResources()
	resources = append(resources, Resource{Address: "house/enum/scene_stage", Module: "house", Name: "scene_stage", Kind: "scenery.enum", Spec: map[string]any{
		"value": []any{map[string]any{"name": "open"}, map[string]any{"name": "closed"}},
	}})
	for index := range resources {
		if resources[index].Address == "house/record/scene_page_query" {
			for _, field := range namedChildren(resources[index].Spec, "field") {
				if stringValue(field["name"]) == "stage_filter" {
					field["type"] = map[string]any{"$expression": "optional(enum.scene_stage)"}
				}
			}
		}
	}
	expanded, diagnostics := expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	byAddress := resourcesByAddress(&Manifest{Resources: expanded})
	if diagnostics := validateTablePage(byAddress, byAddress["house/table_page/scenes"]); hasErrors(diagnostics) {
		t.Fatalf("enum input for string row field diagnostics = %#v", diagnostics)
	}
}

func TestTablePageCRUDPredicatesUseAllowlistedFilterItemTypes(t *testing.T) {
	resources := tablePageFixtureResources()
	for index := range resources {
		if resources[index].Address == "house/crud/scene_api" {
			list := resources[index].Spec["list"].(map[string]any)
			list["filters"] = []any{"name"}
			delete(list, "max_page_size")
		}
		if resources[index].Address == "house/table_page/scenes" {
			resources[index].Spec["predicate"] = map[string]any{"name": "name", "value": "fixed"}
		}
	}
	expanded, diagnostics := expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	byAddress := resourcesByAddress(&Manifest{Resources: expanded})
	base := byAddress["house/table_page/scenes"]
	if diagnostics := validateTablePage(byAddress, base); hasDiagnostic(diagnostics, "SCN2613") || hasDiagnostic(diagnostics, "SCN2625") {
		t.Fatalf("valid CRUD predicate diagnostics = %#v", diagnostics)
	}

	wrongType := base
	wrongType.Spec = cloneMapValue(base.Spec)
	wrongType.Spec["predicate"].(map[string]any)["value"] = 42
	if diagnostics := validateTablePage(byAddress, wrongType); !hasDiagnostic(diagnostics, "SCN2625") {
		t.Fatalf("wrong-type CRUD predicate diagnostics = %#v", diagnostics)
	}

	notAllowlisted := base
	notAllowlisted.Spec = cloneMapValue(base.Spec)
	notAllowlisted.Spec["predicate"].(map[string]any)["name"] = "id"
	if diagnostics := validateTablePage(byAddress, notAllowlisted); !hasDiagnostic(diagnostics, "SCN2625") {
		t.Fatalf("non-allowlisted CRUD predicate diagnostics = %#v", diagnostics)
	}
}

func tablePageBindingPaginationFixtureResources() []Resource {
	resources := append(tablePageFixtureResources(),
		Resource{Address: "house/record/scene_page_query", Module: "house", Name: "scene_page_query", Kind: "scenery.record", Spec: map[string]any{
			"field": []any{
				map[string]any{"name": "q", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "stage_filter", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "sort_field", "type": map[string]any{"$expression": "string"}},
				map[string]any{"name": "sort_direction", "type": map[string]any{"$expression": "string"}},
				map[string]any{"name": "page_number", "type": map[string]any{"$expression": "int32"}},
				map[string]any{"name": "per_page", "type": map[string]any{"$expression": "int32"}},
				map[string]any{"name": "tenant_id", "type": map[string]any{"$expression": "int64"}},
			},
		}},
		Resource{Address: "house/record/scene_page_result", Module: "house", Name: "scene_page_result", Kind: "scenery.record", Spec: map[string]any{
			"field": []any{
				map[string]any{"name": "rows", "type": map[string]any{"$expression": "list(record.scene_row)"}},
				map[string]any{"name": "total_count", "type": map[string]any{"$expression": "int64"}},
				map[string]any{"name": "summary", "type": map[string]any{"$expression": "string"}},
				map[string]any{"name": "stages", "type": map[string]any{"$expression": "list(string)"}},
			},
		}},
		Resource{Address: "house/operation/page_scenes", Module: "house", Name: "page_scenes", Kind: "scenery.operation", Spec: map[string]any{
			"input": map[string]any{"$ref": "record.scene_page_query"}, "result": map[string]any{"name": "success", "type": map[string]any{"$ref": "record.scene_page_result"}},
		}},
		Resource{Address: "house/binding/page_scenes_http", Module: "house", Name: "page_scenes_http", Kind: "scenery.binding", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.page_scenes"}, "protocol": "http", "delivery": "call",
		}},
		Resource{Address: "house/status_map/scene_name", Module: "house", Name: "scene_name", Kind: "scenery.status-map", Spec: map[string]any{
			"status": map[string]any{"name": "fixed", "label": "Fixed", "variant": "neutral"},
		}},
	)
	for index := range resources {
		if resources[index].Address != "house/table_page/scenes" {
			continue
		}
		resources[index].Spec = cloneMapValue(resources[index].Spec)
		resources[index].Spec["source"] = map[string]any{"$ref": "binding.page_scenes_http"}
		resources[index].Spec["items"] = "rows"
		resources[index].Spec["filter"] = map[string]any{"name": "name", "input": "stage_filter", "status_map": map[string]any{"$ref": "status_map.scene_name"}}
		resources[index].Spec["query"] = map[string]any{"search": "q", "sort": "sort_field", "direction": "sort_direction"}
		resources[index].Spec["pagination"] = map[string]any{"page": "page_number", "page_size": "per_page", "total": "total_count"}
		resources[index].Spec["predicate"] = map[string]any{"name": "tenant_id", "value": 42}
		break
	}
	return resources
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
