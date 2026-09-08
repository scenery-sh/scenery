//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"os"

	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

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
