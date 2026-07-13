package agent

import (
	"encoding/json"
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
	err := LoadDurableArtifact(path, &state, &state.ArtifactIdentity, AgentStateKind, agentStateSchemaDescriptor, 0o644, func(fields map[string]json.RawMessage) error {
		if err := requireLegacySchema(fields, "scenery.agent.state.v1"); err != nil {
			return err
		}
		if raw := fields["edge"]; len(raw) > 0 && string(raw) != "null" {
			var edge map[string]json.RawMessage
			if err := json.Unmarshal(raw, &edge); err != nil {
				return err
			}
			if err := requireLegacySchema(edge, legacyEdgeSchemaVersion); err != nil {
				return err
			}
			renameLegacyField(edge, "kind", "edge_kind")
			addIdentityFields(edge, edgeStateIdentity())
			fields["edge"], _ = json.Marshal(edge)
		}
		return nil
	})
	return state, err
}
