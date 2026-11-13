package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileUpdatesDependencyFingerprintAfterSuccessfulBuild(t *testing.T) {
	old := runGo
	runGo = func(_ context.Context, dir string, _ []string, args ...string) error {
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			return os.WriteFile(filepath.Join(dir, "go.sum"), []byte("example.com/dep v1.0.0 h1:dep\n"), 0o644)
		}
		if out, ok := fakeGoBuildOutput(args); ok {
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		}
		return fmt.Errorf("unexpected fake go command: go %s", strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })

	appDir := newBuildTestApp(t)
	workspace, err := workspaceDir(appDir, "buildtest")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/buildtest\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "go.sum", "")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")
	result := prepareCompileTestResult(&Result{
		AppRoot:               appDir,
		AppName:               "buildtest",
		Dir:                   workspace,
		Binary:                filepath.Join(workspace, "scenery-app-test"),
		NeedsTidy:             true,
		DependencyFingerprint: "stale",
		BuildFingerprint:      "test",
		SourceFiles:           []string{"go.mod"},
		GeneratedFiles:        []string{"scenery_internal_main/main.go"},
	})

	if err := Compile(result); err != nil {
		t.Fatalf("Compile: %v", err)
	}
	state, err := loadBuildState(workspace)
	if err != nil {
		t.Fatalf("loadBuildState: %v", err)
	}
	want, err := dependencyFingerprintFromWorkspace(workspace)
	if err != nil {
		t.Fatalf("dependencyFingerprintFromWorkspace: %v", err)
	}
	if state.DependencyFingerprint != want {
		t.Fatalf("saved dependency fingerprint = %q, want post-build fingerprint %q", state.DependencyFingerprint, want)
	}
	final, err := workspaceBuildFingerprint(workspace, result.GoBuildFlags, result.SourceFiles, result.GeneratedFiles)
	if err != nil {
		t.Fatal(err)
	}
	if result.BuildFingerprint != final || state.BuildFingerprint != final || result.Binary != filepath.Join(workspace, workspaceBinaryName(appDir, final)) {
		t.Fatal("binary/result/state retained a pre-tidy build key")
	}
	if _, err := os.Stat(result.Binary); err != nil {
		t.Fatalf("final keyed binary was not built: %v", err)
	}
}
