package main

import (
	"context"

	localagent "scenery.sh/internal/agent"
)

// commandAgentPathsOverride is the in-process agent-home seam for tests.
// Production commands leave it nil so commandAgentPaths reads
// SCENERY_AGENT_HOME (or ~/.scenery) at this CLI/runtime boundary.
var commandAgentPathsOverride *localagent.Paths

func commandAgentPaths() (localagent.Paths, error) {
	if commandAgentPathsOverride != nil {
		return *commandAgentPathsOverride, nil
	}
	return localagent.DefaultPaths()
}

// Ordinary app commands only connect to their exact worktree owner. The
// machine paths above are reserved for explicit machine-edge operations.
func commandWorktreeClient(ctx context.Context, root string) (*localagent.Client, error) {
	paths, err := commandWorktreePaths(root)
	if err != nil {
		return nil, err
	}
	if _, err := paths.LoadRecord(""); err != nil {
		return nil, err
	}
	client := localagent.NewClient(paths.Socket)
	health, err := client.Health(ctx)
	if err != nil {
		client.CloseIdleConnections()
		return nil, err
	}
	if err := localagent.ValidateWorktreeHealth(health, paths); err != nil {
		client.CloseIdleConnections()
		return nil, err
	}
	return client, nil
}
