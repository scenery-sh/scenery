package generate

import (
	"fmt"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/parse"
)

// VerifyImplementation checks native Go services against artifacts rendered
// from the same immutable compiler result without writing them.
func VerifyImplementation(result *compiler.Result) []Diagnostic {
	if result == nil || result.Manifest == nil {
		return nil
	}
	if !usesGoImplementation(result.Manifest.Resources) || !hasNativeGoHandlers(result.Manifest.Resources) {
		return nil
	}
	if err := validateInvariantPackageABIs(result); err != nil {
		return []Diagnostic{{Code: "SCN6208", Severity: "error", Message: err.Error()}}
	}
	idx := newResourceIndex(result.Manifest.Resources)
	var files []generatedFile
	for _, module := range localModules(result.Manifest.Resources) {
		moduleFiles, err := generateModuleContract(result, idx, module)
		if err != nil {
			return []Diagnostic{{Code: "SCN6201", Severity: "error", Message: err.Error(), Address: module.Address}}
		}
		files = append(files, moduleFiles...)
	}
	applicationFiles, err := generateApplicationArtifacts(result, idx)
	if err != nil {
		return []Diagnostic{{Code: "SCN6207", Severity: "error", Message: err.Error()}}
	}
	files = append(files, applicationFiles...)
	libraryFiles, err := generateLibraryArtifacts(result, idx)
	if err != nil {
		return []Diagnostic{{Code: "SCN6207", Severity: "error", Message: err.Error()}}
	}
	files = append(files, libraryFiles...)
	files, err = includeStaleGeneratedFiles(result.Root, files, goGeneratedDescriptorNames(), protectedGoGeneratedDescriptors(result))
	if err != nil {
		return []Diagnostic{{Code: "SCN6207", Severity: "error", Message: err.Error()}}
	}
	overlay, err := generatedGoVerificationOverlay(files)
	if err != nil {
		return []Diagnostic{{Code: "SCN6207", Severity: "error", Message: err.Error()}}
	}
	targets, err := compiler.VerificationGoTargets(result)
	if err != nil {
		return []Diagnostic{{Code: "SCN6202", Severity: "error", Message: fmt.Sprintf("resolve Go verification targets: %v", err)}}
	}
	var diagnostics []Diagnostic
	for _, target := range targets {
		target.Context.Patterns = append(target.Context.Patterns, generatedLibraryPackagePatterns(result.Root, files)...)
		appModel, appModelErr := parse.AnalyzeTarget(result.Root, result.Manifest.Application.Name, overlay, target.Context)
		if appModelErr != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6202", Severity: "error", Message: fmt.Sprintf("staged Go implementation verification failed for %s: %v", target.Address, appModelErr), Address: target.Address})
			continue
		}
		if target.Role == "contract" {
			continue
		}
		diagnostics = append(diagnostics, validateNativeGoServices(appModel, result.Manifest.Resources)...)
		diagnostics = append(diagnostics, validateNativeGoHandlers(appModel, result.Manifest.Resources)...)
		diagnostics = append(diagnostics, validateNativeGoLibraries(appModel, result.Manifest.Resources)...)
	}
	return diagnostics
}

func usesGoImplementation(resources []Resource) bool {
	for _, resource := range resources {
		if resource.Kind == "scenery.go-target" {
			return true
		}
	}
	return false
}

func hasNativeGoHandlers(resources []Resource) bool {
	for _, resource := range resources {
		if resource.Kind != "scenery.operation" || resource.Origin.Kind != "authored" {
			continue
		}
		handler, _ := resource.Spec["handler"].(map[string]any)
		if handler != nil {
			return true
		}
	}
	return false
}
