package generate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// includeStaleUICatalogFiles retires catalog files that disappeared from the
// live or embedded @scenery/ui source. Catalog sync may update files between
// full client generations, so descriptor integrity alone cannot identify these
// stale files reliably.
func includeStaleUICatalogFiles(result *Result, targets []Resource, files []generatedFile) ([]generatedFile, error) {
	expected := make(map[string]bool, len(files))
	for _, file := range files {
		expected[filepath.Clean(file.Path)] = true
	}
	var stale []string
	for _, target := range targets {
		if _, ok := target.Spec["react"].(map[string]any); !ok {
			continue
		}
		outputRoot, err := typeScriptOutputRoot(result, target)
		if err != nil {
			return nil, err
		}
		catalogRoot := filepath.Join(outputRoot, "react", "scenery-ui")
		err = filepath.WalkDir(catalogRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("generated UI catalog contains a symlink: " + path)
			}
			path = filepath.Clean(path)
			if expected[path] {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(catalogRoot, path)
			if err != nil {
				return err
			}
			if trustedGeneratedArtifact("", filepath.ToSlash(relative), data) {
				stale = append(stale, path)
				expected[path] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(stale)
	for _, path := range stale {
		files = append(files, generatedFile{Path: path, Remove: true})
	}
	return files, nil
}
