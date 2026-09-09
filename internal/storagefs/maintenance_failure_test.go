package storagefs

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestMaintenanceSyncFailureReportsUncertainCompletion(t *testing.T) {
	for _, reclaim := range []bool{false, true} {
		s := testStore(t)
		putText(t, s, "a", "old", PutOptions{})
		if reclaim {
			putText(t, s, "a", "new", PutOptions{})
		}
		removed := false
		remove := s.namespace.io.remove
		s.namespace.io.remove = func(r *os.Root, name string) error {
			if err := remove(r, name); err != nil {
				return err
			}
			removed = true
			return nil
		}
		cut := errors.New("post-removal directory synchronization failed")
		s.namespace.io.syncFile = func(*os.File) error {
			if removed {
				return cut
			}
			return nil
		}
		ctx := context.Background()
		if reclaim {
			preview, err := s.namespace.PreviewReclaim(ctx)
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.namespace.ApplyReclaim(ctx, preview.SelectionRevision)
			if !errors.Is(err, cut) || result.Completion != "uncertain" || result.Files != 0 {
				t.Fatalf("reclaim uncertainty: %+v, %v", result, err)
			}
		} else {
			preview, err := s.PreviewDelete(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.ApplyDelete(ctx, "", preview.SelectionRevision)
			if !errors.Is(err, cut) || result.Completion != "uncertain" || result.Objects != 0 {
				t.Fatalf("delete uncertainty: %+v, %v", result, err)
			}
		}
	}
}

func TestObjectDescriptorCannotOutgrowListPage(t *testing.T) {
	object := fixtureObject("a", "body")
	object.Store = strings.Repeat("s", MaxPageBytes)
	if err := ValidateLogicalObject(object); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted unlistable logical object: %v", err)
	}
	object.Store = "files"
	object.Key = strings.Repeat("<", 4096)
	if err := ValidateLogicalObject(object); err != nil {
		t.Fatalf("maximum escaped key rejected: %v", err)
	}
}

func TestInactiveGenerationCleanupCanResumeAfterReferenceRemoval(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	putText(t, s, "a", "old", PutOptions{})
	r, err := s.namespace.BeginRestore(ctx, strings.Repeat("a", 64), false, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Stage(ctx, func(g *Generation) error {
		return g.Put(ctx, fixtureObject("a", "new"), strings.NewReader("new"), "fail")
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	preview, err := s.namespace.PreviewReclaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remove := s.namespace.io.remove
	calls := 0
	cut := errors.New("interrupted after inactive reference removal")
	s.namespace.io.remove = func(root *os.Root, name string) error {
		calls++
		if calls == 2 {
			return cut
		}
		return remove(root, name)
	}
	partial, err := s.namespace.ApplyReclaim(ctx, preview.SelectionRevision)
	if !errors.Is(err, cut) || partial.Files != 1 || partial.Completion != "partial" {
		t.Fatalf("interrupted cleanup: %+v, %v", partial, err)
	}
	s.namespace.io.remove = remove
	if _, err := s.namespace.ApplyReclaim(ctx, preview.SelectionRevision); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale cleanup selection accepted: %v", err)
	}
	fresh, err := s.namespace.PreviewReclaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Files != 1 {
		t.Fatalf("remaining material: %+v", fresh)
	}
	if _, err := s.namespace.ApplyReclaim(ctx, fresh.SelectionRevision); err != nil {
		t.Fatal(err)
	}
	current, _ := s.namespace.Store(Scope{Store: "files", Tenant: "tenant"}, 0)
	if body, _ := readText(t, current, "a"); body != "new" {
		t.Fatal("current generation damaged")
	}
}
