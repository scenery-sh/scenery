package compiler

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

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
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return paths, nil
	} else if err != nil {
		return paths, err
	}
	walk := generatedPathListings(root).Begin(fresh)
	if err := collectGeneratedPaths(root, root, walk, paths); err != nil {
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
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
				continue
			}
			if err := collectGeneratedPaths(root, path, walk, paths); err != nil {
				return err
			}
			continue
		}
		kind := strings.TrimSuffix(entry.Name(), ".json")
		switch kind {
		case "scenery.package-generated", "scenery.generated", "scenery.typescript-client-generated":
		default:
			continue
		}
		if !entry.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var descriptor struct {
			Kind  string   `json:"kind"`
			Files []string `json:"files"`
		}
		if json.Unmarshal(data, &descriptor) != nil || descriptor.Kind != kind {
			continue
		}
		claimed := []string{path}
		valid := true
		for _, file := range descriptor.Files {
			clean := filepath.ToSlash(filepath.Clean(file))
			if file == "" || file != clean || filepath.IsAbs(file) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(file, "\\") {
				valid = false
				break
			}
			claimed = append(claimed, filepath.Join(filepath.Dir(path), file))
		}
		if !valid {
			continue
		}
		for _, file := range claimed {
			rel, err := filepath.Rel(root, file)
			if err != nil {
				return err
			}
			paths[filepath.ToSlash(rel)] = true
		}
	}
	return nil
}
