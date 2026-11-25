package jsbuild

import (
	"crypto/sha256"
	"encoding/json"
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
	Inputs       string
	Executable   string
	Dependencies []string
}

func readBuildCache(root, path string, service Service) Artifact {
	var record buildCacheEntry
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &record) != nil {
		return Artifact{}
	}
	current, err := buildFingerprint(root, service, record.Dependencies)
	if err != nil || current != record.Inputs || len(record.Executable) != 64 || !filepath.IsLocal(record.Executable) {
		return Artifact{}
	}
	destination := filepath.Join(filepath.Dir(path), "worker-"+record.Executable)
	if info, err := os.Lstat(destination); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return Artifact{}
	}
	return Artifact{Path: destination, Digest: "sha256:" + record.Executable, CacheHit: true}
}

func buildFingerprint(root string, service Service, dependencies []string) (string, error) {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "bun-worker/v1\n%q\n", service)
	add := func(path string) error {
		if !filepath.IsLocal(path) {
			return fmt.Errorf("JavaScript build input is outside the application")
		}
		digest, err := fileDigest(filepath.Join(root, path))
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(hash, "%q %s\n", path, digest)
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
		return "", err
	}
	for dir := filepath.Clean(service.Directory); dir != service.Workspace; {
		dir = filepath.Dir(dir)
		for _, name := range []string{"package.json", "bun.lock", "bun.lockb", "package-lock.json", "tsconfig.json"} {
			if err := add(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				return "", err
			}
		}
	}
	for _, path := range dependencies {
		if err := add(path); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// skipModuleDir reports the module directories the fingerprint walk skips.
// Bundle inputs below them are fingerprinted as recorded dependencies.
func skipModuleDir(module, path string) bool {
	return appwalk.SkipDir(module, path) || filepath.Base(path) == "target"
}

func skipModuleFile(name string) bool {
	switch filepath.Ext(name) {
	case ".go", ".rs", ".scn":
		return true
	}
	return watchignore.IsLocalEnvironmentFile(name) || name == ".DS_Store"
}

// cacheDependencies records every bundle input the module walk does not hash:
// installed packages and module files below skipped directories such as
// node_modules, target, dist or out.
func cacheDependencies(root, module string, inputs []string) []string {
	var dependencies []string
	for _, path := range inputs {
		if walkedModuleInput(module, path) {
			continue
		}
		if relative, err := filepath.Rel(root, path); err == nil {
			dependencies = append(dependencies, relative)
		}
	}
	slices.Sort(dependencies)
	return dependencies
}

func walkedModuleInput(module, path string) bool {
	relative, err := filepath.Rel(module, path)
	if err != nil || !filepath.IsLocal(relative) || skipModuleFile(filepath.Base(path)) {
		return false
	}
	dir := module
	for _, name := range strings.Split(filepath.Dir(relative), string(filepath.Separator)) {
		if name == "." {
			continue
		}
		if dir = filepath.Join(dir, name); skipModuleDir(module, dir) {
			return false
		}
	}
	return true
}
