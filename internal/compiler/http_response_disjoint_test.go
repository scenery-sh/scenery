package compiler

import "testing"

func TestSameStatusCompletionsRequireObservableDisjointness(t *testing.T) {
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation", Name: "get", Spec: map[string]any{
		"input": map[string]any{"$ref": "std.type.unit"},
		"result": []any{
			map[string]any{"name": "found", "type": map[string]any{"$ref": "string"}},
			map[string]any{"name": "snapshot", "type": map[string]any{"$ref": "string"}},
		},
	}}
	binding := Resource{Address: "house/binding/get", Module: "house", Kind: "scenery.binding", Name: "get", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.get"}, "delivery": "call",
		"http": map[string]any{"response": []any{
			map[string]any{"name": "found", "when": map[string]any{"$ref": "result.found"}, "status": "200", "body": map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.found"}}},
			map[string]any{"name": "snapshot", "when": map[string]any{"$ref": "result.snapshot"}, "status": "200", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.snapshot"}}},
		}},
	}}
	if diagnostics := validateHTTPResponses(map[string]Resource{}, binding, operation, binding.Spec["http"].(map[string]any)); hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("wire-distinguishable completions were rejected: %#v", diagnostics)
	}
	duplicate := map[string]any{"response": []any{
		map[string]any{"name": "found", "when": map[string]any{"$ref": "result.found"}, "status": "200", "body": map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.found"}}},
		map[string]any{"name": "snapshot", "when": map[string]any{"$ref": "result.snapshot"}, "status": "200", "body": map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.snapshot"}}},
	}}
	if diagnostics := validateHTTPResponses(map[string]Resource{}, binding, operation, duplicate); !hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("wire-indistinguishable completions were accepted: %#v", diagnostics)
	}
}

func TestSameStatusCompletionDisjointnessUsesObservableWireShape(t *testing.T) {
	left := Resource{Address: "house/record/left", Module: "house", Kind: "scenery.record", Name: "left", Spec: map[string]any{"field": map[string]any{"name": "left_id", "wire_name": "id", "type": map[string]any{"$ref": "string"}}}}
	right := Resource{Address: "house/record/right", Module: "house", Kind: "scenery.record", Name: "right", Spec: map[string]any{"field": map[string]any{"name": "right_id", "wire_name": "id", "type": map[string]any{"$ref": "string"}}}}
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation", Name: "get", Spec: map[string]any{"result": []any{
		map[string]any{"name": "left", "type": map[string]any{"$ref": "record.left"}}, map[string]any{"name": "right", "type": map[string]any{"$ref": "record.right"}},
	}}}
	responses := []any{
		map[string]any{"name": "left", "when": map[string]any{"$ref": "result.left"}, "status": "200", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.left"}}},
		map[string]any{"name": "right", "when": map[string]any{"$ref": "result.right"}, "status": "200", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.right"}}},
	}
	binding := Resource{Address: "house/binding/get", Module: "house", Kind: "scenery.binding", Spec: map[string]any{"delivery": "call"}}
	resources := map[string]Resource{left.Address: left, right.Address: right}
	if diagnostics := validateHTTPResponses(resources, binding, operation, map[string]any{"response": responses}); !hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("wire-identical records were accepted: %#v", diagnostics)
	}
	right.Spec["field"].(map[string]any)["wire_name"] = "other_id"
	resources[right.Address] = right
	if diagnostics := validateHTTPResponses(resources, binding, operation, map[string]any{"response": responses}); hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("disjoint records were rejected: %#v", diagnostics)
	}
}
