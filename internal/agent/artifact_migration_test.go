package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentStateRebindsUnchangedSchemaToCurrentSpec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := State{
		ArtifactIdentity: agentStateIdentity(),
		PID:              123,
		Identity:         Identity{Version: "v1.2.3", Commit: "abc"},
		SocketPath:       "/tmp/agent.sock",
		RouterAddr:       "127.0.0.1:4040",
	}
	state.SpecRevision = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SpecRevision != agentStateIdentity().SpecRevision {
		t.Fatalf("spec revision = %q", loaded.SpecRevision)
	}
	if loaded.PID != 123 || loaded.Version != "v1.2.3" || loaded.Commit != "abc" {
		t.Fatalf("state payload changed: %+v", loaded)
	}
	reloaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SpecRevision != agentStateIdentity().SpecRevision || reloaded.PID != 123 {
		t.Fatalf("reloaded state = %+v", reloaded)
	}
}
