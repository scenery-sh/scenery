package storagefs

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math"
	"sort"
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
	err := scanOrderedReferences(ctx, lease, s.scope, prefix, func(ref reference) error {
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
	preview, err := s.previewDeleteHeld(ctx, lease, prefix)
	if err != nil {
		return DeleteResult{}, err
	}
	if preview.SelectionRevision != expected {
		return DeleteResult{}, fmt.Errorf("%w: destructive selection changed; preview again", ErrPrecondition)
	}
	return s.deletePrefixHeld(ctx, lease, prefix)
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
	_, err = s.deletePrefixHeld(ctx, lease, prefix)
	return err
}

func (s *Store) deletePrefixHeld(ctx context.Context, lease *namespaceLease, prefix string) (DeleteResult, error) {
	result := DeleteResult{Completion: "complete"}
	err := scanOrderedReferences(ctx, lease, s.scope, prefix, func(ref reference) error {
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

func addTotals(count, bytes *int64, size int64) error {
	if *count == math.MaxInt64 || size < 0 || size > math.MaxInt64-*bytes {
		return fmt.Errorf("%w: selection totals overflow", ErrCorrupt)
	}
	*count++
	*bytes += size
	return nil
}

func hashJSON(digest hash.Hash, value any) error { return json.NewEncoder(digest).Encode(value) }

// Canonical fingerprints require ordered references. Repeated bounded scans
// avoid an in-memory inventory or persistent index; cost is O(N*ceil(N/128)).
// The caller holds mutation ownership throughout all scans and callbacks.
func scanOrderedReferences(ctx context.Context, lease *namespaceLease, scope Scope, prefix string, visit func(reference) error) error {
	last := ""
	for {
		candidates := &referenceHeap{}
		err := scanReferences(ctx, lease, scope, func(ref reference) error {
			if ref.Object.Key <= last || !strings.HasPrefix(ref.Object.Key, prefix) {
				return nil
			}
			if candidates.Len() == 128 {
				if ref.Object.Key >= (*candidates)[0].Object.Key {
					return nil
				}
				heap.Pop(candidates)
			}
			heap.Push(candidates, ref)
			return nil
		})
		if err != nil {
			return err
		}
		if candidates.Len() == 0 {
			return nil
		}
		sort.Slice(*candidates, func(i, j int) bool { return (*candidates)[i].Object.Key < (*candidates)[j].Object.Key })
		for _, ref := range *candidates {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(ref); err != nil {
				return err
			}
			last = ref.Object.Key
		}
		if candidates.Len() < 128 {
			return nil
		}
	}
}

type referenceHeap []reference

func (h referenceHeap) Len() int           { return len(h) }
func (h referenceHeap) Less(i, j int) bool { return referenceOrder(h[i]) > referenceOrder(h[j]) }
func (h referenceHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *referenceHeap) Push(value any)    { *h = append(*h, value.(reference)) }
func (h *referenceHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func referenceOrder(ref reference) string {
	return ref.Object.Store + "\x00" + ref.Object.Tenant + "\x00" + ref.Object.Key
}

func scanOrderedNamespaceReferences(ctx context.Context, lease *namespaceLease, visit func(reference) error) error {
	last := ""
	for {
		h := &referenceHeap{}
		if err := scanAllReferences(ctx, lease, func(ref reference) error {
			key := referenceOrder(ref)
			if key <= last {
				return nil
			}
			if h.Len() == 128 {
				if key >= referenceOrder((*h)[0]) {
					return nil
				}
				heap.Pop(h)
			}
			heap.Push(h, ref)
			return nil
		}); err != nil {
			return err
		}
		if h.Len() == 0 {
			return nil
		}
		sort.Slice(*h, func(i, j int) bool { return referenceOrder((*h)[i]) < referenceOrder((*h)[j]) })
		for _, ref := range *h {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(ref); err != nil {
				return err
			}
			last = referenceOrder(ref)
		}
		if h.Len() < 128 {
			return nil
		}
	}
}
