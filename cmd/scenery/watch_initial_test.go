package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
)

func TestInitialWatchScanJoinsResultAndFailure(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	want := errors.New("source read failed")
	contract := &compiler.Result{Root: "owned"}
	scan := beginInitialWatchScan(func() (fileSnapshot, error) {
		close(entered)
		<-release
		return fileSnapshot{contract: contract}, want
	})
	<-entered
	select {
	case <-scan.done:
		t.Fatal("scan completed before its source read")
	default:
	}
	close(release)
	for range 2 {
		snapshot, err := scan.wait()
		if snapshot.contract != contract || !errors.Is(err, want) {
			t.Fatalf("startup scan lost its exact result: %p %v", snapshot.contract, err)
		}
	}
}

func TestWorktreeOwnerCloseJoinsInitialScan(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	owner := &worktreeRuntimeOwner{startupScan: beginInitialWatchScan(func() (fileSnapshot, error) {
		close(entered)
		<-release
		return fileSnapshot{}, nil
	})}
	<-entered
	closed := make(chan struct{})
	go func() { owner.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("owner released a still-running startup scan")
	default:
	}
	close(release)
	<-closed
	owner.Close()
}

func TestInitialWatchSnapshotIncludesAssistantInputs(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "assistants", "support")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.ts", "package.json", "package-lock.json"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	compile := func(string) (*compiler.Result, error) {
		return &compiler.Result{ContractStatus: "valid", Manifest: &graph.Manifest{Resources: []graph.Resource{{
			Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support",
			Spec: map[string]any{"implementation": map[string]any{
				"source": "./assistants/support", "package": "./assistants/support/package.json",
				"package_lock": "./assistants/support/package-lock.json",
			}},
		}}}}, nil
	}
	setAssistantImplementationWatch(root, nil)
	t.Cleanup(func() { setAssistantImplementationWatch(root, nil) })
	first, err := scanInitialWatchedFiles(root, compile)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.ts", "package.json", "package-lock.json"} {
		if _, ok := first.files["assistants/support/"+name]; !ok {
			t.Fatalf("initial snapshot omitted %s", name)
		}
	}
	next, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotFingerprint(first) != snapshotFingerprint(next) {
		t.Fatal("unchanged input scope changed after startup")
	}
	if err := os.WriteFile(filepath.Join(source, "index.ts"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotFingerprint(changed) == snapshotFingerprint(first) {
		t.Fatal("assistant source edit did not invalidate the snapshot")
	}
}

func TestInitialWatchSnapshotPreservesSourceFailure(t *testing.T) {
	t.Parallel()
	diagnostic := graph.Diagnostic{
		Code: "SCN1000", Severity: "error", Message: "Invalid character", Path: "app.scn",
		Suggestions: []string{"Fix the source syntax."},
	}
	_, err := scanInitialWatchedFiles(t.TempDir(), func(string) (*compiler.Result, error) {
		return &compiler.Result{ContractStatus: "invalid", Diagnostics: []graph.Diagnostic{diagnostic}}, nil
	})
	if cliExitCode(err) != 2 || !reflect.DeepEqual(cliErrorDiagnostic(err), diagnostic) {
		t.Fatalf("source failure changed: exit=%d diagnostic=%+v", cliExitCode(err), cliErrorDiagnostic(err))
	}
	compileErr := errors.New("source read failed")
	_, err = scanInitialWatchedFiles(t.TempDir(), func(string) (*compiler.Result, error) {
		return nil, compileErr
	})
	if !errors.Is(err, compileErr) {
		t.Fatalf("compiler failure changed: %v", err)
	}
}

func TestStartupMigrationGraphReuseChecksCurrentSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "app.scn")
	if err := os.WriteFile(path, []byte("application \"before\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	got, err := reuseStartupCompilerResult(root, result)
	if err != nil || got != result {
		t.Fatalf("unchanged startup did not reuse its migration graph: %v", err)
	}
	if err := os.WriteFile(path, []byte("application \"after\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = reuseStartupCompilerResult(root, result)
	if err != nil || got == result || got.Manifest.Application.Name != "after" {
		t.Fatalf("changed startup accepted stale migration graph: %v", err)
	}
}
