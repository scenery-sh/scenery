package main

import (
	"context"
	"errors"
	"io"

	"scenery.sh/internal/storagefs"
)

func runStorageCleanup(ctx context.Context, stdout io.Writer, plan *storageNamespacePlan, opts storageCLIOptions) error {
	owner, err := plan.discover(ctx)
	if errors.Is(err, storagefs.ErrUninitialized) && !opts.Yes {
		return writeStorageJSON(stdout, storageCleanupResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.cleanup"), Scope: storageScope(plan, owner, opts), Purge: opts.Purge, DryRun: true})
	}
	if err != nil {
		return err
	}
	namespace, err := storagefs.Bind(plan.Root, plan.Binding, owner.Incarnation)
	if err != nil {
		return err
	}
	if opts.Purge {
		return runStoragePurge(ctx, stdout, plan, namespace, owner, opts)
	}
	response := storageCleanupResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.cleanup"), Scope: storageScope(plan, owner, opts), DryRun: !opts.Yes}
	if !opts.Yes {
		preview, err := namespace.PreviewReclaim(ctx)
		if err != nil {
			return err
		}
		response.Preview = &preview
	} else {
		result, err := namespace.ApplyReclaim(ctx, opts.ExpectRevision)
		if err != nil {
			return err
		}
		response.Result = &result
	}
	return writeStorageJSON(stdout, response)
}

func runStoragePurge(ctx context.Context, stdout io.Writer, plan *storageNamespacePlan, namespace *storagefs.Namespace, owner storagefs.Owner, opts storageCLIOptions) error {
	live, err := plan.Worktree.AcquireExistingLiveLock()
	if err != nil {
		return err
	}
	defer func() { _ = live.Release() }()
	op, err := plan.Worktree.BeginOperation()
	if err != nil {
		return err
	}
	defer func() { _ = op.Close() }()
	// Reload retained root authority under both locks; current configuration
	// or a matching app name cannot grant purge rights over another owner.
	if _, err := plan.Worktree.LoadRecord(plan.Binding.AppID); err != nil {
		return err
	}
	response := storageCleanupResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.cleanup"), Scope: storageScope(plan, owner, opts), Purge: true, DryRun: !opts.Yes}
	if !opts.Yes {
		preview, err := namespace.PreviewPurge(ctx)
		if err != nil {
			return err
		}
		response.PurgePreview = &preview
	} else {
		result, err := namespace.ApplyPurge(ctx, opts.ExpectRevision)
		if err != nil {
			return err
		}
		response.PurgeResult = &result
	}
	return writeStorageJSON(stdout, response)
}
