package scenery

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestApprovalTokenPayloadIsCanonicalAndRejectsDuplicateScopes(t *testing.T) {
	token := NewApprovalToken("sha256:"+strings.Repeat("a", 64), "agent:test", []string{"write", "deploy"}, time.Date(2026, 7, 10, 12, 30, 0, 0, time.FixedZone("offset", 2*60*60)))
	payload, err := ApprovalTokenPayload(token)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"kind":"scenery.approval-token"`) || !strings.Contains(string(payload), `"caller":"agent:test"`) || !strings.Contains(string(payload), `"risk_scopes":["deploy","write"]`) {
		t.Fatalf("payload = %s", payload)
	}
	token.RiskScopes = []string{"deploy", "deploy"}
	if _, err := ApprovalTokenPayload(token); err == nil {
		t.Fatal("duplicate approval scopes were accepted")
	}
}

func TestValidateApprovalTokenEnforcesSignatureShape(t *testing.T) {
	token := NewApprovalToken("sha256:"+strings.Repeat("b", 64), "agent:test", []string{"deploy"}, time.Now().UTC().Add(time.Minute))
	token.Signature = "ed25519:maintainer:" + base64.RawStdEncoding.EncodeToString(make([]byte, 64))
	if err := ValidateApprovalToken(token); err != nil {
		t.Fatal(err)
	}
	token.Signature = "ed25519:key id:" + base64.RawStdEncoding.EncodeToString(make([]byte, 64))
	if err := ValidateApprovalToken(token); err == nil {
		t.Fatal("whitespace in approval key id was accepted")
	}
}
