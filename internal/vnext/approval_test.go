package vnext

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppLocalApprovalVerifierAuthenticatesBoundTokens(t *testing.T) {
	root := t.TempDir()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := json.Marshal(approvalTrustStore{APIVersion: approvalTrustAPIVersion, Keys: map[string]string{"maintainer": base64.RawStdEncoding.EncodeToString(public)}})
	path := filepath.Join(root, ".scenery", "approval-trust.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, store, 0o644); err != nil {
		t.Fatal(err)
	}
	verifier, err := LoadApprovalVerifier(root)
	if err != nil {
		t.Fatal(err)
	}
	token := ApprovalToken{PlanID: "sha256:" + strings.Repeat("a", 64), Caller: "local", RiskScopes: []string{"risk"}, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	payload, err := ApprovalTokenPayload(token)
	if err != nil {
		t.Fatal(err)
	}
	token.Signature = "ed25519:maintainer:" + base64.RawStdEncoding.EncodeToString(ed25519.Sign(private, payload))
	if err := verifier(token, payload); err != nil {
		t.Fatal(err)
	}
	payload[0] ^= 1
	if err := verifier(token, payload); err == nil {
		t.Fatal("tampered approval payload verified")
	}
}

func TestApprovalTrustStoreRejectsWhitespaceInKeyID(t *testing.T) {
	root := t.TempDir()
	public, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := json.Marshal(approvalTrustStore{APIVersion: approvalTrustAPIVersion, Keys: map[string]string{"key id": base64.RawStdEncoding.EncodeToString(public)}})
	path := filepath.Join(root, ".scenery", "approval-trust.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, store, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadApprovalVerifier(root); err == nil {
		t.Fatal("trust store accepted whitespace in key id")
	}
}
