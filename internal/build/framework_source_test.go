package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFrameworkSnapshotIsContentBoundAndIndependent(t *testing.T) {
	root := t.TempDir()
	sourceRoot, target := filepath.Join(root, "source"), filepath.Join(root, "snapshot")
	writeBuildTestFile(t, sourceRoot, "go.mod", "module scenery.sh\n\ngo 1.27.0\n")
	writeBuildTestFile(t, sourceRoot, "runtime/runtime.go", "package runtime\nimport _ \"embed\"\n//go:embed contract.json\nvar contract string\n")
	writeBuildTestFile(t, sourceRoot, "runtime/contract.json", `{"contract":"current"}`)
	writeBuildTestFile(t, sourceRoot, "runtime/runtime_test.go", "package runtime\n")
	before, err := FrameworkSourceManifest(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := materializeFrameworkSource(before, target); err != nil {
		t.Fatal(err)
	}
	frozen, err := FrameworkSourceManifest(target)
	if err != nil || frozen.Digest != before.Digest {
		t.Fatalf("frozen=%+v err=%v", frozen, err)
	}
	writeBuildTestFile(t, sourceRoot, "runtime/runtime_test.go", "package runtime\n// Test-only change.\n")
	afterTestEdit, err := FrameworkSourceManifest(sourceRoot)
	if err != nil || afterTestEdit.Digest != before.Digest {
		t.Fatal("test-only framework edit changed the compiled producer identity")
	}
	writeBuildTestFile(t, sourceRoot, "runtime/contract.json", `{"contract":"changed"}`)
	afterRuntimeEdit, err := FrameworkSourceManifest(sourceRoot)
	if err != nil || afterRuntimeEdit.Digest == before.Digest {
		t.Fatal("embedded runtime contract change did not change producer identity")
	}
	stillFrozen, err := FrameworkSourceManifest(target)
	if err != nil || stillFrozen.Digest != before.Digest {
		t.Fatal("source edit altered the already-selected framework snapshot")
	}
	if err := os.Chmod(filepath.Join(target, "runtime/contract.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, target, "runtime/contract.json", `{"contract":"tampered"}`)
	if err := materializeFrameworkSource(before, target); err == nil {
		t.Fatal("mutated retained snapshot was accepted by its content-addressed path")
	}
}
