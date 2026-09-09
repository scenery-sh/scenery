package storagefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeletePreviewIsSelectorAndETagBound(t *testing.T) {
	s := testStore(t)
	putText(t, s, "dir/a", "one", PutOptions{})
	preview, err := s.PreviewDelete(context.Background(), "dir/")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Objects != 1 || preview.Bytes != 3 {
		t.Fatalf("preview: %+v", preview)
	}
	if _, err := s.ApplyDelete(context.Background(), "other/", preview.SelectionRevision); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("wrong selector: %v", err)
	}
	putText(t, s, "dir/a", "one", PutOptions{})
	if _, err := s.ApplyDelete(context.Background(), "dir/", preview.SelectionRevision); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale preview: %v", err)
	}
	if _, err := s.Head(context.Background(), "dir/a"); err != nil {
		t.Fatal(err)
	}
}

func TestBulkDeleteReportsConfirmedPartialProgress(t *testing.T) {
	s := testStore(t)
	putText(t, s, "dir/a", "one", PutOptions{})
	putText(t, s, "dir/b", "two", PutOptions{})
	preview, err := s.PreviewDelete(context.Background(), "dir/")
	if err != nil {
		t.Fatal(err)
	}
	remove := s.namespace.io.remove
	calls := 0
	failure := errors.New("injected second removal failure")
	s.namespace.io.remove = func(r *os.Root, name string) error {
		calls++
		if calls == 2 {
			return failure
		}
		return remove(r, name)
	}
	result, err := s.ApplyDelete(context.Background(), "dir/", preview.SelectionRevision)
	var partial *PartialDeleteError
	if !errors.As(err, &partial) || result.Objects != 1 || result.Bytes != 3 || result.Completion != "partial" {
		t.Fatalf("partial result: %+v, %v", result, err)
	}
	if _, err := s.ApplyDelete(context.Background(), "dir/", preview.SelectionRevision); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("reused preview: %v", err)
	}
}

func TestReclamationRetainsLiveVersionAndStableLocks(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "old", PutOptions{})
	putText(t, s, "a", "new", PutOptions{})
	before, err := os.Stat(filepath.Join(s.namespace.Path, "maintenance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.namespace.PreviewReclaim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Files != 1 || preview.Bytes != 3 {
		t.Fatalf("reclaim preview: %+v", preview)
	}
	result, err := s.namespace.ApplyReclaim(context.Background(), preview.SelectionRevision)
	if err != nil || result.Files != 1 || result.Completion != "complete" {
		t.Fatalf("reclaim: %+v, %v", result, err)
	}
	if body, _ := readText(t, s, "a"); body != "new" {
		t.Fatal("live payload reclaimed")
	}
	after, err := os.Stat(filepath.Join(s.namespace.Path, "maintenance.lock"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("maintenance inode replaced")
	}
}

func TestActiveStreamExcludesReclamation(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "old", PutOptions{})
	r, _, err := s.Get(context.Background(), "a", GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	putText(t, s, "a", "new", PutOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.namespace.PreviewReclaim(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("reclaim bypassed stream: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.namespace.PreviewReclaim(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptReferenceStopsReclamationBeforeRemoval(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "old", PutOptions{})
	putText(t, s, "a", "new", PutOptions{})
	owner, err := Discover(context.Background(), s.namespace.Path, s.namespace.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.namespace.Path, s.scope.refPath(owner.Generation, "a")), []byte("bad JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.namespace.io.remove = func(*os.Root, string) error { t.Fatal("corrupt namespace was mutated"); return nil }
	if _, err := s.namespace.PreviewReclaim(context.Background()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt preview: %v", err)
	}
	if _, err := s.namespace.ApplyReclaim(context.Background(), "old"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt apply: %v", err)
	}
}

func TestReclaimRejectsChangedMaterial(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "old", PutOptions{})
	putText(t, s, "a", "new", PutOptions{})
	preview, err := s.namespace.PreviewReclaim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	putText(t, s, "a", "newer", PutOptions{})
	if _, err := s.namespace.ApplyReclaim(context.Background(), preview.SelectionRevision); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale reclaim: %v", err)
	}
}

func TestInternalSymlinkCannotSelectAnotherGeneration(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "payload", PutOptions{})
	owner, err := Discover(context.Background(), s.namespace.Path, s.namespace.Binding)
	if err != nil {
		t.Fatal(err)
	}
	partition := filepath.Join(s.namespace.Path, generationPath(owner.Generation), "refs", s.scope.partition())
	moved := partition + "-moved"
	if err := os.Rename(partition, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(moved), partition); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Head(context.Background(), "a"); err == nil {
		t.Fatal("internal symlink accepted")
	}
	if _, err := s.Put(context.Background(), "a", strings.NewReader("replacement"), PutOptions{}); err == nil {
		t.Fatal("internal symlink write accepted")
	}
}
