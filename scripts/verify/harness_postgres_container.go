package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	localagent "scenery.sh/internal/agent"
)

func cleanupHarnessWorktreePostgres(ctx context.Context, root, appID string) error {
	home, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	paths, err := localagent.PathsForWorktree(home.Home, root)
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
	repoRoot, err := discoverSceneryRepoRoot("")
	if err != nil {
		return err
	}
	if err := runProduct(ctx, repoRoot, io.Discard, "down", "--app-root", root, "-o", "json"); err != nil {
		return err
	}
	if err := runProduct(ctx, repoRoot, io.Discard, "prune", "--app-root", root, "--db", "--older-than", "1ns", "-o", "json"); err != nil {
		return err
	}
	after, err := paths.LoadRecord(appID)
	if err != nil {
		return err
	}
	if after.Postgres != nil {
		return fmt.Errorf("owned PostgreSQL authority remains after public prune: %s", root)
	}
	return nil
}
