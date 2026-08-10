package auth

import (
	"testing"

	"scenery.sh/errs"
)

func TestAuditIdentityNormalSessionUsesEffectiveUserAsActor(t *testing.T) {
	data := &AuthData{
		UserID:    "11111111-1111-1111-1111-111111111111",
		TenantID:  "22222222-2222-2222-2222-222222222222",
		SessionID: "session-normal",
	}
	want := AuditIdentity{
		EffectiveUserID: data.UserID,
		ActorUserID:     data.UserID,
		TenantID:        data.TenantID,
		SessionID:       data.SessionID,
	}
	if got := data.AuditIdentity(); got != want {
		t.Fatalf("AuditIdentity() = %#v, want %#v", got, want)
	}
	got, err := CurrentAuditIdentity(WithContext(t.Context(), UID(data.UserID), data))
	if err != nil || got != want {
		t.Fatalf("CurrentAuditIdentity() = %#v, %v, want %#v, nil", got, err, want)
	}
}

func TestAuditIdentityImpersonationPreservesEffectiveAndActorUsers(t *testing.T) {
	data := &AuthData{
		UserID:          "11111111-1111-1111-1111-111111111111",
		ActorUserID:     "33333333-3333-3333-3333-333333333333",
		TenantID:        "22222222-2222-2222-2222-222222222222",
		SessionID:       "session-impersonated",
		ImpersonationID: "impersonation-exact",
	}
	want := AuditIdentity{
		EffectiveUserID: data.UserID,
		ActorUserID:     data.ActorUserID,
		TenantID:        data.TenantID,
		SessionID:       data.SessionID,
		ImpersonationID: data.ImpersonationID,
	}
	if got := data.AuditIdentity(); got != want {
		t.Fatalf("AuditIdentity() = %#v, want %#v", got, want)
	}
}

func TestCurrentAuditIdentityFailsClosedWithoutAuth(t *testing.T) {
	if got := (*AuthData)(nil).AuditIdentity(); got != (AuditIdentity{}) {
		t.Fatalf("nil AuditIdentity() = %#v", got)
	}
	if _, err := CurrentAuditIdentity(t.Context()); errs.Code(err) != errs.Unauthenticated {
		t.Fatalf("CurrentAuditIdentity() error = %v (code %q), want unauthenticated", err, errs.Code(err))
	}
}
