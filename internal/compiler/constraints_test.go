package compiler

import (
	"testing"
)

func TestRecordConstraintsRejectInvalidRangesAndNonRE2Patterns(t *testing.T) {
	resources := []Resource{{Address: "house/record/input", Module: "house", Kind: "scenery.record", Name: "input", Spec: map[string]any{
		"field": []any{
			map[string]any{"name": "name", "type": map[string]any{"$ref": "string"}, "min_length": "10", "max_length": "2", "pattern": "(?=bad)"},
			map[string]any{"name": "count", "type": map[string]any{"$ref": "int64"}, "minimum": "100", "maximum": "1"},
		},
	}}}
	diagnostics := validateConstraints(resources)
	for _, code := range []string{"SCN1224", "SCN1226", "SCN1227"} {
		if !diagnosticsContain(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestTypeScriptDescriptorRetainsFieldConstraints(t *testing.T) {
	resource := Resource{Address: "house/record/input", Module: "house", Kind: "scenery.record", Name: "input", Spec: map[string]any{
		"field": map[string]any{"name": "name", "type": map[string]any{"$ref": "string"}, "min_length": "1", "max_length": "20", "pattern": "^[a-z]+$"},
	}}
	descriptor := tsNamedDescriptor(resource)
	record := descriptor.(map[string]any)
	fields := record["fields"].([]any)
	constraints := fields[0].(map[string]any)["constraints"].(map[string]any)
	if constraints["min_length"] != "1" || constraints["pattern"] != "^[a-z]+$" {
		t.Fatalf("constraints = %#v", constraints)
	}
}
