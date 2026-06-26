package storage

import (
	"context"
	"encoding/base64"
	"io"
	"sort"
	"strings"

	"scenery.sh/internal/authbridge"
)

type tenantIDContextKey struct{}

type auditTenantData interface {
	AuditTenantID() string
}

func WithTenantID(ctx context.Context, tenantID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, tenantIDContextKey{}, strings.TrimSpace(tenantID))
}

func tenantID(ctx context.Context) (string, bool) {
	if ctx != nil {
		if tenant, _ := ctx.Value(tenantIDContextKey{}).(string); strings.TrimSpace(tenant) != "" {
			return strings.TrimSpace(tenant), true
		}
	}
	if data, ok := authbridge.CurrentData(); ok {
		return tenantIDFromAuthData(data)
	}
	return "", false
}

func tenantIDFromAuthData(data any) (string, bool) {
	if tenant, ok := authbridge.TenantID(data); ok && strings.TrimSpace(tenant) != "" {
		return strings.TrimSpace(tenant), true
	}
	if audit, ok := data.(auditTenantData); ok {
		tenant := strings.TrimSpace(audit.AuditTenantID())
		return tenant, tenant != ""
	}
	return "", false
}

type tenantScopedStore struct {
	store Store
}

func newTenantScopedStore(store Store) Store {
	return tenantScopedStore{store: store}
}

func (s tenantScopedStore) Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (*Object, error) {
	physical, prefix, err := s.physicalKey(ctx, key)
	if err != nil {
		return nil, err
	}
	obj, err := s.store.Put(ctx, physical, body, opts)
	return visibleObject(obj, prefix), err
}

func (s tenantScopedStore) PutFile(ctx context.Context, key, localPath string, opts PutOptions) (*Object, error) {
	physical, prefix, err := s.physicalKey(ctx, key)
	if err != nil {
		return nil, err
	}
	obj, err := s.store.PutFile(ctx, physical, localPath, opts)
	return visibleObject(obj, prefix), err
}

func (s tenantScopedStore) Get(ctx context.Context, key string, opts GetOptions) (io.ReadCloser, *Object, error) {
	physical, prefix, err := s.physicalKey(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	body, obj, err := s.store.Get(ctx, physical, opts)
	return body, visibleObject(obj, prefix), err
}

func (s tenantScopedStore) Head(ctx context.Context, key string) (*Object, error) {
	physical, prefix, err := s.physicalKey(ctx, key)
	if err != nil {
		return nil, err
	}
	obj, err := s.store.Head(ctx, physical)
	return visibleObject(obj, prefix), err
}

func (s tenantScopedStore) List(ctx context.Context, opts ListOptions) (*ListPage, error) {
	opts, err := NormalizeListOptions(opts)
	if err != nil {
		return nil, err
	}
	prefix, err := s.tenantPrefix(ctx)
	if err != nil {
		return nil, err
	}
	after := decodeTenantCursor(opts.Cursor)
	underlying := opts
	underlying.Prefix = prefix + opts.Prefix
	underlying.Cursor = ""
	underlying.Limit = MaxListLimit
	out := &ListPage{}
	prefixes := map[string]bool{}
	for {
		page, err := s.store.List(ctx, underlying)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Objects {
			visible := strings.TrimPrefix(obj.Key, prefix)
			if visible <= after {
				continue
			}
			obj.Key = visible
			out.Objects = append(out.Objects, obj)
			if len(out.Objects) >= opts.Limit {
				out.NextCursor = encodeTenantCursor(visible)
				out.Prefixes = sortedKeys(prefixes)
				return out, nil
			}
		}
		for _, item := range page.Prefixes {
			visible := strings.TrimPrefix(item, prefix)
			if visible != "" {
				prefixes[visible] = true
			}
		}
		if page.NextCursor == "" {
			break
		}
		// ponytail: generic pagination scans backend pages; add backend-native tenant cursors if this gets hot.
		underlying.Cursor = page.NextCursor
	}
	out.Prefixes = sortedKeys(prefixes)
	return out, nil
}

func (s tenantScopedStore) Delete(ctx context.Context, key string) error {
	physical, _, err := s.physicalKey(ctx, key)
	if err != nil {
		return err
	}
	return s.store.Delete(ctx, physical)
}

func (s tenantScopedStore) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ValidatePrefix(prefix); err != nil {
		return err
	}
	tenantPrefix, err := s.tenantPrefix(ctx)
	if err != nil {
		return err
	}
	return s.store.DeletePrefix(ctx, tenantPrefix+prefix)
}

func (s tenantScopedStore) physicalKey(ctx context.Context, key string) (string, string, error) {
	if err := ValidateKey(key); err != nil {
		return "", "", err
	}
	prefix, err := s.tenantPrefix(ctx)
	if err != nil {
		return "", "", err
	}
	return prefix + key, prefix, nil
}

func (s tenantScopedStore) tenantPrefix(ctx context.Context) (string, error) {
	tenant, ok := tenantID(ctx)
	if !ok {
		return "", &TenantRequiredError{}
	}
	return "__scenery/tenants/" + base64.RawURLEncoding.EncodeToString([]byte(tenant)) + "/", nil
}

func visibleObject(obj *Object, prefix string) *Object {
	if obj == nil {
		return nil
	}
	out := *obj
	out.Key = strings.TrimPrefix(out.Key, prefix)
	out.Metadata = cloneMetadata(obj.Metadata)
	return &out
}

func encodeTenantCursor(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func decodeTenantCursor(cursor string) string {
	if cursor == "" {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return cursor
	}
	return string(data)
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
