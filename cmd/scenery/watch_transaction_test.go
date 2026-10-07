package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"scenery.sh/internal/workspacetx"
)

func TestWorkspaceWaitCoalescesLiveTransactionAndPreservesFailures(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, ".scenery", "transactions", "change-test")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, _ := workspacetx.NewArtifacts(dir, "")
	encoded, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "change.lock"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	active := workspacetx.RecoverOrReject(root, workspacetx.NormalRead)
	if !workspacetx.IsActive(active) {
		t.Fatalf("live transaction = %v", active)
	}
	var events []string
	ctx := context.WithValue(t.Context(), workspaceWaitObserverKey{}, func(event, _ string) { events = append(events, event) })
	reads := 0
	err = waitForWorkspaceReadWith(ctx, func() error {
		reads++
		if reads < 3 {
			return active
		}
		return nil
	}, time.Millisecond)
	if err != nil || !slices.Equal(events, []string{"build.deferred", "build.resumed"}) {
		t.Fatalf("wait = %v, events %v", err, events)
	}
	invalid := errors.New("failed_precondition: invalid transaction journal")
	if got := waitForWorkspaceReadWith(t.Context(), func() error { return invalid }, time.Millisecond); !errors.Is(got, invalid) {
		t.Fatalf("invalid journal was not preserved: %v", got)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if got := waitForWorkspaceReadWith(canceled, func() error { t.Fatal("read after cancellation"); return nil }, time.Millisecond); !errors.Is(got, context.Canceled) {
		t.Fatalf("cancellation = %v", got)
	}
}
