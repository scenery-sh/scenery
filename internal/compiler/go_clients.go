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
	var moduleServices []Resource
	seenTarget := false
	for _, candidate := range resources {
		if candidate.Kind == "scenery.service" && candidate.Module == service.Module {
			moduleServices = append(moduleServices, candidate)
			if candidate.Address == service.Address {
				seenTarget = true
			}
		}
	}
	if !seenTarget && service.Address != "" {
		moduleServices = append(moduleServices, service)
	}
	sort.Slice(moduleServices, func(i, j int) bool { return moduleServices[i].Address < moduleServices[j].Address })

	usedInterfaces := map[string]bool{}
	byService := make(map[string][]goClientBinding, len(moduleServices))
	for _, current := range moduleServices {
		var result []goClientBinding
		for _, client := range namedChildren(current.Spec, "client") {
			name := stringValue(client["name"])
			bindingRef := refString(client["binding"])
			bindingAddress := resolveResourceRef(current, bindingRef, "binding")
			binding, ok := byAddress[bindingAddress]
			if name == "" || !ok || binding.Kind != "scenery.binding" {
				return nil, fmt.Errorf("service %s client %q references unavailable binding %q", current.Address, name, bindingRef)
			}
			if stringValue(binding.Spec["protocol"]) != "internal" {
				return nil, fmt.Errorf("service %s client %s requires an internal binding", current.Address, name)
			}
			operationAddress := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
			operation, ok := byAddress[operationAddress]
			if !ok || operation.Kind != "scenery.operation" {
				return nil, fmt.Errorf("service %s client %s references unavailable operation", current.Address, name)
			}
			delivery := stringValue(binding.Spec["delivery"])
			if delivery != "call" && delivery != "wait" && delivery != "enqueue" {
				return nil, fmt.Errorf("service %s client %s has unsupported delivery %q", current.Address, name, delivery)
			}
			result = append(result, goClientBinding{
				Name: name, Field: goName(name), Binding: binding, Operation: operation, Delivery: delivery,
			})
		}
		sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
		servicePrefix := goName(current.Name)
		for i := range result {
			base := result[i].Field + "InternalClient"
			iface := base
			if usedInterfaces[iface] {
				iface = servicePrefix + base
				for suffix := 2; usedInterfaces[iface]; suffix++ {
					iface = fmt.Sprintf("%s%s%dInternalClient", servicePrefix, result[i].Field, suffix)
				}
			}
			usedInterfaces[iface] = true
			result[i].InterfaceName = iface
		}
		byService[current.Address] = result
	}
	return byService[service.Address], nil
}
