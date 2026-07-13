package compiler

import (
	"fmt"
	"sort"
	"strings"
)

type goDependencyBinding struct {
	Name          string
	Field         string
	Address       string
	GoType        string
	ImportAlias   string
	ImportPath    string
	CapabilityABI string
	Resolver      string
	RuntimeName   string
}

func serviceGoDependencies(resources []Resource, service Resource) ([]goDependencyBinding, error) {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	var result []goDependencyBinding
	for _, dependency := range namedChildren(service.Spec, "dependency") {
		name := stringValue(dependency["name"])
		reference := refString(dependency["instance"])
		address := resolveResourceRef(service, reference, "data_source")
		instance, ok := byAddress[address]
		if name == "" || !ok {
			return nil, fmt.Errorf("service %s dependency %q references unavailable instance %q", service.Address, name, reference)
		}
		capabilities := stringListSet(instance.Spec["require_capabilities"])
		binding := goDependencyBinding{Name: name, Field: goName(name), Address: address, RuntimeName: instance.Name}
		switch {
		case hasCapabilityPrefix(capabilities, "sql."):
			binding.GoType, binding.ImportAlias, binding.ImportPath, binding.CapabilityABI, binding.Resolver = "datasource.SQL", "datasource", "scenery.sh/datasource", "scenery.datasource/v1", "sql"
			if config, _ := instance.Spec["config"].(map[string]any); config != nil {
				if database := stringValue(config["database"]); database != "" {
					binding.RuntimeName = database
				}
			}
		case hasCapabilityPrefix(capabilities, "object."):
			binding.GoType, binding.ImportAlias, binding.ImportPath, binding.CapabilityABI, binding.Resolver = "object.Store", "object", "scenery.sh/object", "scenery.object/v1", "object"
			if config, _ := instance.Spec["config"].(map[string]any); config != nil {
				if bucket := stringValue(config["bucket"]); bucket != "" {
					binding.RuntimeName = bucket
				}
			}
		default:
			return nil, fmt.Errorf("service %s dependency %s has no supported stable Go capability ABI", service.Address, name)
		}
		result = append(result, binding)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func hasCapabilityPrefix(capabilities map[string]bool, prefix string) bool {
	for capability := range capabilities {
		if strings.HasPrefix(capability, prefix) {
			return true
		}
	}
	return false
}
