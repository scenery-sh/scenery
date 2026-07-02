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

func TestMemoryStoreContract(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore("app")
	obj, err := store.Put(ctx, "docs/a.txt", strings.NewReader("alpha"), public.PutOptions{ContentType: "text/plain", Metadata: map[string]string{"source": "test"}})
	if err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	if obj.Store != "app" || obj.Key != "docs/a.txt" || obj.SizeBytes != 5 || obj.SHA256 == "" || obj.ETag == "" {
		t.Fatalf("object = %+v", obj)
	}
	if _, err := store.Put(ctx, "docs/a.txt", strings.NewReader("new"), public.PutOptions{IfNoneMatch: true}); err == nil {
		t.Fatal("Put IfNoneMatch accepted existing key")
	} else {
		var exists *public.AlreadyExistsError
		if !errors.As(err, &exists) {
			t.Fatalf("Put IfNoneMatch error = %T %[1]v, want AlreadyExistsError", err)
		}
	}
	rc, got, err := store.Get(ctx, "docs/a.txt", public.GetOptions{})
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	_ = rc.Close()
	if string(data) != "alpha" || got.Key != "docs/a.txt" {
		t.Fatalf("get data = %q object = %+v", data, got)
	}
	head, err := store.Head(ctx, "docs/a.txt")
	if err != nil {
		t.Fatalf("Head returned error: %v", err)
	}
	if head.ContentType != "text/plain" || head.Metadata["source"] != "test" {
		t.Fatalf("head metadata = %+v", head)
	}
	offset, length := int64(1), int64(3)
	rc, got, err = store.Get(ctx, "docs/a.txt", public.GetOptions{Offset: &offset, Length: &length})
	if err != nil {
		t.Fatalf("Get range returned error: %v", err)
	}
	data, _ = io.ReadAll(rc)
	_ = rc.Close()
	if string(data) != "lph" || got.SizeBytes != 3 {
		t.Fatalf("range data = %q object = %+v", data, got)
	}
	longLength := int64(99)
	rc, got, err = store.Get(ctx, "docs/a.txt", public.GetOptions{Offset: &offset, Length: &longLength})
	if err != nil {
		t.Fatalf("Get long range returned error: %v", err)
	}
	data, _ = io.ReadAll(rc)
	_ = rc.Close()
	if string(data) != "lpha" || got.SizeBytes != 4 {
		t.Fatalf("long range data = %q object = %+v", data, got)
	}
	if _, err := store.Put(ctx, "docs/nested/b.txt", strings.NewReader("beta"), public.PutOptions{}); err != nil {
		t.Fatalf("Put nested returned error: %v", err)
	}
	page, err := store.List(ctx, public.ListOptions{Prefix: "docs/", Delimiter: "/", Limit: 10})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "docs/a.txt" || len(page.Prefixes) != 1 || page.Prefixes[0] != "docs/nested/" {
		t.Fatalf("page = %+v", page)
	}
	if err := store.Delete(ctx, "docs/a.txt"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := store.Head(ctx, "docs/a.txt"); err == nil {
		t.Fatal("Head after delete returned nil error")
	} else {
		var missing *public.NotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("Head error = %T %[1]v, want NotFoundError", err)
		}
	}
	if err := store.DeletePrefix(ctx, "docs/nested/"); err != nil {
		t.Fatalf("DeletePrefix returned error: %v", err)
	}
	page, err = store.List(ctx, public.ListOptions{})
	if err != nil {
		t.Fatalf("List after delete returned error: %v", err)
	}
	if len(page.Objects) != 0 {
		t.Fatalf("objects after delete = %+v", page.Objects)
	}
}

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
