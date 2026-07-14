package main

import (
	"path/filepath"
	"testing"

	localagent "scenery.sh/internal/agent"
)

// TestIsLiveForeignSceneryAgent proves the stale-agent reaper never targets
// another agent home's live agent: an agent that shares the default router
// address but answers health on its own control socket is not stale. This is
// the guard against test/harness/worktree agent starts SIGTERMing the
// machine's real supervised agent.
func TestIsLiveForeignSceneryAgent(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	startFakeAgentHealthServer(t, paths.SocketPath, 4242)
	liveCommand := "scenery system agent --socket " + paths.SocketPath + " --router-listen 127.0.0.1:9440 --router-http"

	ownSocket := filepath.Join(t.TempDir(), "agent.sock")
	if !isLiveForeignSceneryAgent(liveCommand, ownSocket) {
		t.Fatal("live agent on a different socket must be treated as foreign, not stale")
	}
	// The same socket is never foreign: an unreachable agent on the caller's
	// own socket is exactly what the reaper exists to clean up.
	if isLiveForeignSceneryAgent(liveCommand, paths.SocketPath) {
		t.Fatal("agent on the caller's own socket must stay reapable")
	}
	// A dead foreign socket is stale and stays reapable.
	deadCommand := "scenery system agent --socket " + filepath.Join(t.TempDir(), "dead.sock") + " --router-listen 127.0.0.1:9440 --router-http"
	if isLiveForeignSceneryAgent(deadCommand, ownSocket) {
		t.Fatal("dead foreign agent must stay reapable")
	}
	// No --socket in the command: fall back to the old reap behavior.
	if isLiveForeignSceneryAgent("scenery system agent --router-listen 127.0.0.1:9440", ownSocket) {
		t.Fatal("socket-less command must stay reapable")
	}

	if got := agentCommandSocketPath(liveCommand); got != paths.SocketPath {
		t.Fatalf("agentCommandSocketPath = %q, want %q", got, paths.SocketPath)
	}
}
