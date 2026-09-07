package compiler

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// GeneratedPaths reads exact descriptor claims for source classification and
// missing-output detection. It never authorizes mutation: generation separately
// verifies ownership and content digests before replacing or retiring files.
// Unknown files in a managed directory remain ordinary authored inputs.
func GeneratedPaths(root string) (map[string]bool, error) {
	paths := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		kind := strings.TrimSuffix(entry.Name(), ".json")
		switch kind {
		case "scenery.package-generated", "scenery.generated", "scenery.library-generated", "scenery.typescript-client-generated":
		default:
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
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
	})
	return paths, err
}
