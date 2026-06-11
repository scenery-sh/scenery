package main

import (
	"errors"
	"os"
	"testing"

	"scenery.sh/internal/devdash"
)

func TestDashboardRunStateWriteAndRemove(t *testing.T) {
	t.Parallel()

	state := newDashboardRunState("/tmp/app", devdash.DashboardAddr)
	state.cacheRoot = t.TempDir()
	if err := state.write(); err != nil {
		t.Fatalf("write() error = %v", err)
	}

	path, err := state.path()
	if err != nil {
		t.Fatalf("path() error = %v", err)
	}
	got, err := loadDashboardRunState(path)
	if err != nil {
		t.Fatalf("loadDashboardRunState(%q) error = %v", path, err)
	}
	if got.SupervisorPID != state.SupervisorPID || got.AppRoot != state.AppRoot || got.DashboardAddr != state.DashboardAddr {
		t.Fatalf("loaded state = %+v, want %+v", got, state)
	}

	if err := state.remove(); err != nil {
		t.Fatalf("remove() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state file still exists after remove: %v", err)
	}
}

func TestReapOwnedDashboardRemovesStaleStateForMissingProcess(t *testing.T) {
	t.Parallel()

	state := dashboardRunState{
		SupervisorPID: 1 << 30,
		AppRoot:       "/tmp/app",
		DashboardAddr: devdash.DashboardAddr,
		cacheRoot:     t.TempDir(),
	}
	if err := state.write(); err != nil {
		t.Fatalf("write() error = %v", err)
	}

	path, err := state.path()
	if err != nil {
		t.Fatalf("path() error = %v", err)
	}
	if err := reapOwnedDashboard(path, state); err != nil {
		t.Fatalf("reapOwnedDashboard(%q) error = %v", path, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale state file still exists: %v", err)
	}
}

func TestReapOwnedDashboardKeepsDifferentAppState(t *testing.T) {
	t.Parallel()

	cacheRoot := t.TempDir()
	written := dashboardRunState{
		SupervisorPID: os.Getpid(),
		AppRoot:       "/tmp/other",
		DashboardAddr: devdash.DashboardAddr,
		cacheRoot:     cacheRoot,
	}
	if err := written.write(); err != nil {
		t.Fatalf("write() error = %v", err)
	}

	path, err := written.path()
	if err != nil {
		t.Fatalf("path() error = %v", err)
	}
	expected := dashboardRunState{
		SupervisorPID: os.Getpid(),
		AppRoot:       "/tmp/app",
		DashboardAddr: devdash.DashboardAddr,
		cacheRoot:     cacheRoot,
	}
	if err := reapOwnedDashboard(path, expected); err != nil {
		t.Fatalf("reapOwnedDashboard(%q) error = %v", path, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("different-app state file should remain, got err=%v", err)
	}
}
