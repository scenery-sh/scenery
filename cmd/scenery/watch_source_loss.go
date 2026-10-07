package main

import (
	"context"
	"fmt"
	"time"
)

type watchSourceOwnerKey struct{}

// A missing source tree can be an atomic save. Give it one backup-poll interval
// to return, then let normal owner cleanup stop runtime children. Retained
// database/storage authority is outside that process-lifetime decision.
func requireWatchSource(ctx context.Context, root string) error {
	_, source := worktreeSourceStatus(root)
	if source != "missing" {
		return nil
	}
	timer := time.NewTimer(watchBackupPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	_, source = worktreeSourceStatus(root)
	if source == "missing" {
		return &watchSourceLostError{Root: root}
	}
	return nil
}

type watchSourceLostError struct{ Root string }

func (e *watchSourceLostError) Error() string {
	return fmt.Sprintf("failed_precondition: source_lost: %s no longer contains app.scn; stopping its runtime owner and children while retaining database and storage", e.Root)
}

func (*watchSourceLostError) ExitCode() int { return 3 }
