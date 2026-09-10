package build

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentCandidateSourceStateRejectsChangedInputsWithoutWriting(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	writeBuildTestFile(t, root, ".scenery.json", `{"name":"app"}`)
	writeBuildTestFile(t, root, "go.mod", "module example.test/app\n\ngo 1.27\n")
	writeBuildTestFile(t, root, "main.go", "package app\n")
	writeBuildTestFile(t, workspace, "main.go", "package app\n")
	source, err := currentAppSourceFingerprintFromDisk(root)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := workspaceBuildFingerprint(workspace, nil, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	state := buildState{Version: buildStateVersion, SourceFingerprint: source, BuildFingerprint: fingerprint, SourceStamps: map[string]SourceStamp{"main.go": {}}}
	writeBuildTestFile(t, workspace, workspaceBinaryName(root, fingerprint), "binary")
	if err := saveBuildState(workspace, state); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(workspace, buildStateFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyCurrentSourceState(root, workspace, state); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(workspace, buildStateFile))
	if string(before) != string(after) {
		t.Fatal("inspection changed build state")
	}
	writeBuildTestFile(t, root, "new.go", "package app\n")
	if err := verifyCurrentSourceState(root, workspace, state); err == nil {
		t.Fatal("accepted added authored input")
	}
	if err := os.Remove(filepath.Join(root, "new.go")); err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "main.go", "package app // changed\n")
	if err := verifyCurrentSourceState(root, workspace, state); err == nil {
		t.Fatal("accepted changed consumed input")
	}
}

func TestRuntimeCandidateUsesFreshRuntimeSnapshotNotStandaloneProjection(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	writeBuildTestFile(t, root, ".scenery.json", `{"name":"app"}`)
	writeBuildTestFile(t, root, "main.go", "package app\n")
	writeBuildTestFile(t, workspace, "main.go", "package app\n")
	snapshot := func() (*SourceSnapshot, error) {
		files := map[string]SourceSnapshotFile{}
		paths, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			if strings.HasSuffix(path.Name(), "_test.go") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, path.Name()))
			if err != nil {
				return nil, err
			}
			files[path.Name()] = SourceSnapshotFile{Hash: fmt.Sprintf("%x", sha256.Sum256(data))}
		}
		return &SourceSnapshot{Files: files}, nil
	}
	initial, err := snapshot()
	if err != nil {
		t.Fatal(err)
	}
	source, err := currentAppSourceFingerprintWithSnapshot(root, initial)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := workspaceBuildFingerprint(workspace, nil, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	state := buildState{Version: buildStateVersion, GraphFingerprint: "runtime", SourceFingerprint: source, BuildFingerprint: fingerprint, SourceStamps: map[string]SourceStamp{"main.go": {}}}
	writeBuildTestFile(t, workspace, workspaceBinaryName(root, fingerprint), "binary")
	if err := saveBuildState(workspace, state); err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, root, "main_test.go", "package app // not a runtime input\n")
	if err := verifyCurrentSourceStateWithSnapshot(root, workspace, state, snapshot); err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, root, "added.go", "package app\n")
	if err := verifyCurrentSourceStateWithSnapshot(root, workspace, state, snapshot); err == nil {
		t.Fatal("accepted newly added runtime input")
	}
}
