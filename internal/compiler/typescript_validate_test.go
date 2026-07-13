package compiler

import "testing"

func TestTypeScriptFetchTargetRejectsRepeatedCollectionRequestHeaders(t *testing.T) {
	target := Resource{Address: "app/typescript_client/public", Kind: "scenery.typescript-client", Name: "public", Module: "app", Spec: map[string]any{
		"gateways": []any{map[string]any{"$ref": "app/http_gateway/public"}}, "package": "@test/client", "module": "esm", "runtime": "fetch", "output_root": "generated/client",
	}}
	input := Resource{Address: "house/record/get_input", Kind: "scenery.record", Name: "get_input", Module: "house", Spec: map[string]any{"field": map[string]any{
		"name": "tags", "type": map[string]any{"$expression": "list(string)"},
	}}}
	operation := Resource{Address: "house/operation/get", Kind: "scenery.operation", Name: "get", Module: "house", Spec: map[string]any{"input": map[string]any{"$ref": "record.get_input"}}}
	binding := Resource{Address: "house/binding/get", Kind: "scenery.binding", Name: "get", Module: "house", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "operation": map[string]any{"$ref": "operation.get"}, "protocol": "http",
		"http": map[string]any{"method": "GET", "path": "/get", "header": map[string]any{"name": "x-tag", "to": map[string]any{"$ref": "operation.get.input.tags"}, "encoding": "repeated"}},
	}}
	diagnostics := validateTypeScriptTarget(target, []Resource{target, input, operation, binding})
	if !diagnosticsContain(diagnostics, "SCN6316") {
		t.Fatalf("fetch target accepted an unrepresentable repeated header: %#v", diagnostics)
	}
}

func TestTypeScriptNameValidationReservesRuntimeAndConstructorNames(t *testing.T) {
	target := Resource{Address: "app/typescript_client/public", Module: "app", Kind: "scenery.typescript-client", Name: "public"}
	resources := []Resource{
		{Address: "house/record/json_value", Module: "house", Kind: "scenery.record", Name: "json_value", Spec: map[string]any{}},
		{Address: "house/operation/constructor", Module: "house", Kind: "scenery.operation", Name: "constructor", Spec: map[string]any{"input": map[string]any{"$ref": "house/record/json_value"}}},
	}
	bindings := []Resource{{Address: "house/binding/constructor", Module: "house", Kind: "scenery.binding", Name: "constructor", Spec: map[string]any{"operation": map[string]any{"$ref": "house/operation/constructor"}}}}
	diagnostics := validateTypeScriptNames(target, resources, bindings)
	for _, code := range []string{"SCN6310", "SCN6312"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Fatalf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestTypeScriptNameValidationReservesPreservedUnknownField(t *testing.T) {
	target := Resource{Address: "app/typescript_client/public", Module: "app", Kind: "scenery.typescript-client", Name: "public"}
	record := Resource{Address: "house/record/item", Module: "house", Kind: "scenery.record", Name: "item", Spec: map[string]any{
		"unknown_fields": "preserve", "field": map[string]any{"name": "unknown_fields", "type": map[string]any{"$ref": "json"}},
	}}
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation", Name: "get", Spec: map[string]any{"input": map[string]any{"$ref": "record.item"}}}
	binding := Resource{Address: "house/binding/get", Module: "house", Kind: "scenery.binding", Name: "get", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.get"}}}
	if diagnostics := validateTypeScriptNames(target, []Resource{record, operation}, []Resource{binding}); !hasDiagnostic(diagnostics, "SCN6311") {
		t.Fatalf("missing preserved unknown-field collision: %#v", diagnostics)
	}
}

func TestTypeScriptRetryRequiresIdempotentOperation(t *testing.T) {
	target := Resource{Address: "app/typescript_client/public", Kind: "scenery.typescript-client", Name: "public", Module: "app", Spec: map[string]any{
		"gateways": []any{map[string]any{"$ref": "http_gateway.public"}}, "package": "@test/client", "module": "esm", "runtime": "fetch", "output_root": "generated/client",
		"retry": map[string]any{"policy": "scenery.retry.idempotent", "maximum_attempts": "3"},
	}}
	input := Resource{Address: "house/record/get_input", Kind: "scenery.record", Name: "get_input", Module: "house", Spec: map[string]any{"field": map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}}}}
	operation := Resource{Address: "house/operation/get", Kind: "scenery.operation", Name: "get", Module: "house", Spec: map[string]any{"input": map[string]any{"$ref": "record.get_input"}}}
	binding := Resource{Address: "house/binding/get", Kind: "scenery.binding", Name: "get", Module: "house", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"gateway": map[string]any{"$ref": "http_gateway.public"}, "operation": map[string]any{"$ref": "operation.get"}, "protocol": "http", "http": map[string]any{"method": "POST", "path": "/get", "body": map[string]any{"codec": "json"}},
	}}
	resources := []Resource{target, input, operation, binding}
	if diagnostics := validateTypeScriptTarget(target, resources); !diagnosticsContain(diagnostics, "SCN6309") {
		t.Fatalf("non-idempotent operation was accepted: %#v", diagnostics)
	}
	operation.Spec["idempotency"] = map[string]any{"mode": "keyed"}
	resources[2] = operation
	if diagnostics := validateTypeScriptTarget(target, resources); !diagnosticsContain(diagnostics, "SCN6309") {
		t.Fatalf("keyed operation without a key was accepted: %#v", diagnostics)
	}
	operation.Spec["idempotency"] = map[string]any{"mode": "keyed", "key": []any{map[string]any{"$expression": "input.id"}}}
	resources[2] = operation
	if diagnostics := validateTypeScriptTarget(target, resources); diagnosticsContain(diagnostics, "SCN6309") {
		t.Fatalf("idempotent operation was rejected: %#v", diagnostics)
	}
}
