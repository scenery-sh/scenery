package main

import (
	"context"
	"time"

	"scenery.sh/internal/workspacetx"
)

type workspaceWaitObserverKey struct{}

// waitForWorkspaceRead preserves the compiler's transaction guard and coalesces
// edits while their writer is live. Invalid or abandoned-state errors retain
// their original classification; only a live transaction is waited out.
func waitForWorkspaceRead(ctx context.Context, root string) error {
	return waitForWorkspaceReadWith(ctx, func() error {
		return workspacetx.RecoverOrReject(root, workspacetx.NormalRead)
	}, watchPollInterval)
}

func waitForWorkspaceReadWith(ctx context.Context, read func() error, interval time.Duration) error {
	deferred := false
	observer, _ := ctx.Value(workspaceWaitObserverKey{}).(func(string, string))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := read()
		if !workspacetx.IsActive(err) {
			if deferred && observer != nil {
				observer("build.resumed", "workspace transaction released; rescan latest source")
			}
			return err
		}
		if !deferred && observer != nil {
			observer("build.deferred", err.Error())
		}
		deferred = true
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func scanWatchAfterTransaction(ctx context.Context, root string, previous fileSnapshot) (fileSnapshot, error) {
	for {
		if err := waitForWorkspaceRead(ctx, root); err != nil {
			return fileSnapshot{}, err
		}
		if owned, _ := ctx.Value(watchSourceOwnerKey{}).(bool); owned {
			if err := requireWatchSource(ctx, root); err != nil {
				return fileSnapshot{}, err
			}
		}
		next, err := scanWatchedFilesReusing(root, previous)
		if !workspacetx.IsActive(err) {
			return next, err
		}
	}
}

func rebuildAfterTransaction(ctx context.Context, s *devSupervisor, initial bool, snapshot *fileSnapshot) error {
	for {
		if err := waitForWorkspaceRead(ctx, s.root); err != nil {
			return err
		}
		err := s.RebuildAndRestart(ctx, initial, snapshot)
		if !workspacetx.IsActive(err) {
			return err
		}
		// A writer may have started after the previous scan. Its partial bytes
		// must not become the next candidate, even if it produces no watch event.
		next, scanErr := scanWatchAfterTransaction(ctx, s.root, *snapshot)
		if scanErr != nil {
			return scanErr
		}
		*snapshot = next
	}
}
