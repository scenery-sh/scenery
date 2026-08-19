package main

import localagent "scenery.sh/internal/agent"

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

func commandAgentClient() (*localagent.Client, error) {
	paths, err := commandAgentPaths()
	if err != nil {
		return nil, err
	}
	return localagent.NewClient(paths.SocketPath), nil
}
