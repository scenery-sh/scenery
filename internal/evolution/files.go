package evolution

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/atomicfile"
)

func atomicWriteSynced(path string, data []byte, mode os.FileMode) error {
	return atomicfile.Write(path, data, mode, atomicfile.Options{SyncFile: true, SyncDir: true})
}

func confinedPath(root, relative string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escape")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absoluteRoot, clean)
	current := absoluteRoot
	parts := strings.Split(clean, string(filepath.Separator))
	for _, part := range parts[:max(0, len(parts)-1)] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path escape through symlink: %s", relative)
		}
	}
	return target, nil
}
