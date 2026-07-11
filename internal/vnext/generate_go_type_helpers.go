package vnext

import (
	"fmt"
	"sort"
	"strings"
)

type goContractTypeResolver struct {
	module    string
	resources map[string]Resource
	imports   map[string]string
	embedded  map[string]bool
	err       error
}

func newGoContractTypeResolver(module string, resources []Resource, embedded ...map[string]bool) *goContractTypeResolver {
	resolver := &goContractTypeResolver{
		module: module, resources: resourcesByAddress(&Manifest{Resources: resources}), imports: map[string]string{}, embedded: map[string]bool{},
	}
	if len(embedded) > 0 {
		for address, included := range embedded[0] {
			resolver.embedded[address] = included
		}
	}
	return resolver
}

func (resolver *goContractTypeResolver) Type(value any) string {
	if expression, ok := value.(string); ok {
		return resolver.Expression(expression)
	}
	if reference := refString(value); reference != "" {
		if target, ok := resolver.resources[reference]; ok && isNamedContractType(target) {
			if target.Module == resolver.module || resolver.embedded[target.Address] {
				return goName(target.Name)
			}
			alias := resolver.importAlias(target.Module)
			if alias == "" {
				return "any"
			}
			return alias + "." + goName(target.Name)
		}
		return goType(value)
	}
	if expression, ok := value.(map[string]any); ok {
		if raw, ok := expression["$expression"].(string); ok {
			return resolver.Expression(raw)
		}
	}
	return goType(value)
}

func (resolver *goContractTypeResolver) Expression(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == `resource_ref("secret")` {
		return "scenery.SecretRef"
	}
	if strings.HasPrefix(raw, "tuple(") && strings.HasSuffix(raw, ")") {
		return tupleGoTypeName(raw)
	}
	for _, wrapper := range []struct{ prefix, goPrefix string }{{"optional(", "scenery.Optional["}, {"nullable(", "scenery.Nullable["}, {"list(", "[]"}, {"set(", "scenery.Set["}, {"map(", "map[string]"}} {
		if strings.HasPrefix(raw, wrapper.prefix) && strings.HasSuffix(raw, ")") {
			inner := resolver.Expression(strings.TrimSuffix(strings.TrimPrefix(raw, wrapper.prefix), ")"))
			if strings.HasSuffix(wrapper.goPrefix, "[") {
				return wrapper.goPrefix + inner + "]"
			}
			return wrapper.goPrefix + inner
		}
	}
	return resolver.Type(map[string]any{"$ref": raw})
}

func (resolver *goContractTypeResolver) importAlias(module string) string {
	importPath, ok := moduleContractImportPath(resourceValues(resolver.resources), module)
	if !ok {
		resolver.setError(fmt.Errorf("package %s references type from %s, which has no immutable Go contract import path", resolver.module, module))
		return ""
	}
	alias := goPackageName(module) + "contract"
	if alias == "contract" {
		resolver.setError(fmt.Errorf("package %s cannot derive a Go contract import alias for %s", resolver.module, module))
		return ""
	}
	if previous := resolver.imports[alias]; previous != "" && previous != importPath {
		resolver.setError(fmt.Errorf("generated import alias %s resolves to both %s and %s", alias, previous, importPath))
		return ""
	}
	resolver.imports[alias] = importPath
	return alias
}

func (resolver *goContractTypeResolver) Qualified(resource Resource) string {
	if resource.Module == resolver.module || resolver.embedded[resource.Address] {
		return goName(resource.Name)
	}
	alias := resolver.importAlias(resource.Module)
	if alias == "" {
		return goName(resource.Name)
	}
	return alias + "." + goName(resource.Name)
}

func (resolver *goContractTypeResolver) IsEmbedded(address string) bool {
	return resolver.embedded[address]
}

func (resolver *goContractTypeResolver) Imports() map[string]string {
	result := make(map[string]string, len(resolver.imports))
	for alias, importPath := range resolver.imports {
		result[alias] = importPath
	}
	return result
}

func (resolver *goContractTypeResolver) Err() error { return resolver.err }

func (resolver *goContractTypeResolver) setError(err error) {
	if resolver.err == nil {
		resolver.err = err
	}
}

func (resolver *goContractTypeResolver) referencedResources(resources []Resource, kind string) []Resource {
	selected := map[string]Resource{}
	for _, value := range contractTypeValues(resources) {
		for _, reference := range typeReferences(value) {
			resource, ok := resolver.resources[reference]
			if ok && resource.Kind == kind {
				selected[resource.Address] = resource
			}
		}
	}
	result := make([]Resource, 0, len(selected))
	for _, resource := range selected {
		result = append(result, resource)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result
}

func contractTypeValues(resources []Resource) []any {
	var values []any
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.record/v1":
			for _, field := range namedChildren(resource.Spec, "field") {
				values = append(values, field["type"])
			}
		case "scenery.union/v1":
			for _, variant := range namedChildren(resource.Spec, "variant") {
				values = append(values, variant["type"])
			}
		case "scenery.operation/v1":
			values = append(values, resource.Spec["input"])
			for _, kind := range []string{"result", "error"} {
				for _, variant := range namedChildren(resource.Spec, kind) {
					values = append(values, variant["type"])
				}
			}
		case "scenery.service/v1":
			for _, field := range namedChildren(resource.Spec, "config_schema") {
				values = append(values, field["type"])
			}
		}
	}
	return values
}

func isNamedContractType(resource Resource) bool {
	switch resource.Kind {
	case "scenery.record/v1", "scenery.enum/v1", "scenery.union/v1":
		return true
	default:
		return false
	}
}

func resourceValues(resources map[string]Resource) []Resource {
	result := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		result = append(result, resource)
	}
	return result
}

func goContractTypeClosure(module string, local, all []Resource) ([]Resource, map[string]bool, error) {
	byAddress := resourcesByAddress(&Manifest{Resources: all})
	needed := map[string]bool{}
	for _, resource := range local {
		if isNamedContractType(resource) {
			continue
		}
		for _, value := range contractTypeValues([]Resource{resource}) {
			collectABITypeReferences(value, resource.Module, byAddress, needed)
		}
	}
	for _, resource := range all {
		if resource.Kind != "scenery.module/v1" || moduleInstancePath(resource) != module {
			continue
		}
		if exports, ok := resource.Spec["exports"].(map[string]any); ok {
			for _, value := range exports {
				collectABITypeReferences(value, module, byAddress, needed)
			}
		}
	}
	embedded := map[string]bool{}
	for changed := true; changed; {
		changed = false
		addresses := make([]string, 0, len(needed))
		for address := range needed {
			addresses = append(addresses, address)
		}
		sort.Strings(addresses)
		for _, address := range addresses {
			resource, ok := byAddress[address]
			if !ok || !isNamedContractType(resource) {
				continue
			}
			if resource.Module != module {
				if _, hasContract := moduleContractImportPath(all, resource.Module); !hasContract && !embedded[address] {
					embedded[address] = true
					changed = true
				}
			}
			for _, value := range contractTypeValues([]Resource{resource}) {
				before := len(needed)
				collectABITypeReferences(value, resource.Module, byAddress, needed)
				changed = changed || before != len(needed)
			}
		}
	}
	result := make([]Resource, 0, len(local)+len(embedded))
	for _, resource := range local {
		if !isNamedContractType(resource) || needed[resource.Address] {
			result = append(result, resource)
		}
	}
	for address := range embedded {
		result = append(result, byAddress[address])
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	if err := validateGoContractClosureNames(module, result); err != nil {
		return nil, nil, err
	}
	return result, embedded, nil
}

func validateGoContractClosureNames(module string, resources []Resource) error {
	seen := map[string]string{}
	for _, resource := range resources {
		if !isNamedContractType(resource) {
			continue
		}
		name := goName(resource.Name)
		if previous := seen[name]; previous != "" && previous != resource.Address {
			return fmt.Errorf("package %s generated type %s collides between %s and %s", module, name, previous, resource.Address)
		}
		seen[name] = resource.Address
	}
	return nil
}

func optionalInner(value any) (any, bool) {
	expression, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	raw, ok := expression["$expression"].(string)
	if !ok {
		return nil, false
	}
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "optional(") || !strings.HasSuffix(raw, ")") {
		return nil, false
	}
	return map[string]any{"$expression": strings.TrimSpace(raw[len("optional(") : len(raw)-1])}, true
}

func goTypeExpression(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == `resource_ref("secret")` {
		return "scenery.SecretRef"
	}
	if strings.HasPrefix(raw, "tuple(") && strings.HasSuffix(raw, ")") {
		return tupleGoTypeName(raw)
	}
	for _, wrapper := range []struct{ prefix, goPrefix string }{{"optional(", "scenery.Optional["}, {"nullable(", "scenery.Nullable["}, {"list(", "[]"}, {"set(", "scenery.Set["}, {"map(", "map[string]"}} {
		if strings.HasPrefix(raw, wrapper.prefix) && strings.HasSuffix(raw, ")") {
			inner := goTypeExpression(strings.TrimSuffix(strings.TrimPrefix(raw, wrapper.prefix), ")"))
			if strings.HasSuffix(wrapper.goPrefix, "[") {
				return wrapper.goPrefix + inner + "]"
			}
			return wrapper.goPrefix + inner
		}
	}
	return goType(map[string]any{"$ref": raw})
}
