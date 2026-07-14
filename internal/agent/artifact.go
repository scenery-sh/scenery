package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"scenery.sh/internal/machine"
)

const (
	sessionSchemaDescriptor        = `{"identity":"artifact","session":"agent-session"}`
	routeManifestSchemaDescriptor  = `{"identity":"artifact","manifest":"route-manifest"}`
	portLeaseSchemaDescriptor      = `{"identity":"artifact","lease":"port-lease"}`
	substrateSchemaDescriptor      = `{"identity":"artifact","substrate":"managed-substrate"}`
	agentStateSchemaDescriptor     = `{"identity":"artifact","state":"agent-process"}`
	agentRegistrySchemaDescriptor  = `{"identity":"artifact","registry":"sessions-substrates-aliases"}`
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
func NewEdgeTargetIdentity() machine.ArtifactIdentity    { return edgeTargetIdentity() }

func EdgeTargetSchemaRevision() string {
	return machine.ArtifactSchemaRevision(edgeTargetSchemaDescriptor)
}

// LoadDurableArtifact rebinds unchanged schemas to the current specification
// and migrates one identity-only legacy JSON shape. Payload values stay intact.
func LoadDurableArtifact(path string, target any, identity *machine.ArtifactIdentity, kind, descriptor string, perm os.FileMode, migrate func(map[string]json.RawMessage) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := machine.DecodeArtifact(data, target, identity, kind, descriptor, "rerun the state migration"); err == nil {
		if _, backupErr := os.Stat(path + ".legacy.bak"); backupErr == nil {
			return writeMigrationMarker(path)
		}
		return nil
	}
	currentIdentity := machine.NewArtifactIdentity(kind, descriptor)
	var storedIdentity machine.ArtifactIdentity
	if err := json.Unmarshal(data, &storedIdentity); err == nil &&
		storedIdentity.Kind == currentIdentity.Kind &&
		storedIdentity.SchemaRevision == currentIdentity.SchemaRevision &&
		storedIdentity.SpecRevision != currentIdentity.SpecRevision {
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
		if err := atomicWriteFile(path, current, perm); err != nil {
			return err
		}
		return writeMigrationMarker(path)
	}
	if _, err := os.Stat(path + ".legacy.migrated"); err == nil {
		return fmt.Errorf("legacy state remained after completed migration: %s", path)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if err := migrate(fields); err != nil {
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
	if _, err := os.Stat(path + ".legacy.bak"); errors.Is(err, os.ErrNotExist) {
		if err := atomicWriteFile(path+".legacy.bak", data, 0o600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := atomicWriteFile(path, current, perm); err != nil {
		return err
	}
	return writeMigrationMarker(path)
}

func addIdentityFields(fields map[string]json.RawMessage, identity machine.ArtifactIdentity) {
	encodedIdentity, _ := json.Marshal(identity)
	var identityFields map[string]json.RawMessage
	_ = json.Unmarshal(encodedIdentity, &identityFields)
	for name, value := range identityFields {
		fields[name] = value
	}
}

func writeMigrationMarker(path string) error {
	return atomicWriteFile(path+".legacy.migrated", []byte("current\n"), 0o600)
}

func requireLegacySchema(fields map[string]json.RawMessage, want string) error {
	var got string
	if raw := fields["schema_version"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &got)
	}
	if got != want {
		return fmt.Errorf("unsupported legacy schema %q", got)
	}
	delete(fields, "schema_version")
	return nil
}

func requireLegacySchemaOrMissing(fields map[string]json.RawMessage, want string) error {
	if len(fields["schema_version"]) == 0 {
		if len(fields["schema_revision"]) > 0 || len(fields["spec_revision"]) > 0 || len(fields["producer"]) > 0 {
			return fmt.Errorf("invalid current artifact identity")
		}
		return nil
	}
	return requireLegacySchema(fields, want)
}

func renameLegacyField(fields map[string]json.RawMessage, oldName, newName string) {
	if value, ok := fields[oldName]; ok {
		fields[newName] = value
		delete(fields, oldName)
	}
}
