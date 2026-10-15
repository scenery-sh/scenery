package generate

import "testing"

func TestPublicClientExcludesOpaqueFrameworkCallback(t *testing.T) {
	resources := []Resource{
		{Address: "scenery_auth/operation/connection_callback_files", Kind: "scenery.operation", Name: "connection_callback_files", Module: "scenery_auth"},
		{Address: "scenery_auth/binding/connection_callback_files_http", Kind: "scenery.binding", Module: "scenery_auth", Origin: Origin{Kind: "framework"}, Spec: map[string]any{
			"protocol": "http", "operation": map[string]any{"$ref": "scenery_auth/operation/connection_callback_files"},
			"http": map[string]any{"method": "GET", "path": "/auth/connections/files/callback", "guarantee": "opaque"},
		}},
	}
	if bindings := publicHTTPBindings(resources, Resource{}); len(bindings) != 0 {
		t.Fatalf("browser callback exposed as an ordinary client call: %v", bindings)
	}
}
