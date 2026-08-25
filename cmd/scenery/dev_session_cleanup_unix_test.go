//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func TestCleanupSupersededDevSessionsSelectsSameSessionInProcess(t *testing.T) {
	t.Parallel()

	current := localagent.Session{
		SessionID: "review-a",
		AppRoot:   "/app",
	}
	previous := localagent.Session{
		SessionID: "review-a",
		AppRoot:   "/app",
	}
	unrelated := localagent.Session{
		SessionID: "review-b",
		AppRoot:   "/app",
	}
	var stopped []string
	err := cleanupStaleDevSessionProcessesWithDependencies(context.Background(), current, []localagent.Session{previous, unrelated}, staleDevSessionCleanupDependencies{
		sameScope: sameAgentSession,
		stopRegistered: func(_ context.Context, _ localagent.Session, session localagent.Session, seen map[int]bool) error {
			stopped = append(stopped, session.SessionID)
			seen[41001] = true
			return nil
		},
		stopCommands: func(_ context.Context, _ localagent.Session, seen map[int]bool) error {
			if !seen[41001] {
				t.Fatalf("command cleanup seen = %v", seen)
			}
			return nil
		},
		stopEnvironment: func(_ context.Context, _ localagent.Session, seen map[int]bool) error {
			if !seen[41001] {
				t.Fatalf("environment cleanup seen = %v", seen)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stopped, []string{"review-a"}) {
		t.Fatalf("stopped sessions = %v", stopped)
	}
}

func TestStopDeletedSessionProcessesSelectsOwnerInProcess(t *testing.T) {
	t.Parallel()

	session := localagent.Session{
		SessionID: "review-a",
		AppRoot:   "/app",
		OwnerPID:  41001,
	}
	var ownerPIDs, childPIDs []int
	err := stopDeletedSessionProcessesWithDependencies(context.Background(), session, stopDeletedSessionProcessDependencies{
		shouldSignalOwner: func(got localagent.Session) bool { return got.SessionID == session.SessionID },
		stopOwner: func(_ context.Context, pid int) error {
			ownerPIDs = append(ownerPIDs, pid)
			return nil
		},
		processPIDs: func(localagent.Session) []int { return []int{41001, 41002, 41002} },
		stopChild: func(_ context.Context, pid int) error {
			childPIDs = append(childPIDs, pid)
			return nil
		},
		stopCommands: func(_ context.Context, _ localagent.Session, seen map[int]bool) error {
			if !seen[41001] || !seen[41002] {
				t.Fatalf("command cleanup seen = %v", seen)
			}
			return nil
		},
		stopEnvironment: func(_ context.Context, _ localagent.Session, seen map[int]bool) error {
			if !seen[41001] || !seen[41002] {
				t.Fatalf("environment cleanup seen = %v", seen)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ownerPIDs, []int{41001}) || !slices.Equal(childPIDs, []int{41002}) {
		t.Fatalf("stopped owner/children = %v/%v", ownerPIDs, childPIDs)
	}
}

func TestStopDeletedSessionProcessesSelectsStateRootMatchedOrphanInProcess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, ".scenery", "sessions", "review-a")
	otherStateRoot := filepath.Join(root, ".scenery", "sessions", "review-b")
	session := localagent.Session{SessionID: "review-a", AppRoot: root, StateRoot: stateRoot}
	ps := strings.Join([]string{
		"41001 S " + filepath.Join(stateRoot, "run", "app", "scenery-app-review-a") + " 30",
		"41002 S " + filepath.Join(otherStateRoot, "run", "app", "scenery-app-review-b") + " 30",
		"41003 Z " + filepath.Join(stateRoot, "run", "app", "scenery-app-zombie") + " 30",
	}, "\n")
	var stopped []int
	seen := map[int]bool{}
	err := stopSessionCommandProcessesFromPS(context.Background(), session, seen, ps, func(_ context.Context, pid int) error {
		stopped = append(stopped, pid)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stopped, []int{41001}) {
		t.Fatalf("stopped PIDs = %v, want matched live orphan only", stopped)
	}
	if !seen[41001] || seen[41002] || seen[41003] {
		t.Fatalf("seen PIDs = %v", seen)
	}
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
			RouteManifest: localagent.RouteManifest{Routes: map[string]localagent.RouteRecord{
				localagent.RouteDashboard: {URL: "https://console.custom-domain.onlv.dev:9440/"},
			}},
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
