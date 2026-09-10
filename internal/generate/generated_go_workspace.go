package generate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

// RenderGoWorkspaceFiles returns every generated Go artifact needed by a
// build without reading or writing materialized artifacts in the app checkout.
func RenderGoWorkspaceFiles(result *compiler.Result) (map[string][]byte, error) {
	if result == nil || result.Manifest == nil || result.ContractStatus != "valid" {
		return nil, fmt.Errorf("cannot render generated Go workspace from invalid contract")
	}
	files, err := renderExpectedGoContractFiles(result)
	if err != nil {
		return nil, err
	}
	return renderedGoWorkspaceFiles(result.Root, files)
}

// PrepareGoWorkspace renders once for the private workspace and its analysis.
// Retirement ownership is still inspected against the current checkout.
func PrepareGoWorkspace(result *compiler.Result) (generateapi.GoWorkspaceProjection, error) {
	var projection generateapi.GoWorkspaceProjection
	if result == nil || result.Manifest == nil || result.ContractStatus != "valid" {
		return projection, fmt.Errorf("cannot render generated Go workspace from invalid contract")
	}
	files, err := renderExpectedGoContractFiles(result)
	if err != nil {
		return projection, err
	}
	projection.Files, err = renderedGoWorkspaceFiles(result.Root, files)
	if err != nil {
		return projection, err
	}
	projection.VerificationPatterns = generatedLibraryPackagePatterns(result.Root, files)
	projection.VerificationOverlay, err = generatedGoVerificationOverlay(goVerificationRetirements(result, files))
	return projection, err
}

func renderedGoWorkspaceFiles(root string, files []generatedFile) (map[string][]byte, error) {
	rendered := make(map[string][]byte, len(files))
	for _, file := range files {
		if file.Remove {
			continue
		}
		relative, err := filepath.Rel(root, file.Path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("generated artifact escapes app root: %s", file.Path)
		}
		relative = filepath.ToSlash(relative)
		if _, exists := rendered[relative]; exists {
			return nil, fmt.Errorf("generated artifact path collision: %s", relative)
		}
		rendered[relative] = append([]byte(nil), file.Bytes...)
	}
	return rendered, nil
}

// GoVerificationPatterns returns overlay-only facade packages that must be
// named explicitly because go/packages cannot discover a wholly virtual
// imported directory through ./... alone.
func GoVerificationPatterns(result *compiler.Result) ([]string, error) {
	if result == nil || result.Manifest == nil || result.ContractStatus != "valid" {
		return nil, nil
	}
	files, err := renderExpectedGoContractFiles(result)
	if err != nil {
		return nil, err
	}
	return generatedLibraryPackagePatterns(result.Root, files), nil
}

func generatedLibraryPackagePatterns(root string, files []generatedFile) []string {
	seen := map[string]bool{}
	var patterns []string
	for _, file := range files {
		if filepath.Base(file.Path) != "scenery.library-generated.json" {
			continue
		}
		relative, err := filepath.Rel(root, filepath.Dir(file.Path))
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		pattern := "./" + filepath.ToSlash(relative)
		if !seen[pattern] {
			seen[pattern] = true
			patterns = append(patterns, pattern)
		}
	}
	sort.Strings(patterns)
	return patterns
}
