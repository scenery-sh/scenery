package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	public "scenery.sh/storage"
)

func TestLocalStorePersistsObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store := NewLocalStore("app", root)
	if _, err := store.Put(ctx, "artifacts/report.txt", strings.NewReader("report"), public.PutOptions{
		ContentType: "application/x-report",
		Metadata:    map[string]string{"source": "local"},
	}); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "artifacts", "report.txt"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(data) != "report" {
		t.Fatalf("file data = %q", data)
	}
	page, err := store.List(ctx, public.ListOptions{Prefix: "artifacts/"})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "artifacts/report.txt" || page.Objects[0].Metadata["source"] != "local" {
		t.Fatalf("page = %+v", page)
	}
	offset, length := int64(3), int64(99)
	rc, obj, err := store.Get(ctx, "artifacts/report.txt", public.GetOptions{Offset: &offset, Length: &length})
	if err != nil {
		t.Fatalf("Get long range returned error: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("ReadAll long range returned error: %v", err)
	}
	if string(got) != "ort" || obj.SizeBytes != 3 {
		t.Fatalf("long range data = %q object = %+v", got, obj)
	}
	if _, err := store.Put(ctx, "tmp/remove.txt", strings.NewReader("remove"), public.PutOptions{Metadata: map[string]string{"source": "local"}}); err != nil {
		t.Fatalf("Put tmp returned error: %v", err)
	}
	if err := store.DeletePrefix(ctx, "tmp/"); err != nil {
		t.Fatalf("DeletePrefix returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "__scenery", "metadata", "tmp", "remove.txt.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata sidecar after prefix delete stat err = %v", err)
	}
	if err := store.Delete(ctx, "artifacts/report.txt"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "__scenery", "metadata", "artifacts", "report.txt.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata sidecar after delete stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "artifacts", "report.txt")); !os.IsNotExist(err) {
		t.Fatalf("file after delete stat err = %v", err)
	}
}

func TestLocalStoreIfNoneMatchConcurrent(t *testing.T) {
	t.Parallel()
	assertConcurrentIfNoneMatch(t, NewLocalStore("app", t.TempDir()))
}

func assertConcurrentIfNoneMatch(t *testing.T, store public.Store) {
	t.Helper()
	var success int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.Put(context.Background(), "concurrent/once.txt", strings.NewReader("once"), public.PutOptions{IfNoneMatch: true})
			if err == nil {
				atomic.AddInt32(&success, 1)
				return
			}
			var exists *public.AlreadyExistsError
			if !errors.As(err, &exists) {
				t.Errorf("Put IfNoneMatch error = %T %[1]v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if success != 1 {
		t.Fatalf("successful IfNoneMatch writes = %d, want 1", success)
	}
}
