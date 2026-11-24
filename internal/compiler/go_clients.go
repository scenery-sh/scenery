package compiler

import (
	"fmt"
	"sort"
)

type goClientBinding struct {
	Name          string
	Field         string
	InterfaceName string
	Binding       Resource
	Operation     Resource
	Delivery      string
}

func serviceGoClients(resources []Resource, service Resource) ([]goClientBinding, error) {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	var result []goClientBinding
	for _, client := range namedChildren(service.Spec, "client") {
		name := stringValue(client["name"])
		bindingRef := refString(client["binding"])
		bindingAddress := resolveResourceRef(service, bindingRef, "binding")
		binding, ok := byAddress[bindingAddress]
		if name == "" || !ok || binding.Kind != "scenery.binding" {
			return nil, fmt.Errorf("service %s client %q references unavailable binding %q", service.Address, name, bindingRef)
		}
		if stringValue(binding.Spec["protocol"]) != "internal" {
			return nil, fmt.Errorf("service %s client %s requires an internal binding", service.Address, name)
		}
		operationAddress := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
		operation, ok := byAddress[operationAddress]
		if !ok || operation.Kind != "scenery.operation" {
			return nil, fmt.Errorf("service %s client %s references unavailable operation", service.Address, name)
		}
		delivery := stringValue(binding.Spec["delivery"])
		if delivery != "call" && delivery != "wait" && delivery != "enqueue" {
			return nil, fmt.Errorf("service %s client %s has unsupported delivery %q", service.Address, name, delivery)
		}
		result = append(result, goClientBinding{
			Name: name, Field: goName(name), InterfaceName: goName(name) + "InternalClient",
			Binding: binding, Operation: operation, Delivery: delivery,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
