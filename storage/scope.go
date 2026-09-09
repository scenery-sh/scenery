package storage

import (
	"context"
	"fmt"
	"scenery.sh/internal/authbridge"
	"strings"
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

func selectedTenant(ctx context.Context, store string, scoped bool) (string, error) {
	if !scoped {
		if ctx != nil {
			if explicit, _ := ctx.Value(tenantIDContextKey{}).(string); explicit != "" {
				return "", fmt.Errorf("%w: store %q is not tenant-scoped", ErrInvalidInput, store)
			}
		}
		return "", nil
	}
	tenant, ok := tenantID(ctx)
	if !ok {
		return "", &TenantRequiredError{Store: store}
	}
	return tenant, nil
}
