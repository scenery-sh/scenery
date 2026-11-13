package agent

import (
	"encoding/json"
	"maps"
	"os"

	"scenery.sh/internal/machine"
)

const (
	sessionSchemaDescriptor        = `{"identity":"artifact","session":"agent-session","worktree_proxy":"optional-loopback-backend"}`
	routeManifestSchemaDescriptor  = `{"identity":"artifact","manifest":"route-manifest"}`
	portLeaseSchemaDescriptor      = `{"identity":"artifact","lease":"port-lease"}`
	substrateSchemaDescriptor      = `{"identity":"artifact","substrate":"managed-substrate"}`
	agentOwnerSchemaDescriptor     = `{"identity":"artifact","owner":"agent-process-owner"}`
	agentStateSchemaDescriptor     = `{"identity":"artifact","state":"agent-process","worktree_edge_proxy":true}`
	agentRegistrySchemaDescriptor  = `{"identity":"artifact","registry":"sessions-substrates-aliases","worktree_edge_proxy":true}`
	deployRegistrySchemaDescriptor = `{"identity":"artifact","registry":"deployment-ownership"}`
	edgeStateSchemaDescriptor      = `{"identity":"artifact","state":"managed-edge"}`
	edgeTargetSchemaDescriptor     = `{"identity":"artifact","state":"privileged-edge-target"}`
)

func sessionIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(SessionKind, sessionSchemaDescriptor)
}
func routeManifestIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(RouteManifestKind, routeManifestSchemaDescriptor)
}
func portLeaseIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(PortLeaseKind, portLeaseSchemaDescriptor)
}
func substrateIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(SubstrateKind, substrateSchemaDescriptor)
}
func agentStateIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(AgentStateKind, agentStateSchemaDescriptor)
}
func agentRegistryIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(AgentRegistryKind, agentRegistrySchemaDescriptor)
}
func deployRegistryIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(DeployRegistryKind, deployRegistrySchemaDescriptor)
}
func edgeStateIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(EdgeStateKind, edgeStateSchemaDescriptor)
}
func edgeTargetIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(EdgeTargetKind, edgeTargetSchemaDescriptor)
}

func NewRouteManifestIdentity() machine.ArtifactIdentity { return routeManifestIdentity() }
func NewPortLeaseIdentity() machine.ArtifactIdentity     { return portLeaseIdentity() }
func NewEdgeStateIdentity() machine.ArtifactIdentity     { return edgeStateIdentity() }

// LoadDurableArtifact rebinds unchanged schemas to the current specification.
// Payload values stay intact.
func LoadDurableArtifact(path string, target any, identity *machine.ArtifactIdentity, kind, descriptor string, perm os.FileMode) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decodeErr := machine.DecodeArtifact(data, target, identity, kind, descriptor, "rerun the state migration")
	if decodeErr == nil {
		return nil
	}
	currentIdentity := machine.NewArtifactIdentity(kind, descriptor)
	var storedIdentity machine.ArtifactIdentity
	if err := json.Unmarshal(data, &storedIdentity); err != nil ||
		storedIdentity.Kind != currentIdentity.Kind ||
		storedIdentity.SchemaRevision != currentIdentity.SchemaRevision ||
		storedIdentity.SpecRevision == currentIdentity.SpecRevision {
		return decodeErr
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	addIdentityFields(fields, currentIdentity)
	current, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	current = append(current, '\n')
	if err := machine.DecodeArtifact(current, target, identity, kind, descriptor, "rerun the state migration"); err != nil {
		return err
	}
	return atomicWriteFile(path, current, perm)
}

func addIdentityFields(fields map[string]json.RawMessage, identity machine.ArtifactIdentity) {
	encodedIdentity, _ := json.Marshal(identity)
	var identityFields map[string]json.RawMessage
	_ = json.Unmarshal(encodedIdentity, &identityFields)
	maps.Copy(fields, identityFields)
}
