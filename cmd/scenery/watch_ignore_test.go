package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestScanWatchedFilesSkipsGitignoredPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".gitignore", "/ignored/\n.env\n")
	writeWatchFile(t, root, ".env", "DatabaseURL=sqlite:///tmp/watch.sqlite\n")
	writeWatchFile(t, root, "kept/api.go", "package kept\n")
	writeWatchFile(t, root, "ignored/api.go", "package ignored\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	if _, ok := snapshot["kept/api.go"]; !ok {
		t.Fatalf("snapshot missing kept/api.go: %+v", snapshot)
	}
	for _, ignored := range []string{".env", "ignored/api.go"} {
		if _, ok := snapshot[ignored]; ok {
			t.Fatalf("snapshot unexpectedly included gitignored path %q: %+v", ignored, snapshot)
		}
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
	if _, ok := snapshot["svc/assets/kept.txt"]; !ok {
		t.Fatalf("snapshot missing embedded kept file: %+v", snapshot)
	}
	if _, ok := snapshot["svc/assets/generated/ignored.txt"]; ok {
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
	if _, ok := snapshot["kept/api.go"]; !ok {
		t.Fatalf("snapshot missing kept/api.go: %+v", snapshot)
	}
	for _, ignored := range []string{"reference/api.go", "scratch/drop.go"} {
		if _, ok := snapshot[ignored]; ok {
			t.Fatalf("snapshot unexpectedly included watch.ignore path %q: %+v", ignored, snapshot)
		}
	}
}

func TestScanWatchedFilesSkipsConfigAliasWatchIgnorePaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".config.json", `{
		"name": "watchapp",
		"watch": {
			"ignore": ["reference/"]
		}
	}`)
	writeWatchFile(t, root, "kept/api.go", "package kept\n")
	writeWatchFile(t, root, "reference/api.go", "package reference\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	if _, ok := snapshot[".config.json"]; !ok {
		t.Fatalf("snapshot missing .config.json: %+v", snapshot)
	}
	if _, ok := snapshot["kept/api.go"]; !ok {
		t.Fatalf("snapshot missing kept/api.go: %+v", snapshot)
	}
	if _, ok := snapshot["reference/api.go"]; ok {
		t.Fatalf("snapshot unexpectedly included configured watch.ignore path: %+v", snapshot)
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

func TestWatchIgnoreMatcherUsesGitignoreRules(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".gitignore", strings.Join([]string{
		"/x/",
		"/var/",
		"node_modules",
		"*.log",
		"!important.log",
		"/build/*",
		"!/build/keep.go",
		"nested/**/cache/",
		"",
	}, "\n"))
	ignore := newWatchIgnoreMatcher(root)

	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{path: "x", isDir: true, want: true},
		{path: "x/file.go", want: true},
		{path: "var", isDir: true, want: true},
		{path: "apps/ui/node_modules", isDir: true, want: true},
		{path: "apps/ui/node_modules/pkg/index.js", want: true},
		{path: "debug.log", want: true},
		{path: "important.log", want: false},
		{path: "build/drop.go", want: true},
		{path: "build/keep.go", want: false},
		{path: "nested/a/b/cache", isDir: true, want: true},
		{path: "nested/a/b/cache/file.go", want: true},
		{path: "src/api.go", want: false},
	}
	for _, tt := range tests {
		if got := ignore.ignored(tt.path, tt.isDir); got != tt.want {
			t.Fatalf("ignored(%q, dir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}

func TestWatchIgnoreMatcherUsesConfiguredRulesBeforeGitignoreNegations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".scenery.json", `{
		"name": "watchapp",
		"watch": {
			"ignore": ["reference/"]
		}
	}`)
	writeWatchFile(t, root, ".gitignore", "!reference/\n")
	ignore := newWatchIgnoreMatcher(root)

	if got := ignore.ignored("reference", true); !got {
		t.Fatalf("ignored(reference dir) = %v, want true", got)
	}
	if got := ignore.ignored("reference/api.go", false); !got {
		t.Fatalf("ignored(reference/api.go) = %v, want true", got)
	}
	if got := ignore.ignored("kept/api.go", false); got {
		t.Fatalf("ignored(kept/api.go) = %v, want false", got)
	}
}

func TestWatchIgnoreMatcherNegatedDirectoryDoesNotUnignoreContents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWatchFile(t, root, ".gitignore", "*\n!/foo/\n")
	ignore := newWatchIgnoreMatcher(root)

	if got := ignore.ignored("foo", true); got {
		t.Fatalf("ignored(foo dir) = %v, want false", got)
	}
	if got := ignore.ignored("foo/a.go", false); !got {
		t.Fatalf("ignored(foo/a.go) = %v, want true", got)
	}
}

func TestFileChangeWatcherIgnoresGitignoredEventPaths(t *testing.T) {
	root := t.TempDir()
	writeWatchFile(t, root, ".gitignore", "/ignored/\n")
	writeWatchFile(t, root, "ignored/api.go", "package ignored\n")

	fw := &fileChangeWatcher{
		events:       make(chan struct{}, 1),
		root:         root,
		resolvedRoot: root,
		ignore:       newWatchIgnoreMatcher(root),
	}

	fw.handleEvent(fsnotify.Event{Name: filepath.Join(root, "ignored", "api.go"), Op: fsnotify.Write})
	select {
	case <-fw.Events():
		t.Fatal("expected gitignored path to not signal")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFileChangeWatcherIgnoresConfiguredWatchIgnoreEventPaths(t *testing.T) {
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
		ignore:       newWatchIgnoreMatcher(root),
	}

	fw.handleEvent(fsnotify.Event{Name: filepath.Join(root, "reference", "api.go"), Op: fsnotify.Write})
	select {
	case <-fw.Events():
		t.Fatal("expected configured watch.ignore path to not signal")
	case <-time.After(50 * time.Millisecond):
	}
}
