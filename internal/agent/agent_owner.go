package agent

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"scenery.sh/internal/machine"
)

// AgentOwnerRecord names the process that holds an agent home's agent lock,
// captured right after it acquired the lock. Stopping a stale agent acts only
// on this record and only while the live process still verifies against it.
type AgentOwnerRecord struct {
	machine.ArtifactIdentity
	Owner      Owner  `json:"owner"`
	SocketPath string `json:"socket_path"`
}

func agentOwnerIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(AgentOwnerKind, agentOwnerSchemaDescriptor)
}

// WriteAgentOwner records the current process as the owner of paths' agent
// lock. It is a no-op for agent homes without an owner record path.
func WriteAgentOwner(paths Paths) error {
	if strings.TrimSpace(paths.AgentOwnerPath) == "" {
		return nil
	}
	data, err := json.MarshalIndent(AgentOwnerRecord{
		ArtifactIdentity: agentOwnerIdentity(),
		Owner:            CurrentOwner("scenery agent"),
		SocketPath:       paths.SocketPath,
	}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(paths.AgentOwnerPath, append(data, '\n'), 0o600)
}

// LoadAgentOwner reads the recorded agent owner; a missing record is
// os.ErrNotExist.
func LoadAgentOwner(paths Paths) (AgentOwnerRecord, error) {
	if strings.TrimSpace(paths.AgentOwnerPath) == "" {
		return AgentOwnerRecord{}, os.ErrNotExist
	}
	data, err := os.ReadFile(paths.AgentOwnerPath)
	if err != nil {
		return AgentOwnerRecord{}, err
	}
	var record AgentOwnerRecord
	if err := machine.DecodeArtifact(data, &record, &record.ArtifactIdentity, AgentOwnerKind, agentOwnerSchemaDescriptor, "restart the scenery agent"); err != nil {
		return AgentOwnerRecord{}, err
	}
	if record.Owner.PID <= 0 {
		return AgentOwnerRecord{}, errors.New("agent owner record names no process")
	}
	return record, nil
}
