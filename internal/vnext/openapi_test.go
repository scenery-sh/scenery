package vnext

import (
	"encoding/json"
	"testing"
)

func TestOpenAPIArtifactRecordsSmallestGatewayProjection(t *testing.T) {
	gateway := Resource{Address: "app/http_gateway/public", Module: "app", Kind: "scenery.http-gateway/v1", Name: "public", Spec: map[string]any{"base_path": "/api", "exposure": "internet"}}
	input := Resource{Address: "house/record/input", Module: "house", Kind: "scenery.record/v1", Name: "input", Spec: map[string]any{"field": map[string]any{"name": "id", "type": map[string]any{"$ref": "int64"}}}}
	output := Resource{Address: "house/record/output", Module: "house", Kind: "scenery.record/v1", Name: "output", Spec: map[string]any{"field": map[string]any{"name": "status", "type": map[string]any{"$ref": "string"}}}}
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation/v1", Name: "get", Spec: map[string]any{"input": map[string]any{"$ref": "record.input"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "record.output"}}}}
	binding := Resource{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Name: "get", Spec: map[string]any{
		"gateway": map[string]any{"$ref": "http_gateway.public"}, "operation": map[string]any{"$ref": "operation.get"}, "protocol": "http", "delivery": "call", "authentication": map[string]any{"$ref": "std.authentication.none"},
		"http": map[string]any{"method": "POST", "path": "/items", "guarantee": "framework_enforced", "body": map[string]any{"codec": "json"}, "response": map[string]any{"name": "ok", "when": map[string]any{"$ref": "result.ok"}, "status": "200", "body": map[string]any{"codec": "json"}}},
	}}
	manifest := &Manifest{Application: ApplicationIdentity{Name: "demo", Version: "1.0.0"}, ContractRevision: revisionHash("contract\x00", "demo"), Resources: []Resource{gateway, input, output, operation, binding}}
	httpRevisions, openAPIRevisions := computeHTTPProjectionRevisions(manifest)
	result := &Result{ContractStatus: "valid", Manifest: manifest, HTTPSurfaceRevisions: httpRevisions, OpenAPIRevisions: openAPIRevisions}
	artifact, err := GenerateOpenAPIArtifact(result, "public")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(artifact.Document, &document); err != nil {
		t.Fatal(err)
	}
	paths := document["paths"].(map[string]any)
	operationValue := paths["/api/items"].(map[string]any)["post"].(map[string]any)
	if operationValue["x-scenery-binding"] != binding.Address || document["x-scenery-http-surface-revision"] != httpRevisions[gateway.Address] {
		t.Fatalf("document = %#v", document)
	}
	components := document["components"].(map[string]any)["schemas"].(map[string]any)
	inputSchema := components["HouseInput"].(map[string]any)
	idSchema := inputSchema["properties"].(map[string]any)["id"].(map[string]any)
	if idSchema["format"] != "scenery-int64" {
		t.Fatalf("id schema = %#v", idSchema)
	}
	var descriptor map[string]any
	if err := json.Unmarshal(artifact.Descriptor, &descriptor); err != nil || descriptor["openapi_revision"] != openAPIRevisions[gateway.Address] {
		t.Fatalf("descriptor = %#v err=%v", descriptor, err)
	}
}
