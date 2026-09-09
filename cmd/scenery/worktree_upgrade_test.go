package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/stateupgrade"
)

func TestWorktreeUpgradeArgumentContract(t *testing.T) {
	preview, err := parseWorktreeArgs([]string{"upgrade", "--app-root", "/app", "-o", "json"})
	if err != nil || preview.Yes || !preview.JSON {
		t.Fatalf("read-only preview: %+v, %v", preview, err)
	}
	revision := "sha256:" + strings.Repeat("a", 64)
	apply, err := parseWorktreeArgs([]string{"upgrade", "--yes", "--expect-revision", revision})
	if err != nil || !apply.Yes || apply.ExpectedRevision != revision {
		t.Fatalf("explicit apply: %+v, %v", apply, err)
	}
	for _, args := range [][]string{
		{"upgrade", "--yes"}, {"upgrade", "--expect-revision", revision},
		{"upgrade", "name"}, {"upgrade", "--from", "main"},
		{"list", "--yes"}, {"remove", "name", "--expect-revision", revision},
	} {
		if _, err := parseWorktreeArgs(args); err == nil {
			t.Fatalf("unsupported arguments accepted: %v", args)
		}
	}
}

func TestWorktreeUpgradePreviewsAppliesAndValidatesSchema(t *testing.T) {
	paths, err := localagent.PathsForWorktree(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, acquire := range []func() (*localagent.ProcessLock, error){paths.AcquireLiveLock, paths.AcquireOperationLock} {
		lock, err := acquire()
		if err != nil {
			t.Fatal(err)
		}
		if err := lock.Release(); err != nil {
			t.Fatal(err)
		}
	}
	record := localagent.NewWorktreeRecord(paths, "upgrade-test")
	record.SpecRevision = "sha256:" + strings.Repeat("b", 64)
	before, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Record, before, 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := upgradeRetainedWorktree(t.Context(), paths, record.AppID, worktreeOptions{})
	if err != nil || preview.ChangedFiles != 1 || preview.Applied || preview.Backup != "" {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	actual, err := os.ReadFile(paths.Record)
	if err != nil || !bytes.Equal(before, actual) {
		t.Fatal("preview changed worktree metadata")
	}
	if _, err := upgradeRetainedWorktree(t.Context(), paths, record.AppID, worktreeOptions{Yes: true, ExpectedRevision: "sha256:" + strings.Repeat("c", 64)}); !errors.Is(err, stateupgrade.ErrPrecondition) {
		t.Fatalf("stale approval accepted: %v", err)
	}
	lock, err := paths.AcquireLiveLock()
	if err != nil {
		t.Fatal(err)
	}
	_, busyErr := upgradeRetainedWorktree(t.Context(), paths, record.AppID, worktreeOptions{})
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(busyErr, localagent.ErrProcessLocked) {
		t.Fatalf("live owner accepted: %v", busyErr)
	}
	applied, err := upgradeRetainedWorktree(t.Context(), paths, record.AppID, worktreeOptions{Yes: true, ExpectedRevision: preview.Revision})
	if err != nil || !applied.Applied || applied.Pending || applied.UpdatedFiles != 1 || applied.Backup == "" {
		t.Fatalf("apply: %+v, %v", applied, err)
	}
	if _, err := paths.LoadRecord(record.AppID); err != nil {
		t.Fatalf("current reader rejected upgrade: %v", err)
	}
	current, err := upgradeRetainedWorktree(t.Context(), paths, record.AppID, worktreeOptions{})
	if err != nil || current.ChangedFiles != 0 {
		t.Fatalf("current no-op: %+v, %v", current, err)
	}
	for _, result := range []worktreeUpgradeResult{preview, applied, current} {
		if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", worktreeUpgradeKind+".schema.json"), result); len(diagnostics) != 0 {
			t.Fatalf("upgrade schema: %+v", diagnostics)
		}
	}
}
