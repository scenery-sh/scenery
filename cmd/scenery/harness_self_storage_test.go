package main

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestHarnessDetachInfoReadsCLIEnvelope(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(newCLIEnvelope(true, map[string]any{"kind": "scenery.dev.detach", "schema_revision": "sha256:test", "pid": 4242, "session": map[string]any{"state_root": "/tmp/state"}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	stateRoot, pid, err := harnessDetachInfo(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if stateRoot != "/tmp/state" || pid != 4242 {
		t.Fatalf("detach info = %q, %d, want /tmp/state, 4242", stateRoot, pid)
	}

	// The detached child PID must survive even when the state root is
	// missing, so cleanup can always target the child directly.
	encoded, err = json.Marshal(newCLIEnvelope(true, map[string]any{"kind": "scenery.dev.detach", "schema_revision": "sha256:test", "pid": 555, "session": map[string]any{}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	_, pid, err = harnessDetachInfo(string(encoded))
	if err == nil || pid != 555 {
		t.Fatalf("missing state root: pid = %d, err = %v", pid, err)
	}
}

// TestHarnessCleanupPIDsFromSessions proves the registry fallback only
// targets fingerprint-verified owners, so a stale sessions.json from a
// crashed run cannot signal a reused PID.
func TestHarnessCleanupPIDsFromSessions(t *testing.T) {
	t.Parallel()

	verifiedPID := os.Getpid() + 1000
	stalePID := verifiedPID + 1
	verified := localagent.Owner{PID: verifiedPID, StartedAt: "verified"}
	stale := localagent.Owner{PID: stalePID, StartedAt: "stale"}
	verifyOwner := func(owner localagent.Owner) error {
		if owner.StartedAt == "verified" {
			return nil
		}
		return errors.New("owner fingerprint mismatch")
	}
	sessions := []localagent.Session{
		{
			SessionID: "verified",
			OwnerPID:  verified.PID,
			Owner:     verified,
			Processes: map[string]localagent.Process{
				"frontend": {PID: verified.PID, Owner: verified},
				"stale":    {PID: stale.PID, Owner: stale},
			},
		},
		{
			SessionID: "stale-owner",
			OwnerPID:  stale.PID,
			Owner:     stale,
		},
		{
			SessionID: "missing-owner",
		},
	}
	pids := map[int]bool{}
	harnessCleanupPIDsFromSessions(pids, sessions, verifyOwner)
	if len(pids) != 1 || !pids[verified.PID] {
		t.Fatalf("cleanup pids = %v, want only %d", pids, verified.PID)
	}
}
