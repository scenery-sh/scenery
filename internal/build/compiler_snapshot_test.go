package build

import (
	"crypto/sha256"
	"encoding/hex"
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
	snapshot := captureCompilerSourceSnapshot(t, root, contract, nil)
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
	changedSnapshot := captureCompilerSourceSnapshot(t, root, contract, snapshot)
	// Prove the compiler consumes the capture, not a later mutable-tree edit.
	writeBuildTestFile(t, root, "handler.go", "package app\nconst value = 3\n")
	changed, err := compileWorkspaceContract(root, changedSnapshot)
	if err != nil || changed.WorkspaceRevision == contract.WorkspaceRevision {
		t.Fatalf("same-mtime implementation edit reused startup identity: %v", err)
	}
	expected := *contract
	captured := map[string][]byte{}
	for rel, file := range changedSnapshot.CompilerFiles {
		captured[rel] = file.Data
	}
	if err := compiler.BindCapturedWorkspaceRevision(&expected, captured); err != nil || changed.WorkspaceRevision != expected.WorkspaceRevision {
		t.Fatalf("workspace identity was not bound to captured bytes: %v", err)
	}
	tampered := *changedSnapshot
	tampered.Files = make(map[string]SourceSnapshotFile, len(changedSnapshot.Files))
	for rel, file := range changedSnapshot.Files {
		tampered.Files[rel] = file
	}
	appSource := tampered.Files["app.scn"]
	appSource.Data = []byte("application \"tampered\" {}\n")
	tampered.Files["app.scn"] = appSource
	if _, err := compileWorkspaceContract(root, &tampered); err == nil {
		t.Fatal("tampered captured graph bytes were accepted")
	}
	writeBuildTestFile(t, root, "added.scn", "// new declaration source\n")
	addedSnapshot := captureCompilerSourceSnapshot(t, root, contract, snapshot)
	added, err := compileWorkspaceContract(root, addedSnapshot)
	if err != nil || len(added.Sources) == len(contract.Sources) {
		t.Fatalf("new source membership was ignored: %v", err)
	}
}

func captureCompilerSourceSnapshot(t *testing.T, root string, contract *compiler.Result, baseline *SourceSnapshot) *SourceSnapshot {
	t.Helper()
	snapshot := &SourceSnapshot{Files: map[string]SourceSnapshotFile{}, CompilerFiles: map[string]SourceSnapshotFile{}, CompilerAbsent: map[string]bool{}, CompilerCaptureValid: true, Contract: contract}
	for _, rel := range []string{"app.scn", "go.mod", "handler.go", "added.scn"} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Files[rel] = capturedTestFile(data, rel == "handler.go")
	}
	inputs, err := compiler.WorkspaceRevisionInputs(contract)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs {
		if !input.Present {
			snapshot.CompilerAbsent[input.Path] = input.Implementation
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(input.Path)))
		if err != nil {
			t.Fatal(err)
		}
		snapshot.CompilerFiles[input.Path] = capturedTestFile(data, input.Implementation)
	}
	if baseline == nil {
		snapshot.ContractFiles = map[string]SourceSnapshotFile{}
		for rel, file := range snapshot.Files {
			if !file.Implementation {
				snapshot.ContractFiles[rel] = file
			}
		}
		snapshot.ContractCompilerFiles = map[string]SourceSnapshotFile{}
		for rel, file := range snapshot.CompilerFiles {
			if !file.Implementation {
				snapshot.ContractCompilerFiles[rel] = file
			}
		}
		snapshot.ContractCompilerAbsent = map[string]bool{}
		for rel, implementation := range snapshot.CompilerAbsent {
			if !implementation {
				snapshot.ContractCompilerAbsent[rel] = false
			}
		}
	} else {
		snapshot.ContractFiles = baseline.ContractFiles
		snapshot.ContractCompilerFiles = baseline.ContractCompilerFiles
		snapshot.ContractCompilerAbsent = baseline.ContractCompilerAbsent
	}
	return snapshot
}

func capturedTestFile(data []byte, implementation bool) SourceSnapshotFile {
	sum := sha256.Sum256(data)
	return SourceSnapshotFile{Size: int64(len(data)), Perm: 0o644, Hash: hex.EncodeToString(sum[:]), Data: append([]byte(nil), data...), Implementation: implementation}
}

func TestCapturedContractInputsIncludeAbsentResolverMembership(t *testing.T) {
	t.Parallel()
	snapshot := &SourceSnapshot{
		Files:                  map[string]SourceSnapshotFile{},
		CompilerFiles:          map[string]SourceSnapshotFile{},
		CompilerAbsent:         map[string]bool{"ui/card": false, "ui/card.tsx": false, "go.work.sum": true},
		ContractFiles:          map[string]SourceSnapshotFile{},
		ContractCompilerFiles:  map[string]SourceSnapshotFile{},
		ContractCompilerAbsent: map[string]bool{"ui/card": false, "ui/card.tsx": false},
	}
	if !capturedContractInputsUnchanged(snapshot) {
		t.Fatal("matching absent resolver membership rejected the captured graph")
	}
	delete(snapshot.CompilerAbsent, "ui/card.tsx")
	if capturedContractInputsUnchanged(snapshot) {
		t.Fatal("changed absent resolver membership reused the captured graph")
	}
}
