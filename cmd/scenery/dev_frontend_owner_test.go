package main

import (
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestSessionProcessesUseReadyFrontendOwner(t *testing.T) {
	owner := localagent.Owner{PID: 42, StartedAt: "same-child", Exe: "/node", CmdlineHash: "ready-fingerprint"}
	frontend := &managedFrontendProcess{
		Name: "web", Process: &devManagedProcess{PID: 42}, owner: owner,
	}
	supervisor := &devSupervisor{frontends: map[string]*managedFrontendProcess{"web": frontend}}
	session := &localagent.Session{Processes: map[string]localagent.Process{
		"frontend-web": {PID: 42, Owner: localagent.Owner{PID: 42, Exe: "/usr/bin/env", CmdlineHash: "launcher-fingerprint"}},
	}}
	for range 2 {
		got := supervisor.sessionProcessesFor(session, "")
		if got["frontend-web"].Owner != owner {
			t.Fatalf("frontend owner = %+v, want captured ready owner %+v", got["frontend-web"].Owner, owner)
		}
	}
	if session.Processes["frontend-web"].Owner.Exe != "/usr/bin/env" {
		t.Fatal("session input was mutated")
	}
}
