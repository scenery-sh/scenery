package evolution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/workspacetx"
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
	before := []byte("application \"app\" {}\n")
	after := []byte("application \"app\" {}\n\n")
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

	if _, err := compiler.Compile(root); err == nil || !strings.Contains(err.Error(), "workspace change transaction is active") {
		t.Fatalf("compiler.Compile() error = %v, want active transaction rejection", err)
	}
	if _, err := compiler.CompileDuringChangeTransaction(root); err != nil {
		t.Fatalf("transaction owner could not verify its staged result: %v", err)
	}
}

func TestCommittedResultRevalidationDetectsWorkspaceDrift(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), root)
	excludedPath := filepath.Join(root, "revision-excluded.txt")
	if err := os.WriteFile(excludedPath, []byte("checked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stagedRoot, err := cloneWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stagedRoot)
	staged, checkedFiles, err := validateStagedWorkspace(stagedRoot, false)
	if err != nil || !staged.Valid() {
		t.Fatalf("staged compile: %v diagnostics=%#v", err, staged.Diagnostics)
	}
	actual, err := revalidateCommittedResult(root, staged, checkedFiles)
	if err != nil || actual.WorkspaceRevision != staged.WorkspaceRevision {
		t.Fatalf("unchanged workspace revalidation: %v staged=%s actual=%s", err, staged.WorkspaceRevision, actual.WorkspaceRevision)
	}

	if err := os.WriteFile(excludedPath, []byte("changed after check\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := revalidateCommittedResult(root, staged, checkedFiles); err == nil || !strings.Contains(err.Error(), "differs from checked staging") {
		t.Fatalf("revision-excluded drift error = %v", err)
	}
	if err := os.WriteFile(excludedPath, []byte("checked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extra.scn"), []byte("# changed after check\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := revalidateCommittedResult(root, staged, checkedFiles); err == nil || !strings.Contains(err.Error(), "differs from checked staging") {
		t.Fatalf("source drift error = %v", err)
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
	lock, _ := workspacetx.NewArtifacts(outside, "")
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

func TestChangeTransactionRecoveryRefusesLegacyStateWithoutModification(t *testing.T) {
	for _, test := range []struct {
		name, file, apiVersion string
	}{
		{name: "lock", file: "change.lock", apiVersion: "scenery.change-transaction-lock/v1"},
		{name: "journal", file: "change-apply.json", apiVersion: "scenery.change-transaction/v1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, ".scenery", "transactions")
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, test.file)
			legacy := []byte(`{"api_version":"` + test.apiVersion + `","sentinel":"keep"}`)
			if err := os.WriteFile(path, legacy, 0o600); err != nil {
				t.Fatal(err)
			}
			err := recoverInterruptedChangeTransaction(root, true)
			if err == nil || !strings.Contains(err.Error(), "previous Scenery binary") || !strings.Contains(err.Error(), "no state was modified") {
				t.Fatalf("recovery error = %v, want precise legacy-state refusal", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || string(got) != string(legacy) {
				t.Fatalf("legacy state changed: got %q, err %v", got, readErr)
			}
		})
	}
}
