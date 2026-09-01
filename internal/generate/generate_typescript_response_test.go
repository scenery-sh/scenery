package generate

import (
	"fmt"
	"strings"
	"testing"
)

func TestTypeScriptClientReturnsDeclaredTransportFailuresAsTypedOutcomes(t *testing.T) {
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation", Name: "get", Spec: map[string]any{
		"input": map[string]any{"$ref": "std.type.unit"}, "result": map[string]any{"name": "found", "type": map[string]any{"$ref": "string"}},
	}}
	binding := Resource{Address: "house/binding/get", Module: "house", Kind: "scenery.binding", Name: "get", Spec: map[string]any{
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
	if !strings.Contains(client, "async get(_input: Types.GetInput") {
		t.Fatalf("unit input is not marked unused:\n%s", client)
	}
	for _, unused := range []string{"appendCookie", "appendHeader", "appendQuery", "assertEmptyResponse", "decodeResponseCookie", "decodeResponseHeader", "encodeHTTPValue", "encodeMultipartRequestBody", "encodeRFC3986", "encodeRequestBody", "fetchWithRetry", "RetryRuntime"} {
		if strings.Contains(client, unused) {
			t.Fatalf("client imports unused runtime helper %q:\n%s", unused, client)
		}
	}
	for _, fragment := range []string{
		`Runtime.invoke(this.#transport, bindings.get, _input, options, typeRegistry)`,
		`"role":"failure"`,
		`"kind":"failure"`,
		`"name":"invalid_request"`,
		`"problemCode":"transport.invalid_request"`,
	} {
		if !strings.Contains(client, fragment) {
			t.Fatalf("client missing %q:\n%s", fragment, client)
		}
	}
	if strings.Contains(client, `"throwOnMatch":true`) {
		t.Fatalf("declared transport failure is thrown instead of returned:\n%s", client)
	}
	runtimeSource := renderTSRuntime()
	if !strings.Contains(runtimeSource, `return { kind: "failure", name: candidate.name, problem: payload }`) {
		t.Fatalf("runtime does not return typed failures:\n%s", runtimeSource)
	}
}

func TestTypeScriptClientSelectsSameStatusCompletionsByTypedMapping(t *testing.T) {
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation", Name: "get", Spec: map[string]any{
		"input": map[string]any{"$ref": "std.type.unit"},
		"result": []any{
			map[string]any{"name": "found", "type": map[string]any{"$ref": "string"}},
			map[string]any{"name": "snapshot", "type": map[string]any{"$ref": "string"}},
		},
	}}
	binding := Resource{Address: "house/binding/get", Module: "house", Kind: "scenery.binding", Name: "get", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.get"}, "delivery": "call",
		"http": map[string]any{"method": "GET", "path": "/get", "response": []any{
			map[string]any{"name": "found", "when": map[string]any{"$ref": "result.found"}, "status": "200", "body": map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.found"}}},
			map[string]any{"name": "snapshot", "when": map[string]any{"$ref": "result.snapshot"}, "status": "200", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.snapshot"}}},
		}},
	}}
	client := renderTSClient(Resource{Name: "public"}, []Resource{binding}, []Resource{operation})
	for _, fragment := range []string{
		`"status":200`,
		`"role":"completion"`,
		`"kind":"result"`,
		`"name":"found"`,
		`"name":"snapshot"`,
		`"codec":"text"`,
		`"codec":"json"`,
	} {
		if !strings.Contains(client, fragment) {
			t.Fatalf("client missing %q:\n%s", fragment, client)
		}
	}
	runtimeSource := renderTSRuntime()
	for _, fragment := range []string{
		"const completionMatches: unknown[] = []",
		"if (completionMatches.length === 1) return completionMatches[0]",
		"response.clone()",
		`if (!(cause instanceof SceneryClientError) || cause.code !== "contract_violation") throw cause`,
	} {
		if !strings.Contains(runtimeSource, fragment) {
			t.Fatalf("runtime missing %q", fragment)
		}
	}
}

func TestTypeScriptClientReconstructsResponseHeadersCookiesAndCamelCaseFields(t *testing.T) {
	output := Resource{Address: "house/record/output", Module: "house", Kind: "scenery.record", Name: "output", Spec: map[string]any{
		"field": []any{
			map[string]any{"name": "status_message", "type": map[string]any{"$ref": "string"}},
			map[string]any{"name": "request_id", "type": map[string]any{"$ref": "uuid"}},
			map[string]any{"name": "session_token", "type": map[string]any{"$expression": "optional(string)"}},
		},
	}}
	operation := Resource{Address: "house/operation/process", Module: "house", Kind: "scenery.operation", Name: "process", Spec: map[string]any{
		"input": map[string]any{"$ref": "string"}, "result": map[string]any{"name": "processed", "type": map[string]any{"$ref": "record.output"}},
	}}
	binding := Resource{Address: "house/binding/process", Module: "house", Kind: "scenery.binding", Name: "process", Spec: map[string]any{
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
		`"path":["statusMessage"]`,
		`"name":"x-request-id"`,
		`"encoding":"repeated"`,
		`"path":["requestId"]`,
		`"name":"session"`,
		`"path":["sessionToken"]`,
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
	file := Resource{Address: "house/record/file", Module: "house", Kind: "scenery.record", Name: "file", Spec: map[string]any{"field": []any{
		map[string]any{"name": "bytes", "type": map[string]any{"$ref": "bytes"}},
		map[string]any{"name": "filename", "type": map[string]any{"$ref": "string"}},
		map[string]any{"name": "media_type", "type": map[string]any{"$ref": "string"}},
	}}}
	input := Resource{Address: "house/record/upload_input", Module: "house", Kind: "scenery.record", Name: "upload_input", Spec: map[string]any{"field": []any{
		map[string]any{"name": "note", "type": map[string]any{"$ref": "string"}},
		map[string]any{"name": "asset", "type": map[string]any{"$ref": "record.file"}},
	}}}
	operation := Resource{Address: "house/operation/upload", Module: "house", Kind: "scenery.operation", Name: "upload", Spec: map[string]any{
		"input": map[string]any{"$ref": "record.upload_input"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "std.type.unit"}},
	}}
	body := map[string]any{"codec": "multipart", "to": map[string]any{"$ref": "operation.upload.input"}, "part": []any{
		map[string]any{"name": "description", "to": map[string]any{"$ref": "operation.upload.input.note"}, "kind": "text", "max_bytes": 64},
		map[string]any{"name": "asset-file", "to": map[string]any{"$ref": "operation.upload.input.asset"}, "kind": "file", "media_types": []any{"image/png"}, "max_bytes": 1024, "retain_filename": true},
	}}
	binding := Resource{Address: "house/binding/upload", Module: "house", Kind: "scenery.binding", Name: "upload", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.upload"}, "delivery": "call", "http": map[string]any{
			"method": "POST", "path": "/upload", "body": body,
			"request_limit": map[string]any{"multipart_body_bytes": 2048, "multipart_file_part_bytes": 1024, "multipart_non_file_part_bytes": 64, "multipart_parts": 2},
			"response":      map[string]any{"name": "ok", "when": map[string]any{"$ref": "result.ok"}, "status": "204"},
		},
	}}
	resources := []Resource{file, input, operation}
	client := renderTSClient(Resource{Name: "public"}, []Resource{binding}, resources)
	for _, fragment := range []string{
		`"codec":"multipart"`,
		`"name":"asset-file"`,
		`"mediaTypes":["image/png"]`,
		`"maxBytes":1024`,
		`"retainFilename":true`,
		`"fileProperties":{"bytes":"bytes","filename":"filename","mediaType":"mediaType"}`,
	} {
		if !strings.Contains(client, fragment) {
			t.Fatalf("multipart client missing %q:\n%s", fragment, client)
		}
	}
	if !strings.Contains(renderTSRuntime(), "encodeMultipartRequestBody") {
		t.Fatal("runtime missing encodeMultipartRequestBody")
	}
}

func TestTypeScriptClientPreserves400DualPathOrder(t *testing.T) {
	operation := Resource{Address: "house/operation/update", Module: "house", Kind: "scenery.operation", Name: "update", Spec: map[string]any{
		"input":  map[string]any{"$ref": "std.type.unit"},
		"result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "std.type.unit"}},
		"error":  map[string]any{"name": "invalid_input", "type": map[string]any{"$ref": "std.type.problem"}},
	}}
	binding := Resource{Address: "house/binding/update", Module: "house", Kind: "scenery.binding", Name: "update", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.update"}, "http": map[string]any{"method": "POST", "path": "/update", "response": []any{
			map[string]any{"name": "invalid_input", "when": map[string]any{"$ref": "error.invalid_input"}, "status": "400", "body": map[string]any{"codec": "problem_json", "from": map[string]any{"$ref": "error.invalid_input"}}},
			map[string]any{"name": "invalid_request", "when": map[string]any{"$ref": "transport.invalid_request"}, "status": "400", "body": map[string]any{"codec": "problem_json", "from": map[string]any{"$ref": "transport.problem"}}},
		}},
	}}
	client := renderTSClient(Resource{Name: "public"}, []Resource{binding}, []Resource{operation})
	failure := strings.Index(client, `"problemCode":"transport.invalid_request"`)
	completion := strings.Index(client, `"name":"invalid_input"`)
	if failure < 0 || completion < 0 || failure > completion {
		t.Fatalf("400 dual-path must list failure before completion:\n%s", client)
	}
	if !strings.Contains(client, `"role":"completion"`) || !strings.Contains(client, `"kind":"error"`) {
		t.Fatalf("400 completion mapping missing:\n%s", client)
	}
}

func TestTypeScriptClientInternsSharedResponseCases(t *testing.T) {
	get := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation", Name: "get", Spec: map[string]any{
		"input":  map[string]any{"$ref": "std.type.unit"},
		"result": map[string]any{"name": "found", "type": map[string]any{"$ref": "string"}},
	}}
	list := Resource{Address: "house/operation/list", Module: "house", Kind: "scenery.operation", Name: "list", Spec: map[string]any{
		"input":  map[string]any{"$ref": "std.type.unit"},
		"result": map[string]any{"name": "listed", "type": map[string]any{"$ref": "string"}},
	}}
	transport := []any{
		map[string]any{"name": "invalid_request", "when": map[string]any{"$ref": "transport.invalid_request"}, "status": "400", "body": map[string]any{"codec": "problem_json", "from": map[string]any{"$ref": "transport.problem"}}},
		map[string]any{"name": "not_acceptable", "when": map[string]any{"$ref": "transport.not_acceptable"}, "status": "406", "body": map[string]any{"codec": "problem_json", "from": map[string]any{"$ref": "transport.problem"}}},
	}
	bindings := []Resource{
		{Address: "house/binding/get", Module: "house", Kind: "scenery.binding", Name: "get", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.get"}, "http": map[string]any{"method": "GET", "path": "/get", "response": append([]any{
				map[string]any{"name": "found", "when": map[string]any{"$ref": "result.found"}, "status": "200", "body": map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.found"}}},
			}, transport...)},
		}},
		{Address: "house/binding/list", Module: "house", Kind: "scenery.binding", Name: "list", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.list"}, "http": map[string]any{"method": "GET", "path": "/list", "response": append([]any{
				map[string]any{"name": "listed", "when": map[string]any{"$ref": "result.listed"}, "status": "200", "body": map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.listed"}}},
			}, transport...)},
		}},
	}
	client := renderTSClient(Resource{Name: "public"}, bindings, []Resource{get, list})
	for _, fragment := range []string{
		"const sharedResponses = {",
		"failure_400_invalidRequest:",
		"failure_406_notAcceptable:",
		"const sharedResponseSets = {",
		"...sharedResponseSets.s0",
		`"name":"found"`,
		`"name":"listed"`,
	} {
		if !strings.Contains(client, fragment) {
			t.Fatalf("interned client missing %q:\n%s", fragment, client)
		}
	}
	if got := strings.Count(client, `"problemCode":"transport.invalid_request"`); got != 1 {
		t.Fatalf("transport failure case should appear once, got %d:\n%s", got, client)
	}
	if strings.Count(client, "...sharedResponseSets.s0") != 2 {
		t.Fatalf("both bindings should reuse the shared failure set:\n%s", client)
	}
}

func TestTypeScriptRuntimeExportsTableDrivenHelpers(t *testing.T) {
	runtimeSource := renderTSRuntime()
	for _, fragment := range []string{
		"export async function invoke(",
		"export async function matchResponse(",
		"export interface BindingCall",
		"isProblemCode(payload, candidate.problemCode)",
		"response.clone()",
	} {
		if !strings.Contains(runtimeSource, fragment) {
			t.Fatalf("runtime missing %q", fragment)
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
