package compiler

import "testing"

func TestContentPageExpandsToPageAndRenderer(t *testing.T) {
	resources := contentPageFixture()
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	if diagnostics := validateContentPage(byAddress, byAddress["house/content_page/summary"]); len(diagnostics) != 0 {
		t.Fatalf("validate content page: %#v", diagnostics)
	}
	expanded, diagnostics := expandContentPageResources(resources)
	if len(diagnostics) != 0 {
		t.Fatalf("expand content page: %#v", diagnostics)
	}
	byAddress = resourcesByAddress(&Manifest{Resources: expanded})
	if byAddress["house/page/summary"].Kind != "scenery.page" || stringValue(byAddress["house/renderer/summary_web"].Spec["module"]) != contentPageRendererModule {
		t.Fatalf("missing expanded content-page resources: %#v", byAddress)
	}
}

func TestContentPageRejectsInvalidContractAndCollisions(t *testing.T) {
	resources := contentPageFixture()
	for index := range resources {
		if resources[index].Address == "house/content_page/summary" {
			delete(resources[index].Spec, "content")
			resources[index].Spec["max_width"] = 0
		}
	}
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	if diagnostics := validateContentPage(byAddress, byAddress["house/content_page/summary"]); len(diagnostics) != 2 || !hasDiagnostic(diagnostics, "SCN2617") {
		t.Fatalf("invalid content page diagnostics: %#v", diagnostics)
	}

	resources = contentPageFixture()
	resources = append(resources, Resource{Address: "house/page/summary", Module: "house", Name: "summary", Kind: "scenery.page"})
	if _, diagnostics := expandContentPageResources(resources); !hasDiagnostic(diagnostics, "SCN2618") {
		t.Fatalf("collision diagnostics: %#v", diagnostics)
	}
}

func contentPageFixture() []Resource {
	return []Resource{
		{Address: "house/operation/read", Module: "house", Name: "read", Kind: "scenery.operation", Spec: map[string]any{"input": map[string]any{"$ref": "std.type.unit"}, "result": []any{map[string]any{"name": "success", "type": map[string]any{"$ref": "record.result"}}}}},
		{Address: "house/binding/read_http", Module: "house", Name: "read_http", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.read"}, "protocol": "http", "delivery": "call"}},
		{Address: "house/binding/read_internal", Module: "house", Name: "read_internal", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.read"}, "protocol": "internal", "delivery": "call", "internal": map[string]any{"principal": "inherit"}}},
		{Address: "house/react_component/content", Module: "house", Name: "content", Kind: "scenery.react-component"},
		{Address: "house/content_page/summary", Module: "house", Name: "summary", Kind: "scenery.content-page", Origin: Origin{Kind: "authored"}, Spec: map[string]any{"path": "/summary", "source": map[string]any{"$ref": "binding.read_http"}, "title": "Summary", "max_width": 960, "content": map[string]any{"component": map[string]any{"$ref": "react_component.content"}}}},
	}
}
