package workspacetx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverHonorsFailClosedOwnerStates(t *testing.T) {
	tests := []struct {
		name         string
		state        ownerState
		ownerRead    bool
		journalOnly  bool
		wantBlocked  bool
		wantArtifact bool
	}{
		{name: "unknown lock blocks normal read", state: ownerUnknown, wantBlocked: true, wantArtifact: true},
		{name: "unknown journal blocks owner read", state: ownerUnknown, ownerRead: true, journalOnly: true, wantBlocked: true, wantArtifact: true},
		{name: "live lock blocks owner read", state: ownerLive, ownerRead: true, wantBlocked: true, wantArtifact: true},
		{name: "current lock blocks normal read", state: ownerCurrent, wantBlocked: true, wantArtifact: true},
		{name: "current journal allows owner read", state: ownerCurrent, ownerRead: true, journalOnly: true, wantArtifact: true},
		{name: "recoverable lock is removed", state: ownerRecoverable},
		{name: "recoverable journal is removed", state: ownerRecoverable, journalOnly: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			transactionRoot := filepath.Join(root, ".scenery", "transactions")
			transactionDir := filepath.Join(transactionRoot, "change-test")
			if err := os.MkdirAll(transactionDir, 0o700); err != nil {
				t.Fatal(err)
			}
			lock, journal := NewArtifacts(transactionDir, "")
			artifactPath := filepath.Join(transactionRoot, "change.lock")
			artifact := any(lock)
			if test.journalOnly {
				artifactPath = filepath.Join(transactionRoot, "change-apply.json")
				artifact = journal
			}
			encoded, err := json.Marshal(artifact)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(artifactPath, append(encoded, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}

			err = recoverWithOwnerInspector(root, false, test.ownerRead, func(Owner) ownerState { return test.state })
			if test.wantBlocked {
				if err == nil || !strings.Contains(err.Error(), "workspace change transaction is active") {
					t.Fatalf("recover error = %v, want active transaction", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if exists := pathExists(artifactPath); exists != test.wantArtifact {
				t.Fatalf("artifact exists = %v, want %v", exists, test.wantArtifact)
			}
			if exists := pathExists(transactionDir); exists != test.wantArtifact {
				t.Fatalf("transaction directory exists = %v, want %v", exists, test.wantArtifact)
			}
		})
	}
}
