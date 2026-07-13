package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotWorkspaceIsStableAndRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "scenery.scn"), []byte("application \"test\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := SnapshotWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SnapshotWorkspace(root)
	if err != nil || !EqualWorkspaceSnapshots(first, second) {
		t.Fatalf("stable snapshot = %v, %v", err, second)
	}
	if err := os.Symlink("scenery.scn", filepath.Join(root, "alias.scn")); err != nil {
		t.Fatal(err)
	}
	if _, err := SnapshotWorkspace(root); err == nil {
		t.Fatal("workspace symlink was accepted")
	}
}
