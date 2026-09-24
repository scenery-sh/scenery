package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scenery.sh/internal/devcache"
)

// A workspace names the app root it serves; nothing else in the cache does.
func TestWorkspaceMarkerRoundTripsAndLeavesAnUnchangedMarkerAlone(t *testing.T) {
	workspace := t.TempDir()
	appRoot := filepath.Join(t.TempDir(), "app")
	if err := WriteWorkspaceMarker(workspace, appRoot, "demo"); err != nil {
		t.Fatal(err)
	}
	marker, err := ReadWorkspaceMarker(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if marker.AppRoot != appRoot || marker.AppName != "demo" || marker.Kind != workspaceMarkerKind {
		t.Fatalf("marker = %+v", marker)
	}
	before, err := os.Stat(WorkspaceMarkerPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	old := before.ModTime().Add(-time.Hour)
	if err := os.Chtimes(WorkspaceMarkerPath(workspace), old, old); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspaceMarker(workspace, appRoot, "demo"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(WorkspaceMarkerPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(old) {
		t.Fatalf("an unchanged marker was rewritten: %s -> %s", old, after.ModTime())
	}
	if err := WriteWorkspaceMarker(workspace, appRoot, "renamed"); err != nil {
		t.Fatal(err)
	}
	if marker, err := ReadWorkspaceMarker(workspace); err != nil || marker.AppName != "renamed" {
		t.Fatalf("renamed marker = %+v, %v", marker, err)
	}
	if _, err := ReadWorkspaceMarker(t.TempDir()); !os.IsNotExist(err) {
		t.Fatalf("missing marker error = %v", err)
	}
	writeBuildTestFile(t, workspace, workspaceMarkerFile, `{"kind":"other","app_root":"/x","app_name":"a"}`)
	if _, err := ReadWorkspaceMarker(workspace); err == nil {
		t.Fatal("a marker of another kind was accepted")
	}
}

// The marker and the lock are workspace state, never unexpected membership.
func TestPreparedWorkspaceAcceptsItsMarker(t *testing.T) {
	root := t.TempDir()
	writeBuildTestFile(t, root, "go.mod", "module example.test/prepared\n")
	writeBuildTestFile(t, root, "source.go", "package sample\n")
	result := &Result{Dir: root, SourceFiles: []string{"go.mod", "source.go"}}
	var err error
	result.BuildFingerprint, err = workspaceBuildFingerprint(root, nil, result.SourceFiles, result.GeneratedFiles)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspaceMarker(root, filepath.Join(root, "app"), "sample"); err != nil {
		t.Fatal(err)
	}
	if err := verifyPreparedWorkspace(result); err != nil {
		t.Fatalf("marker rejected: %v", err)
	}
	if err := removeUnexpectedFilesFromListsObserved(root, result.SourceFiles, nil, &workspaceMutation{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWorkspaceMarker(root); err != nil {
		t.Fatalf("membership cleanup removed the marker: %v", err)
	}
}

type pruneTestWorkspace struct {
	path    string
	appRoot string
}

// newPruneTestWorkspace materializes a workspace of appRoot beneath the
// current cache root with its build state last written at updatedAt.
func newPruneTestWorkspace(t *testing.T, appRoot, appName string, updatedAt time.Time, marker bool) pruneTestWorkspace {
	t.Helper()
	path, err := workspaceDir(appRoot, appName)
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, path, buildStateFile, `{"version":"10"}`)
	writeBuildTestFile(t, path, ".scenery-workspace.lock", "")
	writeBuildTestFile(t, path, developmentProcessBinaryDir+"/host-0123456789abcdef", "executable bytes")
	if marker {
		if err := WriteWorkspaceMarker(path, appRoot, appName); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(filepath.Join(path, buildStateFile), updatedAt, updatedAt); err != nil {
		t.Fatal(err)
	}
	return pruneTestWorkspace{path: path, appRoot: appRoot}
}

func isolatePruneTestCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(devcache.SetRoot(root))
	return root
}

func TestPruneWorkspacesRemovesOrphanedAndStaleWorkspacesOnly(t *testing.T) {
	cacheRoot := isolatePruneTestCache(t)
	now := time.Now()
	orphanRoot := filepath.Join(t.TempDir(), "gone")
	orphan := newPruneTestWorkspace(t, orphanRoot, "gone-app", now, true)
	staleRoot := t.TempDir()
	stale := newPruneTestWorkspace(t, staleRoot, "stale-app", now.Add(-48*time.Hour), true)
	recentRoot := t.TempDir()
	recent := newPruneTestWorkspace(t, recentRoot, "recent-app", now, true)
	unmarkedRoot := t.TempDir()
	unmarked := newPruneTestWorkspace(t, unmarkedRoot, "unmarked-app", now.Add(-48*time.Hour), false)
	protectedRoot := t.TempDir()
	protected := newPruneTestWorkspace(t, protectedRoot, "running-app", now.Add(-48*time.Hour), true)
	// Content-addressed caches, fingerprints and look-alike directories
	// without workspace state are never workspaces.
	writeBuildTestFile(t, cacheRoot, "build/shared-binaries/v1/abc/scenery-app", "shared")
	writeBuildTestFile(t, cacheRoot, "build/framework-fingerprint-0123456789abcdef.json", "{}")
	writeBuildTestFile(t, cacheRoot, "build/lookalike-0123456789abcdef/go.mod", "module x\n")
	writeBuildTestFile(t, cacheRoot, "build/retained-go-compiler/v1/unrelated/state", "x")

	cutoff := now.Add(-24 * time.Hour)
	reported, entries, err := PruneWorkspaces(context.Background(), WorkspacePruneOptions{Cutoff: cutoff, ProtectedAppRoots: []string{protectedRoot}})
	if err != nil {
		t.Fatal(err)
	}
	if reported != cacheRoot {
		t.Fatalf("cache root = %s, want %s", reported, cacheRoot)
	}
	byPath := map[string]WorkspacePruneEntry{}
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	if len(entries) != 5 {
		t.Fatalf("entries = %+v", entries)
	}
	for _, want := range []struct {
		workspace pruneTestWorkspace
		removed   bool
		reason    string
	}{
		{orphan, true, WorkspacePruneAppRootMissing},
		{stale, true, WorkspacePruneStale},
		{recent, false, WorkspacePruneRecent},
		{unmarked, true, WorkspacePruneStale},
		{protected, false, WorkspacePruneActive},
	} {
		entry, ok := byPath[want.workspace.path]
		if !ok || entry.Removed != want.removed || entry.Reason != want.reason {
			t.Fatalf("%s: entry = %+v, want removed=%v reason=%s", want.workspace.path, entry, want.removed, want.reason)
		}
		if _, err := os.Stat(want.workspace.path); (err == nil) == want.removed {
			t.Fatalf("%s: exists=%v after prune, want removed=%v", want.workspace.path, err == nil, want.removed)
		}
		if entry.Bytes <= 0 || entry.UpdatedAt == "" {
			t.Fatalf("%s: evidence missing: %+v", want.workspace.path, entry)
		}
	}
	if byPath[orphan.path].AppRoot != orphanRoot || byPath[orphan.path].AppName != "gone-app" || byPath[unmarked.path].AppRoot != "" {
		t.Fatalf("recorded app roots = %+v / %+v", byPath[orphan.path], byPath[unmarked.path])
	}
	for _, kept := range []string{"build/shared-binaries/v1/abc/scenery-app", "build/framework-fingerprint-0123456789abcdef.json", "build/lookalike-0123456789abcdef/go.mod", "build/retained-go-compiler/v1/unrelated/state"} {
		if _, err := os.Stat(filepath.Join(cacheRoot, kept)); err != nil {
			t.Fatalf("prune touched %s: %v", kept, err)
		}
	}
}

// Without a cutoff only orphans go, and an orphan filter narrows them to the
// roots a caller owns: the repository verifier removes the workspaces of its
// own temporary app roots and nothing else.
func TestPruneWorkspacesHonorsTheOrphanFilterAndAppRootScope(t *testing.T) {
	isolatePruneTestCache(t)
	old := time.Now().Add(-72 * time.Hour)
	owned := filepath.Join(t.TempDir(), "probe", "app")
	ownedWorkspace := newPruneTestWorkspace(t, owned, "probe-app", old, true)
	foreign := filepath.Join(t.TempDir(), "elsewhere", "app")
	foreignWorkspace := newPruneTestWorkspace(t, foreign, "foreign-app", old, true)
	existingRoot := t.TempDir()
	existing := newPruneTestWorkspace(t, existingRoot, "existing-app", old, true)

	_, entries, err := PruneWorkspaces(context.Background(), WorkspacePruneOptions{OrphanFilter: func(appRoot string) bool {
		return filepath.Dir(filepath.Dir(appRoot)) == filepath.Dir(filepath.Dir(owned))
	}})
	if err != nil {
		t.Fatal(err)
	}
	removed := map[string]bool{}
	for _, entry := range entries {
		removed[entry.Path] = entry.Removed
		if !entry.Removed && entry.Reason != WorkspacePruneKept {
			t.Fatalf("kept workspace reason = %+v", entry)
		}
	}
	if !removed[ownedWorkspace.path] || removed[foreignWorkspace.path] || removed[existing.path] {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Stat(foreignWorkspace.path); err != nil {
		t.Fatalf("the filtered orphan was removed: %v", err)
	}

	_, scoped, err := PruneWorkspaces(context.Background(), WorkspacePruneOptions{Cutoff: time.Now(), AppRoots: []string{existingRoot}})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].Path != existing.path || !scoped[0].Removed || scoped[0].Reason != WorkspacePruneStale {
		t.Fatalf("scoped prune = %+v", scoped)
	}
	if _, err := os.Stat(foreignWorkspace.path); err != nil {
		t.Fatalf("an app-root scoped prune touched another workspace: %v", err)
	}
}
