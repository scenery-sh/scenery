package main

import (
	"encoding/json"
	"os/exec"
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

	// A live child process with a captured fingerprint stands in for a
	// session owner; the test process itself is excluded by
	// addHarnessCleanupPID, and a dead PID must never verify.
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _, _ = child.Process.Wait() })
	self := localagent.CaptureOwner(child.Process.Pid, "test owner")
	if err := localagent.VerifyOwner(self); err != nil {
		t.Fatalf("child owner fingerprint does not verify: %v", err)
	}
	stale := localagent.Owner{PID: 999999999}
	sessions := []localagent.Session{
		{
			SessionID: "verified",
			OwnerPID:  self.PID,
			Owner:     self,
			Processes: map[string]localagent.Process{
				"frontend": {PID: self.PID, Owner: self},
				"stale":    {PID: 999999998, Owner: stale},
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
	harnessCleanupPIDsFromSessions(pids, sessions)
	if len(pids) != 1 || !pids[self.PID] {
		t.Fatalf("cleanup pids = %v, want only %d", pids, self.PID)
	}
}
