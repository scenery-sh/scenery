package generate

import (
	"strings"
	"testing"
)

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
	if strings.Contains(strings.ToLower(source), "scenery") {
		t.Fatal("shared failure aliases expose framework branding")
	}
	shared := "  | { readonly kind: \"failure\"; readonly name: \"invalid_request\"; readonly problem: Problem }\n" +
		"  | { readonly kind: \"failure\"; readonly name: \"rate_limited\"; readonly problem: Problem }\n"
	if !strings.Contains(source, "type _FailureSet0 =\n"+shared+";\n") || strings.Contains(source, "export type _FailureSet") {
		t.Fatalf("failure set is not an exact private union:\n%s", source)
	}
	for _, name := range []string{"First", "Second", "Subset", "None"} {
		_, tail, _ := strings.Cut(source, "export type "+name+"Outcome =\n")
		outcome, _, _ := strings.Cut(tail, ";\n")
		if got, want := strings.Contains(outcome, "_FailureSet0"), name == "First" || name == "Second"; got != want {
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
