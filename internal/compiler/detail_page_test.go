package compiler

import (
	"strings"
	"testing"
)

func TestDetailPageExpandsWithNormalizedParamsAndLineage(t *testing.T) {
	resources := detailPageFixture()
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	detail := byAddress["house/detail_page/scene"]
	if diagnostics := validateDetailPage(byAddress, detail); len(diagnostics) != 0 {
		t.Fatalf("validate detail page: %#v", diagnostics)
	}
	expanded, diagnostics := expandDetailPageResources(resources)
	if len(diagnostics) != 0 {
		t.Fatalf("expand detail page: %#v", diagnostics)
	}
	byAddress = resourcesByAddress(&Manifest{Resources: expanded})
	page, renderer := byAddress["house/page/scene"], byAddress["house/renderer/scene_web"]
	if page.Kind != "scenery.page" || refString(page.Spec["load"]) != "house/binding/read_internal" {
		t.Fatalf("expanded page = %#v", page)
	}
	params := namedChildren(page.Spec, "param")
	if len(params) != 1 || stringValue(params[0]["name"]) != "scene_id" || stringValue(params[0]["input"]) != "id" {
		t.Fatalf("normalized params = %#v", params)
	}
	if renderer.Kind != "scenery.renderer" || stringValue(renderer.Spec["module"]) != detailPageRendererModule || refString(renderer.Spec["page"]) != page.Address {
		t.Fatalf("expanded renderer = %#v", renderer)
	}
	for _, resource := range []Resource{page, renderer} {
		if resource.Origin.Kind != "expanded" || len(resource.Origin.ExpansionLineage) != 1 || resource.Origin.ExpansionLineage[0].Generator != detail.Address || resource.Origin.ExpansionLineage[0].GeneratorSchemaRevision != "scenery.detail-page" {
			t.Fatalf("lineage for %s = %#v", resource.Address, resource.Origin)
		}
	}
	if diagnostics := validatePageBindings(byAddress, page); len(diagnostics) != 0 {
		t.Fatalf("expanded page binding diagnostics = %#v", diagnostics)
	}
}

func TestDetailPageResolvesPathParamByNameWithoutOverride(t *testing.T) {
	resources := detailPageFixture()
	detail := detailPageResource(resources)
	detail.Spec["path"] = "/scenes/{id}"
	delete(detail.Spec, "param")
	orderedChildren(detail.Spec, "table")[0]["param"] = "id"
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	if diagnostics := validateDetailPage(byAddress, *detail); len(diagnostics) != 0 {
		t.Fatalf("implicit by-name param diagnostics = %#v", diagnostics)
	}
	expanded, diagnostics := expandDetailPageResources(resources)
	if len(diagnostics) != 0 {
		t.Fatalf("expand implicit by-name param = %#v", diagnostics)
	}
	page := resourcesByAddress(&Manifest{Resources: expanded})["house/page/scene"]
	params := namedChildren(page.Spec, "param")
	if len(params) != 1 || stringValue(params[0]["name"]) != "id" || stringValue(params[0]["input"]) != "id" {
		t.Fatalf("implicit normalized params = %#v", params)
	}
}

func TestDetailPageContractDiagnosticMatrix(t *testing.T) {
	tests := []struct {
		name    string
		message string
		mutate  func([]Resource)
	}{
		{"presentation", "presentation must", func(resources []Resource) { detailPageResource(resources).Spec["presentation"] = "drawer" }},
		{"not found completion", "mapped by its HTTP binding to status 404", func(resources []Resource) {
			binding := resourceByAddress(resources, "house/binding/read_http")
			binding.Spec["http"] = map[string]any{"response": map[string]any{"name": "success", "when": map[string]any{"$ref": "result.success"}, "status": "200"}}
		}},
		{"unresolved param", "does not resolve", func(resources []Resource) {
			namedChildren(detailPageResource(resources).Spec, "param")[0]["input"] = "missing"
		}},
		{"incompatible param", "requires a scalar", func(resources []Resource) {
			input := resourceByAddress(resources, "house/record/read_input")
			namedChildren(input.Spec, "field")[0]["type"] = map[string]any{"$expression": "list(string)"}
		}},
		{"double claimed input", "must not claim", func(resources []Resource) {
			detail := detailPageResource(resources)
			detail.Spec["path"] = "/scenes/{scene_id}/{other_id}"
			detail.Spec["param"] = []any{map[string]any{"name": "scene_id", "input": "id"}, map[string]any{"name": "other_id", "input": "id"}}
		}},
		{"override not in path", "is not present", func(resources []Resource) {
			detailPageResource(resources).Spec["param"] = []any{map[string]any{"name": "other_id", "input": "id"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resources := detailPageFixture()
			test.mutate(resources)
			diagnostics := validateDetailPage(resourcesByAddress(&Manifest{Resources: resources}), *detailPageResource(resources))
			if !diagnosticMessageContains(diagnostics, "SCN2629", test.message) {
				t.Fatalf("diagnostics = %#v, want SCN2629 containing %q", diagnostics, test.message)
			}
		})
	}
}

func TestDetailPageSectionActionAndRelatedTableDiagnostics(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		message string
		mutate  func([]Resource)
	}{
		{"section field", "SCN2630", "top-level result fields", func(resources []Resource) {
			namedChildren(orderedChildren(detailPageResource(resources).Spec, "section")[0], "field")[0]["name"] = "missing"
		}},
		{"section status map", "SCN2630", "status_map requires", func(resources []Resource) {
			field := namedChildren(orderedChildren(detailPageResource(resources).Spec, "section")[0], "field")[0]
			field["status_map"] = map[string]any{"$ref": "status_map.scene_status"}
		}},
		{"section hide empty", "SCN2630", "hide_empty must be a boolean", func(resources []Resource) {
			field := namedChildren(orderedChildren(detailPageResource(resources).Spec, "section")[0], "field")[0]
			field["hide_empty"] = "yes"
		}},
		{"action reference", "SCN2631", "same-module form_dialog", func(resources []Resource) {
			orderedChildren(detailPageResource(resources).Spec, "action")[0]["dialog"] = map[string]any{"$ref": "form_dialog.missing"}
		}},
		{"action seed", "SCN2631", "match result fields", func(resources []Resource) {
			result := resourceByAddress(resources, "house/record/scene")
			for _, field := range namedChildren(result.Spec, "field") {
				if stringValue(field["name"]) == "name" {
					field["type"] = map[string]any{"$ref": "int64"}
				}
			}
		}},
		{"actions slot", "SCN2631", "actions slot", func(resources []Resource) {
			detailPageResource(resources).Spec["actions"] = map[string]any{"component": map[string]any{"$ref": "react_component.missing"}}
		}},
		{"related param", "SCN2632", "requires a path parameter", func(resources []Resource) {
			orderedChildren(detailPageResource(resources).Spec, "table")[0]["param"] = "other_id"
		}},
		{"related incompatible input", "SCN2632", "type-compatible operation input", func(resources []Resource) {
			query := resourceByAddress(resources, "house/record/event_query")
			namedChildren(query.Spec, "field")[0]["type"] = map[string]any{"$ref": "int64"}
		}},
		{"related double claim", "SCN2632", "already claimed", func(resources []Resource) {
			related := resourceByAddress(resources, "house/table_page/events")
			related.Spec["predicate"] = []any{map[string]any{"name": "scene_id", "value": "fixed"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resources := detailPageFixture()
			test.mutate(resources)
			diagnostics := validateDetailPage(resourcesByAddress(&Manifest{Resources: resources}), *detailPageResource(resources))
			if !diagnosticMessageContains(diagnostics, test.code, test.message) {
				t.Fatalf("diagnostics = %#v, want %s containing %q", diagnostics, test.code, test.message)
			}
		})
	}
}

func TestDetailPageExpansionRejectsMissingInternalBindingAndCollisions(t *testing.T) {
	resources := detailPageFixture()
	for index := range resources {
		if resources[index].Address == "house/binding/read_internal" {
			resources = append(resources[:index], resources[index+1:]...)
			break
		}
	}
	if _, diagnostics := expandDetailPageResources(resources); !diagnosticMessageContains(diagnostics, "SCN2629", "inherited internal binding") {
		t.Fatalf("missing internal binding diagnostics = %#v", diagnostics)
	}

	resources = detailPageFixture()
	resources = append(resources, Resource{Address: "house/page/scene", Module: "house", Name: "scene", Kind: "scenery.page"})
	if _, diagnostics := expandDetailPageResources(resources); !hasDiagnostic(diagnostics, "SCN2633") {
		t.Fatalf("collision diagnostics = %#v", diagnostics)
	}
}

func diagnosticMessageContains(diagnostics []Diagnostic, code, text string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && strings.Contains(diagnostic.Message, text) {
			return true
		}
	}
	return false
}

func detailPageResource(resources []Resource) *Resource {
	return resourceByAddress(resources, "house/detail_page/scene")
}

func resourceByAddress(resources []Resource, address string) *Resource {
	for index := range resources {
		if resources[index].Address == address {
			return &resources[index]
		}
	}
	return nil
}

func detailPageFixture() []Resource {
	field := func(name, expression string) map[string]any {
		return map[string]any{"name": name, "type": map[string]any{"$ref": expression}}
	}
	return []Resource{
		{Address: "house/record/read_input", Module: "house", Name: "read_input", Kind: "scenery.record", Spec: map[string]any{"field": []any{field("id", "string")}}},
		{Address: "house/record/scene", Module: "house", Name: "scene", Kind: "scenery.record", Spec: map[string]any{"field": []any{field("id", "string"), field("name", "string"), field("status", "string")}}},
		{Address: "house/operation/read", Module: "house", Name: "read", Kind: "scenery.operation", Spec: map[string]any{"input": map[string]any{"$ref": "record.read_input"}, "result": []any{map[string]any{"name": "success", "type": map[string]any{"$ref": "record.scene"}}}, "error": []any{map[string]any{"name": "not_found", "type": map[string]any{"$ref": "std.type.problem"}}}}},
		{Address: "house/binding/read_http", Module: "house", Name: "read_http", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.read"}, "protocol": "http", "delivery": "call", "http": map[string]any{"response": []any{map[string]any{"name": "success", "when": map[string]any{"$ref": "result.success"}, "status": "200"}, map[string]any{"name": "not_found", "when": map[string]any{"$ref": "error.not_found"}, "status": "404"}}}}},
		{Address: "house/binding/read_internal", Module: "house", Name: "read_internal", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.read"}, "protocol": "internal", "delivery": "call", "internal": map[string]any{"principal": "inherit"}}},
		{Address: "house/record/update_input", Module: "house", Name: "update_input", Kind: "scenery.record", Spec: map[string]any{"field": []any{field("id", "string"), field("name", "string")}}},
		{Address: "house/operation/update", Module: "house", Name: "update", Kind: "scenery.operation", Spec: map[string]any{"input": map[string]any{"$ref": "record.update_input"}}},
		{Address: "house/binding/update_http", Module: "house", Name: "update_http", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.update"}, "protocol": "http", "delivery": "call", "http": map[string]any{"method": "POST"}}},
		{Address: "house/form_dialog/update", Module: "house", Name: "update", Kind: "scenery.form-dialog", Spec: map[string]any{"source": map[string]any{"$ref": "binding.update_http"}, "title": "Update"}},
		{Address: "house/react_component/actions", Module: "house", Name: "actions", Kind: "scenery.react-component"},
		{Address: "house/record/event", Module: "house", Name: "event", Kind: "scenery.record", Spec: map[string]any{"field": []any{field("id", "string"), field("message", "string")}}},
		{Address: "house/record/event_list", Module: "house", Name: "event_list", Kind: "scenery.record", Spec: map[string]any{"field": []any{map[string]any{"name": "items", "type": map[string]any{"$expression": "list(record.event)"}}}}},
		{Address: "house/record/event_query", Module: "house", Name: "event_query", Kind: "scenery.record", Spec: map[string]any{"field": []any{field("scene_id", "string")}}},
		{Address: "house/operation/events", Module: "house", Name: "events", Kind: "scenery.operation", Spec: map[string]any{"input": map[string]any{"$ref": "record.event_query"}, "result": []any{map[string]any{"name": "success", "type": map[string]any{"$ref": "record.event_list"}}}}},
		{Address: "house/binding/events_http", Module: "house", Name: "events_http", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.events"}, "protocol": "http", "delivery": "call"}},
		{Address: "house/table_page/events", Module: "house", Name: "events", Kind: "scenery.table-page", Origin: Origin{Kind: "authored"}, Spec: map[string]any{"path": "/events", "source": map[string]any{"$ref": "binding.events_http"}, "items": "items", "title": "Events", "column": []any{map[string]any{"name": "id", "label": "ID"}}}},
		{Address: "house/status_map/scene_status", Module: "house", Name: "scene_status", Kind: "scenery.status-map"},
		{Address: "house/detail_page/scene", Module: "house", Name: "scene", Kind: "scenery.detail-page", Origin: Origin{Kind: "authored", SourceID: "house"}, Spec: map[string]any{
			"path": "/scenes/{scene_id}", "source": map[string]any{"$ref": "binding.read_http"}, "title": "Scene", "presentation": "both",
			"param":   []any{map[string]any{"name": "scene_id", "input": "id"}},
			"section": []any{map[string]any{"name": "summary", "label": "Summary", "field": []any{map[string]any{"name": "id", "label": "ID"}, map[string]any{"name": "name", "label": "Name"}}}},
			"action":  []any{map[string]any{"name": "update", "label": "Update", "dialog": map[string]any{"$ref": "form_dialog.update"}}},
			"table":   []any{map[string]any{"name": "events", "label": "Events", "page": map[string]any{"$ref": "table_page.events"}, "param": "scene_id", "input": "scene_id"}},
		}},
	}
}
