package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestWatchGeneratedPresenceWithoutRebuildLoop(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "service", "scenerycontract")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("service/scenerycontract/scenery.package-generated.json", `{"kind":"scenery.package-generated","files":["types.gen.go"]}`)
	write("service/scenerycontract/types.gen.go", "package scenerycontract\n")
	write("service/scenerycontract/notes.go", "package scenerycontract\nconst authored = 1\n")
	before, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	write("service/scenerycontract/types.gen.go", "package scenerycontract\nconst generated = 2\n")
	after, err := scanWatchedFilesReusing(root, before)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshotsEqual(before, after) || snapshotFingerprint(before) != snapshotFingerprint(after) {
		t.Fatal("generated write changed the authored watch baseline")
	}
	before.retryGenerated = true
	if snapshotsEqual(before, after) || !slices.Contains(changedPaths(before, after), "service/scenerycontract/types.gen.go") {
		t.Fatal("failed build ignored refreshed generated content")
	}
	if err := acceptGeneratedSnapshot(root, &before); err != nil {
		t.Fatal(err)
	}
	if !snapshotsEqual(before, after) {
		t.Fatal("successful build kept retrying generated writes")
	}
	write("service/scenerycontract/notes.go", "package scenerycontract\nconst authored = 123\n")
	after, err = scanWatchedFilesReusing(root, before)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(changedPaths(before, after), "service/scenerycontract/notes.go") {
		t.Fatal("managed directory hid an authored edit")
	}
	// Exact Git ignores must not conceal deleted generated output.
	write(".gitignore", "/service/scenerycontract/\n")
	before, err = scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "types.gen.go")); err != nil {
		t.Fatal(err)
	}
	after, err = scanWatchedFilesReusing(root, before)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(changedPaths(before, after), "service/scenerycontract/types.gen.go") {
		t.Fatal("ignored missing generated package did not trigger preparation")
	}
	if snapshotFingerprint(before) != snapshotFingerprint(after) {
		t.Fatal("missing generated output changed authored fingerprint")
	}
}

func TestWatchAcceptGenerationPreservesConcurrentAuthoredEdit(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := acceptGeneratedSnapshot(root, &before); err != nil {
		t.Fatal(err)
	}
	after, err := scanWatchedFilesReusing(root, before)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(changedPaths(before, after), []string{"main.go"}) {
		t.Fatal("accepting generation swallowed an authored edit during build")
	}
}
