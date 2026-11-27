package compiler

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/dirlisting"
)

// GeneratedPaths reads exact descriptor claims for source classification and
// missing-output detection. It never authorizes mutation: generation separately
// verifies ownership and content digests before replacing or retiring files.
// Unknown files in a managed directory remain ordinary authored inputs.
//
// Directory listings of unchanged directories are reused between discoveries
// in one tree (internal/dirlisting); descriptors are read on every discovery.
func GeneratedPaths(root string) (map[string]bool, error) {
	return generatedPaths(root, false)
}

// ReconcileGeneratedPaths is GeneratedPaths reading every directory, as an
// observation independent of reused listings; it reconciles the listings it
// reads.
func ReconcileGeneratedPaths(root string) (map[string]bool, error) {
	return generatedPaths(root, true)
}

func generatedPaths(root string, fresh bool) (map[string]bool, error) {
	paths := map[string]bool{}
	receipt, err := ReadSQLCArtifactReceipt(root)
	if err != nil {
		return nil, err
	}
	for path := range receipt.Files {
		paths[path] = true
	}
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return paths, nil
	} else if err != nil {
		return paths, err
	}
	walk := generatedPathListings(root).Begin(fresh)
	if err := collectGeneratedPathsRoot(root, walk, paths); err != nil {
		return paths, err
	}
	walk.Finish()
	return paths, nil
}

func generatedPathListings(root string) *dirlisting.Tree {
	return dirlisting.TreeFor("generated\x00" + filepath.Clean(root))
}

// InvalidateGeneratedPathListings makes the next GeneratedPaths of root read
// every directory, for callers whose observation of root became uncertain.
func InvalidateGeneratedPathListings(root string) {
	generatedPathListings(root).Invalidate()
}

func collectGeneratedPathsRoot(root string, walk *dirlisting.Walk, paths map[string]bool) error {
	entries, _, err := walk.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var subdirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") || appwalk.SkipDirName(entry.Name()) {
				continue
			}
			subdirs = append(subdirs, filepath.Join(root, entry.Name()))
			continue
		}
		if err := collectGeneratedDescriptor(root, root, entry, paths); err != nil {
			return err
		}
	}
	if len(subdirs) == 0 {
		return nil
	}
	type branchResult struct {
		paths map[string]bool
		err   error
	}
	results := make([]branchResult, len(subdirs))
	var wg sync.WaitGroup
	for i, dir := range subdirs {
		wg.Go(func() {
			local := map[string]bool{}
			err := collectGeneratedPaths(root, dir, walk, local)
			results[i] = branchResult{paths: local, err: err}
		})
	}
	wg.Wait()
	for _, res := range results {
		if res.err != nil {
			return res.err
		}
		for rel := range res.paths {
			paths[rel] = true
		}
	}
	return nil
}

// collectGeneratedPaths walks dir without following symbolic links.
func collectGeneratedPaths(root, dir string, walk *dirlisting.Walk, paths map[string]bool) error {
	entries, _, err := walk.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") || appwalk.SkipDirName(entry.Name()) {
				continue
			}
			if err := collectGeneratedPaths(root, filepath.Join(dir, entry.Name()), walk, paths); err != nil {
				return err
			}
			continue
		}
		if err := collectGeneratedDescriptor(root, dir, entry, paths); err != nil {
			return err
		}
	}
	return nil
}

func collectGeneratedDescriptor(root, dir string, entry os.DirEntry, paths map[string]bool) error {
	kind := strings.TrimSuffix(entry.Name(), ".json")
	switch kind {
	case "scenery.package-generated", "scenery.generated", "scenery.typescript-client-generated":
	default:
		return nil
	}
	if !entry.Type().IsRegular() {
		return nil
	}
	path := filepath.Join(dir, entry.Name())
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var descriptor struct {
		Kind  string   `json:"kind"`
		Files []string `json:"files"`
	}
	if json.Unmarshal(data, &descriptor) != nil || descriptor.Kind != kind {
		return nil
	}
	claimed := []string{path}
	for _, file := range descriptor.Files {
		clean := filepath.ToSlash(filepath.Clean(file))
		if file == "" || file != clean || filepath.IsAbs(file) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(file, "\\") {
			return nil
		}
		claimed = append(claimed, filepath.Join(filepath.Dir(path), file))
	}
	for _, file := range claimed {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		paths[filepath.ToSlash(rel)] = true
	}
	return nil
}
