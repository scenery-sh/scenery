package auth

import (
	"testing"

	"scenery.sh/internal/authbridge"
)

func TestStandardAuthDataCrossesProcessBoundaryWithItsType(t *testing.T) {
	data := &AuthData{UserID: "user-1", TenantID: "tenant-1", SessionID: "session-1", ActorUserID: "admin-1", ImpersonationID: "impersonation-1"}
	kind, raw, ok, err := authbridge.EncodeData(data)
	if err != nil || !ok || kind != "scenery.auth.standard" {
		t.Fatalf("encode standard auth data = %q, %t, %v", kind, ok, err)
	}
	decoded, err := authbridge.DecodeData(kind, raw)
	typed, isStandard := decoded.(*AuthData)
	if err != nil || !isStandard || *typed != *data || typed.AuditIdentity() != data.AuditIdentity() {
		t.Fatalf("decoded standard auth data = %#v, %v", decoded, err)
	}
	if _, _, ok, err := authbridge.EncodeData(struct{ UserID string }{UserID: "user-1"}); ok || err != nil {
		t.Fatalf("foreign auth data was encoded as standard: %t, %v", ok, err)
	}
	if _, err := authbridge.DecodeData(kind, []byte(`{"UserID":"user-1","Roles":["admin"]}`)); err == nil {
		t.Fatal("standard auth data with an unknown claim was accepted")
	}
	if _, err := authbridge.DecodeData("custom.kind", raw); err == nil {
		t.Fatal("unregistered auth data kind was decoded")
	}
}
