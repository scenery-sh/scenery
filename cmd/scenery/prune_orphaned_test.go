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

type fakeOrphanedDockerWithList struct {
	*fakeWorktreePostgresDocker
	listed []worktreePostgresContainer
}

func (f *fakeOrphanedDockerWithList) ListManagedContainers(context.Context) ([]worktreePostgresContainer, error) {
	return f.listed, nil
}

func TestPruneOrphanedDiscoversUnrecordedDockerContainers(t *testing.T) {
	home := localagent.PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	commandAgentPathsOverride = &home
	t.Cleanup(func() { commandAgentPathsOverride = nil })

	missingRoot := filepath.Join(t.TempDir(), "deleted-worktree")
	recentMissingRoot := filepath.Join(t.TempDir(), "recent-deleted-worktree")
	runningMissingRoot := filepath.Join(t.TempDir(), "running-deleted-worktree")
	existingRoot := t.TempDir()
	recentInstance := "11111111111111111111111111111111"
	runningInstance := "22222222222222222222222222222222"
	missingRec := localagent.WorktreeRecord{AppRoot: missingRoot, AppID: "factoryapp", UserID: os.Getuid(), Postgres: &localagent.WorktreePostgres{InstanceID: orphanedTestInstance}}
	recentRec := localagent.WorktreeRecord{AppRoot: recentMissingRoot, AppID: "factoryapp", UserID: os.Getuid(), Postgres: &localagent.WorktreePostgres{InstanceID: recentInstance}}
	runningRec := localagent.WorktreeRecord{AppRoot: runningMissingRoot, AppID: "factoryapp", UserID: os.Getuid(), Postgres: &localagent.WorktreePostgres{InstanceID: runningInstance}}
	existingRec := localagent.WorktreeRecord{AppRoot: existingRoot, AppID: "liveapp", UserID: os.Getuid(), Postgres: &localagent.WorktreePostgres{InstanceID: "fedcba9876543210fedcba9876543210"}}

	oldTime := time.Now().UTC().Add(-2 * time.Hour)
	base := &fakeWorktreePostgresDocker{
		id:        "owned-daemon",
		volume:    &worktreePostgresVolume{Name: "scenery-pg-data-" + orphanedTestInstance, Labels: worktreePostgresLabels(missingRec)},
		container: &worktreePostgresContainer{ID: "orphan-c", Name: "scenery-pg-" + orphanedTestInstance, Volume: "scenery-pg-data-" + orphanedTestInstance, Running: false, UpdatedAt: oldTime, Labels: worktreePostgresLabels(missingRec)},
	}
	docker := &fakeOrphanedDockerWithList{
		fakeWorktreePostgresDocker: base,
		listed: []worktreePostgresContainer{
			*base.container,
			{ID: "recent-c", Name: "scenery-pg-" + recentInstance, Volume: "scenery-pg-data-" + recentInstance, Running: false, UpdatedAt: time.Now().UTC(), Labels: worktreePostgresLabels(recentRec)},
			{ID: "running-c", Name: "scenery-pg-" + runningInstance, Volume: "scenery-pg-data-" + runningInstance, Running: true, UpdatedAt: oldTime, Labels: worktreePostgresLabels(runningRec)},
			{ID: "live-c", Name: "scenery-pg-fedcba9876543210fedcba9876543210", Volume: "scenery-pg-data-fedcba9876543210fedcba9876543210", Running: false, UpdatedAt: oldTime, Labels: worktreePostgresLabels(existingRec)},
		},
	}
	newOrphanedWorktreeDocker = func(context.Context) (worktreePostgresDocker, error) { return docker, nil }
	t.Cleanup(func() {
		newOrphanedWorktreeDocker = func(ctx context.Context) (worktreePostgresDocker, error) { return newWorktreeDockerClient(ctx) }
	})

	preview, err := runPruneOrphanedForTest(t, "--older-than", "1h")
	if err != nil {
		t.Fatal(err)
	}
	// Running container and existing-root container are excluded; recent exited container is kept by cutoff; old exited container is selected.
	if len(preview.Records) != 2 || orphanedSelected(preview.Records) != 1 || !preview.Records[0].Selected || preview.Records[0].Key != "docker:"+orphanedTestInstance || preview.Records[1].Selected {
		t.Fatalf("preview = %+v", preview)
	}
	applied, err := runPruneOrphanedForTest(t, "--older-than", "1h", "--yes", "--expect-revision", preview.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Removed != 1 || applied.Failed != 0 || base.container != nil || base.volume != nil {
		t.Fatalf("applied = %+v, container=%v volume=%v", applied, base.container, base.volume)
	}
}
