package compiler

import (
	"strings"

	"scenery.sh/internal/spec"
)

func validateStatusMap(statusMap Resource) []Diagnostic {
	variants := stringSliceSet(spec.StatusBadgeVariants())
	seen := map[string]bool{}
	var diagnostics []Diagnostic
	for _, status := range orderedChildren(statusMap.Spec, "status") {
		name := stringValue(status["name"])
		label := strings.TrimSpace(stringValue(status["label"]))
		variant := stringValue(status["variant"])
		if name == "" || seen[name] || label == "" || !variants[variant] {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2620", "status_map statuses require unique names, non-empty labels, and supported variants", statusMap))
		}
		seen[name] = true
	}
	if len(seen) == 0 {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2620", "status_map requires at least one status", statusMap))
	}
	return diagnostics
}
