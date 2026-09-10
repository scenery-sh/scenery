package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceInventoryPreservesFingerprintAlgorithms(t *testing.T) {
	root := t.TempDir()
	for path, data := range map[string]string{"go.mod": "module example.test/app\n", "main.go": "package main\nimport \"fmt\"\nfunc main() { fmt.Println(1) }\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inventory := newWorkspaceInventory(root)
	dep, err := dependencyFingerprintFromInventory(inventory)
	if err != nil {
		t.Fatal(err)
	}
	wantDep, err := dependencyFingerprintFromWorkspace(root)
	if err != nil || dep != wantDep {
		t.Fatalf("dependency fingerprint: %s != %s (%v)", dep, wantDep, err)
	}
	got, err := workspaceBuildFingerprintFromInventory(inventory, []string{"-race"}, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := workspaceBuildFingerprint(root, []string{"-race"}, []string{"main.go"})
	if err != nil || got != want {
		t.Fatalf("build fingerprint: %s != %s (%v)", got, want, err)
	}
	if len(inventory.files) != 3 {
		t.Fatalf("expected one read each for Go source, go.mod and absent go.sum: %d", len(inventory.files))
	}
	// A subsequent operation must read new content, even with the same paths.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, err := workspaceBuildFingerprintFromInventory(newWorkspaceInventory(root), []string{"-race"}, []string{"main.go"})
	if err != nil || next == got {
		t.Fatalf("new operation reused old bytes: %s (%v)", next, err)
	}
}
