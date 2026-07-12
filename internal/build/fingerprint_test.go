package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompileUpdatesDependencyFingerprintAfterSuccessfulBuild(t *testing.T) {
	old := runGo
	runGo = func(_ context.Context, dir string, _ []string, args ...string) error {
		if len(args) == 5 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" && args[4] == "./scenery_internal_main" {
			if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte("example.com/dep v1.0.0 h1:dep\n"), 0o644); err != nil {
				return err
			}
			out := args[3]
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
	result := &Result{
		AppRoot:               appDir,
		AppName:               "buildtest",
		Dir:                   workspace,
		Binary:                filepath.Join(workspace, "scenery-app-test"),
		NeedsTidy:             true,
		DependencyFingerprint: "stale",
		BuildFingerprint:      "test",
		SourceFiles:           []string{"go.mod"},
		GeneratedFiles:        []string{"scenery_internal_main/main.go"},
	}

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
}

func TestCompileReusesExistingBinaryDespiteDependencyFingerprintDrift(t *testing.T) {
	old := runGo
	runGo = func(_ context.Context, dir string, _ []string, args ...string) error {
		return fmt.Errorf("unexpected fake go command in %s: go %s", dir, strings.Join(args, " "))
	}
	t.Cleanup(func() { runGo = old })

	appDir := newBuildTestApp(t)
	workspace, err := workspaceDir(appDir, "buildtest")
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, workspace, "go.mod", "module example.com/buildtest\n\ngo 1.26.3\n")
	writeBuildTestFile(t, workspace, "go.sum", "example.com/dep v1.0.0 h1:dep\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\n\nfunc main() {}\n")
	depFingerprint, err := dependencyFingerprintFromWorkspace(workspace)
	if err != nil {
		t.Fatalf("dependencyFingerprintFromWorkspace: %v", err)
	}
	binary := filepath.Join(workspace, "scenery-app-existing")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write cached binary: %v", err)
	}
	result := &Result{
		AppRoot:               appDir,
		AppName:               "buildtest",
		Dir:                   workspace,
		Binary:                binary,
		NeedsTidy:             true,
		DependencyFingerprint: depFingerprint,
		BuildFingerprint:      "existing",
		ReuseCompiled:         true,
		SourceFiles:           []string{"go.mod"},
		GeneratedFiles:        []string{"scenery_internal_main/main.go"},
	}

	if err := Compile(result); err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if result.NeedsTidy {
		t.Fatal("expected cached compile to clear NeedsTidy")
	}
	state, err := loadBuildState(workspace)
	if err != nil {
		t.Fatalf("loadBuildState: %v", err)
	}
	if state.DependencyFingerprint != depFingerprint {
		t.Fatalf("saved dependency fingerprint = %q, want %q", state.DependencyFingerprint, depFingerprint)
	}
}

func TestCachedGeneratorFingerprintInvalidatesOnSourceMetadata(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	sourcePath := filepath.Join(repo, "internal", "codegen", "sample.go")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("package internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(first) error = %v", err)
	}
	second, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(second) error = %v", err)
	}
	if second != first {
		t.Fatalf("cached fingerprint changed without source metadata change: %q != %q", second, first)
	}
	cachePath, err := generatorFingerprintCachePath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file missing: %v", err)
	}

	if err := os.WriteFile(sourcePath, []byte("package internal\n\nconst X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(sourcePath, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	third, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(third) error = %v", err)
	}
	if third == first {
		t.Fatalf("cached fingerprint did not change after source metadata changed: %q", third)
	}
}

func TestCachedGeneratorFingerprintIncludesRootPackageFiles(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	sourcePath := filepath.Join(repo, "scenery.go")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("package scenery\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(first) error = %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("package scenery\n\nconst X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(sourcePath, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	second, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(second) error = %v", err)
	}
	if second == first {
		t.Fatalf("cached fingerprint did not change after root package source changed: %q", second)
	}
}

func TestCachedGeneratorFingerprintIncludesEmbeddedFiles(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	sourcePath := filepath.Join(repo, "auth", "standard.go")
	embedPath := filepath.Join(repo, "auth", "db", "gen", "schema.sql")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(embedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("package auth\n\nimport \"embed\"\n\n//go:embed db/gen/schema.sql\nvar _ embed.FS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(embedPath, []byte("create table one();\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(first) error = %v", err)
	}
	if err := os.WriteFile(embedPath, []byte("create table two();\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(embedPath, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	second, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(second) error = %v", err)
	}
	if second == first {
		t.Fatalf("cached fingerprint did not change after embedded source changed: %q", second)
	}
}

func TestCachedGeneratorFingerprintIgnoresUnrelatedInternalPackages(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	trackedPath := filepath.Join(repo, "internal", "codegen", "sample.go")
	unrelatedPath := filepath.Join(repo, "internal", "agent", "sample.go")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(trackedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(unrelatedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackedPath, []byte("package codegen\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelatedPath, []byte("package agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(first) error = %v", err)
	}
	if err := os.WriteFile(unrelatedPath, []byte("package agent\n\nconst X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(unrelatedPath, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	second, err := cachedGeneratorFingerprint(repo)
	if err != nil {
		t.Fatalf("cachedGeneratorFingerprint(second) error = %v", err)
	}
	if second != first {
		t.Fatalf("cached fingerprint changed for unrelated internal package: %q != %q", second, first)
	}
}
