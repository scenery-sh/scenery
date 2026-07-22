package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Steady-state rescans must reuse prior content hashes for stat-identical
// files while still detecting real edits, including //go:embed additions
// whose patterns come from the per-process embed cache.
func TestScanWatchedFilesReusingDetectsChanges(t *testing.T) {
	root := t.TempDir()
	appPath := filepath.Join(root, "app.scn")
	goPath := filepath.Join(root, "main.go")
	assetPath := filepath.Join(root, "asset.txt")
	if err := os.WriteFile(appPath, []byte("application \"test\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := scanWatchedFilesReusing(root, first)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshotsEqual(first, unchanged) {
		t.Fatal("reusing scan over an unchanged tree must equal the prior snapshot")
	}

	if err := os.WriteFile(goPath, []byte("package main\n\nimport _ \"embed\"\n\n//go:embed asset.txt\nvar asset string\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edited, err := scanWatchedFilesReusing(root, unchanged)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotsEqual(unchanged, edited) {
		t.Fatal("reusing scan missed an edited go file")
	}
	if stamp, ok := edited.files["asset.txt"]; !ok || !stamp.embed {
		t.Fatalf("embed edit did not stamp asset.txt as embedded: %+v", edited.files)
	}

	stale := edited.files["asset.txt"]
	if err := os.WriteFile(assetPath, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(assetPath, time.Now(), stale.modTime.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	touched, err := scanWatchedFilesReusing(root, edited)
	if err != nil {
		t.Fatal(err)
	}
	if touched.files["asset.txt"].hash == stale.hash {
		t.Fatal("reusing scan reused a hash for a same-size content edit with a new mtime")
	}
}
