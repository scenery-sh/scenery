package vnext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChangeTransactionRecoversInterruptedMultiFileApply(t *testing.T) {
	root := t.TempDir()
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("first.scn", "first-before")
	write("second.scn", "second-before")
	edits := []SourceEdit{
		{Path: "first.scn", BeforeDigest: byteDigest([]byte("first-before")), BeforeExists: true, AfterExists: true, After: []byte("first-after"), Mode: 0o644},
		{Path: "second.scn", BeforeDigest: byteDigest([]byte("second-before")), BeforeExists: true, AfterExists: true, After: []byte("second-after"), Mode: 0o644},
	}
	rollback, _, err := commitPlannedEdits(root, edits, filepath.Join(root, ".scenery", "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first.scn", "second.scn"} {
		if value, _ := os.ReadFile(filepath.Join(root, name)); string(value) != name[:len(name)-4]+"-after" {
			t.Fatalf("%s was not applied: %q", name, value)
		}
	}
	if _, _, err := commitPlannedEdits(root, edits, ""); err == nil {
		t.Fatal("concurrent workspace transaction unexpectedly acquired the lock")
	}
	// Simulate process death before its receipt is durable.
	if err := recoverInterruptedChangeTransaction(root, true); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"first.scn", "first-before"}, {"second.scn", "second-before"}} {
		value, err := os.ReadFile(filepath.Join(root, pair[0]))
		if err != nil || string(value) != pair[1] {
			t.Fatalf("%s was not restored byte-for-byte: %q %v", pair[0], value, err)
		}
	}
	rollback() // Recovery and rollback are idempotent.
}

func TestChangeTransactionRecoveryKeepsReceiptedApply(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.scn")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(root, ".scenery", "receipt.json")
	_, _, err := commitPlannedEdits(root, []SourceEdit{{
		Path: "source.scn", BeforeDigest: byteDigest([]byte("before")), BeforeExists: true,
		AfterExists: true, After: []byte("after"), Mode: 0o644,
	}}, receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(receipt), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recoverInterruptedChangeTransaction(root, true); err != nil {
		t.Fatal(err)
	}
	if value, _ := os.ReadFile(path); string(value) != "after" {
		t.Fatalf("receipted apply was rolled back: %q", value)
	}
}

func TestCompileRejectsActiveTransactionOwnedByCurrentProcess(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scenery.scn")
	before := []byte("language { edition = \"2027\" }\n")
	after := []byte("language { edition = \"2027\" }\n\n")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	rollback, _, err := commitPlannedEdits(root, []SourceEdit{{
		Path: "scenery.scn", BeforeDigest: byteDigest(before), BeforeExists: true,
		AfterExists: true, After: after, Mode: 0o644,
	}}, filepath.Join(root, ".scenery", "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer rollback()

	if _, err := Compile(root); err == nil || !strings.Contains(err.Error(), "workspace change transaction is active") {
		t.Fatalf("Compile() error = %v, want active transaction rejection", err)
	}
	if _, err := compileDuringChangeTransaction(root); err != nil {
		t.Fatalf("transaction owner could not verify its staged result: %v", err)
	}
}

func TestChangeTransactionRecoveryRejectsEscapingMetadata(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	transactionRoot := filepath.Join(root, ".scenery", "transactions")
	if err := os.MkdirAll(transactionRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := changeTransactionLock{APIVersion: "scenery.change-transaction-lock/v1", TransactionDir: outside}
	encoded, _ := json.Marshal(lock)
	if err := os.WriteFile(filepath.Join(transactionRoot, "change.lock"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recoverInterruptedChangeTransaction(root, false); err == nil {
		t.Fatal("escaping transaction directory was accepted")
	}
	if value, err := os.ReadFile(sentinel); err != nil || string(value) != "keep" {
		t.Fatalf("recovery touched escaping path: %q %v", value, err)
	}
}
