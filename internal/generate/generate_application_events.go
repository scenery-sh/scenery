package generate

import (
	"sort"
)

func schedulesForOperations(resources, operations []Resource) []Resource {
	owned := operationAddressSet(operations)
	var schedules []Resource
	for _, resource := range resources {
		if resource.Kind != "scenery.schedule" {
			continue
		}
		invoke, _ := resource.Spec["invoke"].(map[string]any)
		if owned[resolveResourceRef(resource, refString(invoke["operation"]), "operation")] {
			schedules = append(schedules, resource)
		}
	}
	sort.Slice(schedules, func(i, j int) bool { return schedules[i].Address < schedules[j].Address })
	return schedules
}

func operationAddressSet(operations []Resource) map[string]bool {
	owned := make(map[string]bool, len(operations))
	for _, operation := range operations {
		owned[operation.Address] = true
	}
	return owned
}
