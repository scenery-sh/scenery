package main

import (
	"context"
	"errors"
	"io"
	"os"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/storageconfig"
	"scenery.sh/internal/storagefs"
)

// Reject retained recovery before build/setup can mutate a managed database.
func checkStorageStartup(ctx context.Context, root string, cfg appcfg.Config) error {
	plan, err := resolveStorageNamespacePlan(cfg, root, "")
	if err != nil {
		return err
	}
	owner, err := plan.discover(ctx)
	if errors.Is(err, storagefs.ErrUninitialized) {
		if len(cfg.Storage.Stores) > 0 {
			if _, err := os.Lstat(plan.LegacyRoot); err == nil {
				return storagefs.ErrMigration
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	n, err := storagefs.Bind(plan.Root, plan.Binding, owner.Incarnation)
	if err != nil {
		return err
	}
	return n.CheckReady(ctx)
}

func storageTaskLease(ctx context.Context, env []string) (io.Closer, error) {
	raw, ok := storageRuntimeConfigValue(env)
	if !ok {
		return nil, nil
	}
	cfg, _, err := storageconfig.LoadRuntimeConfigValue(raw)
	if err != nil {
		return nil, err
	}
	if cfg.Namespace == nil || !cfg.Namespace.Binding.Managed {
		return nil, nil
	}
	n, err := cfg.Namespace.Handle()
	if err != nil {
		return nil, err
	}
	return n.HoldTask(ctx)
}
