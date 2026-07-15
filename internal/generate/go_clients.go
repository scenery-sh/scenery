package generate

import (
	"fmt"
	"sort"
	"strings"
)

type goClientBinding struct {
	Name           string
	Field          string
	InterfaceName  string
	ContractAlias  string
	ContractImport string
	Binding        Resource
	Operation      Resource
	Delivery       string
}

func serviceGoClients(idx *resourceIndex, service Resource) ([]goClientBinding, error) {
	byAddress := idx.byAddress
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
		contractAlias, contractImport := "", ""
		if operation.Module != service.Module {
			var found bool
			contractImport, found = idx.contractImport(operation.Module)
			if !found {
				return nil, fmt.Errorf("service %s client %s target package %s has no immutable Go contract import path", service.Address, name, operation.Module)
			}
			contractAlias = goPackageName(operation.Module) + "contract"
		}
		delivery := stringValue(binding.Spec["delivery"])
		if delivery != "call" && delivery != "wait" && delivery != "enqueue" {
			return nil, fmt.Errorf("service %s client %s has unsupported delivery %q", service.Address, name, delivery)
		}
		result = append(result, goClientBinding{
			Name: name, Field: goName(name), InterfaceName: goName(name) + "InternalClient", ContractAlias: contractAlias, ContractImport: contractImport,
			Binding: binding, Operation: operation, Delivery: delivery,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func moduleContractImportPath(resources []Resource, module string) (string, bool) {
	for _, resource := range resources {
		if resource.Kind != "scenery.module" || moduleInstancePath(resource) != module {
			continue
		}
		metadata, _ := resource.Spec["package"].(map[string]any)
		goContract, _ := metadata["go_contract"].(map[string]any)
		importPath := strings.TrimSpace(stringValue(goContract["import_path"]))
		if importPath != "" {
			return strings.TrimSuffix(importPath, "/") + "/scenerycontract", true
		}
	}
	return "", false
}

func clientContractQualifier(client goClientBinding) string {
	if client.ContractAlias == "" {
		return ""
	}
	return client.ContractAlias + "."
}

func internalBindingsForOperations(resources []Resource, operations []Resource) []Resource {
	operationAddresses := map[string]bool{}
	for _, operation := range operations {
		operationAddresses[operation.Address] = true
	}
	var bindings []Resource
	for _, binding := range resources {
		if binding.Kind != "scenery.binding" || stringValue(binding.Spec["protocol"]) != "internal" {
			continue
		}
		operationAddress := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
		if operationAddresses[operationAddress] {
			bindings = append(bindings, binding)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Address < bindings[j].Address })
	return bindings
}
