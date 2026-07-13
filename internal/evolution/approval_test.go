package evolution

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	scenery "scenery.sh"
	"scenery.sh/internal/machine"
)

func TestAppLocalApprovalVerifierAuthenticatesBoundTokens(t *testing.T) {
	root := t.TempDir()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := json.Marshal(approvalTrustStore{ArtifactIdentity: machine.NewArtifactIdentity(approvalTrustKind, approvalTrustSchemaDescriptor), Keys: map[string]string{"maintainer": base64.RawStdEncoding.EncodeToString(public)}})
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
	token := scenery.NewApprovalToken("sha256:"+strings.Repeat("a", 64), "local", []string{"risk"}, time.Now().UTC().Add(time.Minute))
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
	store, _ := json.Marshal(approvalTrustStore{ArtifactIdentity: machine.NewArtifactIdentity(approvalTrustKind, approvalTrustSchemaDescriptor), Keys: map[string]string{"key id": base64.RawStdEncoding.EncodeToString(public)}})
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

func TestApprovalTrustStoreMigratesLegacyIdentityAtomically(t *testing.T) {
	root := t.TempDir()
	public, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{
		"padded":   base64.StdEncoding.EncodeToString(public),
		"unpadded": base64.RawStdEncoding.EncodeToString(public),
	}
	legacy, err := json.Marshal(legacyApprovalTrustStore{APIVersion: legacyApprovalTrustAPIVersion, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".scenery", "approval-trust.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadApprovalVerifier(root); err != nil {
		t.Fatal(err)
	}
	backupPath := path + approvalTrustBackupSuffix
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != string(legacy) {
		t.Fatalf("backup changed legacy bytes: got %q want %q", backup, legacy)
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %v", info.Mode().Perm())
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	currentInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if currentInfo.Mode().Perm() != 0o600 {
		t.Fatalf("migrated trust store mode = %v", currentInfo.Mode().Perm())
	}
	var store approvalTrustStore
	if err := decodeArtifactExact(current, &store); err != nil {
		t.Fatal(err)
	}
	if err := machine.ValidateArtifactIdentity(store.ArtifactIdentity, approvalTrustKind, approvalTrustSchemaDescriptor, "test"); err != nil {
		t.Fatal(err)
	}
	if store.Keys["padded"] != keys["padded"] || store.Keys["unpadded"] != keys["unpadded"] {
		t.Fatalf("migrated keys = %#v, want %#v", store.Keys, keys)
	}
	markerPath := path + approvalTrustMarkerSuffix
	if marker, err := os.ReadFile(markerPath); err != nil || string(marker) != approvalTrustMarkerContents {
		t.Fatalf("marker = %q, err = %v", marker, err)
	}
	markerInfo, err := os.Stat(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if markerInfo.Mode().Perm() != 0o600 {
		t.Fatalf("migration marker mode = %v", markerInfo.Mode().Perm())
	}

	// Once marked complete, loading uses only the strict current decoder.
	if err := os.WriteFile(backupPath, []byte("not legacy json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadApprovalVerifier(root); err != nil {
		t.Fatalf("idempotent load consulted legacy backup: %v", err)
	}
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadApprovalVerifier(root); err == nil {
		t.Fatal("completed migration decoded the legacy trust store again")
	}
}

func TestApprovalTrustStoreMigrationFinishesAfterRewrite(t *testing.T) {
	root := t.TempDir()
	public, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{"maintainer": base64.RawStdEncoding.EncodeToString(public)}
	legacy, _ := json.Marshal(legacyApprovalTrustStore{APIVersion: legacyApprovalTrustAPIVersion, Keys: keys})
	current, _ := json.Marshal(approvalTrustStore{ArtifactIdentity: machine.NewArtifactIdentity(approvalTrustKind, approvalTrustSchemaDescriptor), Keys: keys})
	path := filepath.Join(root, ".scenery", "approval-trust.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, current, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+approvalTrustBackupSuffix, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadApprovalVerifier(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(current) {
		t.Fatalf("recovery rewrote current store: got %q want %q", got, current)
	}
	if _, err := os.Stat(path + approvalTrustMarkerSuffix); err != nil {
		t.Fatalf("completion marker: %v", err)
	}
}
