package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestRuntimeSnapshotIgnoresTestOnlyEdits(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeWatchFile(t, root, "svc/api.go", "package svc\n")
	writeWatchFile(t, root, "svc/api_test.go", "package svc\n//go:embed test-fixture.txt\n")
	writeWatchFile(t, root, "svc/test-fixture.txt", "initial fixture")
	before, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := before.files["svc/api_test.go"]; ok {
		t.Fatal("test source is a runtime input")
	}
	if _, ok := before.files["svc/test-fixture.txt"]; ok {
		t.Fatal("test-only embed is a runtime input")
	}
	writeWatchFile(t, root, "svc/api_test.go", "package svc\n// updated test\n")
	writeWatchFile(t, root, "svc/new_test.go", "package svc\n")
	writeWatchFile(t, root, "svc/test-fixture.txt", "changed fixture")
	writeWatchFile(t, root, "README.md", "new documentation")
	after, err := scanWatchedFilesReusing(root, before)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshotsEqual(before, after) || len(changedPaths(before, after)) != 0 || snapshotFingerprint(before) != snapshotFingerprint(after) {
		t.Fatal("test/documentation edits invalidated the runtime snapshot")
	}
	writeWatchFile(t, root, "svc/api.go", "package svc\nconst Revision = 2\n")
	changed, err := scanWatchedFilesReusing(root, after)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotsEqual(after, changed) || !slices.Equal(changedPaths(after, changed), []string{"svc/api.go"}) {
		t.Fatal("runtime source edit did not invalidate the backend")
	}
}

func TestRuntimeSnapshotRetainsExplicitEmbeddedTestFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeWatchFile(t, root, "svc/api.go", "package svc\n//go:embed fixture_test.go\n")
	writeWatchFile(t, root, "svc/fixture_test.go", "package svc\n")
	before, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if stamp, ok := before.files["svc/fixture_test.go"]; !ok || !stamp.embed {
		t.Fatal("runtime embed of a test-named file was lost")
	}
	writeWatchFile(t, root, "svc/fixture_test.go", "package svc\n// changed embedded bytes\n")
	after, err := scanWatchedFilesReusing(root, before)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotsEqual(before, after) || !slices.Equal(changedPaths(before, after), []string{"svc/fixture_test.go"}) {
		t.Fatal("changed runtime embed did not invalidate the backend")
	}
}

func TestRuntimeSnapshotIgnoresMetadataOnlyTouches(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeWatchFile(t, root, "svc/api.go", "package svc\n")
	before, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	touchedAt := before.files["svc/api.go"].modTime.Add(time.Minute)
	if err := os.Chtimes(filepath.Join(root, "svc/api.go"), touchedAt, touchedAt); err != nil {
		t.Fatal(err)
	}
	after, err := scanWatchedFilesReusing(root, before)
	if err != nil {
		t.Fatal(err)
	}
	if after.files["svc/api.go"].modTime.Equal(before.files["svc/api.go"].modTime) {
		t.Fatal("fixture did not change metadata")
	}
	if !snapshotsEqual(before, after) || len(changedPaths(before, after)) != 0 || snapshotFingerprint(before) != snapshotFingerprint(after) {
		t.Fatal("metadata-only touch invalidated the runtime snapshot")
	}
	before.retryGenerated = true
	before.generatedContent = before.files
	after.generatedContent = after.files
	if len(changedGeneratedContent(before, after)) != 0 {
		t.Fatal("metadata-only touch retried a failed generated build")
	}
}
