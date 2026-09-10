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
