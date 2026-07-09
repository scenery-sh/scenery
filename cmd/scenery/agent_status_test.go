package main

import (
	"context"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestStatusSubstratesPrunesDeadOwners(t *testing.T) {
	t.Parallel()

	ctx, client := startSubstrateTestAgent(t)
	livePID := startFakeSubstrateOwner(t)
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     "live",
		OwnerPID: livePID,
		PIDs:     map[string]int{"server": livePID},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     "dead",
		OwnerPID: 99999991,
		PIDs:     map[string]int{"server": 99999991},
	}); err != nil {
		t.Fatal(err)
	}

	substrates, err := statusSubstrates(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if len(substrates) != 1 || substrates[0].Kind != "live" {
		t.Fatalf("substrates = %+v, want only live", substrates)
	}
	if _, err := client.GetSubstrate(ctx, "dead"); !localagent.IsNotFound(err) {
		t.Fatalf("dead substrate still registered: %v", err)
	}
}
