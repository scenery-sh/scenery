package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedBinaryWaitIsContextCancelable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "action.lock")
	release, acquired, err := trySharedBinaryLock(path)
	if err != nil || !acquired {
		t.Fatalf("hold lock: acquired=%v err=%v", acquired, err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := acquireSharedBinaryLock(ctx, path); err == nil || ctx.Err() == nil {
		t.Fatalf("blocked subscriber was not canceled: %v", err)
	}
}

func TestSharedBinaryExistingLockDoesNotRecreateMissingLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vanished.subscriber")
	release, acquired, exists, err := trySharedBinaryExistingLock(path)
	if err != nil || exists || acquired || release != nil {
		t.Fatalf("missing lease: exists=%v acquired=%v release=%v err=%v", exists, acquired, release != nil, err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing lease was recreated: %v", err)
	}
}

func TestSharedBinaryTicketQueueOrdersActiveAndReclaimsStale(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "queue")
	firstPath, releaseFirst, err := createSharedBinaryLease(directory, "00000000000000000001-0000000001-00000000000000000001.ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		releaseFirst()
		_ = os.Remove(firstPath)
	}()
	secondPath, releaseSecond, err := createSharedBinaryLease(directory, "00000000000000000002-0000000001-00000000000000000002.ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		releaseSecond()
		_ = os.Remove(secondPath)
	}()
	stale := filepath.Join(directory, "00000000000000000000-0000000001-00000000000000000000.ticket")
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	position, err := sharedBinaryTicketPosition(directory, filepath.Base(secondPath))
	if err != nil || position != 1 {
		t.Fatalf("second ticket position = %d, err=%v", position, err)
	}
	if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale ticket remains: %v", err)
	}
}
