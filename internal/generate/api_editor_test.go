package generate

import (
	"os"
	"path/filepath"
	"testing"

	generateapi "scenery.sh/internal/generate/api"
)

func TestIsManagedEditorWorkFileRequiresOwnedWorkFile(t *testing.T) {
	root := t.TempDir()
	if generateapi.IsManagedEditorWorkFile(root, "go.work") {
		t.Fatal("unmanaged root reported as managed")
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if generateapi.IsManagedEditorWorkFile(root, "go.work") {
		t.Fatal("user-owned go.work reported as managed")
	}
	if status := generateapi.InspectEditorWorkspace(root); !status.Conflict || status.Message != "go.work is user-owned" {
		t.Fatalf("status = %#v", status)
	}
}

func TestIsManagedEditorWorkFileIgnoresUnrelatedPaths(t *testing.T) {
	if generateapi.IsManagedEditorWorkFile(t.TempDir(), "app.scn") {
		t.Fatal("non-work file reported as managed")
	}
}
