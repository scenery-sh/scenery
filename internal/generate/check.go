package generate

import "scenery.sh/internal/compiler"

// Check verifies generated artifacts and native Go implementations without
// mutating the compiler result or the workspace.
func Check(result *compiler.Result) []Diagnostic {
	if result == nil || !result.Valid() {
		return nil
	}
	var diagnostics []Diagnostic
	if _, err := generateGoContractsFromResult(result, true); err != nil {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN6203", Severity: "error", Message: err.Error()})
	}
	if _, err := generateTypeScriptClientsFromResult(result, "", true); err != nil {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN6204", Severity: "error", Message: err.Error()})
	}
	diagnostics = append(diagnostics, VerifyImplementation(result)...)
	return diagnostics
}
