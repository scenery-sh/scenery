package vnext

import "testing"

func TestHTTPProjectionRevisionChangesOnlyForGatewaySurface(t *testing.T) {
	gateway := Resource{Address: "app/http_gateway/public", Module: "app", Kind: "scenery.http-gateway/v1", Name: "public", Spec: map[string]any{"base_path": "/", "exposure": "internet"}}
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation/v1", Name: "get", Spec: map[string]any{"input": map[string]any{"$ref": "record.input"}, "handler": map[string]any{"method": "Get"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "record.output"}}}}
	binding := Resource{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Name: "get", Spec: map[string]any{"gateway": map[string]any{"$ref": "http_gateway.public"}, "operation": map[string]any{"$ref": "operation.get"}, "protocol": "http", "http": map[string]any{"method": "GET", "path": "/items", "guarantee": "framework_enforced"}}}
	resources := []Resource{
		gateway, operation, binding,
		{Address: "house/record/input", Module: "house", Kind: "scenery.record/v1", Name: "input", Spec: map[string]any{}},
		{Address: "house/record/output", Module: "house", Kind: "scenery.record/v1", Name: "output", Spec: map[string]any{}},
		{Address: "other/record/private", Module: "other", Kind: "scenery.record/v1", Name: "private", Spec: map[string]any{}},
	}
	manifest := &Manifest{Resources: resources}
	httpBase, openAPIBase := computeHTTPProjectionRevisions(manifest)
	if httpBase[gateway.Address] == "" || openAPIBase[gateway.Address] == "" || httpBase[gateway.Address] == openAPIBase[gateway.Address] {
		t.Fatalf("revisions = %#v %#v", httpBase, openAPIBase)
	}
	manifest.Resources[1].Spec["handler"] = map[string]any{"method": "Different"}
	manifest.Resources[5].Spec["unknown_fields"] = "preserve"
	httpUnrelated, _ := computeHTTPProjectionRevisions(manifest)
	if httpUnrelated[gateway.Address] != httpBase[gateway.Address] {
		t.Fatal("implementation/private changes altered HTTP surface revision")
	}
	manifest.Resources[2].Spec["http"].(map[string]any)["path"] = "/changed"
	httpChanged, _ := computeHTTPProjectionRevisions(manifest)
	if httpChanged[gateway.Address] == httpBase[gateway.Address] {
		t.Fatal("route change did not alter HTTP surface revision")
	}
}
