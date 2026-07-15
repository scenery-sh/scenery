package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/machine"
)

const legacyDeployRegistrySchemaVersion = "scenery.deploy.registry.v1"

type DeployRegistry struct {
	machine.ArtifactIdentity
	ACMEEmail string         `json:"acme_email,omitempty"`
	ACMECA    string         `json:"acme_ca,omitempty"`
	Targets   []DeployTarget `json:"targets"`
}

type DeployTarget struct {
	Domain      string    `json:"domain"`
	AppRoot     string    `json:"app_root"`
	RootService string    `json:"root_service,omitempty"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Frontends records production frontends published into the deploy
	// artifact store for direct managed-Caddy serving. The field is
	// additive: an old target without publication metadata keeps the
	// agent-proxy behavior for every route.
	Frontends []DeployTargetFrontend `json:"frontends,omitempty"`
}

// DeployTargetFrontend is one published production frontend on a deploy
// target. Path is the frontend's `current` symlink inside the machine-owned
// deploy artifact store; Root marks the frontend that owns `/`.
type DeployTargetFrontend struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Root        bool      `json:"root,omitempty"`
	ReleaseID   string    `json:"release_id,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
}

func EmptyDeployRegistry() DeployRegistry {
	return DeployRegistry{
		ArtifactIdentity: deployRegistryIdentity(),
		ACMECA:           "production",
		Targets:          []DeployTarget{},
	}
}

func LoadDeployRegistry(path string) (DeployRegistry, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return EmptyDeployRegistry(), nil
	}
	if err != nil {
		return DeployRegistry{}, err
	}
	var registry DeployRegistry
	if err := LoadDurableArtifact(path, &registry, &registry.ArtifactIdentity, DeployRegistryKind, deployRegistrySchemaDescriptor, 0o600, func(fields map[string]json.RawMessage) error {
		return requireLegacySchemaOrMissing(fields, legacyDeployRegistrySchemaVersion)
	}); err != nil {
		return DeployRegistry{}, err
	}
	if registry.ACMECA == "" {
		registry.ACMECA = "production"
	}
	for i := range registry.Targets {
		registry.Targets[i].Domain = strings.ToLower(strings.TrimSpace(registry.Targets[i].Domain))
		registry.Targets[i].AppRoot = filepath.Clean(strings.TrimSpace(registry.Targets[i].AppRoot))
		registry.Targets[i].RootService = strings.TrimSpace(registry.Targets[i].RootService)
		for j := range registry.Targets[i].Frontends {
			frontend := &registry.Targets[i].Frontends[j]
			frontend.Name = strings.TrimSpace(frontend.Name)
			frontend.Path = filepath.Clean(strings.TrimSpace(frontend.Path))
		}
	}
	sortDeployTargets(registry.Targets)
	if registry.Targets == nil {
		registry.Targets = []DeployTarget{}
	}
	return registry, nil
}

func WriteDeployRegistry(path string, registry DeployRegistry) error {
	registry.ArtifactIdentity = deployRegistryIdentity()
	if registry.ACMECA == "" {
		registry.ACMECA = "production"
	}
	if registry.Targets == nil {
		registry.Targets = []DeployTarget{}
	}
	sortDeployTargets(registry.Targets)
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(path, data, 0o600)
}

func sortDeployTargets(targets []DeployTarget) {
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Domain != targets[j].Domain {
			return targets[i].Domain < targets[j].Domain
		}
		return targets[i].AppRoot < targets[j].AppRoot
	})
}
