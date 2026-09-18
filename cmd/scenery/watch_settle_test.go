package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestWatchPollingChecksChangesBeforeLongerPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		writeTestAppFile(t, root, "main.go", "package main\nconst value = 1\n")
		first, err := scanWatchedFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(watchSettleDelay / 2)
			writeTestAppFile(t, root, "main.go", "package main\nconst value = 1234\n")
		}()
		started := time.Now()
		settled, err := waitForSnapshotToSettlePolling(context.Background(), root, first)
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(started) != 2*watchSettleDelay || !slices.Equal(changedPaths(first, settled), []string{"main.go"}) {
			t.Fatalf("polling returned before the final save settled: elapsed=%v paths=%v", time.Since(started), changedPaths(first, settled))
		}
	})
}

func TestWatchEventsCoalesceAtomicAndMultiFileSave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		writeTestAppFile(t, root, "main.go", "package main\n")
		first, err := scanWatchedFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		events := make(chan struct{})
		go func() {
			time.Sleep(20 * time.Millisecond)
			writeTestAppFile(t, root, ".save.tmp", "package main\nconst value = 1\n")
			if err := os.Rename(filepath.Join(root, ".save.tmp"), filepath.Join(root, "main.go")); err != nil {
				t.Error(err)
			}
			events <- struct{}{}
			time.Sleep(20 * time.Millisecond)
			writeTestAppFile(t, root, "shared.go", "package main\ntype Shared string\n")
			events <- struct{}{}
		}()
		started := time.Now()
		settled, err := waitForSnapshotToSettleEvents(context.Background(), root, first, events)
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(started) != watchSettleDelay+40*time.Millisecond || !slices.Equal(changedPaths(first, settled), []string{"main.go", "shared.go"}) {
			t.Fatalf("save batch was not coalesced: elapsed=%v paths=%v", time.Since(started), changedPaths(first, settled))
		}
	})
}

func TestWatchEventsDiscardTheScanAnEventDisturbed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		writeTestAppFile(t, root, "main.go", "package main\n")
		first, err := scanWatchedFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		events := make(chan struct{})
		disturbance := watchScanLead + (watchSettleDelay-watchScanLead)/2
		go func() {
			// The scan of the quiet window already ran; this save is after it.
			time.Sleep(disturbance)
			writeTestAppFile(t, root, "shared.go", "package main\ntype Shared string\n")
			events <- struct{}{}
		}()
		started := time.Now()
		settled, err := waitForSnapshotToSettleEvents(context.Background(), root, first, events)
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(started) != disturbance+watchSettleDelay || !slices.Equal(changedPaths(first, settled), []string{"shared.go"}) {
			t.Fatalf("a disturbed scan was returned: elapsed=%v paths=%v", time.Since(started), changedPaths(first, settled))
		}
	})
}

func TestWatchEventsReturnTheUndisturbedScanWhenTheWindowCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		writeTestAppFile(t, root, "main.go", "package main\n")
		first, err := scanWatchedFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		writeTestAppFile(t, root, "main.go", "package main\nconst value = 1\n")
		started := time.Now()
		settled, err := waitForSnapshotToSettleEvents(context.Background(), root, first, make(chan struct{}))
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(started) != watchSettleDelay || !slices.Equal(changedPaths(first, settled), []string{"main.go"}) {
			t.Fatalf("the quiet window did not return its scan: elapsed=%v paths=%v", time.Since(started), changedPaths(first, settled))
		}
	})
}
