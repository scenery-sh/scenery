//go:build scenery_storage_integration

package storagefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The storage probe runs this filesystem-volume journey explicitly; ordinary
// tests retain the small failure cuts without hundreds of directory operations.
func TestReclaimPressureResumesAcrossBatches(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	n := s.namespace
	putText(t, s, "a", "old", PutOptions{})
	lease, err := n.acquire(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(generationPath(lease.owner.Generation), "staging")
	for i := range 258 {
		if err := lease.root.WriteFile(filepath.Join(staging, fmt.Sprintf("%032x", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	restore, err := n.BeginRestore(ctx, strings.Repeat("a", 64), false, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	if err := restore.Stage(ctx, func(g *Generation) error {
		return g.Put(ctx, fixtureObject("a", "new"), strings.NewReader("new"), "fail")
	}); err != nil {
		t.Fatal(err)
	}
	if err := restore.Complete(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := restore.Close(); err != nil {
		t.Fatal(err)
	}
	normal, err := n.PreviewReclaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if normal.Files != 260 {
		t.Fatalf("expected retired reference, payload and 258 uploads: %+v", normal)
	}
	n.io.createOrderedRun = func(*os.Root, string) (string, io.WriteCloser, error) { return "", nil, syscall.ENOSPC }
	pressure, err := n.PreviewReclaim(ctx)
	if err != nil || pressure.SelectionRevision != normal.SelectionRevision || pressure.Files != normal.Files || pressure.Bytes != normal.Bytes {
		t.Fatalf("fallback changed preview: %+v, %+v, %v", normal, pressure, err)
	}
	remove := n.io.remove
	removed := make(map[string]bool)
	var removedBytes int64
	cut := errors.New("interrupt second batch")
	interrupt := true
	n.io.remove = func(root *os.Root, name string) error {
		if interrupt && len(removed) == 129 {
			return cut
		}
		if removed[name] {
			t.Fatalf("duplicate deletion: %s", name)
		}
		info, err := root.Stat(name)
		if err != nil {
			return err
		}
		if err := remove(root, name); err != nil {
			return err
		}
		removed[name] = true
		removedBytes += info.Size()
		return nil
	}
	partial, err := n.ApplyReclaim(ctx, normal.SelectionRevision)
	if !errors.Is(err, cut) || partial.Completion != "partial" || partial.Files != 129 || partial.Bytes != removedBytes {
		t.Fatalf("partial progress: %+v, %v", partial, err)
	}
	interrupt = false
	fresh, err := n.PreviewReclaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.SelectionRevision == normal.SelectionRevision || fresh.Files != 131 || fresh.Bytes != normal.Bytes-partial.Bytes {
		t.Fatalf("bad resumed preview: %+v", fresh)
	}
	rest, err := n.ApplyReclaim(ctx, fresh.SelectionRevision)
	if err != nil || rest.Completion != "complete" || rest.Files != fresh.Files || rest.Bytes != fresh.Bytes || len(removed) != 260 || removedBytes != normal.Bytes {
		t.Fatalf("resumed totals: %+v, %v", rest, err)
	}
	current, err := n.Store(Scope{Store: "files", Tenant: "tenant"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := readText(t, current, "a"); body != "new" {
		t.Fatal("live object changed")
	}
}
