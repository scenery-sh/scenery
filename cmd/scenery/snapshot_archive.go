package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/snapshotarchive"
	"scenery.sh/internal/storagefs"
)

type snapshotArchive struct {
	path     string
	reader   *snapshotarchive.Reader
	manifest snapshotManifest
}

var snapshotNow = time.Now

func openSnapshotArchivePinned(ctx context.Context, path, expected string) (*snapshotArchive, error) {
	r, err := snapshotarchive.Open(ctx, path, expected)
	if err != nil {
		return nil, err
	}
	return &snapshotArchive{path: path, reader: r, manifest: r.Manifest}, nil
}

func saveSnapshot(ctx context.Context, appRoot string, cfg appcfg.Config, opts snapshotSaveOptions) (_ snapshotSaveResult, returnErr error) {
	plan, err := resolveStorageNamespacePlan(cfg, appRoot, "")
	if err != nil {
		return snapshotSaveResult{}, err
	}
	appRoot = plan.Binding.AppRoot
	manifest := snapshotManifest{CreatedAt: snapshotNow().UTC(), App: snapshotManifestApp{Name: cfg.Name, ID: cfg.AppID()}}
	result := snapshotSaveResult{cliPayloadIdentity: newCLIPayloadIdentity("scenery.snapshot.save"), App: snapshotApp(cfg, appRoot)}
	var operation *localagent.WorktreeOperation
	var capture *storagefs.Capture
	if opts.Storage {
		if len(cfg.Storage.Stores) == 0 {
			return snapshotSaveResult{}, fmt.Errorf("snapshot save --storage requires configured stores")
		}
		if opts.DB {
			_, source, err := configuredSnapshotDatabaseTarget(appRoot, cfg)
			if err != nil {
				return snapshotSaveResult{}, err
			}
			if source != string(postgresdb.SourceManaged) {
				return snapshotSaveResult{}, fmt.Errorf("combined capture requires a managed worktree database; external writers cannot be excluded")
			}
		}
		live, err := plan.Worktree.AcquireLiveLock()
		if err != nil {
			if errors.Is(err, localagent.ErrProcessLocked) {
				return snapshotSaveResult{}, fmt.Errorf("%w: storage capture requires a stopped runtime; run scenery down first", storagefs.ErrPrecondition)
			}
			return snapshotSaveResult{}, fmt.Errorf("storage capture requires a stopped runtime; run scenery down first: %w", err)
		}
		defer func() { returnErr = errors.Join(returnErr, live.Release()) }()
		operation, err = plan.Worktree.BeginOperation()
		if err != nil {
			if errors.Is(err, localagent.ErrProcessLocked) {
				return snapshotSaveResult{}, fmt.Errorf("%w: another managed operation is active", storagefs.ErrPrecondition)
			}
			return snapshotSaveResult{}, err
		}
		defer func() { returnErr = errors.Join(returnErr, operation.Close()) }()
		owner, err := plan.discover(ctx)
		var namespace *storagefs.Namespace
		if errors.Is(err, storagefs.ErrUninitialized) {
			if _, err := os.Lstat(plan.LegacyRoot); err == nil {
				return snapshotSaveResult{}, storagefs.ErrMigration
			} else if !errors.Is(err, os.ErrNotExist) {
				return snapshotSaveResult{}, err
			}
			namespace, err = plan.allocateHeld(ctx, operation)
		} else if err == nil {
			namespace, err = storagefs.Bind(plan.Root, plan.Binding, owner.Incarnation)
		}
		if err != nil {
			return snapshotSaveResult{}, err
		}
		capture, err = namespace.Capture(ctx)
		if err != nil {
			return snapshotSaveResult{}, err
		}
		defer func() { returnErr = errors.Join(returnErr, capture.Close()) }()
		manifest.Storage = &snapshotManifestStorage{Provenance: &snapshotarchive.Provenance{AppRoot: appRoot, WorktreeKey: plan.Worktree.Key}}
		result.Storage = &snapshotStorageResult{Scope: storageScope(plan, capture.Owner(), storageCLIOptions{})}
	}
	var database postgresdb.Database
	var pgRunner snapshotPostgresRunner
	if opts.DB {
		if opts.Storage {
			database, err = resolveCaptureDatabaseHeld(ctx, appRoot, cfg, operation)
		} else {
			_, source, sourceErr := configuredSnapshotDatabaseTarget(appRoot, cfg)
			if sourceErr != nil {
				return snapshotSaveResult{}, sourceErr
			}
			if source == string(postgresdb.SourceManaged) {
				if _, err := plan.Worktree.LoadRecord(cfg.AppID()); err != nil {
					return snapshotSaveResult{}, err
				}
				op, err := plan.Worktree.BeginOperation()
				if err != nil {
					return snapshotSaveResult{}, err
				}
				defer func() { returnErr = errors.Join(returnErr, op.Close()) }()
			}
			database, err = resolveSnapshotDatabase(ctx, appRoot, cfg)
		}
		if err != nil {
			return snapshotSaveResult{}, err
		}
		pgRunner, err = snapshotPostgresRunnerFor(database)
		if err != nil {
			return snapshotSaveResult{}, err
		}
		manifest.DB = &snapshotManifestDB{Database: database.Database, Source: string(database.Source), Schemas: snapshotSchemas(database), DumpFile: "db/database.postgres.dump", DumpFormat: "pg_custom"}
		result.DB = &snapshotDBResult{Database: database.Database, Source: string(database.Source), Action: "saved"}
	}
	output, err := storageOutputPath(plan, opts.Output)
	if err != nil {
		return snapshotSaveResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return snapshotSaveResult{}, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".scenery-snapshot-*")
	if err != nil {
		return snapshotSaveResult{}, err
	}
	defer func() {
		_ = temporary.Close()
		if err := os.Remove(temporary.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	writer := snapshotarchive.NewWriter(temporary, manifest)
	if opts.DB {
		if err := writer.AddDatabase(ctx, func(w io.Writer) error { return pgRunner.Dump(ctx, database, w) }); err != nil {
			return snapshotSaveResult{}, err
		}
	}
	if opts.Storage {
		stores := make([]string, 0, len(cfg.Storage.Stores))
		for name := range cfg.Storage.Stores {
			stores = append(stores, name)
		}
		sort.Strings(stores)
		for _, name := range stores {
			record, err := writer.AddStore(ctx, filepath.Dir(output), name, func(visit func(snapshotarchive.Object, io.Reader) error) error {
				return capture.Visit(ctx, name, func(object storagefs.Object, body io.Reader) error {
					return visit(snapshotarchive.Logical(object), body)
				})
			})
			if err != nil {
				return snapshotSaveResult{}, err
			}
			result.Storage.Stores++
			result.Storage.Files += record.Files
			result.Storage.Bytes += record.Bytes
		}
	}
	if err := writer.Close(); err != nil {
		return snapshotSaveResult{}, err
	}
	if err := temporary.Sync(); err != nil {
		return snapshotSaveResult{}, err
	}
	if err := temporary.Close(); err != nil {
		return snapshotSaveResult{}, err
	}
	verified, err := verifySnapshotPinned(ctx, temporary.Name(), "")
	if err != nil {
		return snapshotSaveResult{}, err
	}
	if err := os.Rename(temporary.Name(), output); err != nil {
		return snapshotSaveResult{}, err
	}
	if err := syncSnapshotDirectory(filepath.Dir(output)); err != nil {
		return snapshotSaveResult{}, err
	}
	result.Archive = output
	result.Files = verified.Files
	result.Bytes = verified.Bytes
	return result, nil
}

func verifySnapshot(path string) (snapshotVerifyResult, error) {
	return verifySnapshotPinned(context.Background(), path, "")
}
func verifySnapshotPinned(ctx context.Context, path, expected string) (snapshotVerifyResult, error) {
	input, err := filepath.Abs(path)
	if err != nil {
		return snapshotVerifyResult{}, err
	}
	archive, err := openSnapshotArchivePinned(ctx, input, expected)
	if err != nil {
		return snapshotVerifyResult{}, err
	}
	defer func() { _ = archive.reader.Close() }()
	result := snapshotVerifyResult{cliPayloadIdentity: newCLIPayloadIdentity("scenery.snapshot.verify"), Archive: input, App: archive.manifest.App, CreatedAt: archive.manifest.CreatedAt, DB: archive.manifest.DB != nil, Storage: archive.manifest.Storage != nil, SHA256: archive.reader.SHA256}
	for _, file := range archive.manifest.Files {
		result.Files++
		result.Bytes += file.Bytes
	}
	if archive.manifest.Storage != nil {
		for _, store := range archive.manifest.Storage.Stores {
			result.Files += store.Files
			result.Bytes += store.Bytes
		}
	}
	return result, nil
}

func syncSnapshotDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
