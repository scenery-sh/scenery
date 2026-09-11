package build

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestStartupCompilerSnapshotRevalidatesSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildTestFile(t, root, "app.scn", "application \"snapshot\" {}\nworkspace {\n implementation_root \"go\" {\n path = \".\"\n revision_include = [\"*.go\", \"go.mod\"]\n revision_exclude = []\n }\n}\n")
	writeBuildTestFile(t, root, "go.mod", "module example.test/snapshot\n")
	writeBuildTestFile(t, root, "handler.go", "package app\nconst value = 1\n")
	contract, err := compiler.Compile(root)
	if err != nil || !contract.Valid() {
		t.Fatalf("compile: %v", err)
	}
	snapshot := &SourceSnapshot{Contract: contract}
	reused, err := compileWorkspaceContract(root, snapshot)
	if err != nil || reused == contract || reused.Manifest == contract.Manifest || reused.WorkspaceRevision != contract.WorkspaceRevision {
		t.Fatalf("snapshot result ownership: %v", err)
	}
	if &reused.Sources[0].Bytes[0] != &contract.Sources[0].Bytes[0] {
		t.Fatal("unchanged startup reloaded the immutable source graph")
	}
	reused.ImplementationStatus = "invalid"
	reused.Manifest.Diagnostics = append(reused.Manifest.Diagnostics, compiler.Diagnostic{Message: "build-only"})
	if contract.ImplementationStatus == "invalid" || len(contract.Manifest.Diagnostics) != 0 {
		t.Fatal("build checks mutated the retained compiler snapshot")
	}
	path := filepath.Join(root, "handler.go")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, root, "handler.go", "package app\nconst value = 2\n")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed, err := compileWorkspaceContract(root, snapshot)
	if err != nil || changed.WorkspaceRevision == contract.WorkspaceRevision {
		t.Fatalf("same-mtime implementation edit reused startup identity: %v", err)
	}
	writeBuildTestFile(t, root, "added.scn", "// new declaration source\n")
	added, err := compileWorkspaceContract(root, snapshot)
	if err != nil || len(added.Sources) == len(contract.Sources) {
		t.Fatalf("new source membership was ignored: %v", err)
	}
}
