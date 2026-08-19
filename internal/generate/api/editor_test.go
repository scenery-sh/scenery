package generateapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsManagedEditorWorkFileRequiresOwnedWorkFile(t *testing.T) {
	root := t.TempDir()
	if IsManagedEditorWorkFile(root, "go.work") {
		t.Fatal("unmanaged root reported as managed")
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsManagedEditorWorkFile(root, "go.work") {
		t.Fatal("user-owned go.work reported as managed")
	}
	if status := InspectEditorWorkspace(root); !status.Conflict || status.Message != "go.work is user-owned" {
		t.Fatalf("status = %#v", status)
	}
}

func TestIsManagedEditorWorkFileIgnoresUnrelatedPaths(t *testing.T) {
	if IsManagedEditorWorkFile(t.TempDir(), "app.scn") {
		t.Fatal("non-work file reported as managed")
	}
}
