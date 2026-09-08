package repoinfo

import (
	"fmt"
	"os"
	"path/filepath"
	appcfg "scenery.sh/internal/app"
	"strings"
)

func DiscoverRoot(start string) (string, error) {
	if start == "" {
		if cwd, err := os.Getwd(); err == nil {
			if root, ok := FindRoot(cwd); ok {
				return root, nil
			}
		}
		start = appcfg.RepoRoot()
	}
	root, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if found, ok := FindRoot(root); ok {
		return found, nil
	}
	return "", fmt.Errorf("no scenery repo root found from %s", root)
}

func FindRoot(start string) (string, bool) {
	dir := filepath.Clean(start)
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			text := string(data)
			if strings.HasPrefix(text, "module scenery.sh\n") || strings.Contains(text, "\nmodule scenery.sh\n") {
				return dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
