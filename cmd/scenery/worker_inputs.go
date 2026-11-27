package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/watchignore"
)

type workerInput struct {
	Digest [sha256.Size]byte
	Mode   fs.FileMode
}

type workerInputs map[string]workerInput

type workerInputScope struct {
	root      string
	generated []string
}

func (watch *workerInputScope) skip(path string) bool {
	rel, err := filepath.Rel(watch.root, path)
	if err != nil {
		return true
	}
	return watch.excluded(rel, false)
}

func (watch *workerInputScope) alwaysIgnored(rel string) bool {
	return watch.excluded(rel, true)
}

func (watch *workerInputScope) excluded(rel string, includeBundleDirs bool) bool {
	if !filepath.IsLocal(rel) {
		return true
	}
	for _, root := range watch.generated {
		if pathUnder(root, rel) {
			return true
		}
	}
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		switch part {
		case "target":
			if !includeBundleDirs {
				return true
			}
		case "scenerycontract", "scenerygen", "scenery_internal_main", "scenery_internal_processes", ".DS_Store", ".git", ".scenery", ".claude":
			return true
		}
		if watchignore.IsLocalEnvironmentFile(part) {
			return true
		}
		if !includeBundleDirs && appwalk.SkipDirName(part) {
			return true
		}
	}
	return false
}

func pathUnder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

// scan hashes the startup inputs before the native host takes ownership. It
// leaves out what the tree's `.gitignore` files ignore: local data, dumps and
// build output are not sources, and hashing them made every scan cost as much
// as the whole checkout. The Rust Host selects the same files (its
// `gitignore` module ports internal/watchignore); the shared fixture
// testdata/worker-input-scope.json keeps the two in step.
func (watch *workerInputScope) scan(start string, inputs workerInputs) error {
	if watch.skip(start) {
		return nil
	}
	type candidate struct {
		path string
		rel  string
		mode fs.FileMode
	}
	var candidates []candidate
	ignore := watchignore.NewGitignore(watch.root)
	if err := filepath.WalkDir(start, func(path string, entry fs.DirEntry, err error) error {
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
		rel, err := filepath.Rel(watch.root, path)
		if err != nil {
			return err
		}
		if rel != "." && ignore.IgnoredEntry(rel, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			ignore.LoadDir(rel)
			return nil
		}
		if !entry.Type().IsRegular() {
			if workerDeclarationInput(rel) || slices.Contains([]string{".go", ".rs"}, filepath.Ext(rel)) {
				return fmt.Errorf("worker input %s must be a regular file", path)
			}
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		candidates = append(candidates, candidate{path: path, rel: rel, mode: info.Mode().Perm()})
		return nil
	}); err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}
	type hashed struct {
		ok     bool
		digest [sha256.Size]byte
		err    error
	}
	results := make([]hashed, len(candidates))
	workers := min(16, max(4, runtime.GOMAXPROCS(0)), len(candidates))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				index := int(next.Add(1)) - 1
				if index >= len(candidates) {
					return
				}
				digest, err := hashWorkerInput(candidates[index].path)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					results[index].err = err
					return
				}
				results[index] = hashed{ok: true, digest: digest}
			}
		})
	}
	wg.Wait()
	for index, item := range results {
		if item.err != nil {
			return item.err
		}
		if item.ok {
			inputs[candidates[index].rel] = workerInput{Digest: item.digest, Mode: candidates[index].mode}
		}
	}
	return nil
}

// hashWorkerInput streams the file: an input may be far larger than memory
// should hold at once.
func hashWorkerInput(path string) (digest [sha256.Size]byte, err error) {
	file, err := os.Open(path)
	if err != nil {
		return digest, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return digest, err
	}
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func (watch *workerInputScope) capture() (workerInputs, error) {
	inputs := make(workerInputs)
	return inputs, watch.scan(watch.root, inputs)
}

func hasNodeModulesComponent(rel string) bool {
	return slices.Contains(strings.Split(rel, string(filepath.Separator)), "node_modules")
}

func (watch *workerInputScope) addExplicit(inputs workerInputs, paths []string) error {
	canonicalRoot, err := filepath.EvalSymlinks(watch.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, rel := range paths {
		rel = filepath.Clean(filepath.FromSlash(rel))
		if rel == "." || !filepath.IsLocal(rel) || watch.alwaysIgnored(rel) {
			continue
		}
		if _, exists := inputs[rel]; exists {
			continue
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(watch.root, rel))
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return err
		}
		resolvedRel, err := filepath.Rel(canonicalRoot, resolved)
		if err != nil || !filepath.IsLocal(resolvedRel) {
			continue
		}
		if resolvedRel != rel && (!hasNodeModulesComponent(rel) || !hasNodeModulesComponent(resolvedRel) || watch.alwaysIgnored(resolvedRel)) {
			continue
		}
		info, err := os.Lstat(resolved)
		if errors.Is(err, os.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
			continue
		}
		if err != nil {
			return err
		}
		digest, err := hashWorkerInput(resolved)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		inputs[rel] = workerInput{Digest: digest, Mode: info.Mode().Perm()}
	}
	return nil
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
