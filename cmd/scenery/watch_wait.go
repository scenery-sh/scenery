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

func waitForSnapshotToSettleEvents(ctx context.Context, root string, current fileSnapshot, events <-chan struct{}) (fileSnapshot, error) {
	timer := time.NewTimer(watchSettleDelay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return fileSnapshot{}, ctx.Err()
		case <-timer.C:
			return scanWatchedFilesReusing(root, current)
		case _, ok := <-events:
			if !ok {
				return scanWatchedFilesReusing(root, current)
			}
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
