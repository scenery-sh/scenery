package vnext

import "testing"

func TestHTTPValidationRejectsUnsafeOrIncompleteBindings(t *testing.T) {
	resources := []Resource{
		{Address: "app/http_gateway/public", Kind: "scenery.http-gateway/v1", Spec: map[string]any{"exposure": "internet", "base_path": "/api"}},
		{Address: "house/operation/get", Module: "house", Kind: "scenery.operation/v1", Spec: map[string]any{"input": map[string]any{"$ref": "string"}}},
		{Address: "house/execution/get", Module: "house", Kind: "scenery.execution/v1", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.get"}, "mode": "direct"}},
		{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{
			"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "operation": map[string]any{"$ref": "operation.get"}, "execution": map[string]any{"$ref": "execution.get"}, "protocol": "http", "delivery": "call",
			"http": map[string]any{"method": "get", "path": "/files/*path", "codec_profile": map[string]any{"$ref": "std.codec.http_json_v1"}, "body": map[string]any{"codec": "raw"}},
		}},
	}
	diagnostics := validateHTTPResources(resources)
	for _, code := range []string{"SCN2101", "SCN2102", "SCN2103", "SCN2104"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestHTTPValidationAcceptsExplicitDenyAllAuthorization(t *testing.T) {
	resources := []Resource{
		{Address: "app/http_gateway/private", Kind: "scenery.http-gateway/v1", Spec: map[string]any{"exposure": "internal", "base_path": "/"}},
		{Address: "house/binding/closed", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{
			"gateway": map[string]any{"$ref": "app/http_gateway/private"}, "protocol": "http", "delivery": "call",
			"authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.none"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"http": map[string]any{"method": "POST", "path": "/closed", "codec_profile": map[string]any{"$ref": "std.codec.http_json_v1"}},
		}},
	}
	diagnostics := validateHTTPResources(resources)
	if hasDiagnostic(diagnostics, "SCN2108") || hasDiagnostic(diagnostics, "SCN2109") {
		t.Fatalf("explicit deny-all binding was rejected: %#v", diagnostics)
	}
}

func TestHTTPValidationRejectsStreamingWithoutAStreamingProfile(t *testing.T) {
	resources := []Resource{
		{Address: "app/http_gateway/public", Kind: "scenery.http-gateway/v1", Spec: map[string]any{"exposure": "internet", "base_path": "/"}},
		{Address: "house/binding/stream", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{
			"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "protocol": "http", "delivery": "stream",
			"authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"http": map[string]any{"method": "GET", "path": "/events", "codec_profile": map[string]any{"$ref": "std.codec.http_json_v1"}, "response": map[string]any{"name": "events", "when": map[string]any{"$ref": "result.events"}, "status": "200", "body": map[string]any{"codec": "server_sent_events", "from": map[string]any{"$ref": "result.events"}}}},
		}},
	}
	diagnostics := validateHTTPResources(resources)
	if !hasDiagnostic(diagnostics, "SCN7008") {
		t.Fatalf("unsupported streaming declaration was accepted: %#v", diagnostics)
	}
}

func TestHTTPRouteKeyJoinsGatewayBasePath(t *testing.T) {
	resources := []Resource{
		{Address: "app/http_gateway/public", Kind: "scenery.http-gateway/v1", Spec: map[string]any{"exposure": "internal", "base_path": "/api/"}},
		{Address: "house/binding/one", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "protocol": "http", "http": map[string]any{"method": "GET", "path": "/users/{id}"}}},
		{Address: "house/binding/two", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "protocol": "http", "http": map[string]any{"method": "GET", "path": "/users/{name}"}}},
	}
	diagnostics := validateHTTPResources(resources)
	if !hasDiagnostic(diagnostics, "SCN2002") {
		t.Fatalf("diagnostics: %#v", diagnostics)
	}
}

func TestHTTPValidationChecksCodecExposureAndForwardedTrust(t *testing.T) {
	resources := []Resource{
		{Address: "app/http_gateway/private", Kind: "scenery.http-gateway/v1", Spec: map[string]any{
			"exposure": "private_network", "base_path": "/", "trusted_proxies": map[string]any{"$ref": "std.trusted_proxies.none"}, "forwarded": map[string]any{"$ref": "std.forwarded_headers.accept"},
		}},
		{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{
			"gateway": map[string]any{"$ref": "app/http_gateway/private"}, "operation": map[string]any{"$ref": "operation.get"}, "execution": map[string]any{"$ref": "execution.get"},
			"protocol": "http", "delivery": "call", "exposure": "internet", "authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"http": map[string]any{"method": "GET", "path": "/items", "codec_profile": map[string]any{"$ref": "std.codec.unknown"}},
		}},
	}
	diagnostics := validateHTTPResources(resources)
	for _, code := range []string{"SCN2105", "SCN2106", "SCN2107"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestHTTPValidationChecksPathMappingsAndResponseCoverage(t *testing.T) {
	resources := []Resource{
		{Address: "app/http_gateway/public", Kind: "scenery.http-gateway/v1", Spec: map[string]any{"exposure": "internet", "base_path": "/"}},
		{Address: "house/operation/get", Module: "house", Kind: "scenery.operation/v1", Spec: map[string]any{
			"result": []any{map[string]any{"name": "found", "type": map[string]any{"$ref": "json"}}, map[string]any{"name": "empty", "type": map[string]any{"$ref": "json"}}},
			"error":  map[string]any{"name": "missing", "type": map[string]any{"$ref": "std.type.problem"}},
		}},
		{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{
			"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "operation": map[string]any{"$ref": "operation.get"}, "execution": map[string]any{"$ref": "execution.get"},
			"protocol": "http", "delivery": "call", "authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"http": map[string]any{
				"method": "GET", "path": "/items/{item_id}", "codec_profile": map[string]any{"$ref": "std.codec.http_json_v1"},
				"path_parameter": map[string]any{"name": "wrong", "to": map[string]any{"$ref": "operation.get.input.item_id"}},
				"response": []any{
					map[string]any{"name": "found", "when": map[string]any{"$ref": "result.found"}, "status": "204", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.found"}}},
					map[string]any{"name": "missing", "when": map[string]any{"$ref": "error.missing"}, "status": "404"},
				},
			},
		}},
	}
	diagnostics := validateHTTPResources(resources)
	for _, code := range []string{"SCN2110", "SCN2111", "SCN2112"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestHTTPValidationRequiresCompleteTypedInputMapping(t *testing.T) {
	resources := []Resource{
		{Address: "app/http_gateway/public", Kind: "scenery.http-gateway/v1", Spec: map[string]any{"exposure": "internet", "base_path": "/"}},
		{Address: "house/record/get_input", Module: "house", Kind: "scenery.record/v1", Spec: map[string]any{"field": []any{
			map[string]any{"name": "item_id", "type": map[string]any{"$ref": "string"}},
			map[string]any{"name": "tags", "type": map[string]any{"$expression": "optional(list(string))"}},
			map[string]any{"name": "payload", "type": map[string]any{"$ref": "json"}},
		}}},
		{Address: "house/operation/get", Module: "house", Name: "get", Kind: "scenery.operation/v1", Spec: map[string]any{
			"input": map[string]any{"$ref": "record.get_input"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "json"}},
		}},
		{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{
			"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "operation": map[string]any{"$ref": "operation.get"}, "execution": map[string]any{"$ref": "execution.get"},
			"protocol": "http", "delivery": "call", "authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"http": map[string]any{
				"method": "POST", "path": "/items/{item_id}", "codec_profile": map[string]any{"$ref": "std.codec.http_json_v1"},
				"path_parameter":  map[string]any{"name": "item_id", "to": map[string]any{"$ref": "operation.get.input.item_id"}},
				"query_parameter": map[string]any{"name": "tag", "to": map[string]any{"$ref": "operation.get.input.tags"}, "encoding": "repeated"},
				"body":            map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation.get.input.payload"}},
				"response":        map[string]any{"name": "ok", "when": map[string]any{"$ref": "result.ok"}, "status": "200", "body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.ok"}}},
			},
		}},
	}
	if diagnostics := validateHTTPResources(resources); hasDiagnostic(diagnostics, "SCN2113") || hasDiagnostic(diagnostics, "SCN2114") || hasDiagnostic(diagnostics, "SCN2115") {
		t.Fatalf("valid mapping diagnostics: %#v", diagnostics)
	}

	httpSpec := resources[3].Spec["http"].(map[string]any)
	httpSpec["query_parameter"] = map[string]any{"name": "item", "to": map[string]any{"$ref": "operation.get.input.item_id"}}
	httpSpec["body"] = map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation.get.input.unknown"}}
	diagnostics := validateHTTPResources(resources)
	if !hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("incomplete/duplicate mapping accepted: %#v", diagnostics)
	}
}

func TestHTTPValidationRejectsNonScalarPathAndConflictingBodySelection(t *testing.T) {
	resources := map[string]Resource{
		"house/record/input": {Address: "house/record/input", Module: "house", Kind: "scenery.record/v1", Spec: map[string]any{"field": []any{
			map[string]any{"name": "tags", "type": map[string]any{"$expression": "list(string)"}},
			map[string]any{"name": "name", "type": map[string]any{"$ref": "string"}},
		}}},
	}
	operation := Resource{Address: "house/operation/search", Module: "house", Name: "search", Kind: "scenery.operation/v1", Spec: map[string]any{"input": map[string]any{"$ref": "record.input"}}}
	resources[operation.Address] = operation
	binding := Resource{Address: "house/binding/search", Module: "house", Kind: "scenery.binding/v1"}
	httpSpec := map[string]any{
		"path_parameter": map[string]any{"name": "tags", "to": map[string]any{"$ref": "operation.search.input.tags"}},
		"body": map[string]any{
			"codec": "json", "to": map[string]any{"$ref": "operation.search.input"},
			"include": []any{map[string]any{"$ref": "operation.search.input.name"}},
			"except":  []any{map[string]any{"$ref": "operation.search.input.tags"}},
		},
	}
	diagnostics := validateHTTPInputMappings(resources, binding, operation, httpSpec)
	for _, code := range []string{"SCN2113", "SCN2114"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Fatalf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestHTTPResponseMappingsMustReconstructOneCompleteOutcome(t *testing.T) {
	resources := map[string]Resource{
		"house/record/output": {Address: "house/record/output", Module: "house", Kind: "scenery.record/v1", Spec: map[string]any{"field": []any{
			map[string]any{"name": "status_message", "type": map[string]any{"$ref": "string"}},
			map[string]any{"name": "request_id", "type": map[string]any{"$ref": "uuid"}},
		}}},
	}
	operation := Resource{Address: "house/operation/process", Module: "house", Name: "process", Kind: "scenery.operation/v1", Spec: map[string]any{
		"result": map[string]any{"name": "processed", "type": map[string]any{"$ref": "record.output"}},
	}}
	binding := Resource{Address: "house/binding/process", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{"delivery": "call"}}
	valid := map[string]any{"response": map[string]any{
		"name": "processed", "when": map[string]any{"$ref": "result.processed"}, "status": "200",
		"body":   map[string]any{"codec": "text", "from": map[string]any{"$ref": "result.processed.status_message"}},
		"header": map[string]any{"name": "x-request-id", "encoding": "repeated", "from": map[string]any{"$ref": "result.processed.request_id"}},
	}}
	if diagnostics := validateHTTPResponses(resources, binding, operation, valid); hasDiagnostic(diagnostics, "SCN2113") || hasDiagnostic(diagnostics, "SCN2114") || hasDiagnostic(diagnostics, "SCN2115") {
		t.Fatalf("valid split response was rejected: %#v", diagnostics)
	}

	response := valid["response"].(map[string]any)
	response["header"].(map[string]any)["from"] = map[string]any{"$ref": "result.processed.status_message"}
	if diagnostics := validateHTTPResponses(resources, binding, operation, valid); !hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("overlapping and incomplete response was accepted: %#v", diagnostics)
	}
}

func TestHTTPCommaEncodingRejectsValuesThatCanContainCommas(t *testing.T) {
	resources := map[string]Resource{}
	if httpMappedTypeSupported(map[string]any{"$expression": "list(string)"}, "comma", resources, "house") {
		t.Fatal("comma-delimited strings were accepted")
	}
	if !httpMappedTypeSupported(map[string]any{"$expression": "list(int64)"}, "comma", resources, "house") {
		t.Fatal("comma-delimited integers were rejected")
	}
}

func TestHTTPUnitInputRequiresNoTransportMapping(t *testing.T) {
	operation := Resource{Address: "house/operation/ping", Module: "house", Name: "ping", Kind: "scenery.operation/v1", Spec: map[string]any{"input": map[string]any{"$ref": "std.type.unit"}}}
	binding := Resource{Address: "house/binding/ping", Module: "house", Kind: "scenery.binding/v1"}
	if diagnostics := validateHTTPInputMappings(map[string]Resource{}, binding, operation, map[string]any{}); hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("unit input without mappings was rejected: %#v", diagnostics)
	}
	body := map[string]any{"body": map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation.ping.input"}}}
	if diagnostics := validateHTTPInputMappings(map[string]Resource{}, binding, operation, body); !hasDiagnostic(diagnostics, "SCN2113") {
		t.Fatalf("unit body mapping was accepted: %#v", diagnostics)
	}
}

func TestHTTPMultipartFileRecordsRequireExactRetainedMetadata(t *testing.T) {
	file := Resource{Address: "house/record/file", Module: "house", Kind: "scenery.record/v1", Name: "file", Spec: map[string]any{"field": []any{
		map[string]any{"name": "bytes", "type": map[string]any{"$ref": "bytes"}},
		map[string]any{"name": "filename", "type": map[string]any{"$ref": "string"}},
		map[string]any{"name": "media_type", "type": map[string]any{"$ref": "string"}},
	}}}
	input := Resource{Address: "house/record/input", Module: "house", Kind: "scenery.record/v1", Name: "input", Spec: map[string]any{"field": map[string]any{"name": "asset", "type": map[string]any{"$ref": "record.file"}}}}
	operation := Resource{Address: "house/operation/upload", Module: "house", Kind: "scenery.operation/v1", Name: "upload", Spec: map[string]any{"input": map[string]any{"$ref": "record.input"}}}
	binding := Resource{Address: "house/binding/upload", Module: "house", Kind: "scenery.binding/v1"}
	resources := map[string]Resource{file.Address: file, input.Address: input, operation.Address: operation}
	body := map[string]any{"codec": "multipart", "to": map[string]any{"$ref": "operation.upload.input"}, "part": map[string]any{
		"name": "asset", "to": map[string]any{"$ref": "operation.upload.input.asset"}, "kind": "file", "media_types": []any{"image/*"}, "max_bytes": 1024, "retain_filename": true,
	}}
	if diagnostics := validateHTTPInputMappings(resources, binding, operation, map[string]any{"body": body}); hasDiagnostic(diagnostics, "SCN2119") {
		t.Fatalf("valid retained file record was rejected: %#v", diagnostics)
	}
	body["part"].(map[string]any)["retain_filename"] = false
	if diagnostics := validateHTTPInputMappings(resources, binding, operation, map[string]any{"body": body}); !hasDiagnostic(diagnostics, "SCN2119") {
		t.Fatalf("file record without retained metadata was accepted: %#v", diagnostics)
	}
}
