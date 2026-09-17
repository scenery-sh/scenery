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

// Retained digests and imports follow content: an edit that keeps the size and
// restores the modification time still changes both fingerprints.
func TestWorkspaceFingerprintsFollowContentBehindRestoredModificationTimes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	for path, data := range map[string]string{"go.mod": "module example.test/app\n", "main.go": "package main\nimport \"fmt\"\nfunc main() { fmt.Println(1) }\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fingerprints := func() (string, string) {
		t.Helper()
		inventory := newWorkspaceInventory(root)
		dependency, err := dependencyFingerprintFromInventory(inventory)
		if err != nil {
			t.Fatal(err)
		}
		build, err := workspaceBuildFingerprintFromInventory(inventory, nil, []string{"main.go"})
		if err != nil {
			t.Fatal(err)
		}
		return dependency, build
	}
	dependency, build := fingerprints()
	if again, againBuild := fingerprints(); again != dependency || againBuild != build {
		t.Fatal("unchanged workspace changed its fingerprints")
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package main\nimport \"net\"\nfunc main() { net.Println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if edited, editedBuild := fingerprints(); edited == dependency || editedBuild == build {
		t.Fatalf("an edit behind a restored modification time kept dependency %t, build %t", edited == dependency, editedBuild == build)
	}
}

// A preparation that consumes a capture uses the generated paths the capture
// excluded; without a captured set it discovers them from disk.
func TestSnapshotSourceFilesUseTheCapturedGeneratedPaths(t *testing.T) {
	root := t.TempDir()
	snapshot := &SourceSnapshot{Files: map[string]SourceSnapshotFile{"main.go": {Hash: "sha256:main"}, "gen/client.go": {Hash: "sha256:client"}}}
	fromDisk, err := snapshotSourceFilesForRoot(root, snapshot)
	if err != nil || len(fromDisk) != 2 {
		t.Fatalf("source files without a captured generated set = %v, %v", fromDisk, err)
	}
	snapshot.Generated = map[string]bool{"gen/client.go": true}
	captured, err := snapshotSourceFilesForRoot(root, snapshot)
	if err != nil || len(captured) != 1 || captured[0] != "main.go" {
		t.Fatalf("source files with a captured generated set = %v, %v", captured, err)
	}
}
