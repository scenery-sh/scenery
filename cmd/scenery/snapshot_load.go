package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/snapshotarchive"
	"scenery.sh/internal/storagefs"
)

func loadSnapshot(ctx context.Context, appRoot string, cfg appcfg.Config, opts snapshotLoadOptions) (_ snapshotLoadResult, returnErr error) {
	if opts.DB && opts.Storage && opts.Mode == "merge" {
		return snapshotLoadResult{}, fmt.Errorf("combined database/storage merge is not replay-safe")
	}
	plan, err := resolveStorageNamespacePlan(cfg, appRoot, "")
	if err != nil {
		return snapshotLoadResult{}, err
	}
	appRoot = plan.Binding.AppRoot
	input, err := filepath.Abs(opts.Input)
	if err != nil {
		return snapshotLoadResult{}, err
	}
	archive, err := openSnapshotArchivePinned(ctx, input, opts.ExpectSHA256)
	if err != nil {
		return snapshotLoadResult{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, archive.reader.Close()) }()
	if archive.manifest.App.ID != cfg.AppID() {
		return snapshotLoadResult{}, fmt.Errorf("snapshot app id %q does not match %q", archive.manifest.App.ID, cfg.AppID())
	}
	if opts.DB && archive.manifest.DB == nil {
		return snapshotLoadResult{}, fmt.Errorf("snapshot does not contain a database")
	}
	if opts.Storage && archive.manifest.Storage == nil {
		return snapshotLoadResult{}, fmt.Errorf("snapshot does not contain storage")
	}
	if err := rejectSnapshotLiveSession(ctx, appRoot); err != nil {
		return snapshotLoadResult{}, err
	}
	result := snapshotLoadResult{cliPayloadIdentity: newCLIPayloadIdentity("scenery.snapshot.load"), Archive: input, App: snapshotApp(cfg, appRoot), Mode: opts.Mode, DryRun: opts.DryRun}
	var targetSource string
	if opts.DB {
		if err := validateSnapshotSchemas(archive.manifest.DB.Schemas); err != nil {
			return snapshotLoadResult{}, err
		}
		var targetName string
		targetName, targetSource, err = configuredSnapshotDatabaseTarget(appRoot, cfg)
		if err != nil {
			return snapshotLoadResult{}, err
		}
		if opts.Mode == "overwrite" && targetSource == string(postgresdb.SourceExternal) {
			return snapshotLoadResult{}, fmt.Errorf("refusing to overwrite an external postgres database")
		}
		result.DB = &snapshotDBResult{Database: targetName, Source: targetSource, Action: opts.Mode}
	}
	if opts.Storage {
		owner, err := plan.discover(ctx)
		if err != nil && !errors.Is(err, storagefs.ErrUninitialized) {
			return snapshotLoadResult{}, err
		}
		result.Storage = &snapshotStorageResult{Scope: storageScope(plan, owner, storageCLIOptions{}), Stores: len(archive.manifest.Storage.Stores)}
		for _, store := range archive.manifest.Storage.Stores {
			result.Storage.Files += store.Files
			result.Storage.Bytes += store.Bytes
		}
		if err := validateSnapshotStorePolicies(ctx, cfg, archive); err != nil {
			return snapshotLoadResult{}, err
		}
		if opts.Mode == "merge" && owner.Incarnation != "" {
			namespace, err := storagefs.Bind(plan.Root, plan.Binding, owner.Incarnation)
			if err != nil {
				return snapshotLoadResult{}, err
			}
			pending, err := namespace.Pending(ctx)
			if err != nil {
				return snapshotLoadResult{}, err
			}
			if pending == nil {
				conflicts, err := snapshotStorageConflicts(ctx, archive, namespace)
				if err != nil {
					return snapshotLoadResult{}, err
				}
				result.Storage.Conflicts = conflicts
				if opts.OnConflict == "fail" && conflicts > 0 {
					return snapshotLoadResult{}, fmt.Errorf("%w: snapshot merge found %d conflicts; choose --on-conflict skip|overwrite", storagefs.ErrPrecondition, conflicts)
				}
			} else if pending.Operation != "restore" || pending.ArchiveSHA256 != archive.reader.SHA256 || pending.Database || pending.Mode != opts.Mode {
				return snapshotLoadResult{}, storagefs.ErrRecovery
			}
		}
	}
	if opts.DryRun {
		return result, nil
	}
	live, err := plan.Worktree.AcquireLiveLock()
	if err != nil {
		return snapshotLoadResult{}, fmt.Errorf("snapshot load requires a stopped worktree owner: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, live.Release()) }()
	op, err := plan.Worktree.BeginOperation()
	if err != nil {
		return snapshotLoadResult{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, op.Close()) }()
	if !opts.Storage {
		if err := rejectSnapshotStorageRecovery(ctx, plan); err != nil {
			return snapshotLoadResult{}, err
		}
		if targetSource == string(postgresdb.SourceManaged) {
			err = restoreWorktreeSnapshotHeld(ctx, appRoot, cfg, archive, opts.Mode, op)
		} else {
			var database postgresdb.Database
			database, err = resolveSnapshotDatabase(ctx, appRoot, cfg)
			if err == nil {
				err = requireSnapshotSchemas(ctx, database, archive.manifest.DB.Schemas)
			}
			if err == nil {
				err = restoreSnapshotDatabase(ctx, archive, database, opts.Mode)
			}
		}
		return result, err
	}
	materialized, err := archive.reader.Materialize(ctx)
	if err != nil {
		return snapshotLoadResult{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, materialized.Close()) }()
	owner, err := plan.discover(ctx)
	var namespace *storagefs.Namespace
	if errors.Is(err, storagefs.ErrUninitialized) {
		namespace, err = plan.allocateHeld(ctx, op)
	} else if err == nil {
		namespace, err = storagefs.Bind(plan.Root, plan.Binding, owner.Incarnation)
	}
	if err != nil {
		return snapshotLoadResult{}, err
	}
	conflict := "fail"
	if opts.Mode == "merge" && opts.OnConflict != "" {
		conflict = opts.OnConflict
	}
	restore, err := namespace.BeginRestore(ctx, archive.reader.SHA256, opts.DB, opts.Mode, conflict)
	if err != nil {
		return snapshotLoadResult{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, restore.Close()) }()
	if err := restore.Stage(ctx, func(g *storagefs.Generation) error {
		for _, store := range archive.manifest.Storage.Stores {
			if err := materialized.VisitStore(ctx, store.Name, func(object snapshotarchive.Object, body io.Reader) error {
				return g.Put(ctx, object.StorageObject(), body, conflict)
			}); err != nil {
				return err
			}
		}
		result.Storage.Conflicts = g.Conflicts
		result.Storage.Skipped = g.Skipped
		result.Storage.Overwritten = g.Overwritten
		result.Storage.Cloned = g.Cloned
		result.Storage.Copied = g.Copied
		return nil
	}); err != nil {
		return snapshotLoadResult{}, err
	}
	var restoreDB func(context.Context) error
	if opts.DB {
		restoreDB = func(ctx context.Context) error {
			return restoreWorktreeSnapshotHeld(ctx, appRoot, cfg, archive, opts.Mode, op)
		}
	}
	if err := restore.Complete(ctx, restoreDB); err != nil {
		return snapshotLoadResult{}, fmt.Errorf("resume snapshot load with --input %q --expect-sha256 %s --mode %s and the same --db/--storage selection: %w", input, archive.reader.SHA256, opts.Mode, err)
	}
	// The maintenance lease is still held. Its confirmed owner is the result's
	// generation; do not re-enter discovery while holding the exclusive lease.
	result.Storage.Scope = storageScope(plan, restore.Owner(), storageCLIOptions{})
	return result, nil
}

// Caller holds live and operation ownership, so no storage lifecycle operation
// can start between this check and SQL mutation. Only matching combined resume
// may finish a pending restore, including one whose generation already switched.
func rejectSnapshotStorageRecovery(ctx context.Context, plan *storageNamespacePlan) error {
	owner, err := plan.discover(ctx)
	if errors.Is(err, storagefs.ErrUninitialized) {
		return nil
	}
	if err != nil {
		return err
	}
	namespace, err := storagefs.Bind(plan.Root, plan.Binding, owner.Incarnation)
	if err != nil {
		return err
	}
	pending, err := namespace.Pending(ctx)
	if err != nil {
		return err
	}
	if pending != nil {
		return &storagefs.RecoveryError{Info: pending.Recovery()}
	}
	return nil
}

func validateSnapshotStorePolicies(ctx context.Context, cfg appcfg.Config, archive *snapshotArchive) error {
	for _, store := range archive.manifest.Storage.Stores {
		policy, ok := cfg.Storage.Stores[store.Name]
		if !ok {
			return fmt.Errorf("snapshot store %q is not configured", store.Name)
		}
		if err := archive.reader.VisitStore(ctx, store.Name, func(object snapshotarchive.Object, _ io.Reader) error {
			if policy.TenantScoped != (object.Tenant != "") {
				return fmt.Errorf("snapshot object tenant does not match store %q policy", store.Name)
			}
			if policy.MaxObjectBytes > 0 && object.SizeBytes > policy.MaxObjectBytes {
				return fmt.Errorf("snapshot object exceeds store %q size limit", store.Name)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func snapshotStorageConflicts(ctx context.Context, archive *snapshotArchive, namespace *storagefs.Namespace) (int64, error) {
	var conflicts int64
	for _, store := range archive.manifest.Storage.Stores {
		if err := archive.reader.VisitStore(ctx, store.Name, func(object snapshotarchive.Object, _ io.Reader) error {
			s, err := namespace.Store(storagefs.Scope{Store: store.Name, Tenant: object.Tenant}, 0)
			if err != nil {
				return err
			}
			_, err = s.Head(ctx, object.Key)
			if errors.Is(err, storagefs.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			conflicts++
			return nil
		}); err != nil {
			return 0, err
		}
	}
	return conflicts, nil
}
