package compiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func fieldExamplesFixture(t *testing.T, examples string) string {
	t.Helper()
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "types")
	if err := os.MkdirAll(moduleRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, appFilename), []byte("application \"examples_app\" {}\nmodule \"types\" { source = \"./types\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package "types" {
}
record "circle" {
  field "radius" { type = float64 }
}
record "square" {
  field "side" {
    type      = float64
    wire_name = "edge"
  }
}
union "shape" {
  discriminator = "kind"
  variant "circle" { type = record.circle }
  variant "square" {
    type     = record.square
    wire_tag = "sq"
  }
}
record "order" {
  field "amount" {
    type        = int64
    description = "Order amount in minor currency units."
    examples    = ["1250"]
    minimum     = 1
  }
  field "timeout" {
    type     = duration
    examples = ["1h30m"]
  }
` + examples + `
}
`
	if err := os.WriteFile(filepath.Join(moduleRoot, packageFilename), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFieldExamplesAreValidatedAndStoredInWireForm(t *testing.T) {
	result, err := Compile(fieldExamplesFixture(t, `  field "shape" {
    type     = union.shape
    examples = [{ kind = "sq", side = 2 }, { kind = "circle", radius = 1.5 }]
  }`))
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	fields := map[string]map[string]any{}
	for _, field := range namedChildren(resourcesByAddress(result.Manifest)["types/record/order"].Spec, "field") {
		fields[stringValue(field["name"])] = field
	}
	encoded, err := json.Marshal(map[string]any{"amount": fields["amount"]["examples"], "timeout": fields["timeout"]["examples"], "shape": fields["shape"]["examples"]})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"amount":  []any{"1250"},
		"timeout": []any{"PT1H30M"},
		"shape":   []any{map[string]any{"kind": "sq", "edge": 2.0}, map[string]any{"kind": "circle", "radius": 1.5}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wire examples = %s", encoded)
	}
	if fields["amount"]["description"] != "Order amount in minor currency units." {
		t.Fatalf("description = %#v", fields["amount"]["description"])
	}
}

func TestInvalidFieldExamplesAreDiagnostics(t *testing.T) {
	for name, field := range map[string]string{
		"constraint": `  field "count" {
    type     = int64
    minimum  = 1
    examples = ["0"]
  }`,
		"type": `  field "flag" {
    type     = bool
    examples = ["yes"]
  }`,
		"union tag": `  field "shape" {
    type     = union.shape
    examples = [{ kind = "triangle", side = 2 }]
  }`,
		"union payload": `  field "shape" {
    type     = union.shape
    examples = [{ kind = "circle", side = 2 }]
  }`,
	} {
		result, err := Compile(fieldExamplesFixture(t, field))
		if err != nil || !hasDiagnostic(result.Diagnostics, "SCN1234") {
			t.Fatalf("%s: err=%v diagnostics=%#v", name, err, result.Diagnostics)
		}
	}
}
