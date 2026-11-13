package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// retainOrphanForTest writes the retained record of a checkout at root, in the
// shape of a producer whose schema the current one no longer decodes.
func retainOrphanForTest(t *testing.T, home, root string) WorktreePaths {
	t.Helper()
	paths, err := PathsForWorktree(home, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths.Directory, "storage"), 0o700); err != nil {
		t.Fatal(err)
	}
	record := fmt.Sprintf(`{"kind":"scenery.worktree","schema_revision":"sha256:old","app_root":%q,"app_id":"books","user_id":%d}`, paths.AppRoot, os.Getuid())
	if err := os.WriteFile(paths.Record, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestOrphanedWorktreeIsRemovedOnlyByItsPreviewedRecord(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	paths := retainOrphanForTest(t, home, filepath.Join(t.TempDir(), "gone"))
	kept := t.TempDir()
	retainOrphanForTest(t, home, kept)

	orphans, err := DiscoverOrphanedWorktrees(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0].Key != paths.Key || orphans[0].AppRoot != paths.AppRoot || orphans[0].AppID != "books" {
		t.Fatalf("orphans = %+v, want only the record of the absent checkout", orphans)
	}
	if err := paths.RemoveOrphaned("sha256:other", nil); err == nil {
		t.Fatal("a changed record was removed")
	}
	live, err := paths.AcquireLiveLock()
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.RemoveOrphaned(orphans[0].RecordDigest, nil); err == nil {
		t.Fatal("state with a live owner was removed")
	}
	if err := live.Release(); err != nil {
		t.Fatal(err)
	}
	released := false
	if err := paths.RemoveOrphaned(orphans[0].RecordDigest, func(orphan OrphanedWorktree) error {
		released = orphan.Key == paths.Key
		if _, err := os.Stat(paths.Record); err != nil {
			t.Errorf("record was removed before its resources were released: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(paths.Directory); !os.IsNotExist(err) || !released {
		t.Fatalf("directory stat = %v, released = %t", err, released)
	}
	existing, err := PathsForWorktree(home, kept)
	if err != nil {
		t.Fatal(err)
	}
	if err := existing.RemoveOrphaned(orphans[0].RecordDigest, nil); err == nil {
		t.Fatal("state of an existing checkout was removed")
	}
	if _, err := os.Stat(existing.Record); err != nil {
		t.Fatal(err)
	}
}

func TestOrphanedWorktreeOutlivesFailedResourceRelease(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	paths := retainOrphanForTest(t, home, filepath.Join(t.TempDir(), "gone"))
	orphans, err := DiscoverOrphanedWorktrees(home)
	if err != nil || len(orphans) != 1 {
		t.Fatalf("orphans = %+v, err = %v", orphans, err)
	}
	if err := paths.RemoveOrphaned(orphans[0].RecordDigest, func(OrphanedWorktree) error { return fmt.Errorf("volume in use") }); err == nil {
		t.Fatal("removal succeeded although its resources were not released")
	}
	if _, err := os.Stat(paths.Record); err != nil {
		t.Fatalf("record did not outlive the failed release: %v", err)
	}
}
