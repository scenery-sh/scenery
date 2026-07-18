package generate

import "scenery.sh/internal/compiler"

type CheckResult struct {
	Diagnostics           []Diagnostic
	ImplementationStatus  string
	ImplementationChecked bool
}

// Check verifies generated artifacts and native Go implementations without
// mutating the compiler result or the workspace.
func Check(result *compiler.Result) CheckResult {
	checked := result != nil && result.Manifest != nil && usesGoImplementation(result.Manifest.Resources) && hasNativeGoHandlers(result.Manifest.Resources)
	check := CheckResult{ImplementationStatus: "not_requested", ImplementationChecked: checked}
	if result == nil || !result.Valid() {
		return check
	}
	refresh := typescriptRefreshSuggestions(result)
	if files, err := renderTypeScriptClientFilesByMode(result, "", true); err != nil {
		check.Diagnostics = append(check.Diagnostics, Diagnostic{Code: "SCN6204", Severity: "error", Message: err.Error(), Suggestions: refresh})
	} else if err := verifyRenderedTypeScriptReact(result, sourceTypeScriptTargets(typescriptTargets(result.Manifest.Resources, "")), files); err != nil {
		check.Diagnostics = append(check.Diagnostics, Diagnostic{Code: "SCN6204", Severity: "error", Message: err.Error(), Suggestions: refresh})
	} else if _, err := finishGeneratedFiles(result.Root, files, true, "generated TypeScript clients are stale"); err != nil {
		check.Diagnostics = append(check.Diagnostics, Diagnostic{Code: "SCN6204", Severity: "error", Message: err.Error(), Suggestions: refresh})
	}
	check.Diagnostics = append(check.Diagnostics, VerifyImplementation(result)...)
	if checked {
		check.ImplementationStatus = "valid"
		for _, diagnostic := range check.Diagnostics {
			if diagnostic.Severity == "error" {
				check.ImplementationStatus = "invalid"
				break
			}
		}
	}
	return check
}

// typescriptRefreshSuggestions names the exact regeneration command for every
// declared TypeScript client target so stale-artifact diagnostics are
// actionable without reading generator internals.
func typescriptRefreshSuggestions(result *compiler.Result) []string {
	var suggestions []string
	for _, target := range typescriptTargets(result.Manifest.Resources, "") {
		suggestions = append(suggestions, "Run `scenery generate --target typescript_client."+target.Name+" -o json` in the app root to refresh committed generated clients.")
	}
	return suggestions
}

// ApplyCheck records one generation/implementation check on the immutable
// compiler snapshot used to perform it.
func ApplyCheck(result *compiler.Result, check CheckResult) {
	if result == nil {
		return
	}
	result.Diagnostics = append(result.Diagnostics, check.Diagnostics...)
	result.ImplementationStatus = check.ImplementationStatus
	if result.Manifest != nil {
		result.Manifest.Diagnostics = append([]Diagnostic{}, result.Diagnostics...)
	}
}
