package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

const cachedContractPath = "svc/scenerycontract/types.gen.go"
const cachedContractBytes = "package scenerycontract\n"

func TestCachedGoProjectionRestoresMissingPublicPackage(t *testing.T) {
	root, result := ordinaryGoCacheFixture(t)
	writeBuildTestFile(t, result.Dir, cachedContractPath, cachedContractBytes)
	current, err := refreshCachedGoProjection(root, result, nil)
	if err != nil || !current {
		t.Fatalf("cache preparation: current=%v err=%v", current, err)
	}
	if _, err := os.Stat(filepath.Join(root, cachedContractPath)); err != nil {
		t.Fatalf("cache hit did not prepare public package: %v", err)
	}
}

func TestCachedGoProjectionRejectsMissingPrivateBytes(t *testing.T) {
	root, result := ordinaryGoCacheFixture(t)
	current, err := refreshCachedGoProjection(root, result, nil)
	if err != nil || current {
		t.Fatalf("missing private projection: current=%v err=%v", current, err)
	}
}

func TestCachedGoProjectionRejectsStalePrivateBytes(t *testing.T) {
	root, result := ordinaryGoCacheFixture(t)
	writeBuildTestFile(t, result.Dir, cachedContractPath, "package scenerycontract\n// stale cache\n")
	current, err := refreshCachedGoProjection(root, result, nil)
	if err != nil || current {
		t.Fatalf("stale private projection: current=%v err=%v", current, err)
	}
}

func TestCachedPreparationCompilesImplementationEditWithoutFullPrepare(t *testing.T) {
	root, original := ordinaryGoCacheFixture(t)
	writeBuildTestFile(t, original.Dir, cachedContractPath, cachedContractBytes)
	goProjectionCalls, typeScriptProjectionCalls := 0, 0
	generateHooks.PrepareBuildGoWorkspace = func(*compiler.Result) (generateapi.GoWorkspaceProjection, error) {
		goProjectionCalls++
		return generateapi.GoWorkspaceProjection{}, nil
	}
	generateHooks.SyncCachedTypeScript = func(*compiler.Result) ([]string, error) {
		typeScriptProjectionCalls++
		return nil, nil
	}
	writeBuildTestFile(t, root, "svc/api.go", "package svc\n\nfunc Hello() string { return \"new-behavior\" }\n")
	contract, err := CompileContractWithSnapshot(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := PreparationFingerprint(appcfg.Config{Name: "buildtest"}, contract)
	if err != nil {
		t.Fatal(err)
	}
	cached, ok, err := LoadCachedPreparationContext(context.Background(), root, appcfg.Config{Name: "buildtest"}, "graph-new", fingerprint, contract)
	if err != nil || !ok || cached == nil {
		t.Fatalf("load cached preparation: ok=%v err=%v", ok, err)
	}
	prepared, err := PrepareCachedWorkspaceWithSnapshotContext(context.Background(), root, appcfg.Config{Name: "buildtest"}, cached.Result, nil)
	if err != nil || !prepared {
		t.Fatalf("prepare cached implementation: prepared=%v err=%v", prepared, err)
	}
	if cached.Result.Target == nil || cached.Result.verification == nil || cached.Result.Contract != contract {
		t.Fatalf("cached preparation is not compile-ready: %#v", cached.Result)
	}
	if goProjectionCalls != 0 || typeScriptProjectionCalls != 0 {
		t.Fatalf("implementation-only edit reran unrelated projections: go=%d typescript=%d", goProjectionCalls, typeScriptProjectionCalls)
	}
	data, err := os.ReadFile(filepath.Join(cached.Result.Dir, "svc/api.go"))
	if err != nil || string(data) != "package svc\n\nfunc Hello() string { return \"new-behavior\" }\n" {
		t.Fatalf("private workspace did not receive edited implementation: %q err=%v", data, err)
	}
}

func TestCachedPreparationRejectsTamperedPersistedProjection(t *testing.T) {
	root, original := ordinaryGoCacheFixture(t)
	writeBuildTestFile(t, original.Dir, "svc/scenery.gen.go", "package svc\n// tampered\n")
	contract, err := CompileContractWithSnapshot(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := PreparationFingerprint(appcfg.Config{Name: "buildtest"}, contract)
	if err != nil {
		t.Fatal(err)
	}
	cached, ok, err := LoadCachedPreparationContext(context.Background(), root, appcfg.Config{Name: "buildtest"}, "graph-current", fingerprint, contract)
	if err != nil || !ok || cached == nil {
		t.Fatalf("load cached preparation: ok=%v err=%v", ok, err)
	}
	prepared, err := PrepareCachedWorkspaceWithSnapshotContext(context.Background(), root, appcfg.Config{Name: "buildtest"}, cached.Result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared {
		t.Fatal("tampered persisted projection was accepted")
	}
}

func ordinaryGoCacheFixture(t *testing.T) (string, *Result) {
	t.Helper()
	root, result := newCachedBuildTestWorkspace(t, "graph-1")
	previous := generateHooks
	t.Cleanup(func() { generateHooks = previous })
	// Test the build-owned hook ordering and byte comparison here. Actual
	// generator publication through a cached candidate CLI is a release proof.
	generateHooks.PrepareBuildGoWorkspace = func(contract *compiler.Result) (generateapi.GoWorkspaceProjection, error) {
		writeBuildTestFile(t, contract.Root, cachedContractPath, cachedContractBytes)
		return generateapi.GoWorkspaceProjection{Files: map[string][]byte{cachedContractPath: []byte(cachedContractBytes)}}, nil
	}
	generateHooks.SyncCachedTypeScript = func(*compiler.Result) ([]string, error) { return nil, nil }
	return root, result
}
