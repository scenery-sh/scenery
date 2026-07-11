package vnext

import (
	"strings"
	"testing"
)

func TestRenderTSTypesHasSingleTrailingNewline(t *testing.T) {
	got := renderTSTypes(nil)
	if !strings.HasSuffix(got, "\n") {
		t.Fatal("generated TypeScript types must end with a newline")
	}
	if strings.HasSuffix(got, "\n\n") {
		t.Fatal("generated TypeScript types must not end with a blank line")
	}
}

func TestTypeScriptFetchTargetRejectsRepeatedCollectionRequestHeaders(t *testing.T) {
	target := Resource{Address: "app/typescript_client/public", Kind: "scenery.typescript-client/v1", Name: "public", Module: "app", Spec: map[string]any{
		"gateways": []any{map[string]any{"$ref": "app/http_gateway/public"}}, "package": "@test/client", "module": "esm", "runtime": "fetch", "output_root": "generated/client",
	}}
	input := Resource{Address: "house/record/get_input", Kind: "scenery.record/v1", Name: "get_input", Module: "house", Spec: map[string]any{"field": map[string]any{
		"name": "tags", "type": map[string]any{"$expression": "list(string)"},
	}}}
	operation := Resource{Address: "house/operation/get", Kind: "scenery.operation/v1", Name: "get", Module: "house", Spec: map[string]any{"input": map[string]any{"$ref": "record.get_input"}}}
	binding := Resource{Address: "house/binding/get", Kind: "scenery.binding/v1", Name: "get", Module: "house", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "operation": map[string]any{"$ref": "operation.get"}, "protocol": "http",
		"http": map[string]any{"method": "GET", "path": "/get", "header": map[string]any{"name": "x-tag", "to": map[string]any{"$ref": "operation.get.input.tags"}, "encoding": "repeated"}},
	}}
	diagnostics := validateTypeScriptTarget(target, []Resource{target, input, operation, binding})
	if !diagnosticsContain(diagnostics, "SCN6316") {
		t.Fatalf("fetch target accepted an unrepresentable repeated header: %#v", diagnostics)
	}
}
