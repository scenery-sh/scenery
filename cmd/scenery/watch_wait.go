package main

import (
	"context"
	"time"
)

func waitForStableChange(ctx context.Context, root string, current fileSnapshot, watcher *fileChangeWatcher, wake <-chan struct{}) (fileSnapshot, bool, error) {
	if watcher != nil {
		return waitForStableChangeEvents(ctx, root, current, watcher.Events(), wake)
	}
	return waitForStableChangePolling(ctx, root, current, wake)
}

func waitForStableChangePolling(ctx context.Context, root string, current fileSnapshot, wake <-chan struct{}) (fileSnapshot, bool, error) {
	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fileSnapshot{}, false, ctx.Err()
		case <-wake:
			next, err := scanWatchedFilesReusing(root, current)
			return next, true, err
		case <-ticker.C:
		}

		next, err := scanWatchedFilesReusing(root, current)
		if err != nil {
			return fileSnapshot{}, false, err
		}
		if snapshotsEqual(current, next) {
			current = next
			continue
		}
		settled, err := waitForSnapshotToSettlePolling(ctx, root, next)
		return settled, false, err
	}
}

func waitForStableChangeEvents(ctx context.Context, root string, current fileSnapshot, events <-chan struct{}, wake <-chan struct{}) (fileSnapshot, bool, error) {
	ticker := time.NewTicker(watchBackupPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fileSnapshot{}, false, ctx.Err()
		case <-wake:
			next, err := scanWatchedFilesReusing(root, current)
			return next, true, err
		case _, ok := <-events:
			if !ok {
				return waitForStableChangePolling(ctx, root, current, wake)
			}
		case <-ticker.C:
			next, err := scanWatchedFilesReusing(root, current)
			if err != nil {
				return fileSnapshot{}, false, err
			}
			if snapshotsEqual(current, next) {
				current = next
				continue
			}
			settled, err := waitForSnapshotToSettlePolling(ctx, root, next)
			return settled, false, err
		}

		next, err := waitForSnapshotToSettleEvents(ctx, root, current, events)
		if err != nil {
			return fileSnapshot{}, false, err
		}
		if snapshotsEqual(current, next) {
			current = next
			continue
		}
		return next, false, nil
	}
}

func waitForSnapshotToSettlePolling(ctx context.Context, root string, current fileSnapshot) (fileSnapshot, error) {
	timer := time.NewTimer(watchSettleDelay)
	defer timer.Stop()
	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fileSnapshot{}, ctx.Err()
		case <-timer.C:
			// Settling may be shorter than the polling interval. Verify the
			// final quiet boundary instead of returning the first observed save.
			next, err := scanWatchedFilesReusing(root, current)
			if err != nil {
				return fileSnapshot{}, err
			}
			if snapshotsEqual(current, next) {
				return next, nil
			}
			current = next
			timer.Reset(watchSettleDelay)
		case <-ticker.C:
			next, err := scanWatchedFilesReusing(root, current)
			if err != nil {
				return fileSnapshot{}, err
			}
			if snapshotsEqual(current, next) {
				current = next
				continue
			}
			current = next
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(watchSettleDelay)
		}
	}
}

// waitForSnapshotToSettleEvents returns a scan of a tree that no event disturbed
// for watchSettleDelay. The scan runs inside that quiet window instead of after
// it: it starts watchScanLead after the last event, and an event that arrives
// before the window closes discards it. The returned snapshot was therefore
// read in a period the window proved quiet, and at most one scan is in flight.
func waitForSnapshotToSettleEvents(ctx context.Context, root string, current fileSnapshot, events <-chan struct{}) (fileSnapshot, error) {
	type scanned struct {
		snapshot fileSnapshot
		err      error
	}
	quiet := time.NewTimer(watchSettleDelay)
	defer quiet.Stop()
	scanLead := min(watchScanLead, watchSettleDelay)
	lead := time.NewTimer(scanLead)
	defer lead.Stop()
	var (
		results  chan scanned // non-nil while a scan runs
		finished *scanned     // the scan no event disturbed
		stale    bool         // an event arrived after the running scan began
		due      bool         // the lead elapsed while a stale scan was running
		settled  bool         // the quiet window closed
	)
	scan := func() {
		results = make(chan scanned, 1)
		stale, due = false, false
		go func(out chan<- scanned) {
			snapshot, err := scanWatchedFilesReusing(root, current)
			out <- scanned{snapshot: snapshot, err: err}
		}(results)
	}
	join := func() {
		if results != nil {
			<-results
		}
	}
	for {
		select {
		case <-ctx.Done():
			join()
			return fileSnapshot{}, ctx.Err()
		case _, ok := <-events:
			if !ok {
				join()
				return scanWatchedFilesReusing(root, current)
			}
			stale, due, settled, finished = true, false, false, nil
			quiet.Reset(watchSettleDelay)
			lead.Reset(scanLead)
		case <-lead.C:
			if results == nil {
				scan()
			} else {
				due = true
			}
		case result := <-results:
			results = nil
			switch {
			case stale && due:
				scan()
			case stale:
			case settled:
				return result.snapshot, result.err
			default:
				finished = &result
			}
		case <-quiet.C:
			settled = true
			if finished != nil {
				return finished.snapshot, finished.err
			}
		}
	}
}
