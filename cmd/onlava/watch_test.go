package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestScanWatchedFilesIncludesWatchedSourceFiles(t *testing.T) {
	root := t.TempDir()

	writeWatchFile(t, root, ".onlava.json", `{"name":"watchapp"}`)
	writeWatchFile(t, root, "go.mod", "module example.com/watchapp\n\ngo 1.26.0\n")
	writeWatchFile(t, root, "go.sum", "example.com/mod v1.0.0 h1:abc\n")
	writeWatchFile(t, root, ".env", "DatabaseURL=postgres://localhost/db\n")
	writeWatchFile(t, root, ".env.local", "DatabaseURL=postgres://localhost/local\n")
	writeWatchFile(t, root, "svc/api.go", "package svc\n")
	writeWatchFile(t, root, "svc/native.cpp", "int main() { return 0; }\n")
	writeWatchFile(t, root, "svc/native.h", "#pragma once\n")
	writeWatchFile(t, root, "svc/native.s", "TEXT noop(SB),$0\n")
	writeWatchFile(t, root, "README.md", "# ignored\n")
	writeWatchFile(t, root, ".git/config", "[core]\n")
	writeWatchFile(t, root, "node_modules/pkg/index.js", "console.log('ignored')\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}

	for _, want := range []string{".onlava.json", "go.mod", "go.sum", ".env", ".env.local", "svc/api.go", "svc/native.cpp", "svc/native.h", "svc/native.s"} {
		if _, ok := snapshot[want]; !ok {
			t.Fatalf("snapshot missing %q: %+v", want, snapshot)
		}
	}
	for _, ignored := range []string{"README.md", ".git/config", "node_modules/pkg/index.js"} {
		if _, ok := snapshot[ignored]; ok {
			t.Fatalf("snapshot unexpectedly included %q: %+v", ignored, snapshot)
		}
	}
}

func TestScanWatchedFilesIncludesEmbeddedFiles(t *testing.T) {
	root := t.TempDir()

	writeWatchFile(t, root, ".onlava.json", `{"name":"watchapp"}`)
	writeWatchFile(t, root, "go.mod", "module example.com/watchapp\n\ngo 1.26.0\n")
	writeWatchFile(t, root, "svc/embed.go", `package svc

import _ "embed"

//go:embed data/config.json "data/with space.txt" assets/*.txt static
var embedded []byte
`)
	writeWatchFile(t, root, "svc/data/config.json", `{"ok":true}`)
	writeWatchFile(t, root, "svc/data/with space.txt", "hello\n")
	writeWatchFile(t, root, "svc/assets/a.txt", "a\n")
	writeWatchFile(t, root, "svc/assets/ignored.md", "ignored\n")
	writeWatchFile(t, root, "svc/static/index.html", "<h1>hi</h1>\n")
	writeWatchFile(t, root, "svc/static/.hidden", "hidden\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	for _, want := range []string{"svc/data/config.json", "svc/data/with space.txt", "svc/assets/a.txt", "svc/static/index.html"} {
		if _, ok := snapshot[want]; !ok {
			t.Fatalf("snapshot missing embedded file %q: %+v", want, snapshot)
		}
	}
	for _, ignored := range []string{"svc/assets/ignored.md", "svc/static/.hidden"} {
		if _, ok := snapshot[ignored]; ok {
			t.Fatalf("snapshot unexpectedly included %q: %+v", ignored, snapshot)
		}
	}
}

func TestShouldIgnoreWatchPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "svc/api.go", want: false},
		{path: "svc/native.cpp", want: false},
		{path: ".env", want: false},
		{path: ".env.local", want: false},
		{path: ".git/config", want: true},
		{path: "node_modules/pkg/index.js", want: true},
		{path: "onlava_internal_main/main.go", want: true},
		{path: "svc/.cache/tmp.go", want: true},
	}
	for _, tt := range tests {
		if got := shouldIgnoreWatchPath(tt.path); got != tt.want {
			t.Fatalf("shouldIgnoreWatchPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestWaitForStableChangeEventsPollsWhenEventsAreMissed(t *testing.T) {
	root := t.TempDir()
	writeWatchFile(t, root, ".onlava.json", `{"name":"watchapp"}`)
	writeWatchFile(t, root, "go.mod", "module example.com/watchapp\n\ngo 1.26.0\n")
	writeWatchFile(t, root, "svc/api.go", "package svc\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	writeWatchFile(t, root, "svc/api.go", "package svc\n\nconst changed = true\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan struct{})
	next, err := waitForStableChangeEvents(ctx, root, snapshot, events)
	if err != nil {
		t.Fatalf("waitForStableChangeEvents returned error: %v", err)
	}
	if snapshotsEqual(snapshot, next) {
		t.Fatal("snapshot did not change")
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
