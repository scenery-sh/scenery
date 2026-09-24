package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/build"
)

// newPruneTestWorkspace materializes a development-cache workspace of appRoot
// as a preparation leaves it, with its build state last written at updatedAt.
func newPruneTestWorkspace(t *testing.T, cacheRoot, appRoot, appName string, updatedAt time.Time) string {
	t.Helper()
	path, err := build.WorkspaceDirAt(cacheRoot, appRoot, appName)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		".scenery-build-state.json":               `{"version":"10"}`,
		".scenery-workspace.lock":                 "",
		"scenery-processes/host-0123456789abcdef": "executable bytes",
	} {
		full := filepath.Join(path, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := build.WriteWorkspaceMarker(path, appRoot, appName); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(path, ".scenery-build-state.json"), updatedAt, updatedAt); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPruneBuildCacheRemovesDisposableBuildStateAndReportsIt(t *testing.T) {
	isolateCommandAgentHome(t)
	cacheRoot := isolateCommandCacheRoot(t)
	now := time.Now()
	orphanRoot := filepath.Join(t.TempDir(), "gone")
	orphan := newPruneTestWorkspace(t, cacheRoot, orphanRoot, "gone-app", now)
	liveRoot := canonicalTestDir(t)
	live := newPruneTestWorkspace(t, cacheRoot, liveRoot, "live-app", now)
	staleRoot := canonicalTestDir(t)
	stale := newPruneTestWorkspace(t, cacheRoot, staleRoot, "stale-app", now.Add(-72*time.Hour))
	// Framework state nothing selects: no receipt names these snapshots.
	digest := strings.Repeat("a", 64)
	snapshot := filepath.Join(liveRoot, ".scenery", "framework", "source", digest)
	producer := filepath.Join(liveRoot, ".scenery", "framework", "bin", digest)
	for _, path := range []string{filepath.Join(snapshot, "go.mod"), filepath.Join(producer, "platform", strings.Repeat("b", 64), "scenery")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The explicit app root scope touches that root's workspace and framework
	// state only: the orphan and the stale sibling stay.
	var scoped bytes.Buffer
	if err := runWorktreePrune(t.Context(), &scoped, []string{"--older-than", "24h", "--build-cache", "--app-root", liveRoot, "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	var response pruneResponse
	if err := decodeCLIJSON(scoped.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.cliPayloadIdentity != newCLIPayloadIdentity("scenery.prune") || !response.BuildCacheCleanup || response.BuildCache == nil {
		t.Fatalf("response = %+v", response)
	}
	if response.BuildCache.CacheRoot != cacheRoot || len(response.BuildCache.Workspaces) != 1 {
		t.Fatalf("scoped build cache = %+v", response.BuildCache)
	}
	if entry := response.BuildCache.Workspaces[0]; entry.Path != live || entry.Removed || entry.Reason != build.WorkspacePruneRecent || entry.AppRoot != liveRoot || entry.AppName != "live-app" {
		t.Fatalf("scoped workspace entry = %+v", entry)
	}
	removedFramework := map[string]bool{}
	for _, entry := range response.BuildCache.Framework {
		if entry.Removed && entry.Reason == build.FrameworkPruneUnselected {
			removedFramework[entry.Path] = true
		}
	}
	if !removedFramework[snapshot] || !removedFramework[producer] || response.BuildCache.BytesReclaimed <= 0 {
		t.Fatalf("framework entries = %+v", response.BuildCache)
	}
	for _, path := range []string{snapshot, producer} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived: %v", path, err)
		}
	}
	for _, path := range []string{orphan, stale, live} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("scoped prune touched %s: %v", path, err)
		}
	}

	// Machine-wide, the orphan and the stale workspace go; the recent one
	// stays, and no framework state is considered without an app root.
	var output bytes.Buffer
	if err := runWorktreePrune(t.Context(), &output, []string{"--older-than", "24h", "--build-cache", "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	response = pruneResponse{}
	if err := decodeCLIJSON(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.BuildCache == nil || len(response.BuildCache.Framework) != 0 || response.DBCleanup || response.StateCleanup {
		t.Fatalf("machine-wide response = %+v", response)
	}
	got := map[string]build.WorkspacePruneEntry{}
	for _, entry := range response.BuildCache.Workspaces {
		got[entry.Path] = entry
	}
	for path, want := range map[string]struct {
		removed bool
		reason  string
	}{orphan: {true, build.WorkspacePruneAppRootMissing}, stale: {true, build.WorkspacePruneStale}, live: {false, build.WorkspacePruneRecent}} {
		entry, ok := got[path]
		if !ok || entry.Removed != want.removed || entry.Reason != want.reason {
			t.Fatalf("%s: entry = %+v, want %+v", path, entry, want)
		}
		if _, err := os.Stat(path); (err == nil) == want.removed {
			t.Fatalf("%s: exists=%v, want removed=%v", path, err == nil, want.removed)
		}
	}
	if response.BuildCache.BytesReclaimed != got[orphan].Bytes+got[stale].Bytes || got[orphan].Bytes <= 0 {
		t.Fatalf("bytes reclaimed = %d, entries %+v", response.BuildCache.BytesReclaimed, got)
	}

	// Without --build-cache the build cache is untouched and reported as such.
	var plain bytes.Buffer
	if err := runWorktreePrune(t.Context(), &plain, []string{"--older-than", "1h", "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	response = pruneResponse{}
	if err := decodeCLIJSON(plain.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.BuildCacheCleanup || response.BuildCache != nil {
		t.Fatalf("default prune reported build cache work: %+v", response)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("default prune removed a workspace: %v", err)
	}
}

func TestPruneBuildCacheHumanOutputReportsReclaimedSpace(t *testing.T) {
	isolateCommandAgentHome(t)
	cacheRoot := isolateCommandCacheRoot(t)
	orphan := newPruneTestWorkspace(t, cacheRoot, filepath.Join(t.TempDir(), "gone"), "gone-app", time.Now())
	var output bytes.Buffer
	if err := runWorktreePrune(t.Context(), &output, []string{"--older-than", "24h", "--build-cache"}); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "removed build workspace "+orphan+" (app_root_missing, ") || !strings.Contains(text, "removed 1 workspaces, kept 0; reclaimed ") {
		t.Fatalf("human output = %q", text)
	}
}

func TestPruneHelpAdvertisesBuildCache(t *testing.T) {
	output := captureStdout(t, func() error {
		return helpCommand([]string{"prune", "-o", "json"})
	})
	var manifest helpManifest
	if err := decodeCLIJSON([]byte(output), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Commands) != 1 || manifest.Commands[0].Command != "prune" {
		t.Fatalf("manifest = %+v", manifest)
	}
	prune := manifest.Commands[0]
	if !containsHelpString(prune.Flags, "--build-cache") || !strings.Contains(prune.Usage[0], "[--build-cache]") {
		t.Fatalf("prune grammar = %+v", prune)
	}
}
