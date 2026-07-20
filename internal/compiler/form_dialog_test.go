package compiler

import "testing"

func TestFormDialogValidatesMutationInputFields(t *testing.T) {
	resources := []Resource{
		{Address: "house/record/create_input", Module: "house", Name: "create_input", Kind: "scenery.record", Spec: map[string]any{
			"field": []any{map[string]any{"name": "name", "type": map[string]any{"$expression": "string"}}},
		}},
		{Address: "house/record/create_result", Module: "house", Name: "create_result", Kind: "scenery.record", Spec: map[string]any{
			"field": []any{map[string]any{"name": "id", "type": map[string]any{"$expression": "string"}}},
		}},
		{Address: "house/operation/create", Module: "house", Name: "create", Kind: "scenery.operation", Spec: map[string]any{
			"input":  map[string]any{"$ref": "record.create_input"},
			"result": map[string]any{"name": "success", "type": map[string]any{"$ref": "record.create_result"}},
		}},
		{Address: "house/binding/create_http", Module: "house", Name: "create_http", Kind: "scenery.binding", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.create"}, "protocol": "http", "delivery": "call", "http": map[string]any{"method": "POST"},
		}},
		{Address: "house/form_dialog/create", Module: "house", Name: "create", Kind: "scenery.form-dialog", Spec: map[string]any{
			"source": map[string]any{"$ref": "binding.create_http"}, "title": "Create",
			"field": map[string]any{"name": "name", "control": "text"},
		}},
	}
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	if diagnostics := validateFormDialog(byAddress, resources[len(resources)-1]); hasErrors(diagnostics) {
		t.Fatalf("valid form dialog diagnostics = %#v", diagnostics)
	}
	dialog := resources[len(resources)-1]
	dialog.Spec = cloneMapValue(dialog.Spec)
	dialog.Spec["field"] = map[string]any{"name": "missing", "control": "unknown"}
	if diagnostics := validateFormDialog(byAddress, dialog); !hasDiagnostic(diagnostics, "SCN2621") {
		t.Fatalf("invalid form dialog diagnostics = %#v", diagnostics)
	}
}
