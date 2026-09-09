package storagefs

import (
	"context"
	"errors"
	"fmt"
	"os"

	"scenery.sh/internal/machine"
)

// The preparation record authorizes rebuilding exactly one uncommitted target
// after a crash, including a crash during creation of its directory structure.
// A retired owner remains retired until explicit overwrite is fully complete.
func (r *Restore) stageGeneration(ctx context.Context, populate func(*Generation) error) error {
	if r.operation == nil {
		if r.lease.owner.State == "retired" {
			if err := retiredGenerationsEmpty(ctx, r.lease.root); err != nil {
				return err
			}
		}
		generation, err := randomID()
		if err != nil {
			return err
		}
		incarnation := ""
		if r.lease.owner.State == "retired" {
			incarnation, err = randomID()
			if err != nil {
				return err
			}
		}
		owner := r.lease.owner
		op := &Operation{ArtifactIdentity: machine.NewArtifactIdentity(operationKind, operationDescriptor), Binding: owner.Binding, Operation: "restore", Incarnation: owner.Incarnation, Generation: owner.Generation, ArchiveSHA256: r.digest, StagedGeneration: generation, StagedIncarnation: incarnation, Database: r.database, Storage: true, Mode: r.mode, OnConflict: r.conflict, Phase: "preparing"}
		if err := r.namespace.io.writeRecord(r.lease.root, "operation.json", op); err != nil {
			return err
		}
		r.operation = op
	}
	op := r.operation
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.lease.root.Mkdir("generations", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := checkDirectory(r.lease.root, "generations"); err != nil {
		return err
	}
	if err := r.namespace.io.syncDirectory(r.lease.root, "."); err != nil {
		return err
	}
	// No irreversible database work may occur in preparing. A recorded private
	// generation can therefore be discarded and rebuilt from the same archive.
	if err := r.lease.root.RemoveAll(generationPath(op.StagedGeneration)); err != nil {
		return err
	}
	if err := r.namespace.io.syncDirectory(r.lease.root, "generations"); err != nil {
		return err
	}
	owner := r.lease.owner
	owner.Generation = op.StagedGeneration
	if op.StagedIncarnation != "" {
		owner.Incarnation = op.StagedIncarnation
	}
	if err := createGeneration(r.namespace.io, r.lease.root, owner); err != nil {
		return err
	}
	stage := &Generation{namespace: r.namespace, lease: r.lease, id: op.StagedGeneration}
	if r.mode == "merge" {
		if err := scanAllReferences(ctx, r.lease, func(ref reference) error {
			scope := Scope{Store: ref.Object.Store, Tenant: ref.Object.Tenant}
			f, err := openOwned(r.lease.root, scope.versionPath(r.lease.owner.Generation, ref.Object.Key, ref.VersionID), os.O_RDONLY)
			if err != nil {
				return err
			}
			err = stage.Put(ctx, ref.Object, f, "fail")
			return errors.Join(err, f.Close())
		}); err != nil {
			return err
		}
	}
	if err := populate(stage); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.namespace.io.syncDirectory(r.lease.root, generationPath(op.StagedGeneration)); err != nil {
		return err
	}
	op.Phase = "staged"
	if err := r.namespace.io.writeRecord(r.lease.root, "operation.json", op); err != nil {
		op.Phase = "preparing"
		return err
	}
	return nil
}

func retiredGenerationsEmpty(ctx context.Context, root *os.Root) error {
	err := scanDirectory(ctx, root, "generations", func(os.FileInfo) error {
		return fmt.Errorf("%w: retired namespace still contains generations; finish the approved purge", ErrRecovery)
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
