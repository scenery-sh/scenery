package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/fsnotify/fsnotify"

	"scenery.sh/internal/watchignore"
)

func TestScanWatchedFilesSkipsGitignoredPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".gitignore", "/ignored/\n.env\n")
	writeWatchFile(t, root, ".env", "DATABASE_URL=postgres://localhost/watch\n")
	writeWatchFile(t, root, "kept/api.go", "package kept\n")
	writeWatchFile(t, root, "ignored/api.go", "package ignored\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	if _, ok := snapshot.files["kept/api.go"]; !ok {
		t.Fatalf("snapshot missing kept/api.go: %+v", snapshot)
	}
	for _, ignored := range []string{".env", "ignored/api.go"} {
		if _, ok := snapshot.files[ignored]; ok {
			t.Fatalf("snapshot unexpectedly included gitignored path %q: %+v", ignored, snapshot)
		}
	}
}

func TestScanWatchedFilesAppliesNestedGitignoresFromDirectoryListings(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, "svc/.gitignore", "local.go\n/cache/\n")
	writeWatchFile(t, root, "svc/api.go", "package svc\n")
	writeWatchFile(t, root, "svc/local.go", "package svc\n")
	writeWatchFile(t, root, "svc/cache/api.go", "package cache\n")
	writeWatchFile(t, root, "other/local.go", "package other\n")

	first, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{"svc/api.go": true, "svc/local.go": false, "svc/cache/api.go": false, "other/local.go": true} {
		if _, ok := first.files[path]; ok != want {
			t.Fatalf("first snapshot includes %s = %v, want %v", path, ok, want)
		}
	}
	writeWatchFile(t, root, "other/.gitignore", "local.go\n")
	second, err := scanWatchedFilesReusing(root, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.files["other/local.go"]; ok || !slices.Contains(second.dirs, "svc") || slices.Contains(second.dirs, "svc/cache") {
		t.Fatalf("rescan after adding other/.gitignore = files %v, dirs %v", second.files, second.dirs)
	}
}

func TestScanWatchedFilesSkipsGitignoredEmbeddedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".gitignore", "/svc/assets/generated/\n")
	writeWatchFile(t, root, "svc/embed.go", `package svc

import _ "embed"

//go:embed assets
var embedded []byte
`)
	writeWatchFile(t, root, "svc/assets/kept.txt", "kept\n")
	writeWatchFile(t, root, "svc/assets/generated/ignored.txt", "ignored\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	if _, ok := snapshot.files["svc/assets/kept.txt"]; !ok {
		t.Fatalf("snapshot missing embedded kept file: %+v", snapshot)
	}
	if _, ok := snapshot.files["svc/assets/generated/ignored.txt"]; ok {
		t.Fatalf("snapshot unexpectedly included gitignored embedded file: %+v", snapshot)
	}
}

func TestScanWatchedFilesSkipsConfiguredWatchIgnorePaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".scenery.json", `{
		"name": "watchapp",
		"watch": {
			"ignore": ["reference/", "scratch/*.go"]
		}
	}`)
	writeWatchFile(t, root, ".gitignore", ".scenery/\n")
	writeWatchFile(t, root, "kept/api.go", "package kept\n")
	writeWatchFile(t, root, "reference/api.go", "package reference\n")
	writeWatchFile(t, root, "scratch/drop.go", "package scratch\n")
	writeWatchFile(t, root, "scratch/notes.txt", "tracked-looking but not watched\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	if _, ok := snapshot.files["kept/api.go"]; !ok {
		t.Fatalf("snapshot missing kept/api.go: %+v", snapshot)
	}
	for _, ignored := range []string{"reference/api.go", "scratch/drop.go"} {
		if _, ok := snapshot.files[ignored]; ok {
			t.Fatalf("snapshot unexpectedly included watch.ignore path %q: %+v", ignored, snapshot)
		}
	}
}

func TestSnapshotFingerprintIgnoresConfiguredWatchIgnoreChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".scenery.json", `{
		"name": "watchapp",
		"watch": {
			"ignore": ["reference/"]
		}
	}`)
	writeWatchFile(t, root, "kept/api.go", "package kept\n")
	writeWatchFile(t, root, "reference/api.go", "package reference\n")

	before, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("initial scanWatchedFiles returned error: %v", err)
	}
	writeWatchFile(t, root, "reference/api.go", "package reference\n\nconst Changed = true\n")
	after, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("second scanWatchedFiles returned error: %v", err)
	}
	if got, want := snapshotFingerprint(after), snapshotFingerprint(before); got != want {
		t.Fatalf("fingerprint changed after watch.ignore-only edit: got %s want %s; before=%+v after=%+v", got, want, before, after)
	}
}

func TestSnapshotFingerprintUsesContentHash(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, "svc/api.go", "package svc\nconst A = 1\n")
	before, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("initial scanWatchedFiles returned error: %v", err)
	}
	writeWatchFile(t, root, "svc/api.go", "package svc\nconst A = 2\n")
	after, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("second scanWatchedFiles returned error: %v", err)
	}
	if got, wantNot := snapshotFingerprint(after), snapshotFingerprint(before); got == wantNot {
		t.Fatalf("fingerprint did not change after same-path content edit: %s", got)
	}
}

func TestFileChangeWatcherIgnoresGitignoredEventPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".gitignore", "/ignored/\n")
	writeWatchFile(t, root, "ignored/api.go", "package ignored\n")

	fw := &fileChangeWatcher{
		events:       make(chan struct{}, 1),
		root:         root,
		resolvedRoot: root,
		ignore:       watchignore.New(root),
	}

	fw.handleEvent(fsnotify.Event{Name: filepath.Join(root, "ignored", "api.go"), Op: fsnotify.Write})
	select {
	case <-fw.Events():
		t.Fatal("expected gitignored path to not signal")
	default:
	}
}

func TestFileChangeWatcherIgnoresConfiguredWatchIgnoreEventPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".scenery.json", `{
		"name": "watchapp",
		"watch": {
			"ignore": ["reference/"]
		}
	}`)
	writeWatchFile(t, root, "reference/api.go", "package reference\n")

	fw := &fileChangeWatcher{
		events:       make(chan struct{}, 1),
		root:         root,
		resolvedRoot: root,
		ignore:       watchignore.New(root),
	}

	fw.handleEvent(fsnotify.Event{Name: filepath.Join(root, "reference", "api.go"), Op: fsnotify.Write})
	select {
	case <-fw.Events():
		t.Fatal("expected configured watch.ignore path to not signal")
	default:
	}
}
