package build

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/graph"
)

func TestSharedBinaryPostBuildCheckRelistsOnlyAfterDirectoryChange(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.go")
	writeBuildTestFile(t, root, "input.go", "package input\n")
	observed := map[string]buildInputFileStamp{}
	for _, path := range []string{root, input} {
		if err := observeBuildInputPath(observed, path); err != nil {
			t.Fatal(err)
		}
	}
	result, _ := newSharedBinaryDomainFixture(t)
	result.BuildInput = &BuildInputManifest{Digest: "same", observed: observed}
	discoveries := 0
	discover := func(context.Context, *Result) (*BuildInputManifest, error) {
		discoveries++
		return &BuildInputManifest{Digest: "same", observed: observed}, nil
	}
	if err := verifySharedBinaryInputs(context.Background(), result, discover); err != nil {
		t.Fatal(err)
	}
	if discoveries != 0 {
		t.Fatalf("stable inputs triggered %d package graph discoveries", discoveries)
	}
	changedAt := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(root, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	if err := verifySharedBinaryInputs(context.Background(), result, discover); err != nil {
		t.Fatal(err)
	}
	if discoveries != 1 {
		t.Fatalf("directory membership stamp triggered %d discoveries, want 1", discoveries)
	}
}

func TestSharedBinaryPostBuildCheckRejectsChangedFileWithoutRelisting(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.go")
	writeBuildTestFile(t, root, "input.go", "package input\nconst Value = 1\n")
	observed := map[string]buildInputFileStamp{}
	for _, path := range []string{root, input} {
		if err := observeBuildInputPath(observed, path); err != nil {
			t.Fatal(err)
		}
	}
	result, _ := newSharedBinaryDomainFixture(t)
	result.BuildInput = &BuildInputManifest{Digest: "same", observed: observed}
	discoveries := 0
	if err := os.WriteFile(input, []byte("package input\nconst Value = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := verifySharedBinaryInputs(context.Background(), result, func(context.Context, *Result) (*BuildInputManifest, error) {
		discoveries++
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "changed during compilation") {
		t.Fatalf("changed file accepted: %v", err)
	}
	if discoveries != 0 {
		t.Fatalf("changed file triggered package graph discovery: %d", discoveries)
	}
}

func newSharedBinaryDomainFixture(t *testing.T) (*Result, goListPackage) {
	t.Helper()
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	workspace := t.TempDir()
	writeBuildTestFile(t, workspace, "go.mod", "module example.test/app\n\ngo 1.27.0\n")
	writeBuildTestFile(t, workspace, "scenery_internal_main/main.go", "package main\nfunc main() {}\n")
	result := &Result{AppRoot: workspace, Dir: workspace, SourceFiles: []string{"scenery_internal_main/main.go"},
		Target:                  &compiler.GoBuildTarget{Name: "dev", Role: "development", Context: gotarget.Context{ModuleRoot: workspace, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}},
		Contract:                &compiler.Result{Manifest: &graph.Manifest{ContractRevision: "sha256:" + strings.Repeat("a", 64)}},
		ImplementationRevisions: map[string]string{"dev": "sha256:" + strings.Repeat("b", 64)},
		RuntimeLinkerMetadata:   map[string]string{"scenery.sh/runtime.linkedGoTarget": "dev"}}
	result.GoEnvironment = gotarget.Environment(result.Target.Context)
	if err := refreshWorkspaceBuildIdentity(result); err != nil {
		t.Fatal(err)
	}
	mainPackage := goListPackage{Dir: filepath.Join(workspace, "scenery_internal_main"), ImportPath: "example.test/app/scenery_internal_main", GoFiles: []string{"main.go"},
		Module: &goListModule{Path: "example.test/app", Dir: workspace, GoMod: filepath.Join(workspace, "go.mod")}}
	return result, mainPackage
}
