package generate

import (
	"reflect"
	"strings"
	"testing"
)

func TestTypeScriptFieldConstraintsProjectExactNumericScalars(t *testing.T) {
	field := map[string]any{
		"name": "value", "type": map[string]any{"$ref": "string"},
		"min_length": map[string]any{"$scalar": "int", "value": "1"},
		"max_length": map[string]any{"$scalar": "int", "value": "128"},
		"minimum":    map[string]any{"$scalar": "decimal", "coefficient": "-125", "scale": "2"},
		"maximum":    map[string]any{"$scalar": "int", "value": "9007199254740993"},
		"pattern":    "^[a-z]+$", "unique_items": true,
	}
	want := map[string]any{
		"min_length": "1", "max_length": "128", "minimum": "-1.25", "maximum": "9007199254740993",
		"pattern": "^[a-z]+$", "unique_items": true,
	}
	if got := tsFieldConstraints(field); !reflect.DeepEqual(got, want) {
		t.Fatalf("constraints = %#v, want %#v", got, want)
	}
	registry := renderTSRegistry([]Resource{{Module: "example", Name: "input", Kind: "scenery.record", Spec: map[string]any{"field": field}}})
	if strings.Contains(registry, "$scalar") || !strings.Contains(registry, `"maximum":"9007199254740993"`) {
		t.Fatalf("registry contains internal or lossy constraints: %s", registry)
	}
	if _, ok := field["min_length"].(map[string]any); !ok {
		t.Fatal("projection mutated the compiler-owned field")
	}
}
