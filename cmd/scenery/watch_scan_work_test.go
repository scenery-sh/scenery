package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The mandatory work of a rescan follows what changed, not the size of the
// tree: an unchanged tree reads no directory listing and hashes no file, and a
// body edit hashes only the edited file.
func TestWatchRescanWorkFollowsChangesNotTreeSize(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 60 {
		name := "pkg" + strconv.Itoa(index)
		write("services/"+name+"/service.go", "package "+name+"\n\nconst message = \"a\"\n")
	}
	// Listings are reused only for directories unchanged for longer than the
	// filesystem timestamp granularity.
	old := time.Now().Add(-time.Hour)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			err = os.Chtimes(path, old, old)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	first, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	directories := first.scanStats.dirsRead + first.scanStats.dirsReused
	if len(first.files) != 60 || directories < 62 {
		t.Fatalf("first scan captured %d files in %d directories", len(first.files), directories)
	}

	unchanged, err := scanWatchedFilesReusing(root, first)
	if err != nil {
		t.Fatal(err)
	}
	if stats := unchanged.scanStats; stats.dirsRead != 0 || stats.dirsReused != directories || stats.filesHashed != 0 {
		t.Fatalf("rescan of an unchanged tree = %+v", stats)
	}

	write("services/pkg7/service.go", "package pkg7\n\nconst message = \"b\"\n")
	edited, err := scanWatchedFilesReusing(root, unchanged)
	if err != nil {
		t.Fatal(err)
	}
	if stats := edited.scanStats; stats.dirsRead != 0 || stats.filesHashed != 1 || snapshotsEqual(unchanged, edited) {
		t.Fatalf("rescan after a body edit = %+v (changed %t)", stats, !snapshotsEqual(unchanged, edited))
	}
}
