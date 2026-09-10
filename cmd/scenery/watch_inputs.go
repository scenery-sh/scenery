package main

import (
	"path/filepath"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
)

func isWatchedFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	if strings.HasSuffix(base, "_test.go") {
		return false
	}
	switch base {
	case ".gitignore", "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	}
	if app.IsConfigFilename(base) || isWatchedRootDotFile(rel) {
		return true
	}
	if strings.HasSuffix(rel, ".worker.ts") || strings.HasSuffix(rel, "/db/schema.hcl") {
		return true
	}
	// Lock changes are authored dependency changes; compilation never rewrites them.
	if strings.HasSuffix(base, ".scn") {
		return true
	}
	switch filepath.Ext(rel) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".f", ".F", ".for", ".f90", ".m", ".mm", ".s", ".S", ".syso", ".swig", ".swigcxx":
		return true
	default:
		return false
	}
}

// A successful build establishes generated presence, not a new authored
// baseline. Do not swallow implementation edits made while the build ran.
func acceptGeneratedSnapshot(root string, snapshot *fileSnapshot) error {
	generated, err := compiler.GeneratedPaths(root)
	if err != nil {
		return err
	}
	snapshot.retryGenerated = false
	snapshot.generated = generated
	for rel := range generated {
		delete(snapshot.files, rel)
	}
	return nil
}

// Failed builds can depend on refreshed generated clients. Healthy builds ignore
// their content so the compiler's own writes cannot create a rebuild loop.
func changedGeneratedContent(before, after fileSnapshot) []string {
	var paths []string
	for path, stamp := range before.generatedContent {
		if other, ok := after.generatedContent[path]; !ok || !stamp.sameContent(other) {
			paths = append(paths, path)
		}
	}
	for path := range after.generatedContent {
		if _, ok := before.generatedContent[path]; !ok {
			paths = append(paths, path)
		}
	}
	return paths
}
