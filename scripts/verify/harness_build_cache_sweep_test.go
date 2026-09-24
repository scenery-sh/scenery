package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"scenery.sh/internal/build"
	"scenery.sh/internal/devcache"
)

func newSweepTestWorkspace(t *testing.T, cacheRoot, appRoot string) string {
	t.Helper()
	path, err := build.WorkspaceDirAt(cacheRoot, appRoot, "probe-app")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".scenery-build-state.json"), []byte(`{"version":"10"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := build.WriteWorkspaceMarker(path, appRoot, "probe-app"); err != nil {
		t.Fatal(err)
	}
	return path
}

// The sweep removes only the workspaces of temporary app roots that no
// longer exist. Existing roots, retained probe roots and orphans outside the
// temporary directory stay for an explicit `scenery prune --build-cache`.
func TestBuildCacheSweepRemovesOnlyVanishedTemporaryAppRoots(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Cleanup(devcache.SetRoot(cacheRoot))
	vanished := filepath.Join(t.TempDir(), "probe", "app")
	vanishedWorkspace := newSweepTestWorkspace(t, cacheRoot, vanished)
	retained := filepath.Join(t.TempDir(), "retained", "app")
	if err := os.MkdirAll(retained, 0o755); err != nil {
		t.Fatal(err)
	}
	retainedWorkspace := newSweepTestWorkspace(t, cacheRoot, retained)
	foreign := filepath.Join(string(filepath.Separator), "no-such-checkout", "app")
	foreignWorkspace := newSweepTestWorkspace(t, cacheRoot, foreign)

	step := runHarnessBuildCacheSweepStep(context.Background(), "/repo", []string{"process-model", "dev-lock"})
	if !step.OK || step.Error != "" || len(step.Diagnostics) != 0 {
		t.Fatalf("step = %+v", step)
	}
	want := []string{"go", "run", "./scripts/verify", "--repo-root", "/repo", "--probe", "process-model", "--probe", "dev-lock", "--summary", "--write"}
	if !slices.Equal(step.Command, want) {
		t.Fatalf("command = %v, want %v", step.Command, want)
	}
	if step.Summary["cache_root"] != cacheRoot || step.Summary["workspaces"] != 3 || step.Summary["removed"] != 1 || step.Summary["bytes_reclaimed"].(int64) <= 0 {
		t.Fatalf("summary = %+v", step.Summary)
	}
	if _, err := os.Stat(vanishedWorkspace); !os.IsNotExist(err) {
		t.Fatalf("vanished probe workspace survived: %v", err)
	}
	for _, kept := range []string{retainedWorkspace, foreignWorkspace} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("sweep removed %s: %v", kept, err)
		}
	}
}

func TestHarnessTemporaryAppRootRecognizesBothSpellings(t *testing.T) {
	temporary := filepath.Join(t.TempDir(), "app")
	if !harnessTemporaryAppRoot(temporary) {
		t.Fatalf("%s is not temporary", temporary)
	}
	if resolved, err := filepath.EvalSymlinks(t.TempDir()); err == nil && !harnessTemporaryAppRoot(filepath.Join(resolved, "app")) {
		t.Fatalf("%s is not temporary", resolved)
	}
	for _, other := range []string{filepath.Join(string(filepath.Separator), "Users", "someone", "Repos", "app"), string(filepath.Separator), filepath.Clean(os.TempDir())} {
		if harnessTemporaryAppRoot(other) {
			t.Fatalf("%s is temporary", other)
		}
	}
}
