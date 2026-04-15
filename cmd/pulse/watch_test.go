package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScanWatchedFilesIncludesOnlyGoFiles(t *testing.T) {
	root := t.TempDir()

	writeWatchFile(t, root, "pulse.app", `{"name":"watchapp"}`)
	writeWatchFile(t, root, "go.mod", "module example.com/watchapp\n\ngo 1.26.0\n")
	writeWatchFile(t, root, ".env", "DatabaseURL=postgres://localhost/db\n")
	writeWatchFile(t, root, "svc/api.go", "package svc\n")
	writeWatchFile(t, root, "README.md", "# ignored\n")
	writeWatchFile(t, root, ".git/config", "[core]\n")
	writeWatchFile(t, root, "node_modules/pkg/index.js", "console.log('ignored')\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}

	for _, want := range []string{"svc/api.go"} {
		if _, ok := snapshot[want]; !ok {
			t.Fatalf("snapshot missing %q: %+v", want, snapshot)
		}
	}
	for _, ignored := range []string{"pulse.app", "go.mod", ".env", "README.md", ".git/config", "node_modules/pkg/index.js"} {
		if _, ok := snapshot[ignored]; ok {
			t.Fatalf("snapshot unexpectedly included %q: %+v", ignored, snapshot)
		}
	}
}

func TestSnapshotsEqual(t *testing.T) {
	a := fileSnapshot{
		"a.go": {size: 1},
		"b.go": {size: 2},
	}
	b := fileSnapshot{
		"b.go": {size: 2},
		"a.go": {size: 1},
	}
	c := fileSnapshot{
		"a.go": {size: 3},
		"b.go": {size: 2},
	}

	if !snapshotsEqual(a, b) {
		t.Fatal("snapshotsEqual returned false for equal snapshots")
	}
	if snapshotsEqual(a, c) {
		t.Fatal("snapshotsEqual returned true for different snapshots")
	}
}

func TestChangedPaths(t *testing.T) {
	before := fileSnapshot{
		"svc/added.go":   {size: 1},
		"svc/deleted.go": {size: 2},
		"svc/same.go":    {size: 3},
		"svc/updated.go": {size: 4},
	}
	after := fileSnapshot{
		"svc/added.go":   {size: 9},
		"svc/new.go":     {size: 5},
		"svc/same.go":    {size: 3},
		"svc/updated.go": {size: 7},
	}

	got := changedPaths(before, after)
	want := []string{
		"svc/added.go",
		"svc/deleted.go",
		"svc/new.go",
		"svc/updated.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changedPaths mismatch\n got: %v\nwant: %v", got, want)
	}
}

func writeWatchFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
