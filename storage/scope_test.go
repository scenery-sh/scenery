package storage

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTenantScopedStoreIsolatesVisibleKeys(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store := newTenantScopedStore(&localRuntimeStore{name: "app", root: root})
	ctxA := WithTenantID(context.Background(), "tenant/a")
	ctxB := WithTenantID(context.Background(), "tenant/b")

	obj, err := store.Put(ctxA, "docs/a.txt", strings.NewReader("alpha"), PutOptions{})
	if err != nil {
		t.Fatalf("Put tenant A returned error: %v", err)
	}
	if obj.Key != "docs/a.txt" {
		t.Fatalf("visible put key = %q", obj.Key)
	}
	if _, err := os.Stat(filepath.Join(root, "__scenery", "tenants", encodedTenant("tenant/a"), "docs", "a.txt")); err != nil {
		t.Fatalf("physical tenant object missing: %v", err)
	}
	if _, err := store.Head(ctxB, "docs/a.txt"); err == nil {
		t.Fatal("tenant B read tenant A object")
	} else {
		if _, ok := errors.AsType[*NotFoundError](err); !ok {
			t.Fatalf("tenant B Head error = %T %[1]v, want NotFoundError", err)
		}
	}

	if _, err := store.Put(ctxB, "docs/a.txt", strings.NewReader("bravo"), PutOptions{}); err != nil {
		t.Fatalf("Put tenant B returned error: %v", err)
	}
	body, obj, err := store.Get(ctxA, "docs/a.txt", GetOptions{})
	if err != nil {
		t.Fatalf("Get tenant A returned error: %v", err)
	}
	data, _ := io.ReadAll(body)
	_ = body.Close()
	if string(data) != "alpha" || obj.Key != "docs/a.txt" {
		t.Fatalf("tenant A get data = %q object = %+v", data, obj)
	}
	if err := store.Delete(ctxB, "docs/a.txt"); err != nil {
		t.Fatalf("tenant B delete returned error: %v", err)
	}
	body, obj, err = store.Get(ctxA, "docs/a.txt", GetOptions{})
	if err != nil {
		t.Fatalf("Get tenant A after tenant B delete returned error: %v", err)
	}
	data, _ = io.ReadAll(body)
	_ = body.Close()
	if string(data) != "alpha" || obj.Key != "docs/a.txt" {
		t.Fatalf("tenant A after tenant B delete data = %q object = %+v", data, obj)
	}
}

func TestTenantScopedStoreListAndDeletePrefixUseVisibleKeys(t *testing.T) {
	t.Parallel()
	ctx := WithTenantID(context.Background(), "tenant")
	root := t.TempDir()
	store := newTenantScopedStore(&localRuntimeStore{name: "app", root: root})
	tenantRoot := filepath.Join(root, "__scenery", "tenants", encodedTenant("tenant"))
	for _, key := range []string{"docs/a.txt", "docs/b.txt", "docs/nested/c.txt"} {
		path := filepath.Join(tenantRoot, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create parent for %s: %v", key, err)
		}
		if err := os.WriteFile(path, []byte(key), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", key, err)
		}
	}
	page, err := store.List(ctx, ListOptions{Prefix: "docs/", Delimiter: "/", Limit: 2})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(page.Objects) != 2 || page.Objects[0].Key != "docs/a.txt" || page.Objects[1].Key != "docs/b.txt" || page.NextCursor == "" {
		t.Fatalf("first page = %+v", page)
	}
	next, err := store.List(ctx, ListOptions{Prefix: "docs/", Delimiter: "/", Cursor: page.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("List next returned error: %v", err)
	}
	if len(next.Objects) != 0 || len(next.Prefixes) != 1 || next.Prefixes[0] != "docs/nested/" {
		t.Fatalf("next page = %+v", next)
	}
	if err := store.DeletePrefix(ctx, "docs/"); err != nil {
		t.Fatalf("DeletePrefix returned error: %v", err)
	}
	empty, err := store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List after delete returned error: %v", err)
	}
	if len(empty.Objects) != 0 || len(empty.Prefixes) != 0 {
		t.Fatalf("objects after delete = %+v prefixes = %+v", empty.Objects, empty.Prefixes)
	}
}

func TestTenantScopedStoreFailsClosedWithoutTenant(t *testing.T) {
	t.Parallel()
	store := newTenantScopedStore(&localRuntimeStore{name: "app", root: t.TempDir()})
	_, err := store.Head(context.Background(), "docs/a.txt")
	if _, ok := errors.AsType[*TenantRequiredError](err); !ok {
		t.Fatalf("Head without tenant error = %T %[1]v, want TenantRequiredError", err)
	}
}

func encodedTenant(tenant string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(tenant))
}
