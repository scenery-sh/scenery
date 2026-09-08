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
	snapshot.generated = generated
	for rel := range generated {
		delete(snapshot.files, rel)
	}
	return nil
}
