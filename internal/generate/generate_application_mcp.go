package generate

import (
	"sort"
)

// mcpToolTarget is the provider-neutral projection consumed by generated
// adapter rendering. The generated invoke closure still lives beside the
// ordinary service adapter so policy and durable dispatch cannot drift.
type mcpToolTarget struct {
	Binding          Resource
	Operation        Resource
	Execution        Resource
	AssistantAddress string
	Name             string
	Title            string
	Description      string
	Approval         string
	ReadOnly         bool
	Destructive      bool
	Idempotent       bool
	OpenWorld        bool
	MaxInputBytes    int64
	MaxResultBytes   int64
}

func mcpBindingsForService(resources []Resource, byAddress map[string]Resource, service Resource, operations []Resource) []mcpToolTarget {
	ownedOperations := map[string]Resource{}
	for _, operation := range operations {
		ownedOperations[operation.Address] = operation
	}
	var targets []mcpToolTarget
	for _, binding := range resources {
		if binding.Kind != "scenery.binding" || stringValue(binding.Spec["protocol"]) != "mcp" {
			continue
		}
		operationAddress := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
		operation, owned := ownedOperations[operationAddress]
		if !owned {
			continue
		}
		execution, ok := executionForBinding(byAddress, binding)
		if !ok {
			continue
		}
		mcpSpec, _ := binding.Spec["mcp"].(map[string]any)
		for _, server := range resources {
			if server.Kind != "scenery.mcp-server" {
				continue
			}
			for _, capability := range namedChildren(server.Spec, "capability") {
				if resolveResourceRef(server, refString(capability["binding"]), "binding") != binding.Address {
					continue
				}
				for _, assistant := range resources {
					if assistant.Kind != "scenery.assistant" || resolveResourceRef(assistant, refString(assistant.Spec["mcp_server"]), "mcp_server") != server.Address {
						continue
					}
					maxInputValue, _ := integerValue(server.Spec["max_input_bytes"])
					maxResultValue, _ := integerValue(server.Spec["max_result_bytes"])
					maxInput, maxResult := int64(maxInputValue), int64(maxResultValue)
					target := mcpToolTarget{
						Binding: binding, Operation: operation, Execution: execution, AssistantAddress: assistant.Address,
						Name: stringValue(capability["name"]), Title: stringValue(mcpSpec["title"]), Description: stringValue(mcpSpec["description"]),
						Approval: stringValue(capability["approval"]), ReadOnly: mcpSpec["read_only"] == true, Destructive: mcpSpec["destructive"] == true,
						Idempotent: mcpSpec["idempotent"] == true, OpenWorld: mcpSpec["open_world"] == true,
						MaxInputBytes: maxInput, MaxResultBytes: maxResult,
					}
					targets = append(targets, target)
				}
			}
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].AssistantAddress != targets[j].AssistantAddress {
			return targets[i].AssistantAddress < targets[j].AssistantAddress
		}
		if targets[i].Name != targets[j].Name {
			return targets[i].Name < targets[j].Name
		}
		return targets[i].Binding.Address < targets[j].Binding.Address
	})
	return targets
}

func mcpBindingResources(targets []mcpToolTarget) []Resource {
	resources := make([]Resource, 0, len(targets))
	seen := map[string]bool{}
	for _, target := range targets {
		if target.Binding.Address == "" || seen[target.Binding.Address] {
			continue
		}
		seen[target.Binding.Address] = true
		resources = append(resources, target.Binding)
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Address < resources[j].Address })
	return resources
}
