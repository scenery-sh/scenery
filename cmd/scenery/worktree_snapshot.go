package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
)

// Preserve the restore marker throughout engine reconciliation. Even a crash
// after the engine becomes ready cannot make ordinary startup bypass recovery.
type worktreeRestoreOperation struct {
	worktreePostgresOperation
	intent *localagent.WorktreePostgresRestore
}

func (op worktreeRestoreOperation) SaveRecord(record localagent.WorktreeRecord) error {
	if record.Postgres != nil {
		if record.Postgres.Restore == nil {
			record.Postgres.Restore = op.intent
		}
		if record.Postgres.Restore != nil && record.Postgres.Phase == "ready" {
			record.Postgres.Phase = "restoring"
		}
	}
	return op.worktreePostgresOperation.SaveRecord(record)
}

// Caller owns the worktree live lock for the whole load, including storage.
func restoreWorktreeSnapshot(ctx context.Context, appRoot string, cfg appcfg.Config, archive *snapshotArchive, mode string) (returnErr error) {
	resolver, err := newWorktreePostgresResolver(ctx, appRoot, cfg.AppID())
	if err != nil {
		return err
	}
	op, err := resolver.beginOperation()
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, op.Close()) }()
	manifest, err := json.Marshal(archive.manifest)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(manifest)
	archiveID := "sha256:" + hex.EncodeToString(sum[:])
	retainedArchive, err := retainWorktreeSnapshot(resolver.paths, archive, archiveID)
	if err != nil {
		return err
	}
	defer func() { _ = retainedArchive.reader.Close() }()
	archive = retainedArchive
	record, readErr := resolver.readRecord()
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	resuming := readErr == nil && record.Postgres != nil && record.Postgres.Restore != nil
	if resuming {
		pending := record.Postgres.Restore
		if pending.ArchiveSHA256 != archiveID || pending.Mode != mode {
			return worktreePostgresPrecondition("restore recovery requires the same verified archive and mode")
		}
		if mode != "overwrite" && pending.SQLStarted {
			return worktreePostgresPrecondition("an interrupted merge has uncertain commit state; inspect it with native PostgreSQL tools before explicit recovery")
		}
	}
	intent := &localagent.WorktreePostgresRestore{ArchiveSHA256: archiveID, Mode: mode, StartedAt: time.Now().UTC()}
	if resuming {
		intent = record.Postgres.Restore
	}
	restoreOperation := worktreeRestoreOperation{worktreePostgresOperation: op, intent: intent}
	if record.Postgres != nil {
		if err := restoreOperation.SaveRecord(record); err != nil {
			return err
		}
	}
	if mode == "overwrite" {
		// Explicit overwrite from a fully verified, retained archive is the
		// migration authority. It never adopts or rewrites legacy resources.
		resolver.legacyClaim = func() error { return nil }
	}
	server, err := resolver.ensureResourceWithOperation(ctx, restoreOperation, mode == "overwrite", true)
	if err != nil {
		return err
	}
	database, err := databaseForWorktreeServer(resolver.paths.AppRoot, cfg, server)
	if err != nil {
		return err
	}
	if mode == "merge" {
		if err := requireSnapshotSchemas(ctx, database, archive.manifest.DB.Schemas); err != nil {
			return err
		}
	}
	record, err = resolver.load()
	if err != nil {
		return err
	}
	record.Postgres.Phase = "restoring"
	record.Postgres.Restore.SQLStarted = true
	if err := op.SaveRecord(record); err != nil {
		return err
	}
	if err := restoreSnapshotDatabase(ctx, archive, database, mode); err != nil {
		record.Postgres.Phase = "restore-failed"
		return errors.Join(err, op.SaveRecord(record))
	}
	record.Postgres.Phase, record.Postgres.Restore = "ready", nil
	return op.SaveRecord(record)
}

func worktreeRestoreArchivePath(paths localagent.WorktreePaths, archiveID string) string {
	return filepath.Join(paths.Directory, "restore-"+strings.TrimPrefix(archiveID, "sha256:")+".zip")
}

// Keep an independently verified owner-only archive outside the checkout
// before the first destructive SQL statement. Recovery survives Git removal.
func retainWorktreeSnapshot(paths localagent.WorktreePaths, source *snapshotArchive, archiveID string) (*snapshotArchive, error) {
	target := worktreeRestoreArchivePath(paths, archiveID)
	verify := func(path string) (*snapshotArchive, error) {
		archive, err := openSnapshotArchive(path)
		if err != nil {
			return nil, err
		}
		manifest, err := json.Marshal(archive.manifest)
		if err != nil {
			_ = archive.reader.Close()
			return nil, err
		}
		digest := sha256.Sum256(manifest)
		if "sha256:"+hex.EncodeToString(digest[:]) != archiveID {
			_ = archive.reader.Close()
			return nil, worktreePostgresPrecondition("retained restore archive does not match its verified manifest")
		}
		return archive, nil
	}
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return nil, worktreePostgresPrecondition("retained restore archive is not a private regular file")
		}
		return verify(target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	input, err := os.Open(source.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = input.Close() }()
	output, err := os.CreateTemp(paths.Directory, ".restore-archive-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = output.Close(); _ = os.Remove(output.Name()) }()
	if _, err := io.Copy(output, input); err != nil {
		return nil, err
	}
	if err := output.Sync(); err != nil {
		return nil, err
	}
	if err := output.Close(); err != nil {
		return nil, err
	}
	verified, err := verify(output.Name())
	if err != nil {
		return nil, fmt.Errorf("verify retained restore archive: %w", err)
	}
	_ = verified.reader.Close()
	if err := os.Rename(output.Name(), target); err != nil {
		return nil, err
	}
	directory, err := os.Open(paths.Directory)
	if err != nil {
		return nil, err
	}
	err = errors.Join(directory.Sync(), directory.Close())
	if err != nil {
		return nil, err
	}
	return verify(target)
}
