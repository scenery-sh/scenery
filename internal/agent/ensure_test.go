package agent

import (
	"context"
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

	client, err := EnsureWith(ctx, Identity{}, PathsForHome(home))
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
