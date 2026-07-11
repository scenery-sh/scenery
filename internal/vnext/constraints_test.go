package vnext

import (
	"strings"
	"testing"
)

func TestRecordConstraintsRejectInvalidRangesAndNonRE2Patterns(t *testing.T) {
	resources := []Resource{{Address: "house/record/input", Module: "house", Kind: "scenery.record/v1", Name: "input", Spec: map[string]any{
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
	resource := Resource{Address: "house/record/input", Module: "house", Kind: "scenery.record/v1", Name: "input", Spec: map[string]any{
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

func TestRecordCrossFieldValidationIsTypedAndGenerated(t *testing.T) {
	resource := Resource{Address: "house/record/run_input", Module: "house", Kind: "scenery.record/v1", Name: "run_input", Spec: map[string]any{
		"field": []any{
			map[string]any{"name": "start_at", "type": map[string]any{"$ref": "datetime"}},
			map[string]any{"name": "end_at", "type": map[string]any{"$ref": "datetime"}},
		},
		"validation": map[string]any{
			"name": "end_after_start", "when": map[string]any{"$expression": "value.end_at <= value.start_at"},
			"code": "HOUSE_INVALID_TIME_RANGE", "message": "end_at must be later than start_at", "path": map[string]any{"$ref": "record.run_input.end_at"},
		},
	}}
	if diagnostics := validateTypeSystem([]Resource{resource}); hasErrors(diagnostics) {
		t.Fatalf("valid record validation diagnostics = %#v", diagnostics)
	}
	source := renderContractTypes([]Resource{resource})
	for _, fragment := range []string{"ValidateContractRecord", "value.end_at <= value.start_at", "HOUSE_INVALID_TIME_RANGE", "record.run_input.end_at"} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("generated record validation missing %q:\n%s", fragment, source)
		}
	}
	descriptor := tsNamedDescriptor(resource).(map[string]any)
	validations := descriptor["validations"].([]any)
	if len(validations) != 1 {
		t.Fatalf("TypeScript validation descriptors = %#v", validations)
	}
	rule := validations[0].(map[string]any)
	expression := rule["expression"].(map[string]any)
	if rule["code"] != "HOUSE_INVALID_TIME_RANGE" || expression["kind"] != "binary" || expression["operator"] != "<=" {
		t.Fatalf("TypeScript validation descriptor = %#v", rule)
	}

	resource.Spec["validation"].(map[string]any)["when"] = map[string]any{"$expression": "input.end_at <= value.start_at"}
	if diagnostics := validateTypeSystem([]Resource{resource}); !hasDiagnostic(diagnostics, "SCN1233") {
		t.Fatalf("invalid validation root was accepted: %#v", diagnostics)
	}
	if err := validateRecordValidationExpression("length([for item in value.items : item]) == 0", map[string]string{"items": "list(string)"}); err == nil || !strings.Contains(err.Error(), "unsupported validation expression") {
		t.Fatalf("unsupported validation comprehension error = %v", err)
	}
}

func TestRecordValidationCompilerRejectsUnknownInputsAndAllowsPureCoreFunctions(t *testing.T) {
	fields := map[string]string{"name": "string"}
	for _, expression := range []string{`input.name == "roof"`, `value.missing == "roof"`, `network(value.name)`} {
		if err := validateRecordValidationExpression(expression, fields); err == nil {
			t.Errorf("expression %q was accepted", expression)
		}
	}
	if err := validateRecordValidationExpression(`starts_with(lower(value.name), "roof")`, fields); err != nil {
		t.Fatalf("core pure functions rejected: %v", err)
	}
}
