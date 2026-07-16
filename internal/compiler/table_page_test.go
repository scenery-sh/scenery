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
