package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
)

// Migration preflight may already have compiled this startup's graph. Verify
// it again before using its assistant input scope; never retain it across runs.
func reuseStartupCompilerResult(root string, result *compiler.Result) (*compiler.Result, error) {
	if result != nil && filepath.Clean(result.Root) == filepath.Clean(root) {
		unchanged, err := compiler.SnapshotUnchanged(result)
		if err != nil {
			return nil, err
		}
		if unchanged {
			return result, nil
		}
	}
	return compiler.Compile(root)
}

// Register authored assistant inputs before the first fingerprint. Otherwise
// an unchanged restart compares a Go-only snapshot with the previous process's
// complete snapshot and needlessly invalidates the graph cache.
func scanInitialWatchedFiles(root string, compile func(string) (*compiler.Result, error)) (fileSnapshot, error) {
	result, err := compile(root)
	if err != nil {
		return fileSnapshot{}, err
	}
	if !result.Valid() {
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Severity == "error" {
				return fileSnapshot{}, &cliDiagnosticError{
					code: contractInvalidExitCode(result), diagnostic: diagnostic,
				}
			}
		}
		return fileSnapshot{}, fmt.Errorf("app contract graph is invalid")
	}
	setAssistantImplementationWatch(root, assistantDefinitionsFromResult(result, root))
	snapshot, err := scanWatchedFilesReusing(root, fileSnapshot{contract: result})
	if err != nil {
		return fileSnapshot{}, err
	}
	unchanged, verifyErr := compiler.SnapshotUnchanged(result)
	if verifyErr != nil {
		return fileSnapshot{}, verifyErr
	}
	if unchanged && snapshot.compilerValid {
		bindSnapshotContract(&snapshot, result)
	} else {
		snapshot.contract = nil
	}
	return snapshot, err
}

func bindSnapshotContract(snapshot *fileSnapshot, result *compiler.Result) {
	if snapshot == nil || result == nil {
		return
	}
	snapshot.contract = result
	snapshot.contractFiles = make(map[string]fileStamp)
	for rel, stamp := range snapshot.files {
		if !implementationSnapshotFile(rel, stamp) {
			snapshot.contractFiles[rel] = stamp
		}
	}
	snapshot.contractCompiler = make(map[string]fileStamp)
	snapshot.contractCompilerAbsent = make(map[string]bool)
	for rel, stamp := range snapshot.compilerFiles {
		if !snapshot.compilerImpl[rel] {
			snapshot.contractCompiler[rel] = stamp
		}
	}
	for rel, implementation := range snapshot.compilerAbsent {
		if !implementation {
			snapshot.contractCompilerAbsent[rel] = false
		}
	}
}

func capturedGraphInputsUnchanged(snapshot fileSnapshot) bool {
	if snapshot.contract == nil || !snapshot.compilerValid {
		return false
	}
	files := make(map[string]fileStamp)
	for rel, stamp := range snapshot.files {
		if !implementationSnapshotFile(rel, stamp) {
			files[rel] = stamp
		}
	}
	compilerFiles := make(map[string]fileStamp)
	for rel, stamp := range snapshot.compilerFiles {
		if !snapshot.compilerImpl[rel] {
			compilerFiles[rel] = stamp
		}
	}
	absent := make(map[string]bool)
	for rel, implementation := range snapshot.compilerAbsent {
		if !implementation {
			absent[rel] = false
		}
	}
	return stampMapsEqual(files, snapshot.contractFiles) &&
		stampMapsEqual(compilerFiles, snapshot.contractCompiler) &&
		boolMapsEqual(absent, snapshot.contractCompilerAbsent)
}

func boolMapsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

// refreshBuildCompilerMembership uses a provisional graph only to discover
// inputs introduced by a declaration/configuration edit. The build still
// recompiles the canonical graph from the refreshed captured bytes; the
// provisional result never becomes an authorization or publication verdict.
func refreshBuildCompilerMembership(root string, snapshot *fileSnapshot) error {
	if snapshot == nil || snapshot.contract == nil || capturedGraphInputsUnchanged(*snapshot) {
		return nil
	}
	discovered, err := compiler.Compile(root)
	if err != nil {
		return err
	}
	carrier := *snapshot
	carrier.contract = discovered
	refreshed, err := scanWatchedFilesReusing(root, carrier)
	if err != nil {
		return err
	}
	// Keep the discovery result only as the membership scope for subsequent
	// freshness scans. Its contract baselines still belong to the last accepted
	// graph, so compilation cannot reuse it and must rebuild from captured bytes.
	*snapshot = refreshed
	return nil
}

// refreshSnapshotContract promotes a successfully served graph to the next
// watch baseline only after re-resolving its complete compiler membership and
// proving that the graph still describes the current authored bytes. Failure
// is intentionally conservative: the served generation remains valid, while
// the next edit recompiles instead of reusing an uncertain graph.
func refreshSnapshotContract(root string, snapshot *fileSnapshot, result *compiler.Result) {
	if snapshot == nil || result == nil || !result.Valid() {
		return
	}
	candidate := *snapshot
	candidate.contract = result
	candidate.compilerValid = true
	generated, err := compiler.GeneratedPaths(root)
	if err != nil {
		return
	}
	candidate.generated = generated
	candidate.captureCompilerRevisionFiles(root, *snapshot)
	if !candidate.compilerValid {
		return
	}
	unchanged, err := compiler.SnapshotUnchanged(result)
	if err != nil || !unchanged {
		return
	}
	bindSnapshotContract(&candidate, result)
	snapshot.contract = candidate.contract
	snapshot.contractFiles = candidate.contractFiles
	snapshot.contractCompiler = candidate.contractCompiler
	snapshot.contractCompilerAbsent = candidate.contractCompilerAbsent
	snapshot.compilerFiles = candidate.compilerFiles
	snapshot.compilerImpl = candidate.compilerImpl
	snapshot.compilerAbsent = candidate.compilerAbsent
	snapshot.compilerValid = candidate.compilerValid
}

func implementationSnapshotFile(rel string, stamp fileStamp) bool {
	if stamp.embed {
		return true
	}
	switch filepath.Ext(filepath.ToSlash(rel)) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".f", ".F", ".for", ".f90", ".m", ".mm", ".s", ".S", ".syso", ".swig", ".swigcxx":
		return true
	default:
		return false
	}
}

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
