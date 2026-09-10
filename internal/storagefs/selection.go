package storagefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math"
	"path/filepath"
	"strings"

	"scenery.sh/internal/atomicfile"
)

type DeletePreview struct {
	Scope             Scope    `json:"scope"`
	Prefix            string   `json:"prefix"`
	Incarnation       string   `json:"incarnation"`
	Generation        string   `json:"generation"`
	Objects           int64    `json:"objects"`
	Bytes             int64    `json:"bytes"`
	Sample            []string `json:"sample"`
	SelectionRevision string   `json:"selection_revision"`
}

type DeleteResult struct {
	Objects    int64  `json:"objects"`
	Bytes      int64  `json:"bytes"`
	Completion string `json:"completion"`
}

// PartialDeleteError reports only durably confirmed progress. An uncertain
// final deletion is not counted as confirmed and requires a fresh preview.
type PartialDeleteError struct {
	Result DeleteResult
	Err    error
}

func (e *PartialDeleteError) Error() string {
	return fmt.Sprintf("storage bulk deletion %s after %d confirmed objects; preview again: %v", e.Result.Completion, e.Result.Objects, e.Err)
}
func (e *PartialDeleteError) Unwrap() error { return e.Err }

func (s *Store) PreviewDelete(ctx context.Context, prefix string) (DeletePreview, error) {
	if err := ValidatePrefix(prefix); err != nil {
		return DeletePreview{}, err
	}
	lease, err := s.namespace.acquire(ctx, false)
	if err != nil {
		return DeletePreview{}, err
	}
	defer func() { _ = lease.Close() }()
	mutation, err := lockFile(ctx, lease.root, "mutation.lock", false)
	if err != nil {
		return DeletePreview{}, err
	}
	defer func() { _ = mutation.Close() }()
	return s.previewDeleteHeld(ctx, lease, prefix)
}

func (s *Store) previewDeleteHeld(ctx context.Context, lease *namespaceLease, prefix string) (DeletePreview, error) {
	run, err := s.orderedDeleteRun(ctx, lease, prefix)
	if err != nil {
		return DeletePreview{}, err
	}
	if run != nil {
		defer func() { _ = run.Remove() }()
	}
	return s.previewDeleteRun(ctx, lease, prefix, run)
}

func (s *Store) previewDeleteRun(ctx context.Context, lease *namespaceLease, prefix string, run *orderedRun[reference]) (DeletePreview, error) {
	preview := DeletePreview{Scope: s.scope, Prefix: prefix, Incarnation: lease.owner.Incarnation, Generation: lease.owner.Generation, Sample: []string{}}
	digest := sha256.New()
	if err := hashJSON(digest, struct {
		Operation               string
		Binding                 Binding
		Incarnation, Generation string
		Scope                   Scope
		Prefix                  string
	}{"delete-prefix", s.namespace.Binding, lease.owner.Incarnation, lease.owner.Generation, s.scope, prefix}); err != nil {
		return DeletePreview{}, err
	}
	err := run.Visit(ctx, func(ref reference) error {
		if err := addTotals(&preview.Objects, &preview.Bytes, ref.Object.SizeBytes); err != nil {
			return err
		}
		if len(preview.Sample) < 8 {
			preview.Sample = append(preview.Sample, ref.Object.Key)
		}
		return hashJSON(digest, ref)
	})
	if err != nil {
		return DeletePreview{}, err
	}
	preview.SelectionRevision = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	return preview, nil
}

func (s *Store) ApplyDelete(ctx context.Context, prefix, expected string) (DeleteResult, error) {
	if expected == "" {
		return DeleteResult{}, ErrPrecondition
	}
	if err := ValidatePrefix(prefix); err != nil {
		return DeleteResult{}, err
	}
	lease, err := s.namespace.acquire(ctx, false)
	if err != nil {
		return DeleteResult{}, err
	}
	defer func() { _ = lease.Close() }()
	mutation, err := lockFile(ctx, lease.root, "mutation.lock", true)
	if err != nil {
		return DeleteResult{}, err
	}
	defer func() { _ = mutation.Close() }()
	run, err := s.orderedDeleteRun(ctx, lease, prefix)
	if err != nil {
		return DeleteResult{}, err
	}
	if run != nil {
		defer func() { _ = run.Remove() }()
	}
	preview, err := s.previewDeleteRun(ctx, lease, prefix, run)
	if err != nil {
		return DeleteResult{}, err
	}
	if preview.SelectionRevision != expected {
		return DeleteResult{}, fmt.Errorf("%w: destructive selection changed; preview again", ErrPrecondition)
	}
	return s.deletePrefixRun(ctx, lease, run)
}

// DeletePrefix is a sequence of durable object deletions, not a transaction.
// Callers receive PartialDeleteError if any interruption occurs during apply.
func (s *Store) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ValidatePrefix(prefix); err != nil {
		return err
	}
	lease, err := s.namespace.acquire(ctx, false)
	if err != nil {
		return err
	}
	defer func() { _ = lease.Close() }()
	mutation, err := lockFile(ctx, lease.root, "mutation.lock", true)
	if err != nil {
		return err
	}
	defer func() { _ = mutation.Close() }()
	run, err := s.orderedDeleteRun(ctx, lease, prefix)
	if err != nil {
		return err
	}
	if run != nil {
		defer func() { _ = run.Remove() }()
	}
	_, err = s.deletePrefixRun(ctx, lease, run)
	return err
}

func (s *Store) deletePrefixRun(ctx context.Context, lease *namespaceLease, run *orderedRun[reference]) (DeleteResult, error) {
	result := DeleteResult{Completion: "complete"}
	err := run.Visit(ctx, func(ref reference) error {
		if err := s.deleteHeld(ctx, lease, ref.Object.Key); err != nil {
			return err
		}
		return addTotals(&result.Objects, &result.Bytes, ref.Object.SizeBytes)
	})
	if err != nil {
		result.Completion = "partial"
		var uncertain *atomicfile.PublicationError
		if errors.As(err, &uncertain) {
			result.Completion = "uncertain"
		}
		return result, &PartialDeleteError{Result: result, Err: err}
	}
	return result, nil
}

func (s *Store) orderedDeleteRun(ctx context.Context, lease *namespaceLease, prefix string) (*orderedRun[reference], error) {
	sorter := newOrderedSorter[reference](lease.root, filepath.Join(generationPath(lease.owner.Generation), "staging"), func(a, b reference) bool {
		return a.Object.Key < b.Object.Key
	})
	err := scanReferences(ctx, lease, s.scope, func(ref reference) error {
		if !strings.HasPrefix(ref.Object.Key, prefix) {
			return nil
		}
		return sorter.Add(ctx, ref)
	})
	if err != nil {
		return nil, sorter.fail(err)
	}
	return sorter.Finish(ctx)
}

func addTotals(count, bytes *int64, size int64) error {
	if *count == math.MaxInt64 || size < 0 || size > math.MaxInt64-*bytes {
		return fmt.Errorf("%w: selection totals overflow", ErrCorrupt)
	}
	*count++
	*bytes += size
	return nil
}

func hashJSON(digest hash.Hash, value any) error { return json.NewEncoder(digest).Encode(value) }

func referenceOrder(ref reference) string {
	return ref.Object.Store + "\x00" + ref.Object.Tenant + "\x00" + ref.Object.Key
}

func scanOrderedNamespaceReferences(ctx context.Context, lease *namespaceLease, visit func(reference) error) error {
	sorter := newOrderedSorter[reference](lease.root, filepath.Join(generationPath(lease.owner.Generation), "staging"), func(a, b reference) bool {
		return referenceOrder(a) < referenceOrder(b)
	})
	if err := scanAllReferences(ctx, lease, func(ref reference) error { return sorter.Add(ctx, ref) }); err != nil {
		return sorter.fail(err)
	}
	run, err := sorter.Finish(ctx)
	if err != nil {
		return err
	}
	if run == nil {
		return nil
	}
	defer func() { _ = run.Remove() }()
	return run.Visit(ctx, visit)
}
