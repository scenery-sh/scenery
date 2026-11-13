package evolution

import (
	"os"
	"path/filepath"
	"testing"

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
	rollback, _, err := commitPlannedEdits(root, edits)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first.scn", "second.scn"} {
		if value, _ := os.ReadFile(filepath.Join(root, name)); string(value) != name[:len(name)-4]+"-after" {
			t.Fatalf("%s was not applied: %q", name, value)
		}
	}
	if _, _, err := commitPlannedEdits(root, edits); err == nil {
		t.Fatal("concurrent workspace transaction unexpectedly acquired the lock")
	}
	// Simulate process death before the transaction is finalized.
	if err := workspacetx.ForceRecover(root); err != nil {
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
