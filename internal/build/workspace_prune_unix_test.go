//go:build unix

package build

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
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
