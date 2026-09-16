//go:build unix

package dirlisting

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func names(t *testing.T, path string) []string {
	t.Helper()
	entries, err := ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result
}

func age(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// A directory unchanged for longer than the timestamp granularity is listed
// once; an unreadable directory whose metadata did not change still answers
// from that listing. A recently modified directory, a changed membership and a
// directory replaced by another are read again.
func TestListingsAreReusedOnlyForUnchangedSettledDirectories(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pkg")
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unreadable := func(fn func()) {
		t.Helper()
		if err := os.Chmod(dir, 0o300); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(dir, 0o700) }()
		fn()
	}

	// Recently modified: never reused.
	if got := names(t, dir); !slices.Equal(got, []string{"a.go", "b.go"}) {
		t.Fatalf("listing = %v", got)
	}
	unreadable(func() {
		if _, err := ReadDir(dir); err == nil {
			t.Fatal("a recently modified directory was answered from an earlier listing")
		}
	})

	age(t, dir)
	if got := names(t, dir); !slices.Equal(got, []string{"a.go", "b.go"}) {
		t.Fatalf("listing = %v", got)
	}
	unreadable(func() {
		if got := names(t, dir); !slices.Equal(got, []string{"a.go", "b.go"}) {
			t.Fatalf("reused listing = %v", got)
		}
	})

	if err := os.WriteFile(filepath.Join(dir, "c.go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); !slices.Equal(got, []string{"a.go", "b.go", "c.go"}) {
		t.Fatalf("listing after adding an entry = %v", got)
	}

	replacement := filepath.Join(root, "replacement")
	if err := os.MkdirAll(replacement, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replacement, "z.go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	age(t, dir)
	_ = names(t, dir)
	age(t, replacement)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, dir); err != nil {
		t.Fatal(err)
	}
	age(t, dir)
	if got := names(t, dir); !slices.Equal(got, []string{"z.go"}) {
		t.Fatalf("listing of a replaced directory = %v", got)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDir(dir); !os.IsNotExist(err) {
		t.Fatalf("listing of a removed directory = %v", err)
	}
}
