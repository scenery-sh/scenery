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

	"scenery.sh/internal/storageconfig"
)

func TestDefaultUsesRuntimeConfigEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv(storageconfig.RuntimeConfigEnv, `{
		"schema_version": "`+storageconfig.RuntimeSchemaVersion+`",
		"default": "app",
		"stores": {
			"app": {"kind": "local", "root": `+quoteJSON(filepath.Join(root, "app"))+`}
		}
	}`)
	store, err := Default(context.Background())
	if err != nil {
		t.Fatalf("Default returned error: %v", err)
	}
	obj, err := store.Put(context.Background(), "docs/a.txt", strings.NewReader("alpha"), PutOptions{
		ContentType: "application/x-scenery-test",
		Metadata:    map[string]string{"Author": "runtime"},
	})
	if err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	if obj.Key != "docs/a.txt" || obj.SizeBytes != 5 {
		t.Fatalf("object = %+v", obj)
	}
	head, err := store.Head(context.Background(), "docs/a.txt")
	if err != nil {
		t.Fatalf("Head returned error: %v", err)
	}
	if head.ContentType != "application/x-scenery-test" || head.Metadata["Author"] != "runtime" {
		t.Fatalf("head metadata = %+v", head)
	}
	body, got, err := store.Get(context.Background(), "docs/a.txt", GetOptions{})
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(data) != "alpha" || got.Key != "docs/a.txt" || got.Metadata["Author"] != "runtime" {
		t.Fatalf("data = %q object = %+v", data, got)
	}
	page, err := store.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "docs/a.txt" || page.Objects[0].Metadata["Author"] != "runtime" {
		t.Fatalf("page = %+v", page)
	}
	offset, length := int64(2), int64(99)
	body, got, err = store.Get(context.Background(), "docs/a.txt", GetOptions{Offset: &offset, Length: &length})
	if err != nil {
		t.Fatalf("Get long range returned error: %v", err)
	}
	data, err = io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		t.Fatalf("ReadAll long range returned error: %v", err)
	}
	if string(data) != "pha" || got.SizeBytes != 3 {
		t.Fatalf("long range data = %q object = %+v", data, got)
	}
	if _, err := store.Put(context.Background(), "tmp/b.txt", strings.NewReader("bravo"), PutOptions{Metadata: map[string]string{"Author": "runtime"}}); err != nil {
		t.Fatalf("Put tmp returned error: %v", err)
	}
	if err := store.DeletePrefix(context.Background(), "tmp/"); err != nil {
		t.Fatalf("DeletePrefix returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "app", "__scenery", "metadata", "tmp", "b.txt.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata sidecar after prefix delete stat err = %v", err)
	}
	if err := store.Delete(context.Background(), "docs/a.txt"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "app", "__scenery", "metadata", "docs", "a.txt.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata sidecar after delete stat err = %v", err)
	}
}

func TestLocalRuntimeStoreIfNoneMatchConcurrent(t *testing.T) {
	store := &localRuntimeStore{name: "app", root: t.TempDir()}
	var success int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.Put(context.Background(), "docs/once.txt", strings.NewReader("once"), PutOptions{IfNoneMatch: true})
			if err == nil {
				atomic.AddInt32(&success, 1)
				return
			}
			var exists *AlreadyExistsError
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

func TestDefaultWithoutRuntimeConfigReturnsNotConfigured(t *testing.T) {
	t.Setenv(storageconfig.RuntimeConfigEnv, "")
	if _, err := Default(context.Background()); err == nil {
		t.Fatal("Default returned nil error")
	} else if _, ok := err.(*NotConfiguredError); !ok {
		t.Fatalf("Default error = %T %[1]v, want NotConfiguredError", err)
	}
}

func quoteJSON(value string) string {
	out := strings.Builder{}
	out.WriteByte('"')
	for _, r := range value {
		if r == '\\' || r == '"' {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	out.WriteByte('"')
	return out.String()
}
