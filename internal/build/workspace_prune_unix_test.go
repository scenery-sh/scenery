//go:build unix

package build

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	appcfg "scenery.sh/internal/app"
)

// A workspace another process is preparing is kept, whatever its age or the
// state of its app root: the lock is the proof of use.
func TestPruneWorkspacesKeepsALockedWorkspace(t *testing.T) {
	isolatePruneTestCache(t)
	old := time.Now().Add(-72 * time.Hour)
	orphan := filepath.Join(t.TempDir(), "gone")
	locked := newPruneTestWorkspace(t, orphan, "locked-app", old, true)
	file, err := os.OpenFile(filepath.Join(locked.path, ".scenery-workspace.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	_, entries, err := PruneWorkspaces(context.Background(), WorkspacePruneOptions{Cutoff: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Removed || entries[0].Reason != WorkspacePruneLocked {
		t.Fatalf("entries = %+v", entries)
	}
	if _, err := os.Stat(locked.path); err != nil {
		t.Fatalf("a locked workspace was removed: %v", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	_, entries, err = PruneWorkspaces(context.Background(), WorkspacePruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].Removed || entries[0].Reason != WorkspacePruneAppRootMissing {
		t.Fatalf("entries after unlock = %+v", entries)
	}
}

// Preparation records the app root a workspace serves, so a later prune can
// tell an orphaned workspace from a live one.
func TestPreparedWorkspaceRecordsItsAppRoot(t *testing.T) {
	original := currentGeneratorFingerprint
	currentGeneratorFingerprint = func() (string, error) { return "fixture-generator", nil }
	t.Cleanup(func() { currentGeneratorFingerprint = original })

	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")
	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil || !ok {
		t.Fatalf("LoadCachedGraph() = %v, %v", ok, err)
	}
	if _, err := ReadWorkspaceMarker(cached.Result.Dir); !os.IsNotExist(err) {
		t.Fatalf("fixture workspace already carries a marker: %v", err)
	}
	prepared, err := PrepareCachedWorkspaceHeldContext(context.Background(), appDir, appcfg.Config{}, cached.Result, nil)
	if err != nil || !prepared {
		t.Fatalf("PrepareCachedWorkspaceHeldContext() = %v, %v", prepared, err)
	}
	cached.Result.ReleaseWorkspace()
	marker, err := ReadWorkspaceMarker(cached.Result.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if marker.AppRoot != appDir || marker.AppName != "buildtest" {
		t.Fatalf("marker = %+v, want app root %s", marker, appDir)
	}
}
