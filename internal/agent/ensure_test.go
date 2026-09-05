package agent

import (
	"context"
	"strings"
	"testing"
)

func TestEnsureWithUsesExplicitHome(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	server, err := NewServer(RunOptions{Home: home, RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() { stopTestAgent(t, cancel, done) })

	client, err := EnsureWith(ctx, Identity{Version: "v99.0.0", Commit: "other-worktree"}, PathsForHome(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForAgentPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	if client.socketPath != server.Paths().SocketPath {
		t.Fatalf("EnsureWith socket = %q, want %q", client.socketPath, server.Paths().SocketPath)
	}
}

func TestSharedAgentCompatibility(t *testing.T) {
	t.Parallel()
	current := Identity{Version: "v2.0.0", Commit: "new"}
	health := HealthResponse{ArtifactIdentity: agentStateIdentity(), Identity: Identity{Version: "v1.0.0", Commit: "old"}}
	if err := compatibleAgent(health, current); err != nil {
		t.Fatalf("different builds with the same contract: %v", err)
	}
	for _, field := range []string{"kind", "schema", "spec", "producer"} {
		broken := health
		switch field {
		case "kind":
			broken.Kind = "unknown"
		case "schema":
			broken.SchemaRevision = "unknown"
		case "spec":
			broken.SpecRevision = "unknown"
		case "producer":
			broken.Producer.Version = ""
		}
		if err := compatibleAgent(broken, current); err == nil || !strings.HasPrefix(err.Error(), "failed_precondition:") || !strings.Contains(err.Error(), "existing sessions are untouched") {
			t.Fatalf("%s mismatch: %v", field, err)
		}
	}
}
