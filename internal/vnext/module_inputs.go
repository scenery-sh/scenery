package vnext

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var resourceRefTypePattern = regexp.MustCompile(`^resource_ref\("([a-z][a-z0-9_]*)"\)$`)

type packageInputDeclaration struct {
	Name      string
	Type      string
	Phase     string
	Default   any
	Optional  bool
	Sensitive bool
	Requires  []string
}

func resolveModuleInstanceInputs(rootResources, packageResources []Resource, packageSources []*Source, module *Block) ([]Resource, []Diagnostic) {
	return resolveModuleInstanceInputsInScope(rootResources, packageResources, packageSources, module, "app")
}

func resolveModuleInstanceInputsInScope(rootResources, packageResources []Resource, packageSources []*Source, module *Block, callerModule string) ([]Resource, []Diagnostic) {
	values, diagnostics := resolveModuleInputValues(rootResources, packageResources, packageSources, module, callerModule)
	resolved, substitutionDiagnostics := substituteResolvedModuleInputs(packageResources, values)
	diagnostics = append(diagnostics, substitutionDiagnostics...)
	return resolved, diagnostics
}

func substituteResolvedModuleInputs(packageResources []Resource, values map[string]any) ([]Resource, []Diagnostic) {
	var diagnostics []Diagnostic
	resolved := make([]Resource, len(packageResources))
	for index, resource := range packageResources {
		resolved[index] = resource
		resolved[index].Spec = cloneStringAnyMap(resource.Spec)
		value, unresolved := substituteModuleInputs(resolved[index].Spec, values)
		resolved[index].Spec, _ = value.(map[string]any)
		for _, name := range unresolved {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN3007", Severity: "error", Message: "resource references unresolved module input " + name, Address: resource.Address})
		}
	}
	return resolved, diagnostics
}

func resolveModuleInputValues(rootResources, packageResources []Resource, packageSources []*Source, module *Block, callerModule string) (map[string]any, []Diagnostic) {
	declarations := packageInputDeclarations(packageSources)
	provided := map[string]any{}
	if expression, ok := module.Attributes["inputs"]; ok {
		provided, _ = expression.Value.(map[string]any)
	}
	values := map[string]any{}
	var diagnostics []Diagnostic
	for name := range provided {
		if _, ok := declarations[name]; !ok {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN3008", "module supplies unknown input "+name, module))
		}
	}
	allResources := make(map[string]Resource, len(rootResources)+len(packageResources))
	for _, resource := range append(append([]Resource(nil), rootResources...), packageResources...) {
		allResources[resource.Address] = resource
	}
	for name, declaration := range declarations {
		value, exists := provided[name]
		if !exists {
			if declaration.Default != nil {
				value, exists = declaration.Default, true
			} else if declaration.Optional {
				continue
			}
		}
		if !exists {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN3007", "module is missing required input "+name, module))
			continue
		}
		if message := validateModuleInputValue(declaration, value, allResources, callerModule); message != "" {
			diagnostics = append(diagnostics, diagnosticForBlock("SCN3008", fmt.Sprintf("module input %s: %s", name, message), module))
			continue
		}
		values[name] = cloneSemanticValue(value)
	}

	return values, diagnostics
}

func packageInputDeclarations(sources []*Source) map[string]packageInputDeclaration {
	declarations := map[string]packageInputDeclaration{}
	for _, source := range sources {
		for _, block := range source.Blocks {
			if block.Type != "input" || len(block.Labels) != 1 {
				continue
			}
			declaration := packageInputDeclaration{Name: block.Labels[0]}
			if expression, ok := block.Attributes["type"]; ok {
				declaration.Type = strings.TrimSpace(expression.Raw)
			}
			if expression, ok := block.Attributes["default"]; ok {
				declaration.Default = expressionValue(expression)
			}
			if expression, ok := block.Attributes["phase"]; ok {
				declaration.Phase, _ = expression.Value.(string)
			}
			if expression, ok := block.Attributes["sensitive"]; ok {
				declaration.Sensitive, _ = expression.Value.(bool)
			}
			if expression, ok := block.Attributes["optional"]; ok {
				declaration.Optional, _ = expression.Value.(bool)
			}
			if expression, ok := block.Attributes["requires"]; ok {
				values, _ := expression.Value.([]any)
				for _, value := range values {
					if text, ok := value.(string); ok {
						declaration.Requires = append(declaration.Requires, text)
					}
				}
				sort.Strings(declaration.Requires)
			}
			declarations[declaration.Name] = declaration
		}
	}
	return declarations
}

func validateModuleInputValue(declaration packageInputDeclaration, value any, resources map[string]Resource, callerModule string) string {
	if matches := resourceRefTypePattern.FindStringSubmatch(declaration.Type); matches != nil {
		reference := refString(value)
		if reference == "" {
			return "requires a typed resource reference"
		}
		address := reference
		parts := strings.Split(reference, ".")
		if !strings.Contains(reference, "/") {
			if len(parts) != 2 || parts[0] != matches[1] {
				return fmt.Sprintf("requires resource_ref(%q), got %q", matches[1], reference)
			}
			module := callerModule
			if rootResourceKinds[parts[0]] {
				module = "app"
			}
			address = resourceAddress(module, parts[0], parts[1])
		}
		resource, exists := resources[address]
		if !exists || resource.Kind != kindForBlock(matches[1]) {
			return "references an unavailable resource " + reference
		}
		available := stringListSet(resource.Spec["effective_capabilities"])
		for _, capability := range declaration.Requires {
			if !available[capability] {
				return "resource does not satisfy required capability " + capability
			}
		}
		return ""
	}
	if declaration.Type == "" {
		return "has no declared type"
	}
	if refString(value) != "" {
		return "requires a value, not a resource reference"
	}
	return ""
}

func substituteModuleInputs(value any, values map[string]any) (any, []string) {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 1 {
			if reference := refString(typed); strings.HasPrefix(reference, "var.") {
				name := strings.TrimPrefix(reference, "var.")
				replacement, ok := values[name]
				if !ok {
					return typed, []string{name}
				}
				return cloneSemanticValue(replacement), nil
			}
		}
		result := make(map[string]any, len(typed))
		var unresolved []string
		for key, item := range typed {
			resolved, missing := substituteModuleInputs(item, values)
			result[key] = resolved
			unresolved = append(unresolved, missing...)
		}
		return result, canonicalStrings(unresolved)
	case []any:
		result := make([]any, len(typed))
		var unresolved []string
		for index, item := range typed {
			resolved, missing := substituteModuleInputs(item, values)
			result[index] = resolved
			unresolved = append(unresolved, missing...)
		}
		return result, canonicalStrings(unresolved)
	default:
		return typed, nil
	}
}

func cloneSemanticValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneStringAnyMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneSemanticValue(item)
		}
		return result
	default:
		return typed
	}
}

func cloneStringAnyMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = cloneSemanticValue(item)
	}
	return result
}

func stringListSet(value any) map[string]bool {
	result := map[string]bool{}
	items, _ := value.([]any)
	for _, item := range items {
		if text, ok := item.(string); ok {
			result[text] = true
		}
	}
	return result
}
