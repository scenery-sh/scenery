package storagefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/machine"
)

type PurgePreview struct {
	Incarnation       string `json:"incarnation"`
	Generation        string `json:"generation"`
	Objects           int64  `json:"objects"`
	Bytes             int64  `json:"bytes"`
	Generations       int64  `json:"generations"`
	SelectionRevision string `json:"selection_revision"`
}
type PurgeResult struct {
	// Nil means retirement was published but its durability is unknown.
	Retired   *bool `json:"retired"`
	Reclaimed bool  `json:"reclaimed"`
}
type PurgeRecoveryError struct {
	Result PurgeResult
	Err    error
}

func (e *PurgeRecoveryError) Error() string {
	return fmt.Sprintf("storage purge requires the same approved retry: %v", e.Err)
}
func (e *PurgeRecoveryError) Unwrap() error { return e.Err }

// The CLI must hold the stopped worktree's live and operation leases before
// calling purge. Namespace code has no authority to stop runtimes or erase SQL.
func (n *Namespace) PreviewPurge(ctx context.Context) (PurgePreview, error) {
	if !n.Binding.Managed {
		return PurgePreview{}, ErrOwnership
	}
	lease, err := n.acquire(ctx, true)
	if err != nil {
		return PurgePreview{}, err
	}
	defer func() { _ = lease.Close() }()
	return n.previewPurgeHeld(ctx, lease)
}

func (n *Namespace) previewPurgeHeld(ctx context.Context, lease *namespaceLease) (PurgePreview, error) {
	preview := PurgePreview{Incarnation: lease.owner.Incarnation, Generation: lease.owner.Generation}
	digest := sha256.New()
	if err := hashJSON(digest, struct {
		Operation string
		Owner     Owner
	}{"purge", lease.owner}); err != nil {
		return PurgePreview{}, err
	}
	err := scanOrderedGenerations(ctx, lease, func(selected *namespaceLease) error {
		preview.Generations++
		if err := hashJSON(digest, selected.owner.Generation); err != nil {
			return err
		}
		if err := n.validateReclamation(ctx, selected); err != nil {
			return err
		}
		if err := scanOrderedNamespaceReferences(ctx, selected, func(ref reference) error {
			if err := addTotals(&preview.Objects, &preview.Bytes, ref.Object.SizeBytes); err != nil {
				return err
			}
			return hashJSON(digest, ref)
		}); err != nil {
			return err
		}
		return n.scanOrderedMaterials(ctx, selected, func(material reclaimMaterial) error { return hashJSON(digest, material) })
	})
	if err != nil {
		return PurgePreview{}, err
	}
	preview.SelectionRevision = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	return preview, nil
}

func (n *Namespace) ApplyPurge(ctx context.Context, expected string) (PurgeResult, error) {
	if !n.Binding.Managed {
		return PurgeResult{}, ErrOwnership
	}
	if expected == "" {
		return PurgeResult{}, ErrPrecondition
	}
	r, err := openNamespaceRoot(n.Path)
	if err != nil {
		return PurgeResult{}, err
	}
	defer func() { _ = r.Close() }()
	maintenance, err := lockFile(ctx, r, "maintenance.lock", true)
	if err != nil {
		return PurgeResult{}, err
	}
	defer func() { _ = maintenance.Close() }()
	owner, err := readOwner(r, n.Binding, n.Incarnation)
	if err != nil {
		return PurgeResult{}, err
	}
	operation, err := readOperation(r, owner)
	if err != nil {
		return PurgeResult{}, err
	}
	if operation != nil {
		if operation.Operation != "purge" || operation.SelectionRevision != expected || operation.Generation != owner.Generation {
			return PurgeResult{}, ErrRecovery
		}
	} else {
		owner, err = loadOwner(r, n.Binding, n.Incarnation, false)
		if err != nil {
			return PurgeResult{}, err
		}
		preview, err := n.previewPurgeHeld(ctx, &namespaceLease{root: r, maintenance: maintenance, owner: owner})
		if err != nil {
			return PurgeResult{}, err
		}
		if preview.SelectionRevision != expected {
			return PurgeResult{}, fmt.Errorf("%w: namespace changed; preview purge again", ErrPrecondition)
		}
		operation = &Operation{ArtifactIdentity: machine.NewArtifactIdentity(operationKind, operationDescriptor), Binding: n.Binding, Operation: "purge", Incarnation: owner.Incarnation, Generation: owner.Generation, SelectionRevision: expected, Storage: true, Phase: "prepared"}
		if err := n.io.writeRecord(r, "operation.json", operation); err != nil {
			return PurgeResult{}, err
		}
	}
	retired := owner.State == "retired"
	result := PurgeResult{Retired: &retired}
	fail := func(err error) (PurgeResult, error) { return result, &PurgeRecoveryError{Result: result, Err: err} }
	if owner.State != "retired" {
		owner.State = "retired"
		if err := n.io.writeRecord(r, "owner.json", owner); err != nil {
			if _, uncertain := errors.AsType[*atomicfile.PublicationError](err); uncertain {
				result.Retired = nil
			}
			return fail(err)
		}
		retired = true
	}
	operation.Phase = "retired"
	if err := n.io.writeRecord(r, "operation.json", operation); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := r.RemoveAll("generations"); err != nil {
		return fail(err)
	}
	if err := n.io.syncDirectory(r, "."); err != nil {
		return fail(err)
	}
	result.Reclaimed = true
	if err := n.io.remove(r, "operation.json"); err != nil {
		return fail(err)
	}
	if err := n.io.syncDirectory(r, "."); err != nil {
		return fail(err)
	}
	return result, nil
}
