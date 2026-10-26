package compiler

import (
	"strings"
	"testing"
)

func TestCLIBindingRejectsReservedAndNonCanonicalCommands(t *testing.T) {
	operation := Resource{
		Address: "house/operation/run", Kind: "scenery.operation", Module: "house", Name: "run",
		Spec: map[string]any{"input": map[string]any{"$ref": "std.type.unit"}},
	}
	for _, test := range []struct {
		name    string
		command []any
		want    string
	}{
		{name: "reserved", command: []any{"build"}, want: "reserved scenery command build"},
		{name: "uppercase", command: []any{"House", "run"}, want: "lower-kebab-case"},
		{name: "underscore", command: []any{"house", "process_scene"}, want: "lower-kebab-case"},
		{name: "trailing hyphen", command: []any{"house", "run-"}, want: "lower-kebab-case"},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding := Resource{
				Address: "house/binding/run_cli", Kind: "scenery.binding", Module: "house", Name: "run_cli",
				Spec: map[string]any{
					"operation": map[string]any{"$ref": "operation.run"}, "protocol": "cli", "delivery": "call",
					"cli": map[string]any{"command": test.command},
				},
			}
			diagnostics := validateCLIBindings([]Resource{operation, binding}, resourcesByAddress(&Manifest{Resources: []Resource{operation, binding}}))
			found := false
			for _, diagnostic := range diagnostics {
				found = found || strings.Contains(diagnostic.Message, test.want)
			}
			if !found {
				t.Fatalf("diagnostics = %#v, want message containing %q", diagnostics, test.want)
			}
		})
	}
}

func TestCLIBindingReservesLiveFlag(t *testing.T) {
	operation := Resource{Address: "house/operation/run", Kind: "scenery.operation", Module: "house", Name: "run", Spec: map[string]any{"input": map[string]any{"$ref": "std.type.unit"}}}
	binding := Resource{Address: "house/binding/run_cli", Kind: "scenery.binding", Module: "house", Name: "run_cli", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.run"}, "protocol": "cli", "delivery": "call", "cli": map[string]any{"command": []any{"run-app"}, "flag": map[string]any{"name": "live"}}}}
	resources := []Resource{operation, binding}
	for _, diagnostic := range validateCLIBindings(resources, resourcesByAddress(&Manifest{Resources: resources})) {
		if diagnostic.Code == "SCN2414" && strings.Contains(diagnostic.Message, "reserved") {
			return
		}
	}
	t.Fatal("live flag was not rejected as reserved")
}
