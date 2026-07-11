package vnext

import (
	"fmt"
	"strings"
	"testing"
)

func TestTypeScriptClientReturnsDeclaredTransportFailuresAsTypedOutcomes(t *testing.T) {
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation/v1", Name: "get", Spec: map[string]any{
		"input": map[string]any{"$ref": "std.type.unit"}, "result": map[string]any{"name": "found", "type": map[string]any{"$ref": "string"}},
	}}
	binding := Resource{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Name: "get", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.get"}, "http": map[string]any{"method": "GET", "path": "/get", "response": []any{
			map[string]any{"name": "found", "when": map[string]any{"$ref": "result.found"}, "status": "200", "body": map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.found"}}},
			map[string]any{"name": "invalid_request", "when": map[string]any{"$ref": "transport.invalid_request"}, "status": "400", "body": map[string]any{"codec": "problem_json", "from": map[string]any{"$ref": "transport.problem"}}},
		}},
	}}
	types := renderTSTypes([]Resource{operation}, []Resource{binding})
	if !strings.Contains(types, `readonly kind: "failure"; readonly name: "invalid_request"; readonly problem: Problem`) {
		t.Fatalf("types omit failure outcome:\n%s", types)
	}
	client := renderTSClient(Resource{Name: "public"}, []Resource{binding}, []Resource{operation})
	if !strings.Contains(client, `return { kind: "failure", name: "invalid_request", problem: payload as Types.Problem }`) {
		t.Fatalf("client does not return typed failure:\n%s", client)
	}
	if strings.Contains(client, `server returned transport.invalid_request`) {
		t.Fatalf("declared transport failure is thrown instead of returned:\n%s", client)
	}
}

func TestTypeScriptClientSelectsSameStatusCompletionsByTypedMapping(t *testing.T) {
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation/v1", Name: "get", Spec: map[string]any{
		"input": map[string]any{"$ref": "std.type.unit"},
		"result": []any{
			map[string]any{"name": "found", "type": map[string]any{"$ref": "string"}},
			map[string]any{"name": "snapshot", "type": map[string]any{"$ref": "string"}},
		},
	}}
	binding := Resource{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Name: "get", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.get"}, "delivery": "call",
		"http": map[string]any{"method": "GET", "path": "/get", "response": []any{
			map[string]any{"name": "found", "when": map[string]any{"$ref": "result.found"}, "status": "200", "body": map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.found"}}},
			map[string]any{"name": "snapshot", "when": map[string]any{"$ref": "result.snapshot"}, "status": "200", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.snapshot"}}},
		}},
	}}
	if diagnostics := validateHTTPResponses(map[string]Resource{}, binding, operation, binding.Spec["http"].(map[string]any)); hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("wire-distinguishable same-status completions were rejected: %#v", diagnostics)
	}
	client := renderTSClient(Resource{Name: "public"}, []Resource{binding}, []Resource{operation})
	for _, fragment := range []string{
		"const completionMatches: Types.GetOutcome[] = []",
		`completionMatches.push({ kind: "result", name: "found"`,
		`completionMatches.push({ kind: "result", name: "snapshot"`,
		"if (completionMatches.length === 1) return completionMatches[0]!",
	} {
		if !strings.Contains(client, fragment) {
			t.Fatalf("client missing %q:\n%s", fragment, client)
		}
	}

	duplicate := binding.Spec["http"].(map[string]any)
	duplicate["response"] = []any{
		map[string]any{"name": "found", "when": map[string]any{"$ref": "result.found"}, "status": "200", "body": map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.found"}}},
		map[string]any{"name": "snapshot", "when": map[string]any{"$ref": "result.snapshot"}, "status": "200", "body": map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.snapshot"}}},
	}
	if diagnostics := validateHTTPResponses(map[string]Resource{}, binding, operation, duplicate); !hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("wire-indistinguishable same-status completions were accepted: %#v", diagnostics)
	}
}

func TestSameStatusCompletionDisjointnessUsesObservableWireShape(t *testing.T) {
	left := Resource{Address: "house/record/left", Module: "house", Kind: "scenery.record/v1", Name: "left", Spec: map[string]any{"field": map[string]any{
		"name": "left_id", "wire_name": "id", "type": map[string]any{"$ref": "string"},
	}}}
	right := Resource{Address: "house/record/right", Module: "house", Kind: "scenery.record/v1", Name: "right", Spec: map[string]any{"field": map[string]any{
		"name": "right_id", "wire_name": "id", "type": map[string]any{"$ref": "string"},
	}}}
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation/v1", Name: "get", Spec: map[string]any{
		"result": []any{
			map[string]any{"name": "left", "type": map[string]any{"$ref": "record.left"}},
			map[string]any{"name": "right", "type": map[string]any{"$ref": "record.right"}},
		},
	}}
	responses := []any{
		map[string]any{"name": "left", "when": map[string]any{"$ref": "result.left"}, "status": "200", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.left"}}},
		map[string]any{"name": "right", "when": map[string]any{"$ref": "result.right"}, "status": "200", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.right"}}},
	}
	binding := Resource{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{"delivery": "call"}}
	resources := map[string]Resource{left.Address: left, right.Address: right}
	if diagnostics := validateHTTPResponses(resources, binding, operation, map[string]any{"response": responses}); !hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("nominally different but wire-identical records were accepted: %#v", diagnostics)
	}

	right.Spec["field"].(map[string]any)["wire_name"] = "other_id"
	resources[right.Address] = right
	if diagnostics := validateHTTPResponses(resources, binding, operation, map[string]any{"response": responses}); hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("closed records with disjoint required wire fields were rejected: %#v", diagnostics)
	}
}

func TestTypeScriptClientReconstructsResponseHeadersCookiesAndCamelCaseFields(t *testing.T) {
	output := Resource{Address: "house/record/output", Module: "house", Kind: "scenery.record/v1", Name: "output", Spec: map[string]any{
		"field": []any{
			map[string]any{"name": "status_message", "type": map[string]any{"$ref": "string"}},
			map[string]any{"name": "request_id", "type": map[string]any{"$ref": "uuid"}},
			map[string]any{"name": "session_token", "type": map[string]any{"$expression": "optional(string)"}},
		},
	}}
	operation := Resource{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Name: "process", Spec: map[string]any{
		"input": map[string]any{"$ref": "string"}, "result": map[string]any{"name": "processed", "type": map[string]any{"$ref": "record.output"}},
	}}
	binding := Resource{Address: "house/binding/process", Module: "house", Kind: "scenery.binding/v1", Name: "process", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.process"}, "delivery": "call",
		"http": map[string]any{"method": "GET", "path": "/process", "response": map[string]any{
			"name": "processed", "when": map[string]any{"$ref": "result.processed"}, "status": "200",
			"body":   map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.processed.status_message"}},
			"header": map[string]any{"name": "x-request-id", "from": map[string]any{"$ref": "result.processed.request_id"}, "encoding": "repeated"},
			"cookie": map[string]any{"name": "session", "from": map[string]any{"$ref": "result.processed.session_token"}},
		}},
	}}
	resources := []Resource{output, operation}
	client := renderTSClient(Resource{Name: "public"}, []Resource{binding}, resources)
	for _, fragment := range []string{
		`mergeResponseValue(payload, ["statusMessage"], decoded, binding)`,
		`decodeResponseHeader(response, "x-request-id", "repeated"`,
		`mergeResponseValue(payload, ["requestId"]`,
		`decodeResponseCookie(response, "session"`,
		`mergeResponseValue(payload, ["sessionToken"]`,
	} {
		if !strings.Contains(client, fragment) {
			t.Fatalf("client missing %q:\n%s", fragment, client)
		}
	}
	runtimeSource := renderTSRuntime()
	for _, fragment := range []string{"export function decodeResponseHeader", "export function decodeResponseCookie", "export function mergeResponseValue", "getSetCookie"} {
		if !strings.Contains(runtimeSource, fragment) {
			t.Fatalf("runtime missing %q", fragment)
		}
	}
}

func TestTypeScriptClientUsesDeclaredMultipartPartContract(t *testing.T) {
	file := Resource{Address: "house/record/file", Module: "house", Kind: "scenery.record/v1", Name: "file", Spec: map[string]any{"field": []any{
		map[string]any{"name": "bytes", "type": map[string]any{"$ref": "bytes"}},
		map[string]any{"name": "filename", "type": map[string]any{"$ref": "string"}},
		map[string]any{"name": "media_type", "type": map[string]any{"$ref": "string"}},
	}}}
	input := Resource{Address: "house/record/upload_input", Module: "house", Kind: "scenery.record/v1", Name: "upload_input", Spec: map[string]any{"field": []any{
		map[string]any{"name": "note", "type": map[string]any{"$ref": "string"}},
		map[string]any{"name": "asset", "type": map[string]any{"$ref": "record.file"}},
	}}}
	operation := Resource{Address: "house/operation/upload", Module: "house", Kind: "scenery.operation/v1", Name: "upload", Spec: map[string]any{
		"input": map[string]any{"$ref": "record.upload_input"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "std.type.unit"}},
	}}
	body := map[string]any{"codec": "multipart", "to": map[string]any{"$ref": "operation.upload.input"}, "part": []any{
		map[string]any{"name": "description", "to": map[string]any{"$ref": "operation.upload.input.note"}, "kind": "text", "max_bytes": 64},
		map[string]any{"name": "asset-file", "to": map[string]any{"$ref": "operation.upload.input.asset"}, "kind": "file", "media_types": []any{"image/png"}, "max_bytes": 1024, "retain_filename": true},
	}}
	binding := Resource{Address: "house/binding/upload", Module: "house", Kind: "scenery.binding/v1", Name: "upload", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.upload"}, "delivery": "call", "http": map[string]any{
			"method": "POST", "path": "/upload", "body": body,
			"request_limit": map[string]any{"multipart_body_bytes": 2048, "multipart_file_part_bytes": 1024, "multipart_non_file_part_bytes": 64, "multipart_parts": 2},
			"response":      map[string]any{"name": "ok", "when": map[string]any{"$ref": "result.ok"}, "status": "204"},
		},
	}}
	resources := []Resource{file, input, operation}
	client := renderTSClient(Resource{Name: "public"}, []Resource{binding}, resources)
	for _, fragment := range []string{
		"encodeMultipartRequestBody(input,",
		`"name":"asset-file"`,
		`"mediaTypes":["image/png"]`,
		`"maxBytes":1024`,
		`"retainFilename":true`,
		`"fileProperties":{"bytes":"bytes","filename":"filename","mediaType":"mediaType"}`,
		"headers.set(\"content-type\", multipartBody.contentType)",
	} {
		if !strings.Contains(client, fragment) {
			t.Fatalf("multipart client missing %q:\n%s", fragment, client)
		}
	}
}

func TestUnitTypeMapsAcrossGoAndTypeScriptGenerators(t *testing.T) {
	unit := map[string]any{"$ref": "std.type.unit"}
	if got := goType(unit); got != "scenery.Unit" {
		t.Fatalf("Go unit type = %q", got)
	}
	if got := tsType(unit); got != "Unit" {
		t.Fatalf("TypeScript unit type = %q", got)
	}
	if got := fmt.Sprint(tsDescriptor(unit, "house")); !strings.Contains(got, "unit") {
		t.Fatalf("TypeScript unit descriptor = %s", got)
	}
}

func TestStandardTypesRequireExactReferences(t *testing.T) {
	for _, test := range []struct {
		reference string
		tsType    string
		goType    string
	}{
		{reference: "vendor.type.problem", tsType: "unknown", goType: "any"},
		{reference: "vendor.type.unit", tsType: "unknown", goType: "any"},
		{reference: "vendor.type.execution_receipt", tsType: "unknown", goType: "any"},
	} {
		value := map[string]any{"$ref": test.reference}
		if got := tsType(value); got != test.tsType {
			t.Errorf("TypeScript type for %q = %q, want %q", test.reference, got, test.tsType)
		}
		if got := goType(value); got != test.goType {
			t.Errorf("Go type for %q = %q, want %q", test.reference, got, test.goType)
		}
		if got := fmt.Sprint(tsDescriptor(value, "house")); strings.Contains(got, "problem") || strings.Contains(got, "unit") || strings.Contains(got, "execution_receipt") {
			t.Errorf("descriptor for %q was classified as a standard type: %s", test.reference, got)
		}
	}
}
