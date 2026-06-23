package storage

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hugelgupf/p9/fsimpl/localfs"
	"github.com/hugelgupf/p9/p9"

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
	if _, err := store.Put(ctx, "artifacts/report.txt", strings.NewReader("report"), public.PutOptions{}); err != nil {
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
	if len(page.Objects) != 1 || page.Objects[0].Key != "artifacts/report.txt" {
		t.Fatalf("page = %+v", page)
	}
	if err := store.Delete(ctx, "artifacts/report.txt"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "artifacts", "report.txt")); !os.IsNotExist(err) {
		t.Fatalf("file after delete stat err = %v", err)
	}
}

func TestZeroFSStoreUsesP9Socket(t *testing.T) {
	ctx := context.Background()
	shortRoot, err := os.MkdirTemp("/tmp", "scn-zerofs-store-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortRoot) })
	root := filepath.Join(shortRoot, "objects")
	socketPath := filepath.Join(shortRoot, "zerofs.9p.sock")
	startTestP9Server(t, socketPath, root)
	store := NewZeroFSStore("app", socketPath, ZeroFSStoreOptions{Prefix: "app", MaxObjectBytes: 64})
	if _, err := store.Put(ctx, "artifacts/report.txt", strings.NewReader("report"), public.PutOptions{ContentType: "text/plain"}); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "app", "artifacts", "report.txt"))
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
	if len(page.Objects) != 1 || page.Objects[0].Key != "artifacts/report.txt" {
		t.Fatalf("page = %+v", page)
	}
	rc, obj, err := store.Get(ctx, "artifacts/report.txt", public.GetOptions{})
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(got) != "report" || obj.SizeBytes != 6 || obj.SHA256 == "" {
		t.Fatalf("get data = %q object = %+v", got, obj)
	}
	if _, err := store.Put(ctx, "artifacts/report.txt", strings.NewReader(strings.Repeat("x", 65)), public.PutOptions{}); err == nil {
		t.Fatal("oversized Put returned nil error")
	}
	rc, _, err = store.Get(ctx, "artifacts/report.txt", public.GetOptions{})
	if err != nil {
		t.Fatalf("Get after oversized Put returned error: %v", err)
	}
	got, err = io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("ReadAll after oversized Put returned error: %v", err)
	}
	if string(got) != "report" {
		t.Fatalf("oversized Put corrupted existing object: %q", got)
	}
	if err := store.Delete(ctx, "artifacts/report.txt"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "app", "artifacts", "report.txt")); !os.IsNotExist(err) {
		t.Fatalf("file after delete stat err = %v", err)
	}
}

func startTestP9Server(t *testing.T, socketPath, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen p9 socket: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := p9.NewServer(localfs.Attacher(root))
	done := make(chan error, 1)
	go func() {
		done <- server.ServeContext(ctx, ln)
	}()
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("p9 server did not stop")
		}
		_ = os.Remove(socketPath)
	})
}
