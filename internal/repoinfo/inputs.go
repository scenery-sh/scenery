package repoinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// HashInputs binds an explicit inventory, file contents, modes, deletions and
// symlink targets. It never follows symlinks or executes an inventory command.
func HashInputs(root string, paths []string) (string, error) {
	paths = slices.Clone(paths)
	slices.Sort(paths)
	paths = slices.Compact(paths)
	hash := sha256.New()
	for _, path := range paths {
		if path == "" {
			continue
		}
		if !filepath.IsLocal(path) {
			return "", fmt.Errorf("validation input is outside repository: %q", path)
		}
		abs := filepath.Join(root, path)
		info, err := os.Lstat(abs)
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(hash, "%s\x00missing\x00", path)
			continue
		}
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", path, info.Mode())
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(abs)
			if err != nil {
				return "", err
			}
			_, _ = fmt.Fprintf(hash, "%s\x00", target)
		case info.Mode().IsRegular():
			file, err := os.Open(abs)
			if err != nil {
				return "", err
			}
			content := sha256.New()
			_, copyErr := io.Copy(content, file)
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
			_, _ = fmt.Fprintf(hash, "%x\x00", content.Sum(nil))
		default:
			return "", fmt.Errorf("unsupported validation input: %s", path)
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
