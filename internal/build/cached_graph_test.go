package build

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestSyncWorkspaceRemovesStaleFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeBuildTestFile(t, root, "go.mod", "module example.com/test\n")
	writeBuildTestFile(t, root, "svc/api.go", "package svc\n")
	if err := removeUnexpectedFilesFromLists(root, []string{"go.mod", "svc/api.go"}, []string{"scenery_internal_main/x"}); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	stalePath := filepath.Join(root, "svc", "stale.go")
	if err := os.WriteFile(stalePath, []byte("package svc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeUnexpectedFilesFromLists(root, []string{"go.mod", "svc/api.go"}, []string{"scenery_internal_main/x"}); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale file to be removed, stat err = %v", err)
	}
}

func TestLoadCachedGraph(t *testing.T) {
	t.Parallel()

	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil {
		t.Fatal("expected cached graph to load")
	}
	if string(cached.Metadata) != `{"ok":true}` {
		t.Fatalf("metadata = %s", cached.Metadata)
	}
	if string(cached.APIEncoding) != `{"api":"v1"}` {
		t.Fatalf("api encoding = %s", cached.APIEncoding)
	}
	if cached.Result == nil || cached.Result.Dir == "" {
		t.Fatal("expected cached result to include workspace")
	}
	if cached.Result.AppRoot != appDir || cached.Result.AppName != "buildtest" {
		t.Fatalf("cached result identity = %+v", cached.Result)
	}
}

func TestLoadCachedPreparationSurvivesImplementationFingerprintChange(t *testing.T) {
	t.Parallel()

	appDir, original := newCachedBuildTestWorkspace(t, "graph-before")
	writeBuildTestFile(t, appDir, "svc/api.go", "package svc\n\nfunc Hello() string { return \"after\" }\n")
	contract, err := CompileContractWithSnapshot(appDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if contract.Manifest.ContractRevision != original.Contract.Manifest.ContractRevision {
		t.Fatalf("implementation body changed contract revision: %s -> %s", original.Contract.Manifest.ContractRevision, contract.Manifest.ContractRevision)
	}
	fingerprint, err := PreparationFingerprint(appcfg.Config{Name: "buildtest"}, contract)
	if err != nil {
		t.Fatal(err)
	}
	cached, ok, err := LoadCachedPreparationContext(context.Background(), appDir, appcfg.Config{Name: "buildtest"}, "graph-after", fingerprint, contract)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("implementation-only edit did not reuse declaration preparation")
	}
	if cached.Result.GraphFingerprint != "graph-after" || cached.Result.Contract != contract {
		t.Fatalf("cached preparation is not bound to current inputs: %#v", cached.Result)
	}
}

func TestLoadCachedGraphRejectsMissingPayloads(t *testing.T) {
	t.Parallel()

	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")
	state, err := loadBuildState(result.Dir)
	if err != nil {
		t.Fatalf("load build state: %v", err)
	}
	state.Metadata = nil
	if err := saveBuildState(result.Dir, state); err != nil {
		t.Fatalf("save build state: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if ok || cached != nil {
		t.Fatalf("expected missing cached payloads to reject graph hit, got ok=%v cached=%#v", ok, cached)
	}
}

func TestLoadCachedGraphRequiresMatchingGoBuildFlags(t *testing.T) {
	t.Parallel()

	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")
	flags := []string{"-tags=roofmapnet_native"}

	state, err := loadBuildState(result.Dir)
	if err != nil {
		t.Fatalf("loadBuildState() error = %v", err)
	}
	buildFingerprint, err := workspaceBuildFingerprint(result.Dir, flags, sourceFilesFromStamps(state.SourceStamps), state.GeneratedFiles)
	if err != nil {
		t.Fatalf("workspaceBuildFingerprint() error = %v", err)
	}
	state.GoBuildFlags = append([]string(nil), flags...)
	state.BuildFingerprint = buildFingerprint
	if err := saveBuildState(result.Dir, state); err != nil {
		t.Fatalf("saveBuildState() error = %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{
		Name:  "buildtest",
		Build: appcfg.BuildConfig{GoFlags: flags},
	}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() with matching flags error = %v", err)
	}
	if !ok || cached == nil {
		t.Fatal("expected cached graph with matching build flags")
	}
	if strings.Join(cached.Result.GoBuildFlags, "\x00") != strings.Join(flags, "\x00") {
		t.Fatalf("cached build flags = %#v, want %#v", cached.Result.GoBuildFlags, flags)
	}

	cached, ok, err = LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() with missing flags error = %v", err)
	}
	if ok || cached != nil {
		t.Fatalf("expected missing build flags to reject cached graph, got ok=%v cached=%#v", ok, cached)
	}
}

func TestCompileCachedGraphWritesLatestBuildManifest(t *testing.T) {
	useFakeGoRunner(t)
	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")
	if err := os.WriteFile(result.Binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write cached binary: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}
	prepared, err := RefreshCachedWorkspace(appDir, cached.Result)
	if err != nil {
		t.Fatalf("refresh cached workspace: %v", err)
	}
	if !prepared {
		t.Fatal("expected cached graph workspace to remain prepared")
	}
	if contract := cached.Result.Contract; contract == nil || !contract.Valid() || contract.Root != appDir {
		t.Fatalf("reused executable must retain current compiled requirements: %+v", contract)
	}
	if cached.Result.Target == nil || cached.Result.BuildInput != nil || len(cached.Result.ImplementationRevisions) != 0 {
		t.Fatal("bare cached executable unexpectedly supplied candidate preflight identity")
	}

	if err := Compile(cached.Result); err != nil {
		t.Fatalf("compile cached result: %v", err)
	}
	if cached.Result.BuildInput == nil || len(cached.Result.ImplementationRevisions) == 0 {
		t.Fatal("identity-bound build did not prepare candidate preflight identity")
	}

	manifest, ok, err := ReadLatestBuildManifest(appDir)
	if err != nil {
		t.Fatalf("ReadLatestBuildManifest after cached compile: %v", err)
	}
	if !ok {
		t.Fatal("expected latest build manifest after cached compile")
	}
	if manifest.App.Root != appDir || manifest.App.Name != "buildtest" {
		t.Fatalf("manifest app = %+v", manifest.App)
	}
	if manifest.Build.Phase != "compiled" {
		t.Fatalf("phase after cached compile = %q", manifest.Build.Phase)
	}
}

func TestLoadCachedGraphRejectsBuildStateVersionFour(t *testing.T) {
	t.Parallel()

	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")

	statePath := filepath.Join(result.Dir, buildStateFile)
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read build state: %v", err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode build state: %v", err)
	}
	state["version"] = "4"
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatalf("encode old build state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatalf("write old build state: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if ok || cached != nil {
		t.Fatalf("expected old build state to be rejected, got ok=%v cached=%#v", ok, cached)
	}
}

func TestLoadCachedGraphRejectsGeneratorFingerprintMismatch(t *testing.T) {
	t.Parallel()

	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")

	statePath := filepath.Join(result.Dir, buildStateFile)
	state, err := loadBuildState(result.Dir)
	if err != nil {
		t.Fatalf("load build state: %v", err)
	}
	state.GeneratorFingerprint = "stale-generator"
	if err := saveBuildState(result.Dir, state); err != nil {
		t.Fatalf("save stale build state: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if ok || cached != nil {
		t.Fatalf("expected stale generator fingerprint to be rejected, got ok=%v cached=%#v", ok, cached)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file should remain for fallback regeneration: %v", err)
	}
}
