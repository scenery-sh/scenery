package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/watchignore"
)

type workerInput struct {
	Digest [sha256.Size]byte
	Mode   fs.FileMode
}

type workerInputs map[string]workerInput

func changedWorkerInputs(before, after workerInputs) []string {
	var changed []string
	for path, input := range after {
		if previous, exists := before[path]; !exists || previous != input {
			changed = append(changed, path)
		}
	}
	for path := range before {
		if _, exists := after[path]; !exists {
			changed = append(changed, path)
		}
	}
	slices.Sort(changed)
	return changed
}

type workerInputWatch struct {
	root      string
	generated []string
	fs        *fsnotify.Watcher
	addFailed bool

	mu       sync.RWMutex
	explicit map[string]bool
}

func openWorkerInputs(root string) (*workerInputWatch, error) {
	watcher, err := fsnotify.NewBufferedWatcher(256)
	if err != nil {
		return nil, err
	}
	return &workerInputWatch{root: root, fs: watcher}, nil
}

func (watch *workerInputWatch) setExplicit(paths []string, register bool) {
	next := make(map[string]bool, len(paths))
	dirs := make(map[string]bool, len(paths))
	for _, path := range paths {
		clean := filepath.Clean(path)
		if clean == "." || !filepath.IsLocal(clean) {
			continue
		}
		next[clean] = true
		dirs[filepath.Dir(filepath.Join(watch.root, clean))] = true
	}
	watch.mu.Lock()
	watch.explicit = next
	watch.mu.Unlock()
	if !register || watch.fs == nil {
		return
	}
	for dir := range dirs {
		_ = watch.fs.Add(dir)
	}
}

func (watch *workerInputWatch) explicitList() []string {
	watch.mu.RLock()
	defer watch.mu.RUnlock()
	if len(watch.explicit) == 0 {
		return nil
	}
	paths := make([]string, 0, len(watch.explicit))
	for path := range watch.explicit {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

func (watch *workerInputWatch) eventRelevant(path string) bool {
	if !watch.skip(path) {
		return true
	}
	rel, err := filepath.Rel(watch.root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return false
	}
	clean := filepath.Clean(rel)
	watch.mu.RLock()
	defer watch.mu.RUnlock()
	if watch.explicit[clean] {
		return true
	}
	for exp := range watch.explicit {
		if pathUnder(clean, exp) {
			return true
		}
	}
	return false
}

func (watch *workerInputWatch) scanExplicitFile(rel string, inputs workerInputs, register bool) error {
	full := filepath.Join(watch.root, rel)
	if register && watch.fs != nil {
		_ = watch.fs.Add(filepath.Dir(full))
	}
	delete(inputs, rel)
	info, err := os.Lstat(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	data, err := os.ReadFile(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	inputs[rel] = workerInput{Digest: sha256.Sum256(data), Mode: info.Mode().Perm()}
	return nil
}

func (watch *workerInputWatch) scanExplicit(inputs workerInputs, register bool) error {
	for _, rel := range watch.explicitList() {
		if err := watch.scanExplicitFile(rel, inputs, register); err != nil {
			return err
		}
	}
	return nil
}

func (watch *workerInputWatch) skip(path string) bool {
	rel, err := filepath.Rel(watch.root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return true
	}
	for _, root := range watch.generated {
		if pathUnder(root, rel) {
			return true
		}
	}
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		switch part {
		case "target", "scenerycontract", "scenerygen", "scenery_internal_main", "scenery_internal_processes", ".DS_Store":
			return true
		}
		if appwalk.SkipDirName(part) || watchignore.IsLocalEnvironmentFile(part) {
			return true
		}
	}
	return false
}

func (watch *workerInputWatch) skippedByWalk(rel string) bool {
	full := filepath.Join(watch.root, rel)
	if watch.skip(full) {
		return true
	}
	root := filepath.Clean(watch.root)
	for dir := filepath.Dir(full); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if appwalk.SkipDir(root, dir) {
			return true
		}
	}
	return false
}

func pathUnder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

// scan hashes bytes directly. Register each directory before enumerating its
// children so writes racing with discovery are also visible to the watcher.
func (watch *workerInputWatch) scan(start string, inputs workerInputs, register bool) error {
	if watch.skip(start) {
		return nil
	}
	return filepath.WalkDir(start, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path != watch.root {
			return nil
		}
		if err != nil {
			return err
		}
		if watch.skip(path) || entry.IsDir() && appwalk.SkipDir(watch.root, path) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if register {
				if err := watch.fs.Add(path); err != nil {
					if errors.Is(err, os.ErrNotExist) && path != watch.root {
						return filepath.SkipDir
					}
					if !watch.addFailed {
						fmt.Fprintf(os.Stderr, "application watch coverage reduced; using reconciliation: %v\n", err)
						watch.addFailed = true
					}
				}
			}
			return nil
		}
		rel, err := filepath.Rel(watch.root, path)
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			if workerDeclarationInput(rel) || slices.Contains([]string{".go", ".rs"}, filepath.Ext(rel)) {
				return fmt.Errorf("worker input %s must be a regular file", path)
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		inputs[rel] = workerInput{Digest: sha256.Sum256(data), Mode: info.Mode().Perm()}
		return nil
	})
}

func (watch *workerInputWatch) capture(previous workerInputs, paths map[string]struct{}, reconcile, register bool) (workerInputs, error) {
	if reconcile {
		inputs := make(workerInputs)
		if err := watch.scan(watch.root, inputs, register); err != nil {
			return nil, err
		}
		return inputs, watch.scanExplicit(inputs, register)
	}
	inputs := maps.Clone(previous)
	explicit := watch.explicitList()
	touchedExplicit := make(map[string]bool)
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		rel, err := filepath.Rel(watch.root, path)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		delete(inputs, rel)
		// A removed or renamed directory invalidates its descendants.
		prefix := rel + string(filepath.Separator)
		maps.DeleteFunc(inputs, func(name string, _ workerInput) bool { return strings.HasPrefix(name, prefix) })
		for _, exp := range explicit {
			if exp == rel || rel == "." || strings.HasPrefix(exp, prefix) {
				touchedExplicit[exp] = true
			}
		}
		if err := watch.scan(path, inputs, register); err != nil {
			return nil, err
		}
	}
	for exp := range touchedExplicit {
		if err := watch.scanExplicitFile(exp, inputs, register); err != nil {
			return nil, err
		}
	}
	return inputs, nil
}

func workerDeclarationInput(path string) bool {
	switch filepath.Base(path) {
	case ".scenery.json", "go.mod", "go.sum", "go.work", "go.work.sum", "Cargo.toml", "Cargo.lock", "rust-toolchain", "rust-toolchain.toml":
		return true
	}
	return filepath.Ext(path) == ".scn" || cargoConfigInput(path)
}

func cargoConfigInput(path string) bool {
	return filepath.Base(filepath.Dir(path)) == ".cargo" &&
		(filepath.Base(path) == "config" || filepath.Base(path) == "config.toml")
}

func workerCompilerInputs(result *compiler.Result) (map[string]bool, error) {
	inputs, err := compiler.WorkspaceRevisionInputs(result)
	if err != nil {
		return nil, err
	}
	declarations := make(map[string]bool)
	for _, input := range inputs {
		if !input.Implementation {
			declarations[filepath.FromSlash(input.Path)] = true
		}
	}
	return declarations, nil
}

const workerEditDebounce = 20 * time.Millisecond
