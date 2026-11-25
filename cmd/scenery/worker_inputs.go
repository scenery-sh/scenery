package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

func pathUnder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

// scan hashes the startup inputs before the native host takes ownership.
func (watch *workerInputScope) scan(start string, inputs workerInputs) error {
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

func (watch *workerInputScope) capture() (workerInputs, error) {
	inputs := make(workerInputs)
	return inputs, watch.scan(watch.root, inputs)
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
