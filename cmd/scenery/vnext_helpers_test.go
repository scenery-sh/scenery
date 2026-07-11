package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadMigrationApprovalTokensEnforcesSchemaShape(t *testing.T) {
	signature := "ed25519:test:" + base64.RawStdEncoding.EncodeToString(make([]byte, 64))
	valid := `{"plan_id":"sha256:` + strings.Repeat("a", 64) + `","caller":"agent:test","risk_scopes":["deploy"],"expires_at":"2026-07-10T12:30:00Z","signature":"` + signature + `"}`
	root := t.TempDir()
	write := func(name, value string) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if tokens, err := readMigrationApprovalTokens([]string{write("valid.json", valid)}); err != nil || len(tokens) != 1 {
		t.Fatalf("valid token = %#v, %v", tokens, err)
	}
	unknown := strings.TrimSuffix(valid, "}") + `,"extra":true}`
	if _, err := readMigrationApprovalTokens([]string{write("unknown.json", unknown)}); err == nil {
		t.Fatal("approval token with unknown property was accepted")
	}
	duplicate := strings.Replace(valid, `["deploy"]`, `["deploy","deploy"]`, 1)
	if _, err := readMigrationApprovalTokens([]string{write("duplicate.json", duplicate)}); err == nil {
		t.Fatal("approval token with duplicate risk scope was accepted")
	}
}
