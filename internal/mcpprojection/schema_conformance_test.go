package mcpprojection

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"scenery.sh/internal/graph"
)

// validateInstance checks wire JSON against an advertised schema with a real
// JSON Schema validator, the way an MCP client checks structured content.
func validateInstance(t *testing.T, schema any, instance string) error {
	t.Helper()
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var parsed jsonschema.Schema
	if err := json.Unmarshal(encoded, &parsed); err != nil {
		t.Fatalf("schema is not JSON Schema: %v\n%s", err, encoded)
	}
	resolved, err := parsed.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema: %v\n%s", err, encoded)
	}
	var value any
	if err := json.Unmarshal([]byte(instance), &value); err != nil {
		t.Fatal(err)
	}
	return resolved.Validate(value)
}

func taggedUnionResources(open bool) map[string]graph.Resource {
	return map[string]graph.Resource{
		"app/record/circle": {Address: "app/record/circle", Kind: recordKind, Module: "app", Spec: map[string]any{
			"unknown_fields": "reject",
			"field":          []any{map[string]any{"name": "radius", "type": map[string]any{"$expression": "float64"}}},
		}},
		"app/record/square": {Address: "app/record/square", Kind: recordKind, Module: "app", Spec: map[string]any{
			"unknown_fields": "reject",
			"field":          []any{map[string]any{"name": "side", "wire_name": "edge", "type": map[string]any{"$expression": "float64"}}},
		}},
		"app/union/shape": {Address: "app/union/shape", Kind: unionKind, Module: "app", Spec: map[string]any{
			"discriminator": "kind",
			"open":          open,
			"variant": []any{
				map[string]any{"name": "circle", "type": map[string]any{"$ref": "app/record/circle"}},
				map[string]any{"name": "square", "wire_tag": "sq", "type": map[string]any{"$ref": "app/record/square"}},
			},
		}},
	}
}

func TestTaggedUnionSchemaAcceptsTheFlattenedWireForm(t *testing.T) {
	owner := graph.Resource{Address: "app/operation/test", Module: "app"}
	closed, _, err := schemaForType(owner, map[string]any{"$ref": "app/union/shape"}, "app", taggedUnionResources(false), newSchemaState())
	if err != nil {
		t.Fatal(err)
	}
	for _, accepted := range []string{`{"kind":"circle","radius":2}`, `{"kind":"sq","edge":3}`} {
		if err := validateInstance(t, closed, accepted); err != nil {
			t.Fatalf("closed union rejected %s: %v", accepted, err)
		}
	}
	for _, rejected := range []string{`{"radius":2}`, `{"kind":"square","edge":3}`, `{"kind":"circle","edge":3}`, `{"kind":"other","x":1}`} {
		if err := validateInstance(t, closed, rejected); err == nil {
			t.Fatalf("closed union accepted %s", rejected)
		}
	}
	open, _, err := schemaForType(owner, map[string]any{"$ref": "app/union/shape"}, "app", taggedUnionResources(true), newSchemaState())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateInstance(t, open, `{"kind":"other","x":1}`); err != nil {
		t.Fatalf("open union rejected an unknown variant: %v", err)
	}
	if err := validateInstance(t, open, `{"kind":"circle","edge":3}`); err == nil {
		t.Fatal("open union accepted a known tag with another variant's payload")
	}
}

func TestDurableCapabilityAdvertisesTheAcceptanceResult(t *testing.T) {
	fixture, workspaceRevision := readNativeGraphFixture(t)
	manifest := cloneManifest(fixture)
	resourceByAddress(manifest, "house/execution/process_scene_direct").Spec["mode"] = "durable"
	projected, err := ProjectManifest(manifest, workspaceRevision, "support")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(projected.Capabilities[0].OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	accepted := `{"outcome":"accepted","receipt":{"durable_identity":"house/execution/process_scene_direct","execution_id":"job-1","accepted_revision":"sha256:abc"}}`
	if err := validateInstance(t, schema, accepted); err != nil {
		t.Fatalf("durable schema rejected the runtime acceptance result: %v", err)
	}
	if err := validateInstance(t, schema, `{"outcome":"processed","value":{}}`); err == nil {
		t.Fatal("durable schema accepted a synchronous outcome")
	}
}

func TestFieldDescriptionsAndExamplesReachTheToolSchema(t *testing.T) {
	resources := taggedUnionResources(false)
	resources["app/record/order"] = graph.Resource{Address: "app/record/order", Kind: recordKind, Module: "app", Spec: map[string]any{
		"field": []any{
			map[string]any{"name": "amount", "type": map[string]any{"$expression": "int64"}, "description": "Order amount in minor currency units.", "examples": []any{"1250"}},
			map[string]any{"name": "shape", "type": map[string]any{"$ref": "app/union/shape"}, "examples": []any{map[string]any{"kind": "sq", "edge": 2}}},
		},
	}}
	owner := graph.Resource{Address: "app/operation/test", Module: "app"}
	schema, _, err := schemaForType(owner, map[string]any{"$ref": "app/record/order"}, "app", resources, newSchemaState())
	if err != nil {
		t.Fatal(err)
	}
	properties := schema.(map[string]any)["properties"].(map[string]any)
	amount := properties["amount"].(map[string]any)
	if amount["description"] != "Order amount in minor currency units." {
		t.Fatalf("amount schema = %#v", amount)
	}
	for name, property := range properties {
		examples, _ := property.(map[string]any)["examples"].([]any)
		if len(examples) == 0 {
			t.Fatalf("%s has no examples: %#v", name, property)
		}
		for _, example := range examples {
			encoded, _ := json.Marshal(example)
			if err := validateInstance(t, property, string(encoded)); err != nil {
				t.Fatalf("%s example %s does not match its own schema: %v", name, encoded, err)
			}
		}
	}
}
