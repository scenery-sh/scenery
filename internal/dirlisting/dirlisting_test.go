//go:build unix

package dirlisting

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// settledClock makes every directory older than stableAge, so a listing is
// reusable as soon as it is read.
func settledClock(t *testing.T) {
	t.Helper()
	previous := now
	now = func() time.Time { return time.Now().Add(time.Hour) }
	t.Cleanup(func() { now = previous })
}

func names(entries []fs.DirEntry) []string {
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result
}

// freshNames is the independent reference: an uncached os.ReadDir.
func freshNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	return names(entries)
}

func read(t *testing.T, walk *Walk, path string) ([]fs.DirEntry, bool) {
	t.Helper()
	entries, reused, err := walk.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	return entries, reused
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Membership changes whose directory modification time was restored, as a
// rename followed by restoring the directory's times, a new declaration file
// or a new generated descriptor, are observed exactly as a fresh read observes
// them.
func TestReusedListingsMatchFreshReadsAfterRestoredModificationTimes(t *testing.T) {
	settledClock(t)
	for name, change := range map[string]func(dir string){
		"rename":               func(dir string) { _ = os.Rename(filepath.Join(dir, "old.go"), filepath.Join(dir, "new.go")) },
		"new declaration":      func(dir string) { write(t, filepath.Join(dir, "package.scn"), "service \"x\" {}\n") },
		"generated descriptor": func(dir string) { write(t, filepath.Join(dir, "scenery.generated.json"), "{}") },
		"removed entry":        func(dir string) { _ = os.Remove(filepath.Join(dir, "old.go")) },
		"replaced by directory": func(dir string) {
			_ = os.Remove(filepath.Join(dir, "old.go"))
			_ = os.Mkdir(filepath.Join(dir, "old.go"), 0o700)
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "pkg")
			write(t, filepath.Join(dir, "old.go"), "package pkg\n")
			info, err := os.Lstat(dir)
			if err != nil {
				t.Fatal(err)
			}
			tree := NewTree()
			read(t, tree.Begin(false), dir)
			if entries, reused := read(t, tree.Begin(false), dir); !reused || !slices.Equal(names(entries), []string{"old.go"}) {
				t.Fatalf("settled listing was not reused: %v %t", names(entries), reused)
			}
			change(dir)
			if err := os.Chtimes(dir, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			entries, reused := read(t, tree.Begin(false), dir)
			if want := freshNames(t, dir); reused || !slices.Equal(names(entries), want) {
				t.Fatalf("listing after the change = %v (reused %t), fresh read = %v", names(entries), reused, want)
			}
			for _, entry := range entries {
				fresh, err := os.Lstat(filepath.Join(dir, entry.Name()))
				if err != nil || entry.IsDir() != fresh.IsDir() {
					t.Fatalf("entry %s type disagrees with a fresh stat: %v", entry.Name(), err)
				}
			}
		})
	}
}

// Info of a reused entry reports the file's current metadata, not the metadata
// observed when its directory was listed.
func TestReusedEntryInfoReadsCurrentMetadata(t *testing.T) {
	settledClock(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "service.go")
	write(t, path, "package a\n")
	tree := NewTree()
	entries, _ := read(t, tree.Begin(false), dir)
	if _, err := entries[0].Info(); err != nil {
		t.Fatal(err)
	}
	write(t, path, "package a\n\nconst changed = true\n")
	entries, reused := read(t, tree.Begin(false), dir)
	info, err := entries[0].Info()
	fresh, freshErr := os.Lstat(path)
	if !reused || err != nil || freshErr != nil || info.Size() != fresh.Size() || !info.ModTime().Equal(fresh.ModTime()) {
		t.Fatalf("reused entry info = %v (%v), fresh = %v (%v), reused %t", info, err, fresh, freshErr, reused)
	}
}

// A directory modified within the timestamp granularity is read again.
func TestRecentlyModifiedDirectoriesAreReadAgain(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.go"), "package a\n")
	tree := NewTree()
	read(t, tree.Begin(false), dir)
	if _, reused := read(t, tree.Begin(false), dir); reused || tree.Len() != 0 {
		t.Fatalf("a recently modified directory was retained (reused %t, %d listings)", reused, tree.Len())
	}
}

// A completed walk evicts directories it no longer visits, and a Tree never
// retains more listings than its limit.
func TestListingsAreBoundedAndEvictedAfterCompleteWalks(t *testing.T) {
	settledClock(t)
	root := t.TempDir()
	var dirs []string
	for index := range 120 {
		dir := filepath.Join(root, fmt.Sprintf("d%03d", index))
		write(t, filepath.Join(dir, "a.go"), "package a\n")
		dirs = append(dirs, dir)
	}
	tree := NewTree()
	walk := tree.Begin(false)
	read(t, walk, root)
	for _, dir := range dirs {
		read(t, walk, dir)
	}
	walk.Finish()
	if tree.Len() != 121 {
		t.Fatalf("complete walk retained %d listings, want 121", tree.Len())
	}
	for _, dir := range dirs {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	walk = tree.Begin(false)
	if entries, _ := read(t, walk, root); len(entries) != 0 {
		t.Fatalf("root still lists %v", names(entries))
	}
	walk.Finish()
	if tree.Len() != 1 {
		t.Fatalf("walk after removing every subdirectory retained %d listings, want 1", tree.Len())
	}

	limited := NewTree()
	limited.limit = 3
	walk = limited.Begin(false)
	for index := range 5 {
		dir := filepath.Join(root, fmt.Sprintf("limited%d", index))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		read(t, walk, dir)
	}
	if limited.Len() > 3 {
		t.Fatalf("limited tree retained %d listings", limited.Len())
	}
}

// A fresh walk reads every directory and replaces what it reads, so a
// reconciliation corrects a listing that no metadata change invalidated.
func TestFreshWalksReconcileRetainedListings(t *testing.T) {
	settledClock(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "real.go"), "package a\n")
	tree := NewTree()
	read(t, tree.Begin(false), dir)
	tree.mu.Lock()
	tree.listings[dir].members = []member{{name: "stale.go"}}
	tree.mu.Unlock()
	if entries, reused := read(t, tree.Begin(false), dir); !reused || !slices.Equal(names(entries), []string{"stale.go"}) {
		t.Fatalf("test listing was not reused: %v", names(entries))
	}
	if entries, reused := read(t, tree.Begin(true), dir); reused || !slices.Equal(names(entries), freshNames(t, dir)) {
		t.Fatalf("fresh walk = %v (reused %t)", names(entries), reused)
	}
	if entries, _ := read(t, tree.Begin(false), dir); !slices.Equal(names(entries), freshNames(t, dir)) {
		t.Fatalf("listing after reconciliation = %v", names(entries))
	}
	tree.Invalidate()
	if tree.Len() != 0 {
		t.Fatal("invalidated tree retained listings")
	}
}

// The registry retains a bounded number of Trees.
func TestTreeRegistryIsBounded(t *testing.T) {
	first := TreeFor(t.Name() + "/first")
	for index := range maxTrees {
		TreeFor(fmt.Sprintf("%s/%d", t.Name(), index))
	}
	if TreeFor(t.Name()+"/first") == first {
		t.Fatal("the registry retained more Trees than its bound")
	}
	if missing, _, err := NewTree().Begin(false).ReadDir(filepath.Join(t.TempDir(), "missing")); err == nil || missing != nil {
		t.Fatalf("missing directory = %v, %v", missing, err)
	}
}
