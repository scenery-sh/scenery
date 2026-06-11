//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func TestCleanupSupersededDevSessionsStopsSameSessionChildren(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stale := startSleepProcessForCleanupTest(t)
	other := startSleepProcessForCleanupTest(t)
	defer func() {
		_ = killProcessTree(other)
		_, _ = other.Process.Wait()
	}()

	current := localagent.Session{
		SessionID: "review-a",
		AppRoot:   root,
		OwnerPID:  os.Getpid(),
		Owner:     localagent.CurrentOwner("test"),
	}
	previous := localagent.Session{
		SessionID: "review-a",
		AppRoot:   root,
		OwnerPID:  os.Getpid(),
		Processes: map[string]localagent.Process{
			"electric": {PID: stale.Process.Pid, Owner: localagent.CaptureOwner(stale.Process.Pid, "test")},
		},
	}
	unrelated := localagent.Session{
		SessionID: "review-b",
		AppRoot:   root,
		OwnerPID:  os.Getpid(),
		Processes: map[string]localagent.Process{
			"electric": {PID: other.Process.Pid, Owner: localagent.CaptureOwner(other.Process.Pid, "test")},
		},
	}

	if err := cleanupStaleDevSessionProcesses(context.Background(), current, []localagent.Session{previous, unrelated}); err != nil {
		t.Fatalf("cleanupStaleDevSessionProcesses: %v", err)
	}
	waitForProcessExitForCleanupTest(t, stale.Process.Pid)
	_, _ = stale.Process.Wait()
	if !processAliveForTest(other.Process.Pid) {
		t.Fatal("cleanup killed child from a different session")
	}
}

func TestCleanupStaleDevSessionProcessesStopsStateRootMatchedOrphans(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	current := localagent.Session{
		SessionID: "review-a",
		AppRoot:   root,
		StateRoot: filepath.Join(root, ".scenery", "sessions", "review-a"),
		OwnerPID:  os.Getpid(),
		Owner:     localagent.CurrentOwner("test"),
	}
	stale := startStateRootAppProcessForCleanupTest(t, current.StateRoot)
	otherStateRoot := filepath.Join(root, ".scenery", "sessions", "review-b")
	other := startStateRootAppProcessForCleanupTest(t, otherStateRoot)
	defer func() {
		_ = killProcessTree(other)
		_, _ = other.Process.Wait()
	}()

	if err := cleanupStaleDevSessionProcesses(context.Background(), current, nil); err != nil {
		t.Fatalf("cleanupStaleDevSessionProcesses: %v", err)
	}
	waitForProcessExitForCleanupTest(t, stale.Process.Pid)
	_, _ = stale.Process.Wait()
	if !processAliveForTest(other.Process.Pid) {
		t.Fatal("cleanup killed state-root matched child from a different session")
	}
}

func TestStopDeletedSessionProcessesStopsOwner(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	owner := startSleepProcessForCleanupTest(t)
	session := localagent.Session{
		SessionID: "review-a",
		AppRoot:   root,
		StateRoot: filepath.Join(root, ".scenery", "sessions", "review-a"),
		OwnerPID:  owner.Process.Pid,
		Owner:     localagent.CaptureOwner(owner.Process.Pid, "test"),
	}

	if err := stopDeletedSessionProcesses(context.Background(), session); err != nil {
		t.Fatalf("stopDeletedSessionProcesses: %v", err)
	}
	waitForProcessExitForCleanupTest(t, owner.Process.Pid)
	_, _ = owner.Process.Wait()
}

func TestStopDeletedSessionProcessesStopsStateRootMatchedOrphan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session := localagent.Session{
		SessionID: "review-a",
		AppRoot:   root,
		StateRoot: filepath.Join(root, ".scenery", "sessions", "review-a"),
	}
	stale := startStateRootAppProcessForCleanupTest(t, session.StateRoot)

	if err := stopDeletedSessionProcesses(context.Background(), session); err != nil {
		t.Fatalf("stopDeletedSessionProcesses: %v", err)
	}
	waitForProcessExitForCleanupTest(t, stale.Process.Pid)
	_, _ = stale.Process.Wait()
}

func TestMarkInconsistentStatusSessionsMarksDeadOwnerStale(t *testing.T) {
	t.Parallel()

	sessions := markInconsistentStatusSessions([]localagent.Session{
		{
			SessionID: "live",
			Status:    "running",
			OwnerPID:  os.Getpid(),
			Owner:     localagent.CurrentOwner("test"),
		},
		{
			SessionID: "dead",
			Status:    "running",
			OwnerPID:  99999999,
			Owner: localagent.Owner{
				PID:         99999999,
				StartedAt:   "not-live",
				CmdlineHash: "sha256:not-live",
				Exe:         "/not/live",
			},
		},
		{
			SessionID: "moved-owner",
			Status:    "running",
			OwnerPID:  os.Getpid(),
			Owner: localagent.Owner{
				PID:         99999998,
				StartedAt:   "stale-owner-field",
				CmdlineHash: "sha256:stale-owner-field",
				Exe:         "/stale/owner",
			},
		},
		{
			SessionID: "fingerprint-mismatch",
			Status:    "running",
			OwnerPID:  os.Getpid(),
			Owner: func() localagent.Owner {
				owner := localagent.CurrentOwner("test")
				owner.CmdlineHash = "sha256:not-current"
				return owner
			}(),
		},
	})
	if sessions[0].Status != "running" {
		t.Fatalf("live status = %q, want running", sessions[0].Status)
	}
	if sessions[1].Status != "stale" {
		t.Fatalf("dead status = %q, want stale", sessions[1].Status)
	}
	if sessions[1].StatusReason == "" {
		t.Fatal("dead owner status reason is empty")
	}
	if sessions[2].Status != "running" {
		t.Fatalf("moved owner status = %q, want running", sessions[2].Status)
	}
	if sessions[3].Status != "degraded" {
		t.Fatalf("fingerprint mismatch status = %q, want degraded", sessions[3].Status)
	}
	if !strings.Contains(sessions[3].StatusReason, "fingerprint mismatch") {
		t.Fatalf("fingerprint mismatch reason = %q", sessions[3].StatusReason)
	}
}

func TestMarkInconsistentStatusSessionsMarksConfiguredEdgeInternalRouterRouteDegraded(t *testing.T) {
	t.Parallel()

	sessions := markInconsistentStatusSessions([]localagent.Session{
		{
			SessionID: "custom-domain",
			Status:    "running",
			OwnerPID:  os.Getpid(),
			Owner:     localagent.CurrentOwner("test"),
			RouteNamespace: localagent.RouteNamespace{
				BaseDomain: "onlv.dev",
			},
			Routes: map[string]string{
				localagent.RouteDashboard: "https://console.custom-domain.onlv.dev:9440/",
			},
		},
	})
	if sessions[0].Status != "degraded" {
		t.Fatalf("status = %q, want degraded", sessions[0].Status)
	}
	for _, want := range []string{"onlv.dev", "internal/diagnostic router port 9440", "scenery system edge status"} {
		if !strings.Contains(sessions[0].StatusReason, want) {
			t.Fatalf("status reason missing %q: %q", want, sessions[0].StatusReason)
		}
	}
}

func TestPruneSessionEligibleKeepsLiveOwnerPIDWhenOwnerFieldIsStale(t *testing.T) {
	t.Parallel()

	session := localagent.Session{
		SessionID: "review-a",
		Status:    "running",
		UpdatedAt: time.Now().Add(-24 * time.Hour),
		OwnerPID:  os.Getpid(),
		Owner: localagent.Owner{
			PID:         99999997,
			StartedAt:   "stale-owner-field",
			CmdlineHash: "sha256:stale-owner-field",
			Exe:         "/stale/owner",
		},
	}
	if pruneSessionEligible(session, time.Now()) {
		t.Fatal("session with live owner_pid and stale owner field should not be pruned")
	}
}

func startSleepProcessForCleanupTest(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	configureChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep fixture: %v", err)
	}
	return cmd
}

func startStateRootAppProcessForCleanupTest(t *testing.T, stateRoot string) *exec.Cmd {
	t.Helper()
	appPath := filepath.Join(stateRoot, "run", "app", "scenery-app-test")
	if err := os.MkdirAll(filepath.Dir(appPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appPath, []byte("#!/bin/sh\nsleep \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Concurrent fork/exec in parallel tests can briefly hold the freshly
	// written script's write fd, so retry ETXTBSY starts.
	deadline := time.Now().Add(2 * time.Second)
	for {
		cmd := exec.Command(appPath, "30")
		configureChildProcess(cmd)
		err := cmd.Start()
		if err == nil {
			return cmd
		}
		if !errors.Is(err, syscall.ETXTBSY) || time.Now().After(deadline) {
			t.Fatalf("start app path sleep fixture: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForProcessExitForCleanupTest(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if info, ok := inspectProcess(pid); !ok || strings.Contains(info.stat, "Z") {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive", pid)
}
