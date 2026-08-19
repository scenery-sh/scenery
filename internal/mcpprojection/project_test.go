package mcpprojection

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/mcpcontract"
)

func TestProjectNativeFixtureMatchesGolden(t *testing.T) {
	manifest, workspaceRevision := readNativeGraphFixture(t)
	projected, err := ProjectManifest(manifest, workspaceRevision, "support")
	if err != nil {
		t.Fatal(err)
	}
	if err := projected.Validate(); err != nil {
		t.Fatalf("projected manifest is invalid: %v", err)
	}
	if len(projected.Capabilities) != 1 || projected.Capabilities[0].Name != "house__process_scene" {
		t.Fatalf("capabilities = %#v", projected.Capabilities)
	}
	if len(projected.Connections) != 1 || projected.Connections[0].Namespace != "docs" {
		t.Fatalf("connections = %#v", projected.Connections)
	}
	var outputSchema map[string]any
	if err := json.Unmarshal(projected.Capabilities[0].OutputSchema, &outputSchema); err != nil {
		t.Fatalf("decode output schema: %v", err)
	}
	if outputSchema["type"] != "object" {
		t.Fatalf("output schema type = %#v", outputSchema["type"])
	}
	variants, ok := outputSchema["oneOf"].([]any)
	if !ok || len(variants) != 1 {
		t.Fatalf("output schema variants = %#v", outputSchema["oneOf"])
	}
	variant, ok := variants[0].(map[string]any)
	if !ok {
		t.Fatalf("output schema variant = %#v", variants[0])
	}
	outputProperties, ok := variant["properties"].(map[string]any)
	if !ok || outputProperties["outcome"] == nil || outputProperties["value"] == nil {
		t.Fatalf("output schema does not expose outcome/value envelope: %#v", variant)
	}
	if !containsString(variant["required"], "outcome") || !containsString(variant["required"], "value") {
		t.Fatalf("output envelope required fields = %#v", variant["required"])
	}
	encoded, err := mcpcontract.MarshalCanonical(projected)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "native_manifest.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	golden = bytes.TrimSpace(golden)
	parsedGolden, err := mcpcontract.Parse(golden)
	if err != nil {
		t.Fatalf("golden manifest is not a valid canonical manifest: %v", err)
	}
	canonicalGolden, err := mcpcontract.MarshalCanonical(parsedGolden)
	if err != nil {
		t.Fatalf("canonicalize golden manifest: %v", err)
	}
	if !bytes.Equal(golden, canonicalGolden) {
		t.Fatalf("golden manifest is not canonical JSON:\n got: %s\nwant: %s", golden, canonicalGolden)
	}
	if !bytes.Equal(encoded, golden) {
		t.Fatalf("native MCP manifest drifted:\n got: %s\nwant: %s", encoded, golden)
	}
	for _, reference := range []string{"support", "mcp_server.support", "app/mcp_server/support"} {
		projected, err := ProjectManifest(manifest, workspaceRevision, reference)
		if err != nil {
			t.Fatalf("project server %q: %v", reference, err)
		}
		projectedEncoded, err := mcpcontract.MarshalCanonical(projected)
		if err != nil {
			t.Fatalf("marshal server %q: %v", reference, err)
		}
		if !bytes.Equal(encoded, projectedEncoded) {
			t.Fatalf("equivalent server reference %q changed the projected bytes", reference)
		}
	}
	durableManifest := cloneManifest(manifest)
	execution := resourceByAddress(durableManifest, "house/execution/process_scene_direct")
	execution.Spec["mode"] = "durable"
	durable, err := ProjectManifest(durableManifest, workspaceRevision, "support")
	if err != nil {
		t.Fatal(err)
	}
	if len(durable.Capabilities) != 1 || !durable.Capabilities[0].Durable {
		t.Fatalf("durable capability = %#v", durable.Capabilities)
	}
}

func TestSchemaProjectionCoversCompositeShapes(t *testing.T) {
	resources := map[string]graph.Resource{
		"app/record/input": {
			Address: "app/record/input", Kind: recordKind, Module: "app",
			Spec: map[string]any{
				"unknown_fields": "reject",
				"field": []any{
					map[string]any{"name": "unit", "type": map[string]any{"$expression": "std.type.unit"}},
					map[string]any{"name": "set_values", "type": map[string]any{"$expression": "set(string)"}},
					map[string]any{"name": "map_values", "type": map[string]any{"$expression": "map(int32)"}},
					map[string]any{"name": "tuple_values", "type": map[string]any{"$expression": "tuple(string,int32)"}},
					map[string]any{"name": "optional_value", "type": map[string]any{"$expression": "optional(string)"}},
					map[string]any{"name": "nullable_value", "type": map[string]any{"$expression": "nullable(string)"}},
				},
			},
		},
		"app/enum/mode": {
			Address: "app/enum/mode", Kind: enumKind, Module: "app",
			Spec: map[string]any{"open": false, "value": []any{
				map[string]any{"name": "alpha"}, map[string]any{"name": "beta", "wire_value": "b"},
			}},
		},
		"app/union/value": {
			Address: "app/union/value", Kind: unionKind, Module: "app",
			Spec: map[string]any{"variant": []any{
				map[string]any{"name": "text", "type": map[string]any{"$expression": "string"}},
				map[string]any{"name": "count", "type": map[string]any{"$expression": "int32"}},
			}},
		},
	}
	owner := graph.Resource{Address: "app/operation/test", Module: "app"}
	schema, _, err := schemaForType(owner, map[string]any{"$ref": "app/record/input"}, "app", resources, newSchemaState())
	if err != nil {
		t.Fatal(err)
	}
	object, ok := schema.(map[string]any)
	if !ok || object["type"] != "object" || object["additionalProperties"] != false {
		t.Fatalf("record schema = %#v", schema)
	}
	properties, ok := object["properties"].(map[string]any)
	if !ok {
		t.Fatalf("record properties = %#v", object["properties"])
	}
	unit, ok := properties["unit"].(map[string]any)
	if !ok || unit["type"] != "object" || unit["maxProperties"] != 0 {
		t.Fatalf("unit schema = %#v", properties["unit"])
	}
	set, ok := properties["set_values"].(map[string]any)
	if !ok || set["type"] != "array" || set["uniqueItems"] != true {
		t.Fatalf("set schema = %#v", properties["set_values"])
	}
	mapSchema, ok := properties["map_values"].(map[string]any)
	if !ok || mapSchema["type"] != "object" || mapSchema["additionalProperties"] == nil {
		t.Fatalf("map schema = %#v", properties["map_values"])
	}
	tuple, ok := properties["tuple_values"].(map[string]any)
	if !ok || tuple["type"] != "array" {
		t.Fatalf("tuple schema = %#v", properties["tuple_values"])
	}
	if prefixItems, ok := tuple["prefixItems"].([]any); !ok || len(prefixItems) != 2 {
		t.Fatalf("tuple prefixItems = %#v", tuple["prefixItems"])
	}
	if _, ok := properties["optional_value"]; !ok || containsString(object["required"], "optional_value") {
		t.Fatalf("optional field requiredness = %#v", object["required"])
	}
	nullable, ok := properties["nullable_value"].(map[string]any)
	if !ok {
		t.Fatalf("nullable schema = %#v", properties["nullable_value"])
	}
	if alternatives, ok := nullable["anyOf"].([]any); !ok || len(alternatives) != 2 {
		t.Fatalf("nullable anyOf = %#v", nullable["anyOf"])
	}

	enum, _, err := schemaForType(owner, map[string]any{"$ref": "app/enum/mode"}, "app", resources, newSchemaState())
	if err != nil {
		t.Fatal(err)
	}
	enumObject, ok := enum.(map[string]any)
	if !ok || !containsString(enumObject["enum"], "alpha") || !containsString(enumObject["enum"], "b") {
		t.Fatalf("enum schema = %#v", enum)
	}
	union, _, err := schemaForType(owner, map[string]any{"$ref": "app/union/value"}, "app", resources, newSchemaState())
	if err != nil {
		t.Fatal(err)
	}
	unionObject, ok := union.(map[string]any)
	if !ok {
		t.Fatalf("union schema = %#v", union)
	}
	if variants, ok := unionObject["oneOf"].([]any); !ok || len(variants) != 2 {
		t.Fatalf("union oneOf = %#v", unionObject["oneOf"])
	}

	operation := graph.Resource{Address: "app/operation/outcomes", Module: "app", Spec: map[string]any{
		"result": []any{map[string]any{"name": "success", "type": map[string]any{"$ref": "app/record/input"}}},
		"error":  []any{map[string]any{"name": "invalid", "type": map[string]any{"$expression": "std.type.problem"}}},
	}}
	output, _, err := operationOutputSchema(operation, resources)
	if err != nil {
		t.Fatal(err)
	}
	var outputObject map[string]any
	if err := json.Unmarshal(output, &outputObject); err != nil {
		t.Fatal(err)
	}
	outputVariants, ok := outputObject["oneOf"].([]any)
	if !ok || len(outputVariants) != 2 {
		t.Fatalf("outcome variants = %#v", outputObject["oneOf"])
	}
	for _, raw := range outputVariants {
		variant, ok := raw.(map[string]any)
		if !ok || variant["type"] != "object" || variant["additionalProperties"] != false {
			t.Fatalf("outcome variant = %#v", raw)
		}
		properties, ok := variant["properties"].(map[string]any)
		if !ok {
			t.Fatalf("outcome variant properties = %#v", variant["properties"])
		}
		outcome, ok := properties["outcome"].(map[string]any)
		if !ok || outcome["const"] == nil {
			t.Fatalf("outcome discriminator = %#v", properties["outcome"])
		}
		if stringValue(outcome["const"]) == "invalid" {
			if properties["problem"] == nil || !containsString(variant["required"], "problem") || containsString(variant["required"], "value") {
				t.Fatalf("error outcome variant = %#v", variant)
			}
		} else if stringValue(outcome["const"]) == "success" {
			if properties["value"] == nil || !containsString(variant["required"], "value") || containsString(variant["required"], "problem") {
				t.Fatalf("result outcome variant = %#v", variant)
			}
		} else {
			t.Fatalf("unexpected outcome discriminator = %#v", outcome["const"])
		}
	}
}

func TestSchemaProjectionRejectsRecursiveTypes(t *testing.T) {
	resources := map[string]graph.Resource{
		"app/record/recursive": {
			Address: "app/record/recursive", Kind: recordKind, Module: "app",
			Spec: map[string]any{"field": []any{map[string]any{"name": "next", "type": map[string]any{"$ref": "app/record/recursive"}}}},
		},
	}
	_, _, err := schemaForType(graph.Resource{Address: "app/operation/recursive", Module: "app"}, map[string]any{"$ref": "app/record/recursive"}, "app", resources, newSchemaState())
	if err == nil || !strings.Contains(err.Error(), "recursive record type app/record/recursive") {
		t.Fatalf("recursive type error = %v", err)
	}
}

func TestProjectRejectsInvalidLimitsDuplicateNamesAndUnsupportedTypes(t *testing.T) {
	fixture, workspaceRevision := readNativeGraphFixture(t)
	manifest := cloneManifest(fixture)
	server := resourceByAddress(manifest, "app/mcp_server/support")
	server.Spec["max_input_bytes"] = map[string]any{"$scalar": "int", "value": "0"}
	if _, err := ProjectManifest(manifest, workspaceRevision, "support"); err == nil || !strings.HasPrefix(err.Error(), projectionLimitsCode+":") {
		t.Fatalf("invalid limit error = %v", err)
	}
	server.Spec["max_input_bytes"] = map[string]any{"$scalar": "int", "value": "262144"}
	capability := namedChildren(server.Spec, "capability")[0]
	server.Spec["capability"] = []any{capability, cloneMap(capability)}
	if _, err := ProjectManifest(manifest, workspaceRevision, "support"); err == nil || !strings.HasPrefix(err.Error(), toolIdentityCode+":") {
		t.Fatalf("duplicate name error = %v", err)
	}
	server.Spec["capability"] = capability
	binding := resourceByAddress(manifest, "house/binding/process_scene_mcp")
	operation := resourceByAddress(manifest, "house/operation/process_scene")
	operation.Spec["input"] = map[string]any{"$expression": "opaque"}
	binding.Spec["operation"] = map[string]any{"$ref": operation.Address}
	if _, err := ProjectManifest(manifest, workspaceRevision, "support"); err == nil || !strings.HasPrefix(err.Error(), projectionTypeCode+":") {
		t.Fatalf("unsupported type error = %v", err)
	}
}

func TestProjectRejectsSensitiveOutputWithoutOptIn(t *testing.T) {
	fixture, workspaceRevision := readNativeGraphFixture(t)
	manifest := cloneManifest(fixture)
	record := resourceByAddress(manifest, "house/record/process_scene_result")
	fields := namedChildren(record.Spec, "field")
	fields[0]["sensitive"] = true
	record.Spec["field"] = fields[0]
	if _, err := ProjectManifest(manifest, workspaceRevision, "support"); err == nil || !strings.HasPrefix(err.Error(), sensitiveOutputCode+":") {
		t.Fatalf("sensitive output error = %v", err)
	}
}

func readNativeGraphFixture(t *testing.T) (*graph.Manifest, string) {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join("testdata", "native_graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		WorkspaceRevision string          `json:"workspace_revision"`
		Manifest          *graph.Manifest `json:"manifest"`
	}
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Manifest == nil || fixture.WorkspaceRevision == "" {
		t.Fatal("native graph fixture is incomplete")
	}
	return fixture.Manifest, fixture.WorkspaceRevision
}

func cloneManifest(manifest *graph.Manifest) *graph.Manifest {
	encoded, _ := json.Marshal(manifest)
	var copy graph.Manifest
	_ = json.Unmarshal(encoded, &copy)
	return &copy
}

func cloneMap(value map[string]any) map[string]any {
	encoded, _ := json.Marshal(value)
	var copy map[string]any
	_ = json.Unmarshal(encoded, &copy)
	return copy
}

func resourceByAddress(manifest *graph.Manifest, address string) *graph.Resource {
	for index := range manifest.Resources {
		if manifest.Resources[index].Address == address {
			return &manifest.Resources[index]
		}
	}
	return nil
}

func containsString(value any, want string) bool {
	switch values := value.(type) {
	case []string:
		for _, candidate := range values {
			if candidate == want {
				return true
			}
		}
	case []any:
		for _, candidate := range values {
			if text, ok := candidate.(string); ok && text == want {
				return true
			}
		}
	}
	return false
}
