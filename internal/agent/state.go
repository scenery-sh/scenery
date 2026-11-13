package agent

import (
	"errors"
	"os"
)

func LoadState(path string) (State, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, err
		}
		return State{}, err
	}
	var state State
	err := LoadDurableArtifact(path, &state, &state.ArtifactIdentity, AgentStateKind, agentStateSchemaDescriptor, 0o644)
	return state, err
}
