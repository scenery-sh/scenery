package data

import (
	"context"
	"fmt"
	"strings"

	"github.com/pbrazdil/onlava/internal/authbridge"
)

// StandardAuthPermissions scopes data access to the current standard-auth tenant.
//
// It maps auth.AuthData.TenantID to data tenant keys. Apps can wrap another
// permission provider in Base for object, field, and row-level rules after the
// tenant check has passed.
type StandardAuthPermissions struct {
	Base Permissions
}

func (p StandardAuthPermissions) CanReadObject(ctx context.Context, actor Actor, ref ObjectRef) error {
	if err := p.checkTenant(actor, ref.TenantKey); err != nil {
		return err
	}
	return p.base().CanReadObject(ctx, actor, ref)
}

func (p StandardAuthPermissions) CanWriteObject(ctx context.Context, actor Actor, ref ObjectRef) error {
	if err := p.checkTenant(actor, ref.TenantKey); err != nil {
		return err
	}
	return p.base().CanWriteObject(ctx, actor, ref)
}

func (p StandardAuthPermissions) CanReadField(ctx context.Context, actor Actor, ref FieldRef) error {
	if err := p.checkTenant(actor, ref.TenantKey); err != nil {
		return err
	}
	return p.base().CanReadField(ctx, actor, ref)
}

func (p StandardAuthPermissions) CanWriteField(ctx context.Context, actor Actor, ref FieldRef) error {
	if err := p.checkTenant(actor, ref.TenantKey); err != nil {
		return err
	}
	return p.base().CanWriteField(ctx, actor, ref)
}

func (p StandardAuthPermissions) RowFilter(ctx context.Context, actor Actor, ref ObjectRef) (*Filter, error) {
	if err := p.checkTenant(actor, ref.TenantKey); err != nil {
		return nil, err
	}
	return p.base().RowFilter(ctx, actor, ref)
}

func (p StandardAuthPermissions) base() Permissions {
	if p.Base == nil {
		return AllowAllPermissions{}
	}
	return p.Base
}

func (p StandardAuthPermissions) checkTenant(actor Actor, tenantKey string) error {
	expected := strings.TrimSpace(tenantKey)
	if expected == "" {
		return fmt.Errorf("permission denied: data tenant key is required")
	}
	actual, ok := TenantKeyFromActor(actor)
	if !ok {
		return fmt.Errorf("permission denied: standard auth tenant_id is required for data tenant %q", expected)
	}
	if actual != expected {
		return fmt.Errorf("permission denied: auth tenant %q cannot access data tenant %q", actual, expected)
	}
	return nil
}

func TenantKeyFromActor(actor Actor) (string, bool) {
	if tenantKey := strings.TrimSpace(actor.TenantKey); tenantKey != "" {
		return tenantKey, true
	}
	if tenantKey, ok := authbridge.TenantID(actor.Data); ok {
		return tenantKey, true
	}
	switch data := actor.Data.(type) {
	case interface{ AuditTenantID() string }:
		tenantKey := strings.TrimSpace(data.AuditTenantID())
		return tenantKey, tenantKey != ""
	default:
		return "", false
	}
}
