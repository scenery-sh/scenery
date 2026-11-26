package main

import "testing"

// Source edits reach the running host unless a restart was requested or a
// failed restart left its predecessor serving.
func TestHostOwnsChangesUntilARestartIsDue(t *testing.T) {
	t.Parallel()
	s := &devSupervisor{rebuildRequests: make(chan struct{}, 1)}
	if s.hostOwnsChanges() {
		t.Fatal("changes went to a host that is not running")
	}
	s.host = &devHostGeneration{}
	if !s.hostOwnsChanges() {
		t.Fatal("a running host did not own its changes")
	}
	s.requestHostRestart()
	if s.hostOwnsChanges() || !s.hostOwnsChanges() {
		t.Fatal("a restart request was not consumed exactly once")
	}
	s.restartPending = true
	for range 2 {
		if s.hostOwnsChanges() {
			t.Fatal("a pending restart let the old host own changes")
		}
	}
}
