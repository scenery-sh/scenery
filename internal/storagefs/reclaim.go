package storagefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"syscall"

	"scenery.sh/internal/atomicfile"
)

type ReclaimSample struct {
	Kind  string `json:"kind"`
	Bytes int64  `json:"bytes"`
}
type ReclaimPreview struct {
	Incarnation       string          `json:"incarnation"`
	Generation        string          `json:"generation"`
	Files             int64           `json:"files"`
	Bytes             int64           `json:"bytes"`
	Sample            []ReclaimSample `json:"sample"`
	SelectionRevision string          `json:"selection_revision"`
}
type ReclaimResult struct {
	Files      int64  `json:"files"`
	Bytes      int64  `json:"bytes"`
	Completion string `json:"completion"`
}
type PartialReclaimError struct {
	Result ReclaimResult
	Err    error
}

func (e *PartialReclaimError) Error() string {
	return fmt.Sprintf("storage reclamation %s after %d confirmed files; preview again: %v", e.Result.Completion, e.Result.Files, e.Err)
}
func (e *PartialReclaimError) Unwrap() error { return e.Err }

// Reclamation excludes all active uploads and streams through the exclusive
// maintenance lease. Neither previews nor apply replace the stable lock files.
func (n *Namespace) PreviewReclaim(ctx context.Context) (ReclaimPreview, error) {
	lease, err := n.acquire(ctx, true)
	if err != nil {
		return ReclaimPreview{}, err
	}
	defer func() { _ = lease.Close() }()
	if err := n.validateNamespaceReclamation(ctx, lease); err != nil {
		return ReclaimPreview{}, err
	}
	return n.previewReclaimHeld(ctx, lease)
}

func (n *Namespace) previewReclaimHeld(ctx context.Context, lease *namespaceLease) (ReclaimPreview, error) {
	preview := ReclaimPreview{Incarnation: lease.owner.Incarnation, Generation: lease.owner.Generation, Sample: []ReclaimSample{}}
	digest := sha256.New()
	if err := hashJSON(digest, struct {
		Operation               string
		Binding                 Binding
		Incarnation, Generation string
	}{"reclaim", n.Binding, lease.owner.Incarnation, lease.owner.Generation}); err != nil {
		return ReclaimPreview{}, err
	}
	err := n.scanOrderedNamespaceMaterials(ctx, lease, func(material reclaimMaterial) error {
		if err := addTotals(&preview.Files, &preview.Bytes, material.Bytes); err != nil {
			return err
		}
		if len(preview.Sample) < 8 {
			preview.Sample = append(preview.Sample, ReclaimSample{Kind: material.Kind, Bytes: material.Bytes})
		}
		return hashJSON(digest, material)
	})
	if err != nil {
		return ReclaimPreview{}, err
	}
	preview.SelectionRevision = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	return preview, nil
}

func (n *Namespace) ApplyReclaim(ctx context.Context, expected string) (ReclaimResult, error) {
	if expected == "" {
		return ReclaimResult{}, ErrPrecondition
	}
	lease, err := n.acquire(ctx, true)
	if err != nil {
		return ReclaimResult{}, err
	}
	defer func() { _ = lease.Close() }()
	if err := n.validateNamespaceReclamation(ctx, lease); err != nil {
		return ReclaimResult{}, err
	}
	preview, err := n.previewReclaimHeld(ctx, lease)
	if err != nil {
		return ReclaimResult{}, err
	}
	if preview.SelectionRevision != expected {
		return ReclaimResult{}, fmt.Errorf("%w: reclaimable material changed; preview again", ErrPrecondition)
	}
	if err := scanOrderedGenerations(ctx, lease, func(selected *namespaceLease) error { return n.syncReferenceDirectories(ctx, selected) }); err != nil {
		return ReclaimResult{}, err
	}
	result := ReclaimResult{Completion: "complete"}
	err = n.scanOrderedNamespaceMaterials(ctx, lease, func(material reclaimMaterial) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := n.io.remove(lease.root, material.Path); err != nil {
			return err
		}
		if err := n.io.syncDirectory(lease.root, filepath.Dir(material.Path)); err != nil {
			return &atomicfile.PublicationError{Err: err}
		}
		return addTotals(&result.Files, &result.Bytes, material.Bytes)
	})
	if err != nil {
		result.Completion = "partial"
		var uncertain *atomicfile.PublicationError
		if errors.As(err, &uncertain) {
			result.Completion = "uncertain"
		}
		return result, &PartialReclaimError{Result: result, Err: err}
	}
	return result, nil
}

func (n *Namespace) scanOrderedMaterials(ctx context.Context, lease *namespaceLease, visit func(reclaimMaterial) error) error {
	return n.scanOrderedGenerationMaterials(ctx, lease, false, visit)
}

// Inactive generation references sort before their payload paths. Removing and
// syncing each reference first makes an interrupted cleanup safely repeatable.
// Empty generation metadata/directories remain as bounded per-restore lineage;
// purge is the operation that retires that allocation's complete control state.
func (n *Namespace) scanOrderedNamespaceMaterials(ctx context.Context, lease *namespaceLease, visit func(reclaimMaterial) error) error {
	active := lease.owner.Generation
	return scanOrderedGenerations(ctx, lease, func(selected *namespaceLease) error {
		return n.scanOrderedGenerationMaterials(ctx, selected, selected.owner.Generation != active, visit)
	})
}

func (n *Namespace) validateNamespaceReclamation(ctx context.Context, lease *namespaceLease) error {
	return scanOrderedGenerations(ctx, lease, func(selected *namespaceLease) error { return n.validateReclamation(ctx, selected) })
}

func (n *Namespace) scanOrderedGenerationMaterials(ctx context.Context, lease *namespaceLease, inactive bool, visit func(reclaimMaterial) error) error {
	if err := removeStaleOrderedRuns(ctx, lease.root, filepath.Join(generationPath(lease.owner.Generation), "staging")); err != nil {
		return err
	}
	sorter := newOrderedSorter[reclaimMaterial](lease.root, filepath.Join(generationPath(lease.owner.Generation), "staging"), func(a, b reclaimMaterial) bool {
		return a.Path < b.Path
	})
	sorter.create = n.io.createOrderedRun
	fallback := func(err error) error {
		if !errors.Is(err, syscall.ENOSPC) && !errors.Is(err, syscall.EDQUOT) {
			return err
		}
		// No visitor has run yet. Remove all partial scratch before rescanning;
		// callback failures must never restart deletion or double-count progress.
		if cleanup := removeStaleOrderedRuns(ctx, lease.root, sorter.staging); cleanup != nil {
			return errors.Join(err, cleanup)
		}
		return scanBoundedGenerationMaterials(ctx, lease, inactive, visit)
	}
	if err := scanGenerationMaterials(ctx, lease, inactive, func(material reclaimMaterial) error { return sorter.Add(ctx, material) }); err != nil {
		return fallback(sorter.fail(err))
	}
	run, err := sorter.Finish(ctx)
	if err != nil {
		return fallback(err)
	}
	if run == nil {
		return nil
	}
	defer func() { _ = run.Remove() }()
	return run.Visit(ctx, visit)
}

// Disk-pressure recovery uses bounded sorted batches and no scratch writes.
// The maintenance lease stabilizes the selection; callbacks may remove entries.
func scanBoundedGenerationMaterials(ctx context.Context, lease *namespaceLease, inactive bool, visit func(reclaimMaterial) error) error {
	const batchSize = 128
	last := ""
	for {
		batch := make([]reclaimMaterial, 0, batchSize)
		err := scanGenerationMaterials(ctx, lease, inactive, func(material reclaimMaterial) error {
			if material.Path <= last {
				return nil
			}
			i := sort.Search(len(batch), func(i int) bool { return batch[i].Path >= material.Path })
			if i == batchSize {
				return nil
			}
			if len(batch) < batchSize {
				batch = append(batch, reclaimMaterial{})
			}
			copy(batch[i+1:], batch[i:len(batch)-1])
			batch[i] = material
			return nil
		})
		if err != nil {
			return err
		}
		for _, material := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(material); err != nil {
				return err
			}
			last = material.Path
		}
		if len(batch) < batchSize {
			return nil
		}
	}
}
