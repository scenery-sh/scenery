package compiler

import "strings"

// validateGeneratedPageRoute owns the routing metadata shared by every
// generated React page macro. Query parameters are optional at the URL
// boundary, but their declared values use the ordinary Scenery type system.
func validateGeneratedPageRoute(resources map[string]Resource, page Resource) []Diagnostic {
	var diagnostics []Diagnostic
	seen := map[string]bool{}
	for _, search := range namedChildren(page.Spec, "search") {
		name := stringValue(search["name"])
		if name == "" || seen[name] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2619", "page search parameters require unique names", page))
		}
		seen[name] = true
		if !generatedPageSearchTypeSupported(resources, page.Module, search["type"]) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2619", "page search parameter "+name+" has an unsupported type", page))
		}
	}

	group := strings.TrimSpace(stringValue(page.Spec["nav_group"]))
	hasNavigationMetadata := group != "" ||
		page.Spec["nav_order"] != nil ||
		strings.TrimSpace(stringValue(page.Spec["nav_label"])) != "" ||
		strings.TrimSpace(stringValue(page.Spec["nav_icon"])) != "" ||
		len(stringValues(page.Spec["nav_active_paths"])) > 0
	if hasNavigationMetadata && group == "" {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2619", "page navigation metadata requires nav_group", page))
	}
	if value, exists := page.Spec["nav_order"]; exists {
		order, valid := integerValue(value)
		if !valid || order < 0 {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2619", "page nav_order must be a non-negative integer", page))
		}
	}
	for _, path := range stringValues(page.Spec["nav_active_paths"]) {
		if !validHTTPPath(path) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2619", "page nav_active_paths must contain valid absolute routes", page))
		}
	}
	return diagnostics
}

func generatedPageSearchTypeSupported(resources map[string]Resource, module string, value any) bool {
	expression := strings.TrimSpace(typeExpression(value))
	switch expression {
	case "string", "bool":
		return true
	}
	if !strings.HasPrefix(expression, "enum.") {
		return false
	}
	enum := resources[resourceAddress(module, "enum", strings.TrimPrefix(expression, "enum."))]
	return enum.Kind == "scenery.enum" && enum.Spec["open"] != true
}
