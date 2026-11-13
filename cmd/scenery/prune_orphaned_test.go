package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

const orphanedTestInstance = "0123456789abcdef0123456789abcdef"

// retainOrphanedWorktree writes the retained record of an absent checkout and
// returns its paths; with database it claims a managed cluster on daemon.
func retainOrphanedWorktree(t *testing.T, home, root, daemon string) localagent.WorktreePaths {
	t.Helper()
	paths, err := localagent.PathsForWorktree(home, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	database := ""
	if daemon != "" {
		database = fmt.Sprintf(`,"postgres":{"instance_id":%q,"daemon_id":%q,"daemon_endpoint":"unix:///owned/docker.sock","container":"scenery-pg-%s","volume":"scenery-pg-data-%s"}`, orphanedTestInstance, daemon, orphanedTestInstance, orphanedTestInstance)
	}
	record := fmt.Sprintf(`{"kind":"scenery.worktree","schema_revision":"sha256:old","app_root":%q,"app_id":"books","user_id":%d,"updated_at":"2020-01-01T00:00:00Z"%s}`, paths.AppRoot, os.Getuid(), database)
	if err := os.WriteFile(paths.Record, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	// The record's last write bounds its age.
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(paths.Record, old, old); err != nil {
		t.Fatal(err)
	}
	return paths
}

func orphanedTestDocker(t *testing.T, paths localagent.WorktreePaths) *fakeWorktreePostgresDocker {
	t.Helper()
	record := localagent.WorktreeRecord{AppRoot: paths.AppRoot, AppID: "books", UserID: os.Getuid(), Postgres: &localagent.WorktreePostgres{InstanceID: orphanedTestInstance}}
	labels := worktreePostgresLabels(record)
	docker := &fakeWorktreePostgresDocker{id: "owned-daemon",
		volume:    &worktreePostgresVolume{Name: "scenery-pg-data-" + orphanedTestInstance, Labels: labels},
		container: &worktreePostgresContainer{ID: "owned-container", Name: "scenery-pg-" + orphanedTestInstance, Volume: "scenery-pg-data-" + orphanedTestInstance, Running: true, Labels: labels},
	}
	newOrphanedWorktreeDocker = func(context.Context) (worktreePostgresDocker, error) { return docker, nil }
	t.Cleanup(func() {
		newOrphanedWorktreeDocker = func(ctx context.Context) (worktreePostgresDocker, error) { return newWorktreeDockerClient(ctx) }
	})
	return docker
}

func runPruneOrphanedForTest(t *testing.T, args ...string) (pruneOrphanedResponse, error) {
	t.Helper()
	var stdout bytes.Buffer
	err := runWorktreePrune(context.Background(), &stdout, append([]string{"--orphaned", "-o", "json"}, args...))
	var response pruneOrphanedResponse
	if stdout.Len() > 0 {
		if decodeErr := decodeCLIJSON(stdout.Bytes(), &response); decodeErr != nil {
			t.Fatalf("decode %q: %v", stdout.String(), decodeErr)
		}
	}
	return response, err
}

func TestPruneOrphanedRemovesOnlyThePreviewedSelection(t *testing.T) {
	home := localagent.PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	commandAgentPathsOverride = &home
	t.Cleanup(func() { commandAgentPathsOverride = nil })
	paths := retainOrphanedWorktree(t, home.Home, filepath.Join(t.TempDir(), "gone"), "owned-daemon")
	docker := orphanedTestDocker(t, paths)
	if err := os.WriteFile(filepath.Join(paths.Directory, "restore.zip"), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}

	preview, err := runPruneOrphanedForTest(t, "--older-than", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Applied || len(preview.Records) != 1 || !preview.Records[0].Selected || preview.Records[0].Removed || preview.Records[0].Database == nil || !strings.Contains(preview.Next, preview.Revision) {
		t.Fatalf("preview = %+v", preview)
	}
	if _, err := os.Stat(paths.Record); err != nil || docker.writes != 0 {
		t.Fatalf("preview changed state: stat = %v, docker writes = %d", err, docker.writes)
	}
	if _, err := runPruneOrphanedForTest(t, "--older-than", "1h", "--yes", "--expect-revision", "sha256:stale"); err == nil || docker.writes != 0 {
		t.Fatalf("a stale revision applied: err = %v, docker writes = %d", err, docker.writes)
	}
	applied, err := runPruneOrphanedForTest(t, "--older-than", "1h", "--yes", "--expect-revision", preview.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Removed != 1 || !applied.Records[0].Removed || docker.volume != nil || docker.container != nil {
		t.Fatalf("applied = %+v, volume = %v, container = %v", applied, docker.volume, docker.container)
	}
	if _, err := os.Lstat(paths.Directory); !os.IsNotExist(err) {
		t.Fatalf("directory stat = %v", err)
	}
}

func TestPruneOrphanedKeepsUnverifiedDatabaseAndRecentState(t *testing.T) {
	home := localagent.PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	commandAgentPathsOverride = &home
	t.Cleanup(func() { commandAgentPathsOverride = nil })
	foreign := retainOrphanedWorktree(t, home.Home, filepath.Join(t.TempDir(), "gone"), "another-daemon")
	docker := orphanedTestDocker(t, foreign)
	recent := retainOrphanedWorktree(t, home.Home, filepath.Join(t.TempDir(), "recent"), "")
	if err := os.Chtimes(recent.Record, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	preview, err := runPruneOrphanedForTest(t, "--older-than", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Records) != 2 || preview.Next != "" {
		t.Fatalf("preview = %+v", preview)
	}
	for _, record := range preview.Records {
		if record.Selected || record.Reason == "" {
			t.Fatalf("record = %+v, want kept with a reason", record)
		}
	}
	applied, err := runPruneOrphanedForTest(t, "--older-than", "1h", "--yes", "--expect-revision", preview.Revision)
	if err != nil || applied.Removed != 0 || docker.writes != 0 {
		t.Fatalf("applied = %+v, err = %v, docker writes = %d", applied, err, docker.writes)
	}
	for _, paths := range []localagent.WorktreePaths{foreign, recent} {
		if _, err := os.Stat(paths.Record); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPruneOrphanedRejectsScopedAndHalfConfirmedRequests(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--orphaned", "--older-than", "1h", "--yes"},
		{"--orphaned", "--older-than", "1h", "--expect-revision", "sha256:x"},
		{"--orphaned", "--older-than", "1h", "--db"},
		{"--orphaned", "--older-than", "1h", "--app-root", "/tmp/app"},
		{"--older-than", "1h", "--yes", "--expect-revision", "sha256:x"},
	} {
		if _, err := parsePruneArgs(args); err == nil {
			t.Errorf("parsePruneArgs(%q) succeeded", args)
		}
	}
}
