package compiler

import (
	"maps"
	"slices"
	"sort"
	"strings"
)

func validateDataSemantics(resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	var diagnostics []Diagnostic
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.data-source":
			provider := byAddress[resolveResourceRef(resource, refString(resource.Spec["provider"]), "provider")]
			if provider.Kind != "scenery.provider" || !dataLifecycleAllowed(stringValue(resource.Spec["lifecycle"])) {
				diagnostics = append(diagnostics, dataDiagnostic("SCN2505", "data source requires a typed provider and managed, external, attached, or ephemeral lifecycle", resource))
			}
			if duplicate := duplicateStrings(stringValues(resource.Spec["require_capabilities"])); duplicate != "" {
				diagnostics = append(diagnostics, dataDiagnostic("SCN2505", "data source repeats required capability "+duplicate, resource))
			}
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Address != diagnostics[j].Address {
			return diagnostics[i].Address < diagnostics[j].Address
		}
		if diagnostics[i].Path != diagnostics[j].Path {
			return diagnostics[i].Path < diagnostics[j].Path
		}
		return diagnostics[i].Message < diagnostics[j].Message
	})
	return diagnostics
}

func unwrapCRUDListType(value string) string {
	for {
		unwrapped := false
		for _, wrapper := range []string{"optional", "nullable"} {
			if inner, ok := wrappedFixtureType(value, wrapper); ok {
				value, unwrapped = inner, true
				break
			}
		}
		if !unwrapped {
			return strings.TrimSpace(value)
		}
	}
}

func containsDataString(values []string, target string) bool {
	return slices.Contains(values, target)
}

func dataLifecycleAllowed(value string) bool {
	return value == "managed" || value == "external" || value == "attached" || value == "ephemeral"
}

func duplicateStrings(values []string) string {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return value
		}
		seen[value] = true
	}
	return ""
}

func dataDiagnostic(code, message string, resource Resource) Diagnostic {
	return Diagnostic{Code: code, Severity: "error", Message: message, Address: resource.Address}
}

func cloneMapValue(value any) map[string]any {
	source, _ := value.(map[string]any)
	if len(source) == 0 {
		return map[string]any{}
	}
	result := make(map[string]any, len(source))
	maps.Copy(result, source)
	return result
}
