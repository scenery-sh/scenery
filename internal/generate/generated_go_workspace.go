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

// PrepareBuildGoWorkspace publishes public packages and returns those exact
// bytes with private composition for build and native analysis. Publication
// retains its fresh snapshot, module ownership and retirement checks.
func PrepareBuildGoWorkspace(result *compiler.Result) (generateapi.GoWorkspaceProjection, error) {
	projection, input, err := preparePublicGoWorkspace(result)
	if err != nil {
		return projection, err
	}
	if usesGoImplementation(result.Manifest.Resources) {
		files, err := renderExpectedGoApplicationFiles(result, input)
		if err != nil {
			return projection, err
		}
		private, err := renderedGoWorkspaceFiles(result.Root, files)
		if err != nil {
			return projection, err
		}
		if err := mergeGoWorkspaceFiles(projection.Files, private); err != nil {
			return projection, err
		}
	}
	return projection, nil
}

// Public publication and ownership checks are identical for either executable.
// Selecting the private renderer must not require ordinary application artifacts.
func preparePublicGoWorkspace(result *compiler.Result) (generateapi.GoWorkspaceProjection, projectionInput, error) {
	var projection generateapi.GoWorkspaceProjection
	if result == nil || result.Manifest == nil || result.ContractStatus != "valid" {
		return projection, projectionInput{}, fmt.Errorf("cannot render generated Go workspace from invalid contract")
	}
	input := newProjectionInput(result)
	files, err := renderGoPackageProjection(result, input)
	if err != nil {
		return projection, input, err
	}
	_, err = generateFromResult(result, false, "generated contracts are stale", func(current *compiler.Result) ([]generatedFile, error) {
		if err := validateGoPackageLocations(current, files); err != nil {
			return nil, err
		}
		return includeStaleGeneratedFiles(current.Root, cloneProjection(files), goGeneratedDescriptorNames(), protectedGoGeneratedDescriptors(current))
	})
	if err != nil {
		return projection, input, err
	}
	projection.Files, err = renderedGoWorkspaceFiles(result.Root, files)
	if err != nil {
		return projection, input, err
	}
	projection.VerificationPatterns = generatedLibraryPackagePatterns(result.Root, files)
	return projection, input, nil
}

func mergeGoWorkspaceFiles(destination, selected map[string][]byte) error {
	for path, data := range selected {
		if _, exists := destination[path]; exists {
			return fmt.Errorf("generated artifact path collision: %s", path)
		}
		destination[path] = data
	}
	return nil
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
