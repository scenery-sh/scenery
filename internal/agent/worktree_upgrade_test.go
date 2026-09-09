package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/stateupgrade"
)

func upgradeRecordFixture(t *testing.T) (WorktreePaths, WorktreeRecord, []byte) {
	t.Helper()
	p, err := PathsForWorktree(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Prepare(); err != nil {
		t.Fatal(err)
	}
	record := NewWorktreeRecord(p, "upgrade-test")
	record.SpecRevision = "sha256:" + strings.Repeat("b", 64)
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Record, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, record, data
}

func TestPrepareWorktreeUpgradePreservesAuthority(t *testing.T) {
	p, original, data := upgradeRecordFixture(t)
	if _, err := p.LoadRecord(original.AppID); err == nil {
		t.Fatal("ordinary read accepted an old specification")
	}
	record, changes, err := p.PrepareSpecUpgrade(original.AppID)
	if err != nil || len(changes) != 1 || record.AppRoot != original.AppRoot || !record.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("prepare: %v", err)
	}
	if !bytes.Equal(changes[0].Before, data) || !machine.ArtifactPayloadEqual(changes[0].Before, changes[0].After) {
		t.Fatal("authority payload changed")
	}
	actual, err := os.ReadFile(p.Record)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatalf("preparation wrote state: %v", err)
	}
	if _, _, err := p.PrepareSpecUpgrade("foreign-app"); err == nil {
		t.Fatal("foreign ownership accepted")
	}
	if err := os.WriteFile(p.Record, changes[0].After, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Directory, stateupgrade.PendingName), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadRecord(original.AppID); !errors.Is(err, stateupgrade.ErrPrecondition) {
		t.Fatalf("ordinary read bypassed pending transaction: %v", err)
	}
}

func TestWorktreeUpgradeRetainsStoppedRegistryAndRejectsLiveOwner(t *testing.T) {
	p, record, _ := upgradeRecordFixture(t)
	registry := registryFile{ArtifactIdentity: agentRegistryIdentity(), Sessions: []Session{{SessionID: "stopped", AppRoot: p.AppRoot, BaseAppID: record.AppID, OwnerPID: 123, Owner: Owner{PID: 123, StartedAt: "recorded-start"}}}}
	registry.SpecRevision = record.SpecRevision
	data, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	path := p.ControlPaths().RegistryPath
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.prepareSpecUpgrade(record.AppID, func(Owner) error { return nil }); err == nil {
		t.Fatal("live retained process accepted")
	}
	_, changes, err := p.prepareSpecUpgrade(record.AppID, func(Owner) error { return errors.New("process no longer exists") })
	if err != nil || len(changes) != 2 || !bytes.Equal(data, changes[1].Before) || !machine.ArtifactPayloadEqual(data, changes[1].After) {
		t.Fatalf("stopped history was not retained: %v", err)
	}
}

func TestExistingWorktreeUpgradeLocksNeverAllocate(t *testing.T) {
	p, err := PathsForWorktree(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcquireExistingOperationLock(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing operation lock: %v", err)
	}
	if _, err := os.Stat(p.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview created retained state")
	}
	lock, err := p.AcquireOperationLock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	if _, err := p.AcquireExistingOperationLock(); !errors.Is(err, ErrProcessLocked) {
		t.Fatalf("concurrent operation accepted: %v", err)
	}
}
