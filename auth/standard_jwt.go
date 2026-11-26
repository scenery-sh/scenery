package auth

import (
	"strings"
	"time"
)

// AuthUserID is the unique identifier for a standard-auth user.
type AuthUserID string

// TenantID is the unique identifier for a tenant.
type TenantID string

// AuthData is the data associated with an authenticated standard-auth user.
type AuthData struct {
	UserID          AuthUserID
	TenantID        TenantID
	SessionID       string
	ActorUserID     AuthUserID
	ImpersonationID string
}

// AuditIdentity is the impersonation-aware identity applications persist with
// audited work. EffectiveUserID is the subject whose access is exercised;
// ActorUserID is the real initiator of the session.
type AuditIdentity struct {
	EffectiveUserID AuthUserID
	ActorUserID     AuthUserID
	TenantID        TenantID
	SessionID       string
	ImpersonationID string
}

// Impersonating reports whether the token represents a platform impersonation session.
func (d *AuthData) Impersonating() bool {
	return d != nil && strings.TrimSpace(string(d.ActorUserID)) != ""
}

// AuditIdentity returns the complete audit identity carried by this auth data.
func (d *AuthData) AuditIdentity() AuditIdentity {
	if d == nil {
		return AuditIdentity{}
	}
	actor := d.UserID
	if d.Impersonating() {
		actor = d.ActorUserID
	}
	return AuditIdentity{
		EffectiveUserID: d.UserID,
		ActorUserID:     actor,
		TenantID:        d.TenantID,
		SessionID:       d.SessionID,
		ImpersonationID: d.ImpersonationID,
	}
}

// AuditTenantID exposes the active tenant for generic audit context readers.
func (d *AuthData) AuditTenantID() string {
	if d == nil {
		return ""
	}
	return strings.TrimSpace(string(d.TenantID))
}

type AccessTokenOptions struct {
	UserID          AuthUserID
	TenantID        TenantID
	SessionID       string
	ActorUserID     AuthUserID
	ImpersonationID string
	ExpiresIn       time.Duration
	Now             time.Time
}
