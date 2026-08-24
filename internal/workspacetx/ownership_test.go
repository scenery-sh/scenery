package workspacetx

import (
	"os"
	"testing"
)

func TestCurrentOwnerRecognizesItself(t *testing.T) {
	owner := currentOwner()
	if !ownerIsCurrent(owner) {
		t.Fatalf("current owner was not recognized: %#v", owner)
	}
}

func TestCurrentOwnerRequiresStoredFingerprint(t *testing.T) {
	if ownerIsCurrent(Owner{PID: os.Getpid()}) {
		t.Fatal("owner without a stored process fingerprint was accepted")
	}
}

func TestCurrentOwnerWithNoInspectionUsesProcessToken(t *testing.T) {
	owner := fullTestOwner(42, currentProcessOwnerToken)
	if state := ownerStateWithLive(owner, Owner{}, ownerProcessUnknown, 42); state != ownerCurrent {
		t.Fatalf("uninspectable current owner state = %v, want current", state)
	}
}

func TestCurrentOwnerWithPartialOrMismatchedInspectionIsUnknown(t *testing.T) {
	owner := fullTestOwner(42, currentProcessOwnerToken)
	tests := map[string]Owner{
		"partial":  {PID: 42, Exe: owner.Exe},
		"mismatch": {PID: 42, StartedAt: "new", Exe: owner.Exe, CmdlineHash: owner.CmdlineHash},
	}
	for name, live := range tests {
		t.Run(name, func(t *testing.T) {
			if state := ownerStateWithLive(owner, live, ownerProcessLive, 42); state != ownerUnknown {
				t.Fatalf("current owner state = %v, want unknown", state)
			}
		})
	}
}

func TestPriorProcessSamePIDNeverAuthorizesOwnerRead(t *testing.T) {
	owner := fullTestOwner(42, "change-transaction:prior-process")
	tests := []struct {
		name     string
		live     Owner
		liveness ownerProcessLiveness
		want     ownerState
	}{
		{name: "uninspectable", liveness: ownerProcessUnknown, want: ownerUnknown},
		{name: "partial match", live: Owner{PID: 42, Exe: owner.Exe}, liveness: ownerProcessLive, want: ownerUnknown},
		{name: "complete match", live: owner, liveness: ownerProcessLive, want: ownerLive},
		{name: "fingerprint mismatch", live: Owner{PID: 42, StartedAt: "new", Exe: owner.Exe, CmdlineHash: owner.CmdlineHash}, liveness: ownerProcessLive, want: ownerRecoverable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if state := ownerStateWithLive(owner, test.live, test.liveness, 42); state != test.want {
				t.Fatalf("prior-process owner state = %v, want %v", state, test.want)
			}
		})
	}
}

func TestForeignOwnerRecoveryRequiresProof(t *testing.T) {
	owner := fullTestOwner(42, "change-transaction:foreign-process")
	tests := []struct {
		name     string
		live     Owner
		liveness ownerProcessLiveness
		want     ownerState
	}{
		{name: "live complete match", live: owner, liveness: ownerProcessLive, want: ownerLive},
		{name: "unknown complete match", live: owner, liveness: ownerProcessUnknown, want: ownerUnknown},
		{name: "live uninspectable", liveness: ownerProcessLive, want: ownerUnknown},
		{name: "dead uninspectable", liveness: ownerProcessDead, want: ownerRecoverable},
		{name: "live fingerprint mismatch", live: Owner{PID: 42, StartedAt: "new", Exe: owner.Exe, CmdlineHash: owner.CmdlineHash}, liveness: ownerProcessLive, want: ownerRecoverable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if state := ownerStateWithLive(owner, test.live, test.liveness, 7); state != test.want {
				t.Fatalf("foreign owner state = %v, want %v", state, test.want)
			}
		})
	}
}

func fullTestOwner(pid int, createdBy string) Owner {
	return Owner{PID: pid, StartedAt: "old", Exe: "/usr/local/bin/scenery", CmdlineHash: "sha256:args", CreatedBy: createdBy}
}
