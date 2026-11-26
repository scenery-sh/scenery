package generate

import (
	"testing"
)

func TestMCPOnlyBindingOwnershipIncludesExecution(t *testing.T) {
	binding := Resource{Address: "house/binding/process_scene_mcp", Kind: "scenery.binding", Spec: map[string]any{
		"execution": map[string]any{"$ref": "house/execution/process_scene_direct"},
	}}
	targets := []mcpToolTarget{{Binding: binding}}
	bindings := mcpBindingResources(targets)
	if got := resourceAddresses(bindings); len(got) != 1 || got[0] != binding.Address {
		t.Fatalf("MCP binding ownership = %#v, want %q", got, binding.Address)
	}
	resources := append([]Resource(nil), bindings...)
	resources = append(resources, Resource{Address: "house/execution/process_scene_direct", Kind: "scenery.execution"})
	got := referencedExecutions(resourcesByAddress(&Manifest{Resources: resources}), bindings)
	if len(got) != 1 || got[0] != "house/execution/process_scene_direct" {
		t.Fatalf("MCP execution ownership = %#v, want execution address", got)
	}
}
