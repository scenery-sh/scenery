package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

// TestStopRecordedStaleAgentActsOnlyOnAVerifiedRecord proves stale-agent
// cleanup consults only this agent home's owner record: without a record,
// with a record naming this process, or with a record whose identity no longer
// matches the live PID, nothing is signaled. Another home's live agent is never
// selected because no command line or router address is consulted.
func TestStopRecordedStaleAgentActsOnlyOnAVerifiedRecord(t *testing.T) {
	t.Parallel()

	paths := localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	if err := stopRecordedStaleAgent(paths, 10*time.Millisecond); err != nil {
		t.Fatalf("missing record: %v", err)
	}
	if err := localagent.WriteAgentOwner(paths); err != nil {
		t.Fatal(err)
	}
	if err := stopRecordedStaleAgent(paths, 10*time.Millisecond); err != nil {
		t.Fatalf("record naming this process: %v", err)
	}
	record, err := localagent.LoadAgentOwner(paths)
	if err != nil {
		t.Fatal(err)
	}
	// PID 1 is live but is not the recorded process; signaling it would fail
	// with a permission error, so a nil result proves it was not selected.
	record.Owner = localagent.Owner{PID: 1, StartedAt: "Thu Jan  1 00:00:00 1970"}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.AgentOwnerPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stopRecordedStaleAgent(paths, 10*time.Millisecond); err != nil {
		t.Fatalf("record whose identity moved: %v", err)
	}
}
