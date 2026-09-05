package generate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTypeScriptValidationUsesClientPropertiesOnly(t *testing.T) {
	source := `value.scene_id == "" || value.field_map.snake_key == ""`
	resource := Resource{Spec: map[string]any{"validation": map[string]any{"name": "required", "when": map[string]any{"$expression": source}}}}
	encoded, err := json.Marshal(tsValidationDescriptors(resource)[0].(map[string]any)["expression"])
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sceneId", "fieldMap", "snake_key"} {
		if !strings.Contains(string(encoded), `"name":"`+name+`"`) {
			t.Fatalf("missing projected name %q: %s", name, encoded)
		}
	}
	if !strings.Contains(validationProgramJSON(source), `"name":"scene_id"`) {
		t.Fatal("TypeScript projection changed the shared Go validation program")
	}
}

func TestTypeScriptValidationCapabilityFollowsReachableRecords(t *testing.T) {
	validated := Resource{Address: "geometry/record/point", Module: "geometry", Kind: "scenery.record", Name: "point", Spec: map[string]any{
		"field":      map[string]any{"name": "x", "type": map[string]any{"$ref": "float64"}},
		"validation": map[string]any{"name": "positive", "when": map[string]any{"$expression": "value.x <= 0"}, "code": "POINT_INVALID", "message": "x must be positive"},
	}}
	resources := []Resource{
		validated,
		{Address: "house/record/shape", Module: "house", Kind: "scenery.record", Name: "shape", Spec: map[string]any{"field": map[string]any{"name": "point", "type": map[string]any{"$expression": "list(geometry/record/point)"}}}},
		{Address: "house/operation/get", Module: "house", Kind: "scenery.operation", Name: "get", Spec: map[string]any{"input": map[string]any{"$ref": "house/record/shape"}}},
	}
	bindings := []Resource{{Address: "house/binding/get", Module: "house", Kind: "scenery.binding", Name: "get", Spec: map[string]any{"operation": map[string]any{"$ref": "house/operation/get"}}}}
	if !tsRuntimeCapabilitiesFor(Resource{}, bindings, resources).validation {
		t.Fatal("nested cross-module validation was omitted")
	}
	resources = append(resources[:1], resources[2:]...)
	if tsRuntimeCapabilitiesFor(Resource{}, bindings, resources).validation {
		t.Fatal("unreachable validation retained the evaluator")
	}
}

func TestTypeScriptFailureSetsPreserveExactOperationUnions(t *testing.T) {
	var operations, bindings []Resource
	for _, name := range []string{"first", "second", "subset", "none"} {
		operation := Resource{Address: "house/operation/" + name, Module: "house", Kind: "scenery.operation", Name: name, Spec: map[string]any{
			"input":  map[string]any{"$ref": "std.type.unit"},
			"result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "string"}},
			"error":  map[string]any{"name": "business_error", "type": map[string]any{"$ref": "std.type.problem"}},
		}}
		operations = append(operations, operation)
		responses := []any{}
		if name != "none" {
			responses = append(responses, map[string]any{"name": "invalid_request", "when": map[string]any{"$ref": "transport.invalid_request"}})
		}
		if name == "first" || name == "second" {
			responses = append(responses, map[string]any{"name": "rate_limited", "when": map[string]any{"$ref": "admission.rate_limited"}})
		}
		if name == "first" {
			responses = append(responses, map[string]any{"name": "accepted", "when": map[string]any{"$ref": "dispatch.enqueued"}})
		}
		bindings = append(bindings, Resource{Module: "house", Spec: map[string]any{"operation": map[string]any{"$ref": "operation." + name}, "http": map[string]any{"response": responses}}})
	}
	source := renderTSTypes(operations, bindings)
	shared := "  | { readonly kind: \"failure\"; readonly name: \"invalid_request\"; readonly problem: Problem }\n" +
		"  | { readonly kind: \"failure\"; readonly name: \"rate_limited\"; readonly problem: Problem }\n"
	if !strings.Contains(source, "type _SceneryFailureSet0 =\n"+shared+";\n") || strings.Contains(source, "export type _Scenery") {
		t.Fatalf("failure set is not an exact private union:\n%s", source)
	}
	for _, name := range []string{"First", "Second", "Subset", "None"} {
		_, tail, _ := strings.Cut(source, "export type "+name+"Outcome =\n")
		outcome, _, _ := strings.Cut(tail, ";\n")
		if got, want := strings.Contains(outcome, "_SceneryFailureSet0"), name == "First" || name == "Second"; got != want {
			t.Fatalf("%s shared failure membership = %v, want %v", name, got, want)
		}
		if !strings.Contains(outcome, `readonly kind: "error"; readonly name: "business_error"`) {
			t.Fatalf("%s lost its business error", name)
		}
		if name == "Subset" && (strings.Contains(outcome, "rate_limited") || !strings.Contains(outcome, "invalid_request")) {
			t.Fatal("subset operation failure names changed")
		}
		if name == "First" && !strings.Contains(outcome, `readonly kind: "enqueue"; readonly name: "accepted"; readonly receipt: EnqueueReceipt`) {
			t.Fatal("enqueue outcome disappeared into the failure set")
		}
	}
}
