package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreePathsBindRootAndStateHome(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	a, err := PathsForWorktree(filepath.Join(base, "state"), root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PathsForWorktree(filepath.Join(base, "other-state"), root)
	if err != nil {
		t.Fatal(err)
	}
	if a.Key != b.Key || a.Directory == b.Directory || a.Socket == b.Socket || len(a.Socket) > 100 {
		t.Fatal("root identity or state namespace is not isolated")
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	orphan, err := PathsForWorktree(filepath.Join(base, "state"), root)
	if err != nil || orphan != a {
		t.Fatalf("orphan identity changed: %v", err)
	}
}

func TestWorktreePathsRejectUnsafeFiles(t *testing.T) {
	base := t.TempDir()
	p, err := PathsForWorktree(base, filepath.Join(base, "app"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "unrelated"), p.LiveLock); err != nil {
		t.Fatal(err)
	}
	if lock, err := p.AcquireLiveLock(); err == nil {
		_ = lock.Release()
		t.Fatal("symlink lock accepted")
	}
	if _, err := os.Stat(filepath.Join(base, "unrelated")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("symlink target modified")
	}
	if err := os.Chmod(p.Directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.Prepare(); err == nil || !strings.Contains(err.Error(), "not private") {
		t.Fatal("nonprivate state directory accepted")
	}
}

func TestWorktreeLocksHaveSeparateScopes(t *testing.T) {
	p, err := PathsForWorktree(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live, err := p.AcquireLiveLock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Release() })
	if other, err := p.AcquireLiveLock(); !errors.Is(err, ErrProcessLocked) {
		if other != nil {
			_ = other.Release()
		}
		t.Fatalf("duplicate live owner: %v", err)
	}
	op, err := p.BeginOperation()
	if err != nil {
		t.Fatal(err)
	}
	if err := op.Close(); err != nil {
		t.Fatal(err)
	}
	if err := op.SaveRecord(NewWorktreeRecord(p, "app")); err == nil {
		t.Fatal("write without lock accepted")
	}
}
