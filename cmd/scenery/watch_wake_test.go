package main

import (
	"context"
	"testing"
	"time"
)

// A wake on the rebuild-request channel must end the watch wait with
// forced=true even though no watched file changed: the requester fixed
// build inputs the watcher cannot see (e.g. a ui catalog sync).
func TestWaitForStableChangePollingWakes(t *testing.T) {
	root := t.TempDir()
	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	next, forced, err := waitForStableChangePolling(ctx, root, snapshot, wake)
	if err != nil {
		t.Fatal(err)
	}
	if !forced {
		t.Fatal("wake did not force a rebuild")
	}
	if !snapshotsEqual(snapshot, next) {
		t.Fatal("wake with no file changes should return an equal snapshot")
	}
}

// RequestRebuildIfBuildFailed is a no-op on a healthy session and coalesces
// repeated requests into one pending wake on a failed one.
func TestRequestRebuildIfBuildFailedGatesOnFailure(t *testing.T) {
	s := &devSupervisor{rebuildRequests: make(chan struct{}, 1)}

	s.RequestRebuildIfBuildFailed()
	select {
	case <-s.rebuildRequestChan():
		t.Fatal("healthy session must not request a rebuild")
	default:
	}

	s.mu.Lock()
	s.buildFailed = true
	s.mu.Unlock()
	s.RequestRebuildIfBuildFailed()
	s.RequestRebuildIfBuildFailed()
	select {
	case <-s.rebuildRequestChan():
	default:
		t.Fatal("failed session did not request a rebuild")
	}
	select {
	case <-s.rebuildRequestChan():
		t.Fatal("requests must coalesce into a single pending wake")
	default:
	}
}
