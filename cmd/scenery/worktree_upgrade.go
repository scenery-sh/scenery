package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/spec"
	"scenery.sh/internal/stateupgrade"
	"scenery.sh/internal/storagefs"
)

const worktreeUpgradeKind = "scenery.worktree.upgrade"

type worktreeUpgradeResult struct {
	cliPayloadIdentity
	OK                 bool   `json:"ok"`
	AppRoot            string `json:"app_root"`
	WorktreeKey        string `json:"worktree_key"`
	TargetSpecRevision string `json:"target_spec_revision"`
	Revision           string `json:"revision"`
	MetadataFiles      int    `json:"metadata_files"`
	ChangedFiles       int    `json:"changed_files"`
	UpdatedFiles       int    `json:"updated_files"`
	Pending            bool   `json:"pending"`
	Applied            bool   `json:"applied"`
	Backup             string `json:"backup,omitempty"`
}

func runWorktreeUpgrade(ctx context.Context, stdout io.Writer, opts worktreeOptions) error {
	root, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	paths, err := commandWorktreePaths(root)
	if err != nil {
		return err
	}
	result, err := upgradeRetainedWorktree(ctx, paths, cfg.AppID(), opts)
	if err != nil {
		return worktreePostgresPrecondition("retained-state upgrade: " + err.Error())
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	action := "preview"
	if result.Applied {
		action = "applied"
	}
	_, err = fmt.Fprintf(stdout, "%s: %d of %d metadata files require an identity upgrade\nrevision: %s\n", action, result.ChangedFiles, result.MetadataFiles, result.Revision)
	return err
}

func upgradeRetainedWorktree(ctx context.Context, paths localagent.WorktreePaths, appID string, opts worktreeOptions) (worktreeUpgradeResult, error) {
	var result worktreeUpgradeResult
	store, err := stateupgrade.Open(paths.Directory)
	if err != nil {
		return result, err
	}
	defer func() { _ = store.Close() }()
	live, err := paths.AcquireExistingLiveLock()
	if err != nil {
		return result, err
	}
	defer func() { _ = live.Release() }()
	operation, err := paths.AcquireExistingOperationLock()
	if err != nil {
		return result, err
	}
	defer func() { _ = operation.Release() }()
	record, changes, err := paths.PrepareSpecUpgrade(appID)
	if err != nil {
		return result, err
	}
	if record.Postgres != nil && record.Postgres.Major != 18 {
		return result, fmt.Errorf("the retained PostgreSQL major requires an explicit validated engine migration")
	}
	storagePath := filepath.Join(paths.Directory, "storage")
	if _, err := os.Lstat(storagePath); err == nil {
		// A stopped owner's maintenance lease should be free. Bound refusal of
		// an unexpected external holder; do not wait indefinitely or take over.
		leaseCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		binding := storagefs.Binding{AppID: record.AppID, AppRoot: paths.AppRoot, WorktreeKey: paths.Key, UserID: record.UserID, Managed: true}
		storage, err := storagefs.PrepareSpecUpgrade(leaseCtx, storagePath, binding)
		if err != nil {
			return result, err
		}
		defer func() { _ = storage.Close() }()
		changes = append(changes, storage.Changes...)
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	plan, err := store.Resolve(changes)
	if err != nil {
		return result, err
	}
	result = worktreeUpgradeResult{
		cliPayloadIdentity: newCLIPayloadIdentity(worktreeUpgradeKind), OK: true,
		AppRoot: paths.AppRoot, WorktreeKey: paths.Key, TargetSpecRevision: string(spec.CurrentRevision()),
		Revision: plan.Revision, MetadataFiles: len(plan.Changes), ChangedFiles: plan.ChangedFiles(), UpdatedFiles: plan.Updated, Pending: plan.Pending,
	}
	if opts.Yes {
		result.Backup, err = store.Apply(plan, opts.ExpectedRevision)
		if err != nil {
			return worktreeUpgradeResult{}, err
		}
		result.Applied, result.Pending, result.UpdatedFiles = true, false, result.ChangedFiles
	}
	return result, nil
}
