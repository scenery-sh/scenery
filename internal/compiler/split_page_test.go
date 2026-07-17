package compiler

import "testing"

func TestSplitPageExpandsToPageAndRenderer(t *testing.T) {
	resources := []Resource{
		{Address: "house/operation/read", Module: "house", Name: "read", Kind: "scenery.operation", Spec: map[string]any{"input": map[string]any{"$ref": "std.type.unit"}, "result": []any{map[string]any{"name": "success", "type": map[string]any{"$ref": "record.result"}}}}},
		{Address: "house/binding/read_http", Module: "house", Name: "read_http", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.read"}, "protocol": "http", "delivery": "call"}},
		{Address: "house/binding/read_internal", Module: "house", Name: "read_internal", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.read"}, "protocol": "internal", "delivery": "call", "internal": map[string]any{"principal": "inherit"}}},
		{Address: "house/react_component/sidebar", Module: "house", Name: "sidebar", Kind: "scenery.react-component"},
		{Address: "house/react_component/detail", Module: "house", Name: "detail", Kind: "scenery.react-component"},
		{Address: "house/split_page/inbox", Module: "house", Name: "inbox", Kind: "scenery.split-page", Origin: Origin{Kind: "authored"}, Spec: map[string]any{"path": "/inbox", "source": map[string]any{"$ref": "binding.read_http"}, "title": "Inbox", "sidebar": map[string]any{"component": map[string]any{"$ref": "react_component.sidebar"}}, "detail": map[string]any{"component": map[string]any{"$ref": "react_component.detail"}}}},
	}
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	if diagnostics := validateSplitPage(byAddress, byAddress["house/split_page/inbox"]); len(diagnostics) != 0 {
		t.Fatalf("validate split page: %#v", diagnostics)
	}
	expanded, diagnostics := expandSplitPageResources(resources)
	if len(diagnostics) != 0 {
		t.Fatalf("expand split page: %#v", diagnostics)
	}
	byAddress = resourcesByAddress(&Manifest{Resources: expanded})
	if byAddress["house/page/inbox"].Kind != "scenery.page" || stringValue(byAddress["house/renderer/inbox_web"].Spec["module"]) != splitPageRendererModule {
		t.Fatalf("missing expanded split-page resources: %#v", byAddress)
	}
}
