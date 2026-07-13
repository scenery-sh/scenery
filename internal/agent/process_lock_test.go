package agent

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestProcessLockRejectsSecondOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.lock")
	first, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if _, err := AcquireProcessLock(path); !errors.Is(err, ErrProcessLocked) {
		t.Fatalf("second lock error = %v", err)
	}
}

func TestNewServerRejectsSecondOwnerForSameHome(t *testing.T) {
	home := t.TempDir()
	first, err := NewServer(RunOptions{Home: home, RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	_, err = NewServer(RunOptions{Home: home, SocketPath: filepath.Join(home, "run", "other.sock"), RouterAddr: "127.0.0.1:0"})
	if !errors.Is(err, ErrProcessLocked) {
		t.Fatalf("second server error = %v", err)
	}
}
