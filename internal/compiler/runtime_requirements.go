package compiler

import "sort"

// RuntimeServices is the existing native registration selection. A declaration
// without an implemented operation is not an eagerly required runtime service.
func RuntimeServices(resources []Resource) []Resource {
	var services []Resource
	for _, resource := range resources {
		if resource.Kind != "scenery.service" || resource.Origin.Kind != "authored" && !IsProviderCRUDService(resource) {
			continue
		}
		implementation, _ := resource.Spec["implementation"].(map[string]any)
		if implementation == nil {
			continue
		}
		for _, operation := range ServiceOperations(resources, resource) {
			if handler, _ := operation.Spec["handler"].(map[string]any); handler != nil {
				services = append(services, resource)
				break
			}
		}
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Address < services[j].Address })
	return services
}

func IsProviderCRUDService(service Resource) bool {
	if service.Kind != "scenery.service" || stringValue(service.Spec["runtime"]) != "provider" {
		return false
	}
	implementation, _ := service.Spec["implementation"].(map[string]any)
	return stringValue(implementation["adapter"]) == "provider_crud_v1"
}

func ServiceOperations(resources []Resource, service Resource) []Resource {
	var operations []Resource
	for _, resource := range resources {
		if resource.Kind == "scenery.operation" && resource.Module == service.Module && resolveResourceRef(resource, refString(resource.Spec["service"]), "service") == service.Address {
			operations = append(operations, resource)
		}
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].Address < operations[j].Address })
	return operations
}

// DurableExecutionsForOperations selects exactly the durable registrations
// emitted for these operations; an unused engine alone has no SQL requirement.
func DurableExecutionsForOperations(resources, operations []Resource) []Resource {
	operationAddresses := map[string]bool{}
	for _, operation := range operations {
		operationAddresses[operation.Address] = true
	}
	var executions []Resource
	for _, execution := range resources {
		if execution.Kind != "scenery.execution" || stringValue(execution.Spec["mode"]) != "durable" {
			continue
		}
		operationAddress := resolveResourceRef(execution, refString(execution.Spec["operation"]), "operation")
		if operationAddresses[operationAddress] {
			executions = append(executions, execution)
		}
	}
	sort.Slice(executions, func(i, j int) bool { return executions[i].Address < executions[j].Address })
	return executions
}
