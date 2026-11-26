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
	usedInterfaces := map[string]bool{
		"PackageIdentity":                 true,
		"PackageContractABIRevision":      true,
		"PackageImportPath":               true,
		"GoImplementationABIRange":        true,
		"RuntimeABIRange":                 true,
		"unmarshalGeneratedContractValue": true,
	}
	reserveService := func(candidate Resource) {
		name := goName(candidate.Name)
		usedInterfaces[name] = true
		for _, suffix := range []string{"Dependencies", "Config", "Clients", "ConstructorInput"} {
			usedInterfaces[name+suffix] = true
		}
	}
	var moduleServices []Resource
	seenTarget := false
	for _, candidate := range resources {
		if candidate.Module != service.Module {
			continue
		}
		reserveCompilerNonClientSymbols(usedInterfaces, candidate)
		if candidate.Kind == "scenery.service" {
			reserveService(candidate)
			moduleServices = append(moduleServices, candidate)
			if candidate.Address == service.Address {
				seenTarget = true
			}
		}
	}
	if !seenTarget && service.Address != "" {
		reserveService(service)
		moduleServices = append(moduleServices, service)
	}
	sort.Slice(moduleServices, func(i, j int) bool { return moduleServices[i].Address < moduleServices[j].Address })
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

type compilerVariantWrapper struct {
	kind        string
	variantName string
	wrapperName string
}

func compilerOperationVariantWrappers(name string, res Resource) []compilerVariantWrapper {
	localUsed := map[string]bool{
		name:                           true,
		name + "Input":                 true,
		"Unmarshal" + name + "Input":   true,
		"Clone" + name + "Input":       true,
		name + "Outcome":               true,
		"Marshal" + name + "Outcome":   true,
		"Unmarshal" + name + "Outcome": true,
		"Clone" + name + "Outcome":     true,
	}
	var wrappers []compilerVariantWrapper
	for _, kind := range []string{"result", "error"} {
		for _, variant := range namedChildren(res.Spec, kind) {
			variantName := stringValue(variant["name"])
			wrapper := name + goName(variantName)
			if localUsed[wrapper] {
				base := name + goName(kind) + "Variant" + goName(variantName)
				wrapper = base
				for suffix := 2; localUsed[wrapper]; suffix++ {
					wrapper = fmt.Sprintf("%s%d", base, suffix)
				}
			}
			localUsed[wrapper] = true
			wrappers = append(wrappers, compilerVariantWrapper{
				kind:        kind,
				variantName: variantName,
				wrapperName: wrapper,
			})
		}
	}
	return wrappers
}

func reserveCompilerNonClientSymbols(used map[string]bool, res Resource) {
	name := goName(res.Name)
	switch res.Kind {
	case "scenery.record":
		used[name] = true
	case "scenery.enum":
		used[name] = true
		for _, value := range namedChildren(res.Spec, "value") {
			used[name+goName(stringValue(value["name"]))] = true
		}
	case "scenery.union":
		used[name] = true
		used["Marshal"+name+"JSON"] = true
		used["Unmarshal"+name+"JSON"] = true
		used["marshal"+name+"Variant"] = true
		for _, variant := range namedChildren(res.Spec, "variant") {
			used[name+goName(stringValue(variant["name"]))] = true
		}
		if res.Spec["open"] == true {
			used[name+"Unknown"] = true
		}
	case "scenery.operation":
		for _, symbol := range []string{
			name,
			name + "Input",
			"Unmarshal" + name + "Input",
			"Clone" + name + "Input",
			name + "Outcome",
			"Marshal" + name + "Outcome",
			"Unmarshal" + name + "Outcome",
			"Clone" + name + "Outcome",
		} {
			used[symbol] = true
		}
		for _, wrapper := range compilerOperationVariantWrappers(name, res) {
			used[wrapper.wrapperName] = true
		}
	}
}
