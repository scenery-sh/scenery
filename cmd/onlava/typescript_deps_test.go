package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureTypeScriptWorkerDependenciesRunsBunInstallAndWritesMarker(t *testing.T) {
	outputDir := t.TempDir()
	writeTestAppFile(t, outputDir, "package.json", `{
  "dependencies": {
    "@temporalio/activity": "1.17.2",
    "@temporalio/worker": "1.17.2",
    "tsx": "4.20.6"
  }
}
`)
	binDir := t.TempDir()
	fakeBun := filepath.Join(binDir, "bun")
	writeTestAppFile(t, binDir, "bun", `#!/bin/sh
printf "%s\n" "$PWD" > install.cwd
for pkg in "@temporalio/activity" "@temporalio/worker" "tsx"; do
  mkdir -p "node_modules/$pkg"
  printf "{}\n" > "node_modules/$pkg/package.json"
done
`)
	if err := os.Chmod(fakeBun, 0o755); err != nil {
		t.Fatal(err)
	}

	restore := stubTypeScriptWorkerInstaller(t, fakeBun)
	defer restore()

	installed, err := ensureTypeScriptWorkerDependencies(context.Background(), outputDir)
	if err != nil {
		t.Fatalf("ensureTypeScriptWorkerDependencies returned error: %v", err)
	}
	if !installed {
		t.Fatal("expected dependencies to be installed")
	}
	if data, err := os.ReadFile(filepath.Join(outputDir, "install.cwd")); err != nil || strings.TrimSpace(string(data)) != outputDir {
		t.Fatalf("install cwd = %q, err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, typeScriptWorkerDepsMarkerFile)); err != nil {
		t.Fatalf("expected dependency marker: %v", err)
	}

	installed, err = ensureTypeScriptWorkerDependencies(context.Background(), outputDir)
	if err != nil {
		t.Fatalf("second ensureTypeScriptWorkerDependencies returned error: %v", err)
	}
	if installed {
		t.Fatal("expected second dependency check to reuse installed dependencies")
	}
}

func TestEnsureTypeScriptWorkerDependenciesReportsMissingInstaller(t *testing.T) {
	outputDir := t.TempDir()
	writeTestAppFile(t, outputDir, "package.json", `{"dependencies":{"@temporalio/worker":"1.17.2"}}`)

	prevLookPath := typeScriptWorkerLookPath
	defer func() { typeScriptWorkerLookPath = prevLookPath }()
	typeScriptWorkerLookPath = func(string) (string, error) {
		return "", exec.ErrNotFound
	}

	_, err := ensureTypeScriptWorkerDependencies(context.Background(), outputDir)
	if err == nil || !strings.Contains(err.Error(), "install Bun or npm") {
		t.Fatalf("error = %v", err)
	}
}

func stubTypeScriptWorkerInstaller(t *testing.T, fakeBun string) func() {
	t.Helper()
	prevLookPath := typeScriptWorkerLookPath
	prevCommandContext := typeScriptWorkerCommandContext
	typeScriptWorkerLookPath = func(name string) (string, error) {
		if name == "bun" {
			return fakeBun, nil
		}
		return "", exec.ErrNotFound
	}
	typeScriptWorkerCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name != fakeBun {
			t.Fatalf("command name = %q, want %q", name, fakeBun)
		}
		if len(args) != 1 || args[0] != "install" {
			t.Fatalf("command args = %v, want [install]", args)
		}
		return exec.CommandContext(ctx, name, args...)
	}
	return func() {
		typeScriptWorkerLookPath = prevLookPath
		typeScriptWorkerCommandContext = prevCommandContext
	}
}
