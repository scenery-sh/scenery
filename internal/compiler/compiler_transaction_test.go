package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/workspacetx"
)

func TestCompileRecoversStaleInterruptedWorkspaceTransactionBeforeReadingSource(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	target := filepath.Join(root, appFilename)
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	after := []byte("application \"partially-applied\" {\n")
	transactionRoot := filepath.Join(root, ".scenery", "transactions")
	transactionDir := filepath.Join(transactionRoot, "change-stale")
	stage := filepath.Join(transactionDir, "staged", "000000")
	backup := filepath.Join(transactionDir, "backups", "000000")
	if err := os.MkdirAll(filepath.Dir(stage), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(target, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, after, 0o644); err != nil {
		t.Fatal(err)
	}
	lock, journal := workspacetx.NewArtifacts(transactionDir, "")
	lock.Owner.PID = 999999
	journal.Owner = lock.Owner
	journal.Entries = []workspacetx.Entry{{
		Path: appFilename, Stage: stage, Backup: backup,
		BeforeDigest: testByteDigest(before), AfterDigest: testByteDigest(after), BeforeExists: true, AfterExists: true,
	}}
	writeTransactionJSON(t, filepath.Join(transactionRoot, "change.lock"), lock)
	writeTransactionJSON(t, filepath.Join(transactionRoot, "change-apply.json"), journal)

	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("Compile() after recovery: %v diagnostics=%#v", err, result.Diagnostics)
	}
	restored, err := os.ReadFile(target)
	if err != nil || string(restored) != string(before) {
		t.Fatalf("source was not restored before compilation: %v", err)
	}
	for _, path := range []string{transactionDir, filepath.Join(transactionRoot, "change.lock"), filepath.Join(transactionRoot, "change-apply.json")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("transaction artifact remains after recovery: %s", path)
		}
	}
}

func writeTransactionJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testByteDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
