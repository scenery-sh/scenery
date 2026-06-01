//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
)

func TestCleanupSupersededDevSessionsStopsSameSessionChildren(t *testing.T) {
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
			"api": {PID: stale.Process.Pid, Owner: localagent.CaptureOwner(stale.Process.Pid, "test")},
		},
	}
	unrelated := localagent.Session{
		SessionID: "review-b",
		AppRoot:   root,
		OwnerPID:  os.Getpid(),
		Processes: map[string]localagent.Process{
			"api": {PID: other.Process.Pid, Owner: localagent.CaptureOwner(other.Process.Pid, "test")},
		},
	}

	if err := cleanupSupersededDevSessions(context.Background(), current, []localagent.Session{previous, unrelated}); err != nil {
		t.Fatalf("cleanupSupersededDevSessions: %v", err)
	}
	_, _ = stale.Process.Wait()
	waitForProcessExitForCleanupTest(t, stale.Process.Pid)
	if !processAliveForTest(other.Process.Pid) {
		t.Fatal("cleanup killed child from a different session")
	}
}

func TestMarkInconsistentStatusSessionsMarksDeadOwnerStale(t *testing.T) {
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
	})
	if sessions[0].Status != "running" {
		t.Fatalf("live status = %q, want running", sessions[0].Status)
	}
	if sessions[1].Status != "stale" {
		t.Fatalf("dead status = %q, want stale", sessions[1].Status)
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

func waitForProcessExitForCleanupTest(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAliveForTest(pid) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive", pid)
}
