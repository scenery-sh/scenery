package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestHarnessStorageProbeNoState(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, entry, kind string
		wantErr           bool
	}{
		{name: "missing home", kind: "missing"},
		{name: "empty home"},
		{name: "telemetry", entry: "telemetry.jsonl", kind: "file"},
		{name: "worktree state", entry: "worktrees", kind: "directory", wantErr: true},
		{name: "lock", entry: "agent.lock", kind: "file", wantErr: true},
		{name: "telemetry directory", entry: "telemetry.jsonl", kind: "directory", wantErr: true},
		{name: "telemetry symlink", entry: "telemetry.jsonl", kind: "symlink", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			home := filepath.Join(t.TempDir(), "home")
			if tt.kind != "missing" {
				if err := os.Mkdir(home, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(home, tt.entry)
			var err error
			switch tt.kind {
			case "file":
				err = os.WriteFile(path, []byte("{}\n"), 0o600)
			case "directory":
				err = os.Mkdir(path, 0o700)
			case "symlink":
				err = os.Symlink("missing", path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := verifyHarnessStorageProbeNoState(home); (err != nil) != tt.wantErr {
				t.Fatalf("unallocated home inspection = %v, want error %t", err, tt.wantErr)
			}
		})
	}
}

func TestHarnessDetachInfoReadsCLIEnvelope(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(newCLIEnvelope(true, map[string]any{"kind": "scenery.dev.detach", "schema_revision": "sha256:test", "pid": 4242, "session": map[string]any{"backends": map[string]localagent.Backend{localagent.RouteAPI: {Network: "unix", Addr: "/tmp/api.sock"}}}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	apiSocket, pid, err := harnessDetachInfo(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if apiSocket != "/tmp/api.sock" || pid != 4242 {
		t.Fatalf("detach info = %q, %d, want /tmp/api.sock, 4242", apiSocket, pid)
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
	owners := map[int]localagent.Owner{}
	harnessCleanupOwnersFromSessions(owners, sessions, verifyOwner)
	if len(owners) != 1 || owners[verified.PID].StartedAt != verified.StartedAt {
		t.Fatalf("cleanup owners = %v, want only %d", owners, verified.PID)
	}
}
