package generate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"scenery.sh/internal/dirlisting"
)

// One artifact check renders several projections of one immutable compiler
// result and looks for generated descriptors beneath one root for each of
// them. Both are pure observations of inputs the check never changes, so a
// check shares them between its projections instead of hashing the result and
// walking the tree once per projection. Nothing is shared across checks except
// directory listings that are still proven current (internal/dirlisting).

// typescriptGeneratedDescriptorName names the descriptor of a generated
// TypeScript client.
const typescriptGeneratedDescriptorName = "scenery.typescript-client-generated.json"

type generatedDescriptor struct {
	path    string
	symlink bool
}

type artifactCheckScope struct {
	mu          sync.Mutex
	input       *projectionInput
	descriptors map[string][]generatedDescriptor
	scanErrors  map[string]error
}

// artifactCheckScopes holds the scope of every check in progress by the result
// it checks. The scope keeps its result reachable, so a pointer is never reused
// for another result while a scope names it.
var artifactCheckScopes sync.Map

// beginArtifactCheckScope starts sharing for result and root until the returned
// function is called. The caller must not change result meanwhile.
func beginArtifactCheckScope(result *Result) func() {
	if result == nil {
		return func() {}
	}
	if _, loaded := artifactCheckScopes.LoadOrStore(result, &artifactCheckScope{descriptors: map[string][]generatedDescriptor{}, scanErrors: map[string]error{}}); loaded {
		return func() {}
	}
	return func() { artifactCheckScopes.Delete(result) }
}

func artifactCheckScopeFor(result *Result) *artifactCheckScope {
	if result == nil {
		return nil
	}
	scope, _ := artifactCheckScopes.Load(result)
	shared, _ := scope.(*artifactCheckScope)
	return shared
}

// sharedProjectionInput is newProjectionInput, computed once per check.
func sharedProjectionInput(result *Result) projectionInput {
	scope := artifactCheckScopeFor(result)
	if scope == nil {
		return computeProjectionInput(result)
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.input == nil {
		input := computeProjectionInput(result)
		scope.input = &input
	}
	return *scope.input
}

// generatedDescriptorsBeneath lists, in walk order, every entry beneath root
// that is named like a generated descriptor. A check in progress for result
// walks each root once.
func generatedDescriptorsBeneath(result *Result, root string) ([]generatedDescriptor, error) {
	scope := artifactCheckScopeFor(result)
	if scope == nil {
		return scanGeneratedDescriptors(root)
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	clean := filepath.Clean(root)
	if descriptors, ok := scope.descriptors[clean]; ok {
		return descriptors, scope.scanErrors[clean]
	}
	descriptors, err := scanGeneratedDescriptors(root)
	scope.descriptors[clean], scope.scanErrors[clean] = descriptors, err
	return descriptors, err
}

func generatedDescriptorName(name string) bool {
	return name == typescriptGeneratedDescriptorName || goGeneratedDescriptorNames()[name]
}

// scanGeneratedDescriptors walks root as filepath.WalkDir does, skipping the
// directories a generated artifact never lives in, and reads each directory
// through the listings retained for root.
func scanGeneratedDescriptors(root string) ([]generatedDescriptor, error) {
	walk := dirlisting.TreeFor("generated-descriptors\x00" + filepath.Clean(root)).Begin(false)
	var descriptors []generatedDescriptor
	var visit func(path string, entry fs.DirEntry) error
	visit = func(path string, entry fs.DirEntry) error {
		if path != root && entry.IsDir() && skipGeneratedArtifactScanDirectory(entry.Name()) {
			return nil
		}
		if generatedDescriptorName(entry.Name()) {
			descriptors = append(descriptors, generatedDescriptor{path: path, symlink: entry.Type()&os.ModeSymlink != 0})
		}
		if !entry.IsDir() {
			return nil
		}
		entries, _, err := walk.ReadDir(path)
		if err != nil {
			return err
		}
		for _, child := range entries {
			if err := visit(filepath.Join(path, child.Name()), child); err != nil {
				return err
			}
		}
		return nil
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if err := visit(root, fs.FileInfoToDirEntry(info)); err != nil {
		return nil, err
	}
	walk.Finish()
	return descriptors, nil
}

func includeStaleGeneratedFiles(root string, files []generatedFile, descriptorNames, protectedDescriptors map[string]bool) ([]generatedFile, error) {
	return includeStaleGeneratedFilesOf(nil, root, files, descriptorNames, protectedDescriptors)
}

// includeStaleGeneratedFilesOf is includeStaleGeneratedFiles within a check of
// result, which looks for descriptors beneath each root once.
func includeStaleGeneratedFilesOf(result *Result, root string, files []generatedFile, descriptorNames, protectedDescriptors map[string]bool) ([]generatedFile, error) {
	expected := make(map[string]bool, len(files))
	expectedBytes := make(map[string][]byte, len(files))
	expectedDescriptors := map[string]bool{}
	for _, file := range files {
		path := filepath.Clean(file.Path)
		expected[path] = true
		expectedBytes[path] = file.Bytes
		if descriptorNames[filepath.Base(path)] {
			expectedDescriptors[path] = true
		}
	}
	stale := map[string]bool{}
	owned := map[string]bool{}
	descriptors, err := generatedDescriptorsBeneath(result, root)
	if err != nil {
		return nil, err
	}
	for _, descriptor := range descriptors {
		path := descriptor.path
		if !descriptorNames[filepath.Base(path)] || protectedDescriptors[filepath.Clean(path)] {
			continue
		}
		if descriptor.symlink {
			return nil, fmt.Errorf("generated descriptor is a symlink: %s", path)
		}
		base := filepath.Dir(path)
		ownedFiles, verified, err := verifyGeneratedDescriptorWithExpected(path, expectedBytes)
		if err != nil {
			return nil, err
		}
		if !verified {
			return nil, fmt.Errorf("failed_precondition: cannot replace or retire unverified generated descriptor %s; preserve the output and review its ownership or hand edits", path)
		}
		owned[filepath.Clean(path)] = true
		for _, relative := range ownedFiles {
			ownedPath := filepath.Clean(filepath.Join(base, filepath.FromSlash(relative)))
			owned[ownedPath] = true
			if !expected[ownedPath] {
				stale[ownedPath] = true
			}
		}
		if !expectedDescriptors[filepath.Clean(path)] {
			stale[filepath.Clean(path)] = true
		}
	}
	for path := range expected {
		if owned[path] {
			continue
		}
		if _, err := os.Lstat(path); err == nil {
			return nil, fmt.Errorf("failed_precondition: generated output %s exists without verified ownership; preserve it and review the missing descriptor or foreign file", path)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	paths := make([]string, 0, len(stale))
	for path := range stale {
		if !expected[path] {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		files = append(files, generatedFile{Path: path, Remove: true})
	}
	return files, nil
}
