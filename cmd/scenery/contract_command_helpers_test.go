package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	scenery "scenery.sh"
)

func TestReadApprovalTokensEnforcesSchemaShape(t *testing.T) {
	signature := "ed25519:test:" + base64.RawStdEncoding.EncodeToString(make([]byte, 64))
	token := scenery.NewApprovalToken("sha256:"+strings.Repeat("a", 64), "agent:test", []string{"deploy"}, time.Date(2026, 7, 10, 12, 30, 0, 0, time.UTC))
	token.Signature = signature
	encoded, _ := json.Marshal(token)
	valid := string(encoded)
	root := t.TempDir()
	write := func(name, value string) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if tokens, err := readApprovalTokens([]string{write("valid.json", valid)}); err != nil || len(tokens) != 1 {
		t.Fatalf("valid token = %#v, %v", tokens, err)
	}
	unknown := strings.TrimSuffix(valid, "}") + `,"extra":true}`
	if _, err := readApprovalTokens([]string{write("unknown.json", unknown)}); err == nil {
		t.Fatal("approval token with unknown property was accepted")
	}
	duplicate := strings.Replace(valid, `["deploy"]`, `["deploy","deploy"]`, 1)
	if _, err := readApprovalTokens([]string{write("duplicate.json", duplicate)}); err == nil {
		t.Fatal("approval token with duplicate risk scope was accepted")
	}
}
