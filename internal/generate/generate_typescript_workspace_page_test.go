package generate

import (
	"testing"
)

func TestWorkspaceStatsBindingIsIncludedByReactClient(t *testing.T) {
	operation := Resource{Address: "work/operation/workspace_stats", Module: "work", Name: "workspace_stats", Kind: "scenery.operation"}
	binding := Resource{Address: "work/binding/workspace_stats_http", Module: "work", Name: "workspace_stats_http", Kind: "scenery.binding", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "operation": map[string]any{"$ref": operation.Address}, "protocol": "http", "http": map[string]any{"method": "GET"},
	}}
	workspace := Resource{Address: "work/workspace_page/operations", Module: "work", Name: "operations", Kind: "scenery.workspace-page", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"stats": map[string]any{"source": map[string]any{"$ref": binding.Address}},
	}}
	target := Resource{Kind: "scenery.typescript-client", Spec: map[string]any{
		"gateways": []any{map[string]any{"$ref": "app/http_gateway/public"}}, "react": map[string]any{},
	}}
	selected := publicHTTPBindings([]Resource{operation, binding, workspace}, target)
	if len(selected) != 1 || selected[0].Address != binding.Address {
		t.Fatalf("workspace stats bindings = %#v", resourceAddresses(selected))
	}
}
