package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"scenery.sh/internal/dirlisting"
	"scenery.sh/internal/watchignore"
)

func TestWatchTreeRelativePathsAndControlMatchWalkDir(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/one.go", "a/two.go", "skip/hidden.go", "space dir/žluťoučký.go", "z/last.go"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeRoot, err := filepath.Rel(cwd, root)
	if err != nil {
		t.Fatal(err)
	}
	type observation struct {
		path, rel string
		dir       bool
		missing   bool
	}
	for _, spelling := range []string{root, root + string(filepath.Separator), relativeRoot} {
		for _, control := range []string{"", "skip-directory", "skip-file-siblings", "skip-all"} {
			var expected, actual []observation
			visit := func(dst *[]observation, path, rel string, entry fs.DirEntry, err error) error {
				*dst = append(*dst, observation{path, rel, entry != nil && entry.IsDir(), errors.Is(err, fs.ErrNotExist)})
				switch {
				case control == "skip-directory" && rel == "skip":
					return filepath.SkipDir
				case control == "skip-file-siblings" && rel == "a/one.go":
					return filepath.SkipDir
				case control == "skip-all" && rel == "a/one.go":
					return filepath.SkipAll
				}
				return err
			}
			if err := filepath.WalkDir(spelling, func(path string, entry fs.DirEntry, err error) error {
				rel, relErr := filepath.Rel(spelling, path)
				if relErr != nil {
					return relErr
				}
				if rel == "." {
					rel = ""
				}
				return visit(&expected, path, filepath.ToSlash(rel), entry, err)
			}); err != nil {
				t.Fatal(err)
			}
			walk := dirlisting.NewTree().Begin(true)
			if err := walkWatchTree(spelling, watchignore.New(spelling), walk, nil, func(path, rel string, entry fs.DirEntry, err error) error {
				return visit(&actual, path, rel, entry, err)
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(expected, actual) {
				t.Fatalf("root %q control %q: expected %#v, got %#v", spelling, control, expected, actual)
			}
		}
	}
}
