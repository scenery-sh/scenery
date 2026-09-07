package main

import (
	"context"
	"errors"
	"os"
)

func cleanupHarnessWorktreePostgres(ctx context.Context, root, appID string) error {
	paths, err := commandWorktreePaths(root)
	if err != nil {
		return err
	}
	record, err := paths.LoadRecord(appID)
	if errors.Is(err, os.ErrNotExist) || (err == nil && record.Postgres == nil) {
		return nil
	}
	if err != nil {
		return err
	}
	live, err := paths.AcquireLiveLock()
	if err != nil {
		return err
	}
	defer func() { _ = live.Release() }()
	resolver, err := newWorktreePostgresResolver(ctx, root, appID)
	if err != nil {
		return err
	}
	if err := resolver.stop(ctx); err != nil {
		return err
	}
	return resolver.remove(ctx)
}
