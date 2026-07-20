package build

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
)

func TestRefreshCachedWorkspaceResyncsMissingSourceFiles(t *testing.T) {
	t.Parallel()

	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

	newFile := "svc/helper.go"
	writeBuildTestFile(t, appDir, newFile, "package svc\n\nfunc helper() {}\n")

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}
	if _, err := os.Stat(filepath.Join(cached.Result.Dir, filepath.FromSlash(newFile))); !os.IsNotExist(err) {
		t.Fatalf("expected cached workspace to initially miss %s, stat err=%v", newFile, err)
	}

	reused, err := RefreshCachedWorkspace(appDir, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if reused {
		t.Fatal("expected a changed build fingerprint without a binary to force full preparation")
	}
	if _, err := os.Stat(filepath.Join(cached.Result.Dir, filepath.FromSlash(newFile))); err != nil {
		t.Fatalf("expected refreshed workspace to include %s: %v", newFile, err)
	}
	found := slices.Contains(cached.Result.SourceFiles, newFile)
	if !found {
		t.Fatalf("refreshed source files missing %s: %v", newFile, cached.Result.SourceFiles)
	}
}

func TestRefreshCachedWorkspaceResyncsChangedSourceFiles(t *testing.T) {
	t.Parallel()

	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

	// Regression: a git pull changed svc/api.go without the dev watcher
	// reporting it; the workspace copy must still be refreshed from disk
	// instead of being trusted because it exists.
	const updated = `package svc

import "context"

func Hello(ctx context.Context) error { return nil }

func pulledInChange() {}
`
	writeBuildTestFile(t, appDir, "svc/api.go", updated)

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}

	reused, err := RefreshCachedWorkspace(appDir, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if reused {
		t.Fatal("expected a changed build fingerprint without a binary to force full preparation")
	}
	data, err := os.ReadFile(filepath.Join(cached.Result.Dir, "svc", "api.go"))
	if err != nil {
		t.Fatalf("read workspace copy: %v", err)
	}
	if string(data) != updated {
		t.Fatalf("workspace copy = %q, want resynced %q", data, updated)
	}
}

func TestRefreshCachedWorkspaceFallsBackWhenSourceFileMissing(t *testing.T) {
	t.Parallel()

	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")

	target := filepath.Join(result.Dir, "svc", "api.go")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove source file: %v", err)
	}
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

	reused, err := RefreshCachedWorkspace(cached.Result.AppRoot, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if !reused {
		t.Fatal("expected restored workspace source to reuse the existing fingerprint binary")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected missing source file to be restored: %v", err)
	}
}

func TestRefreshCachedWorkspaceMarksNeedsTidyWhenImportsChange(t *testing.T) {
	t.Parallel()

	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")

	writeBuildTestFile(t, appDir, "svc/extra.go", `package svc

import _ "rsc.io/quote"
`)

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}

	reused, err := RefreshCachedWorkspace(appDir, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if reused {
		t.Fatal("expected changed imports without a matching binary to force full preparation")
	}
	if !cached.Result.NeedsTidy {
		t.Fatal("expected refreshed cached workspace to require go mod tidy")
	}
}

func TestRefreshCachedWorkspaceSeedsDependencyFingerprintBeforeReuse(t *testing.T) {
	t.Parallel()

	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")

	if err := seedSceneryGoSum(result.Dir, repoRoot(t)); err != nil {
		t.Fatalf("seedSceneryGoSum() error = %v", err)
	}
	depFingerprint, err := dependencyFingerprintFromWorkspace(result.Dir)
	if err != nil {
		t.Fatalf("dependencyFingerprintFromWorkspace() error = %v", err)
	}
	state, err := loadBuildState(result.Dir)
	if err != nil {
		t.Fatalf("loadBuildState() error = %v", err)
	}
	state.DependencyFingerprint = depFingerprint
	if err := saveBuildState(result.Dir, state); err != nil {
		t.Fatalf("saveBuildState() error = %v", err)
	}
	if err := os.WriteFile(result.Binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write cached binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(result.Dir, "go.sum"), nil, 0o644); err != nil {
		t.Fatalf("write stale workspace go.sum: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}
	reused, err := RefreshCachedWorkspace(appDir, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if !reused {
		t.Fatal("expected cached workspace refresh to be reusable")
	}
	if cached.Result.NeedsTidy {
		t.Fatal("expected seeded dependency fingerprint to avoid tidy")
	}
	if !cached.Result.ReuseCompiled {
		t.Fatal("expected existing fingerprint binary to be reused")
	}
	if cached.Result.DependencyFingerprint != depFingerprint {
		t.Fatalf("dependency fingerprint = %q, want seeded %q", cached.Result.DependencyFingerprint, depFingerprint)
	}
}

func TestRefreshCachedWorkspaceFallsBackWhenBinaryMissing(t *testing.T) {
	t.Parallel()

	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")
	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}
	if cached.Result.ReuseCompiled {
		t.Fatal("expected fixture to begin without a compiled binary")
	}

	reused, err := RefreshCachedWorkspace(appDir, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if reused || cached.Result.ReuseCompiled {
		t.Fatal("expected a missing fingerprint binary to force full preparation")
	}
}

func TestRefreshCachedWorkspaceFallsBackWhenGeneratedFileMissing(t *testing.T) {
	t.Parallel()

	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")

	target := filepath.Join(result.Dir, "svc", "scenery.gen.go")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove generated file: %v", err)
	}

	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}

	reused, err := RefreshCachedWorkspace(appDir, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if reused {
		t.Fatal("expected cached workspace refresh to force regeneration when a generated file is missing")
	}
}

func TestRefreshCachedWorkspaceFallsBackWhenFrameworkChanges(t *testing.T) {
	t.Parallel()

	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")
	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil {
		t.Fatalf("LoadCachedGraph() error = %v", err)
	}
	if !ok || cached == nil || cached.Result == nil {
		t.Fatal("expected cached graph to load")
	}
	cached.Result.FrameworkFingerprint = "sha256:stale"

	reused, err := RefreshCachedWorkspace(appDir, cached.Result)
	if err != nil {
		t.Fatalf("RefreshCachedWorkspace() error = %v", err)
	}
	if reused {
		t.Fatal("expected framework drift to force a full build preparation")
	}
}

func TestSyncSourceFilesDetectsNewFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()

	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	writeBuildTestFile(t, appRoot, "svc/api.go", "package svc\n\nimport \"embed\"\n\n//go:embed templates/*\nvar _ embed.FS\n")

	_, prevStamps, err := syncSourceFiles(root, appRoot, nil, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}

	const asset = "svc/templates/cv_classic.css"
	writeBuildTestFile(t, appRoot, asset, "body { color: black; }\n")

	got, _, err := syncSourceFiles(root, appRoot, prevStamps, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(asset))); err != nil {
		t.Fatalf("expected new asset to be synced into workspace: %v", err)
	}
	found := slices.Contains(got, asset)
	if !found {
		t.Fatalf("expected source files to include %s, got %v", asset, got)
	}
}

func TestSyncSourceFilesResyncsFilesChangedOnDisk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()

	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	writeBuildTestFile(t, appRoot, "svc/api.go", "package svc\n\nfunc oldSymbol() {}\n")

	_, prevStamps, err := syncSourceFiles(root, appRoot, nil, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}

	// Simulate a change the file watcher never reported, e.g. a git pull
	// rewriting the file between a watch snapshot and the workspace sync.
	const updated = "package svc\n\nfunc replacementSymbol() {}\n"
	writeBuildTestFile(t, appRoot, "svc/api.go", updated)

	if _, _, err := syncSourceFiles(root, appRoot, prevStamps, nil); err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "svc", "api.go"))
	if err != nil {
		t.Fatalf("read workspace copy: %v", err)
	}
	if string(data) != updated {
		t.Fatalf("workspace copy = %q, want resynced %q", data, updated)
	}
}

func TestSyncSourceFilesRestoresDeletedWorkspaceCopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()

	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	writeBuildTestFile(t, appRoot, "svc/api.go", "package svc\n\nfunc helper() {}\n")

	_, prevStamps, err := syncSourceFiles(root, appRoot, nil, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "svc", "api.go")); err != nil {
		t.Fatalf("remove workspace copy: %v", err)
	}

	if _, _, err := syncSourceFiles(root, appRoot, prevStamps, nil); err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "svc", "api.go")); err != nil {
		t.Fatalf("expected deleted workspace copy to be restored: %v", err)
	}
}

func TestSyncGeneratedFilesKeepsPathsThatAreNowRegularSourceFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()
	rel := "house/rooftopology_api.go"
	writeBuildTestFile(t, appRoot, rel, "package house\n\nfunc helper() {}\n")
	writeBuildTestFile(t, root, rel, "package house\n\nfunc oldGenerated() {}\n")

	got, err := syncGeneratedFiles(root, appRoot, &codegen.Output{
		Generated: map[string][]byte{},
	}, []string{rel}, []string{rel})
	if err != nil {
		t.Fatalf("syncGeneratedFiles() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("generated files = %v, want empty", got)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("expected source-backed file to remain: %v", err)
	}
	if string(data) != "package house\n\nfunc oldGenerated() {}\n" {
		t.Fatalf("unexpected file contents after syncGeneratedFiles: %q", data)
	}
}
