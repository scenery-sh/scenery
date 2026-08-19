package main

import (
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestCommandAgentPathsUsesExplicitOverride(t *testing.T) {
	home := t.TempDir()
	paths := isolateCommandAgentHomeAt(t, home)
	got, err := commandAgentPaths()
	if err != nil {
		t.Fatal(err)
	}
	if got.Home != paths.Home || got.DeployPath != localagent.PathsForHome(home).DeployPath {
		t.Fatalf("commandAgentPaths = %+v, want home %q", got, home)
	}
}
