package scenery

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestApprovalTokenPayloadIsCanonicalAndRejectsDuplicateScopes(t *testing.T) {
	token := ApprovalToken{
		PlanID: "sha256:" + strings.Repeat("a", 64),
		Caller: "agent:test", RiskScopes: []string{"write", "deploy"},
		ExpiresAt: time.Date(2026, 7, 10, 12, 30, 0, 0, time.FixedZone("offset", 2*60*60)),
	}
	payload, err := ApprovalTokenPayload(token)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"caller":"agent:test","expires_at":"2026-07-10T10:30:00Z","plan_id":"sha256:` + strings.Repeat("a", 64) + `","risk_scopes":["deploy","write"]}`
	if string(payload) != want {
		t.Fatalf("payload = %s, want %s", payload, want)
	}
	token.RiskScopes = []string{"deploy", "deploy"}
	if _, err := ApprovalTokenPayload(token); err == nil {
		t.Fatal("duplicate approval scopes were accepted")
	}
}

func TestValidateApprovalTokenEnforcesSignatureShape(t *testing.T) {
	token := ApprovalToken{
		PlanID: "sha256:" + strings.Repeat("b", 64), Caller: "agent:test", RiskScopes: []string{"deploy"},
		ExpiresAt: time.Now().UTC().Add(time.Minute),
		Signature: "ed25519:maintainer:" + base64.RawStdEncoding.EncodeToString(make([]byte, 64)),
	}
	if err := ValidateApprovalToken(token); err != nil {
		t.Fatal(err)
	}
	token.Signature = "ed25519:key id:" + base64.RawStdEncoding.EncodeToString(make([]byte, 64))
	if err := ValidateApprovalToken(token); err == nil {
		t.Fatal("whitespace in approval key id was accepted")
	}
}
