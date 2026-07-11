package vnext

import (
	"fmt"

	"scenery.sh/internal/parse"
)

func verifyGoImplementation(result *Result) []Diagnostic {
	if result == nil || result.Manifest == nil {
		return nil
	}
	if !hasNativeGoHandlers(result.Manifest.Resources) {
		return nil
	}
	if err := validateInvariantPackageABIs(result); err != nil {
		return []Diagnostic{{Code: "SCN6208", Severity: "error", Message: err.Error()}}
	}
	var files []generatedFile
	for _, module := range localModules(result.Manifest.Resources) {
		moduleFiles, err := generateModuleContract(result, module)
		if err != nil {
			return []Diagnostic{{Code: "SCN6201", Severity: "error", Message: err.Error(), Address: module.Address}}
		}
		files = append(files, moduleFiles...)
	}
	bootstrapOverlay, err := goGenerationBootstrapOverlay(result, files)
	if err != nil {
		return []Diagnostic{{Code: "SCN6207", Severity: "error", Message: err.Error()}}
	}
	bridgeFiles, err := generateLegacyBridgeArtifacts(result, nativeApplicationServices(result), bootstrapOverlay)
	if err != nil {
		return []Diagnostic{{Code: "SCN6207", Severity: "error", Message: err.Error()}}
	}
	files = append(files, bridgeFiles...)
	applicationFiles, err := generateApplicationArtifacts(result)
	if err != nil {
		return []Diagnostic{{Code: "SCN6207", Severity: "error", Message: err.Error()}}
	}
	files = append(files, applicationFiles...)
	files, err = includeStaleGeneratedFiles(result.Root, files, goGeneratedDescriptorNames(), protectedGoGeneratedDescriptors(result))
	if err != nil {
		return []Diagnostic{{Code: "SCN6207", Severity: "error", Message: err.Error()}}
	}
	result.verifiedGoFiles = append([]generatedFile(nil), files...)
	result.hasVerifiedGoFiles = true
	overlay, err := generatedGoVerificationOverlay(files)
	if err != nil {
		return []Diagnostic{{Code: "SCN6207", Severity: "error", Message: err.Error()}}
	}
	targets, err := goVerificationTargets(result)
	if err != nil {
		return []Diagnostic{{Code: "SCN6202", Severity: "error", Message: fmt.Sprintf("resolve Go verification targets: %v", err)}}
	}
	var diagnostics []Diagnostic
	for _, target := range targets {
		appModel, err := parse.AppWithOverlayTarget(result.Root, result.Manifest.Application.Name, overlay, target.Context)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6202", Severity: "error", Message: fmt.Sprintf("staged Go implementation verification failed for %s: %v", target.Resource.Address, err), Address: target.Resource.Address})
			continue
		}
		if stringValue(target.Effective["role"]) == "contract" {
			continue
		}
		diagnostics = append(diagnostics, validateNativeGoServices(appModel, result.Manifest.Resources, result.Migration)...)
		diagnostics = append(diagnostics, validateNativeGoHandlers(appModel, result.Manifest.Resources, result.Migration)...)
	}
	return diagnostics
}

func hasNativeGoHandlers(resources []Resource) bool {
	for _, resource := range resources {
		if resource.Kind != "scenery.operation/v1" || resource.Origin.Kind != "authored" {
			continue
		}
		handler, _ := resource.Spec["handler"].(map[string]any)
		if handler != nil && handler["adapter"] != "legacy_go_v0" {
			return true
		}
	}
	return false
}
