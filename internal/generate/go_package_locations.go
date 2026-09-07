package generate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"scenery.sh/internal/scn"
)

// Validate both the declared mapping and the module the Go tool will actually
// find. A managed output root does not authorize crossing a nested go.mod.
func validateGoPackageLocations(result *Result, files []generatedFile) error {
	var managed []string
	for _, source := range result.Sources {
		if source.Relative != scn.AppFilename {
			continue
		}
		for _, block := range source.Blocks {
			if block.Type == "workspace" {
				managed = literalStringList(block, "managed_generated_roots")
			}
		}
	}
	for _, file := range files {
		name := filepath.Base(file.Path)
		if name != "scenery.package-generated.json" && name != "scenery.library-generated.json" {
			continue
		}
		directory := filepath.Dir(file.Path)
		if err := rejectGeneratedPathSymlinks(result.Root, directory); err != nil {
			return err
		}
		allowed := false
		for _, declared := range managed {
			root := filepath.Join(result.Root, filepath.FromSlash(declared))
			if pathWithin(result.Root, root) && pathWithin(root, directory) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("failed_precondition: Go output %s is outside workspace.managed_generated_roots", directory)
		}
		var descriptor struct {
			ImportPath   string `json:"import_path"`
			FacadeImport string `json:"facade_import"`
		}
		if err := json.Unmarshal(file.Bytes, &descriptor); err != nil {
			return err
		}
		importPath := descriptor.ImportPath
		if name == "scenery.library-generated.json" {
			importPath = descriptor.FacadeImport
		}
		moduleRoot, moduleImport := "", ""
		for _, resource := range result.Manifest.Resources {
			if resource.Kind != "scenery.go-module" {
				continue
			}
			root := filepath.Clean(filepath.Join(result.Root, filepath.FromSlash(stringValue(resource.Spec["root"]))))
			if pathWithin(result.Root, root) && pathWithin(root, directory) && len(root) > len(moduleRoot) {
				moduleRoot, moduleImport = root, stringValue(resource.Spec["import_path"])
			}
		}
		if moduleRoot == "" {
			return fmt.Errorf("failed_precondition: Go output %s has no owning declared go_module", directory)
		}
		relative, _ := filepath.Rel(moduleRoot, directory)
		if relative == "." || importPath != strings.TrimSuffix(moduleImport, "/")+"/"+filepath.ToSlash(relative) {
			return fmt.Errorf("failed_precondition: generated import %s does not match its declared Go module and output directory %s", importPath, directory)
		}
		for current := directory; ; current = filepath.Dir(current) {
			moduleFile := filepath.Join(current, "go.mod")
			if err := rejectGeneratedPathSymlinks(result.Root, moduleFile); err != nil {
				return err
			}
			contents, err := os.ReadFile(moduleFile)
			if err == nil {
				if current != moduleRoot {
					return fmt.Errorf("failed_precondition: generated package %s crosses undeclared module %s; preserve the file and review the declared Go module mapping", importPath, moduleFile)
				}
				parsed, err := modfile.Parse(moduleFile, contents, nil)
				if err != nil || parsed.Module == nil || parsed.Module.Mod.Path != moduleImport {
					return fmt.Errorf("failed_precondition: %s does not declare the expected module %s", moduleFile, moduleImport)
				}
				break
			}
			if !os.IsNotExist(err) {
				return err
			}
			if current == moduleRoot {
				return fmt.Errorf("failed_precondition: declared Go module %s requires %s before generation", moduleImport, moduleFile)
			}
		}
	}
	return nil
}
