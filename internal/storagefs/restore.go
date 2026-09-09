package storagefs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/machine"
)

// Restore owns exclusive maintenance. The caller owns the stopped worktree live
// and operation locks for its entire lifetime, including any database callback.
// Closing an interrupted restore preserves its recorded recovery barrier.
type Restore struct {
	namespace *Namespace
	lease     *namespaceLease
	operation *Operation
	digest    string
	database  bool
	mode      string
	conflict  string
}

func (n *Namespace) BeginRestore(ctx context.Context, digest string, database bool, mode, conflict string) (*Restore, error) {
	if !isHexID(digest, 32) || (mode != "overwrite" && mode != "merge") || (database && mode != "overwrite") {
		return nil, fmt.Errorf("%w: restore requires a SHA-256 and replay-safe selection", ErrInvalid)
	}
	if (conflict != "fail" && conflict != "skip" && conflict != "overwrite") || (mode != "merge" && conflict != "fail") {
		return nil, ErrInvalid
	}
	r, err := openNamespaceRoot(n.Path)
	if err != nil {
		return nil, err
	}
	l, err := lockFile(ctx, r, "maintenance.lock", true)
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	lease := &namespaceLease{root: r, maintenance: l}
	fail := func(err error) (*Restore, error) { _ = lease.Close(); return nil, err }
	owner, err := loadOwner(r, n.Binding, n.Incarnation, true)
	if err != nil {
		return fail(err)
	}
	if owner.State != "ready" && (owner.State != "retired" || !n.Binding.Managed || mode != "overwrite") {
		return fail(ErrRetired)
	}
	lease.owner = owner
	op, err := readOperation(r, owner)
	if err != nil {
		return fail(err)
	}
	if op != nil {
		if op.Operation != "restore" || op.ArchiveSHA256 != digest || op.Database != database || op.Mode != mode || op.OnConflict != conflict {
			return fail(operationRecovery(op))
		}
		if op.Phase != "preparing" {
			incarnation := op.Incarnation
			if op.StagedIncarnation != "" {
				incarnation = op.StagedIncarnation
			}
			if err := validateGeneration(r, n.Binding, incarnation, op.StagedGeneration); err != nil {
				return fail(err)
			}
		}
	}
	return &Restore{namespace: n, lease: lease, operation: op, digest: digest, database: database, mode: mode, conflict: conflict}, nil
}

func (r *Restore) Close() error   { return r.lease.Close() }
func (r *Restore) Owner() Owner   { return r.lease.owner }
func (r *Restore) Resuming() bool { return r.operation != nil }

// Stage builds a private complete generation. The callback must enumerate the
// already-validated archive from the same opened input. A failed stage leaves
// the active generation untouched and retains a pinned rebuild instruction.
func (r *Restore) Stage(ctx context.Context, populate func(*Generation) error) error {
	if r.operation != nil && r.operation.Phase != "preparing" {
		return nil
	}
	return r.stageGeneration(ctx, populate)
}

// Complete repeats database overwrite whenever its completion is uncertain.
// Only a durable generation switch proves the database step already finished.
func (r *Restore) Complete(ctx context.Context, restoreDatabase func(context.Context) error) error {
	op := r.operation
	if op == nil || op.Phase == "preparing" {
		return fmt.Errorf("%w: restore was not staged", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.lease.owner.Generation != op.StagedGeneration {
		if op.Database {
			if restoreDatabase == nil {
				return fmt.Errorf("%w: database restore callback required", ErrInvalid)
			}
			if err := restoreDatabase(ctx); err != nil {
				return err
			}
			op.Phase = "database"
			if err := r.namespace.io.writeRecord(r.lease.root, "operation.json", op); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		owner := r.lease.owner
		owner.Generation = op.StagedGeneration
		if op.StagedIncarnation != "" {
			owner.Incarnation = op.StagedIncarnation
			owner.State = "ready"
		}
		if err := r.namespace.io.writeRecord(r.lease.root, "owner.json", owner); err != nil {
			return err
		}
		r.lease.owner = owner
	}
	op.Phase = "switched"
	if err := r.namespace.io.writeRecord(r.lease.root, "operation.json", op); err != nil {
		return err
	}
	if err := r.namespace.io.remove(r.lease.root, "operation.json"); err != nil {
		return err
	}
	if err := r.namespace.io.syncDirectory(r.lease.root, "."); err != nil {
		return &atomicfile.PublicationError{Err: err}
	}
	r.operation = nil
	return nil
}

func createGeneration(disk diskIO, root *os.Root, owner Owner) error {
	base := generationPath(owner.Generation)
	if err := root.Mkdir(base, 0o700); err != nil {
		return err
	}
	if err := disk.syncDirectory(root, "generations"); err != nil {
		return err
	}
	for _, part := range []string{"refs", "versions", "staging"} {
		if err := root.Mkdir(filepath.Join(base, part), 0o700); err != nil {
			return err
		}
	}
	gen := generation{ArtifactIdentity: machine.NewArtifactIdentity(generationKind, generationDescriptor), Binding: owner.Binding, Incarnation: owner.Incarnation, Generation: owner.Generation}
	return disk.writeRecord(root, filepath.Join(base, "generation.json"), gen)
}
