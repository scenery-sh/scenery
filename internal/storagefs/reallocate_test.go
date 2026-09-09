package storagefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func retiredNamespace(t *testing.T) *Namespace {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	binding := testBinding(root)
	binding.Managed, binding.WorktreeKey = true, strings.Repeat("a", 64)
	n, err := allocate(context.Background(), filepath.Join(root, "storage"), binding, testDiskIO())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := n.PreviewPurge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := n.ApplyPurge(context.Background(), preview.SelectionRevision)
	if err != nil || result.Retired == nil || !*result.Retired || !result.Reclaimed {
		t.Fatalf("purge: %+v %v", result, err)
	}
	return n
}

func TestRetiredOverwritePublishesNewIncarnationOnlyAfterStage(t *testing.T) {
	n := retiredNamespace(t)
	ctx := context.Background()
	lockBefore, _ := os.Stat(filepath.Join(n.Path, "maintenance.lock"))
	if _, err := allocate(ctx, n.Path, n.Binding, testDiskIO()); !errors.Is(err, ErrRetired) {
		t.Fatal(err)
	}
	r, err := n.BeginRestore(ctx, strings.Repeat("b", 64), false, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Stage(ctx, func(g *Generation) error {
		return g.Put(ctx, fixtureObject("a", "value"), strings.NewReader("value"), "fail")
	}); err != nil {
		t.Fatal(err)
	}
	if r.Owner().State != "retired" || r.Owner().Incarnation != n.Incarnation {
		t.Fatal("revived owner during staging")
	}
	if err := r.Complete(ctx, nil); err != nil {
		t.Fatal(err)
	}
	owner := r.Owner()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if owner.State != "ready" || owner.Incarnation == n.Incarnation {
		t.Fatal("reused retired identity")
	}
	if err := n.CheckReady(ctx); !errors.Is(err, ErrOwnership) {
		t.Fatalf("old handle survived: %v", err)
	}
	current := &Namespace{Path: n.Path, Binding: n.Binding, Incarnation: owner.Incarnation, io: testDiskIO()}
	if err := current.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
	store, _ := current.Store(Scope{Store: "files", Tenant: "tenant"}, 0)
	if _, err := store.Head(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	lockAfter, _ := os.Stat(filepath.Join(n.Path, "maintenance.lock"))
	if !os.SameFile(lockBefore, lockAfter) {
		t.Fatal("replaced lifetime lock")
	}
}

func TestRetiredIncompleteStageRequiresPinnedResume(t *testing.T) {
	n := retiredNamespace(t)
	ctx := context.Background()
	digest := strings.Repeat("b", 64)
	r, err := n.BeginRestore(ctx, digest, false, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	cut := errors.New("interrupted extraction")
	if err := r.Stage(ctx, func(*Generation) error { return cut }); !errors.Is(err, cut) {
		t.Fatal(err)
	}
	_ = r.Close()
	owner, err := Discover(ctx, n.Path, n.Binding)
	if err != nil || owner.State != "retired" {
		t.Fatalf("failed import revived owner: %+v %v", owner, err)
	}
	if _, err := n.BeginRestore(ctx, strings.Repeat("c", 64), false, "overwrite", "fail"); !errors.Is(err, ErrRecovery) {
		t.Fatal(err)
	}
	r, err = n.BeginRestore(ctx, digest, false, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := r.Complete(ctx, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("committed incomplete generation: %v", err)
	}
	if err := r.Stage(ctx, func(g *Generation) error {
		return g.Put(ctx, fixtureObject("a", "value"), strings.NewReader("value"), "fail")
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(ctx, nil); err != nil {
		t.Fatal(err)
	}
}
