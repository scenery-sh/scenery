package vnext

import (
	"strings"
	"testing"
)

func TestCLIBindingRejectsReservedAndNonCanonicalCommands(t *testing.T) {
	operation := Resource{
		Address: "house/operation/run", Kind: "scenery.operation/v1", Module: "house", Name: "run",
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
				Address: "house/binding/run_cli", Kind: "scenery.binding/v1", Module: "house", Name: "run_cli",
				Spec: map[string]any{
					"operation": map[string]any{"$ref": "operation.run"}, "protocol": "cli", "delivery": "call",
					"cli": map[string]any{"command": test.command},
				},
			}
			diagnostics := validateCLIBindings([]Resource{operation, binding})
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
