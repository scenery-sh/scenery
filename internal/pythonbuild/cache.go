package pythonbuild

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/watchignore"
)

type buildCacheEntry struct {
	Inputs      string
	Executable  string
	Interpreter string
}

type moduleInputDigest struct {
	path   string
	digest string
}

func readBuildCache(root, path string, service Service, interpreter string) Artifact {
	var record buildCacheEntry
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &record) != nil {
		return Artifact{}
	}
	if record.Interpreter != interpreter {
		return Artifact{}
	}
	current, err := buildFingerprint(root, service, interpreter)
	if err != nil || current != record.Inputs || len(record.Executable) != 64 || !filepath.IsLocal(record.Executable) {
		return Artifact{}
	}
	destination := filepath.Join(filepath.Dir(path), "worker-"+record.Executable)
	if info, err := os.Lstat(destination); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return Artifact{}
	}
	return Artifact{Path: destination, Digest: "sha256:" + record.Executable, Interpreter: interpreter, CacheHit: true}
}

func captureModuleInputs(root string, service Service) ([]moduleInputDigest, error) {
	var inputs []moduleInputDigest
	add := func(path string) error {
		if !filepath.IsLocal(path) {
			return fmt.Errorf("python build input is outside the application")
		}
		digest, err := fileDigest(filepath.Join(root, path))
		if err != nil {
			return err
		}
		inputs = append(inputs, moduleInputDigest{path: path, digest: digest})
		return nil
	}
	module := filepath.Join(root, service.Directory)
	err := filepath.WalkDir(module, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipModuleDir(module, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || skipModuleFile(entry.Name()) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return add(rel)
	})
	if err != nil {
		return nil, err
	}
	for dir := filepath.Clean(service.Directory); ; {
		for _, name := range []string{"pyproject.toml", "uv.lock", ".python-version"} {
			if err := add(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
		if dir == service.Workspace {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	slices.SortFunc(inputs, func(a, b moduleInputDigest) int { return strings.Compare(a.path, b.path) })
	inputs = slices.Compact(inputs)
	return inputs, nil
}

func buildFingerprintFromDigests(service Service, interpreter string, moduleInputs []moduleInputDigest) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "python-worker/v1\n%q\n%q\n", service, interpreter)
	for _, entry := range moduleInputs {
		_, _ = fmt.Fprintf(hash, "%q %s\n", entry.path, entry.digest)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func buildFingerprint(root string, service Service, interpreter string) (string, error) {
	moduleInputs, err := captureModuleInputs(root, service)
	if err != nil {
		return "", err
	}
	return buildFingerprintFromDigests(service, interpreter, moduleInputs), nil
}

func skipModuleDir(module, path string) bool {
	base := filepath.Base(path)
	return appwalk.SkipDir(module, path) || base == "target" || base == ".mypy_cache" || base == ".ruff_cache"
}

func skipModuleFile(name string) bool {
	switch filepath.Ext(name) {
	case ".go", ".rs", ".scn", ".pyc", ".pyo":
		return true
	}
	if (strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py")) || strings.HasSuffix(name, "_test.py") {
		return true
	}
	return watchignore.IsLocalEnvironmentFile(name) || name == ".DS_Store"
}

// Prune removes stale compiled Python worker zipapps in .scenery/python that
// are neither referenced by any cached build entry nor in keep.
func Prune(root string, keep []string) error {
	privateRoot := filepath.Join(root, ".scenery", "python")
	info, err := os.Stat(privateRoot)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
		return nil
	}
	if err != nil {
		return err
	}
	return withCacheLock(privateRoot, func() error {
		entries, err := os.ReadDir(privateRoot)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		retained := make(map[string]bool, len(keep))
		for _, path := range keep {
			if path != "" {
				retained[filepath.Clean(path)] = true
			}
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.Type().IsRegular() || !strings.HasPrefix(name, "build-") || !strings.HasSuffix(name, ".json") {
				continue
			}
			var record buildCacheEntry
			if raw, err := os.ReadFile(filepath.Join(privateRoot, name)); err == nil && json.Unmarshal(raw, &record) == nil &&
				len(record.Executable) == 64 && filepath.IsLocal(record.Executable) {
				retained[filepath.Join(privateRoot, "worker-"+record.Executable)] = true
			}
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.Type().IsRegular() || !isPythonWorkerBinaryName(name) {
				continue
			}
			path := filepath.Join(privateRoot, name)
			if !retained[filepath.Clean(path)] {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
		return nil
	})
}

func isPythonWorkerBinaryName(name string) bool {
	digest, ok := strings.CutPrefix(name, "worker-")
	if !ok || len(digest) != 64 {
		return false
	}
	for i := 0; i < len(digest); i++ {
		c := digest[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
