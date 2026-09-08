package devdash

import (
	sha256 "crypto/sha256"
	errors "errors"
	fmt "fmt"
	fs "io/fs"
	os "os"
	filepath "path/filepath"
	sort "sort"
	strings "strings"
)

func DashboardBundleHashDir(dir string) (string, bool, error) {
	if strings.TrimSpace(dir) == "" {
		return "", false, nil
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	hash, err := DashboardBundleHash(os.DirFS(dir))
	return hash, true, err
}

// DashboardBundleHash identifies the emitted asset-name set. Content hashes
// remain in the build's asset filenames; placeholder files are not a bundle.
func DashboardBundleHash(fsys fs.FS) (string, error) {
	if fsys == nil {
		return "", fs.ErrNotExist
	}
	names := []string{}
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path == "." || path == "placeholder.txt" {
			return nil
		}
		names = append(names, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", fs.ErrNotExist
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		_, _ = h.Write([]byte(name))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
