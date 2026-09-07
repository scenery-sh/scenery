package workspacetx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverPublicationBetweenBackupAndInstall(t *testing.T) {
	root, journal := publicationRecoveryFixture(t)
	if err := recoverWithOwnerInspector(root, false, false, func(Owner) ownerState { return ownerRecoverable }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, journal.Entries[0].Path))
	if err != nil || string(data) != "before\n" {
		t.Fatalf("restored target = %q, %v", data, err)
	}
	if pathExists(journal.Directory) || pathExists(filepath.Join(root, ".scenery/transactions/change.lock")) {
		t.Fatal("recovery left transaction state")
	}
}

func TestRecoverPublicationRejectsEditedBackup(t *testing.T) {
	root, journal := publicationRecoveryFixture(t)
	if err := os.WriteFile(journal.Entries[0].Backup, []byte("external edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := recoverWithOwnerInspector(root, false, false, func(Owner) ownerState { return ownerRecoverable })
	if err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("edited backup accepted: %v", err)
	}
	data, err := os.ReadFile(journal.Entries[0].Backup)
	if err != nil || string(data) != "external edit\n" {
		t.Fatalf("edited backup changed: %q %v", data, err)
	}
}

func TestRecoverCommittedPublicationOnlyCleansMetadata(t *testing.T) {
	root, journal := publicationRecoveryFixture(t)
	entry := journal.Entries[0]
	if err := os.Rename(entry.Stage, filepath.Join(root, entry.Path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal.Receipt, []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recoverWithOwnerInspector(root, false, false, func(Owner) ownerState { return ownerRecoverable }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, entry.Path))
	if err != nil || string(data) != "after\n" {
		t.Fatalf("committed bytes rolled back: %q %v", data, err)
	}
}

func publicationRecoveryFixture(t *testing.T) (string, Journal) {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, ".scenery/transactions/change-test")
	lock, journal := NewArtifacts(directory, filepath.Join(directory, "committed"))
	entry := Entry{Path: "contract.go", Stage: filepath.Join(directory, "staged/000000"), Backup: filepath.Join(directory, "backups/000000"), BeforeExists: true, BeforeDigest: digest([]byte("before\n")), AfterExists: true, AfterDigest: digest([]byte("after\n"))}
	journal.Entries = []Entry{entry}
	for path, data := range map[string][]byte{entry.Stage: []byte("after\n"), entry.Backup: []byte("before\n")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, artifact := range map[string]any{"change.lock": lock, "change-apply.json": journal} {
		data, err := json.Marshal(artifact)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(directory), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, journal
}
