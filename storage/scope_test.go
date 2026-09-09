package storage

import (
	"context"
	"errors"
	"reflect"
	"scenery.sh/internal/storagefs"
	"strings"
	"testing"
)

func TestTenantSelectionIsSeparateFromEveryLogicalKey(t *testing.T) {
	backend := &recordingStore{object: &Object{Key: "a"}}
	var scopes []storagefs.Scope
	local := &localRuntimeStore{name: "app", tenantScoped: true, resolve: func(_ context.Context, scope storagefs.Scope, _ bool) (Store, error) {
		scopes = append(scopes, scope)
		return backend, nil
	}}
	ctx := WithTenantID(context.Background(), "tenant/a")
	if _, err := local.Put(ctx, "a", strings.NewReader("body"), PutOptions{}); err != nil {
		t.Fatal(err)
	}
	body, _, err := local.Get(ctx, "a", GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	if _, err := local.Head(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := local.List(ctx, ListOptions{Prefix: "dir/", Cursor: "opaque"}); err != nil {
		t.Fatal(err)
	}
	if err := local.Delete(ctx, "a", DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := local.DeletePrefix(ctx, "dir/"); err != nil {
		t.Fatal(err)
	}
	for _, scope := range scopes {
		if scope.Store != "app" || scope.Tenant != "tenant/a" {
			t.Fatalf("wrong scope: %+v", scope)
		}
	}
	if !reflect.DeepEqual(backend.keys, []string{"a", "a", "a", "a", "dir/"}) {
		t.Fatalf("keys were encoded: %v", backend.keys)
	}
	if backend.listOptions.Cursor != "opaque" || backend.listOptions.Prefix != "dir/" {
		t.Fatal("list scope rewrote cursor/prefix")
	}
}
func TestTenantScopedStoreFailsClosedWithoutTenant(t *testing.T) {
	local := &localRuntimeStore{name: "app", tenantScoped: true, resolve: func(context.Context, storagefs.Scope, bool) (Store, error) {
		t.Fatal("resolved backend without tenant")
		return nil, nil
	}}
	_, err := local.Head(context.Background(), "a")
	var required *TenantRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("missing tenant: %v", err)
	}
}
func TestUnscopedStoreRejectsExplicitTenant(t *testing.T) {
	local := &localRuntimeStore{name: "app", resolve: func(context.Context, storagefs.Scope, bool) (Store, error) {
		t.Fatal("resolved backend with forbidden tenant")
		return nil, nil
	}}
	if _, err := local.Head(WithTenantID(context.Background(), "tenant"), "a"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("explicit tenant on unscoped store: %v", err)
	}
}
