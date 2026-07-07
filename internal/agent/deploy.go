package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DeployRegistrySchemaVersion = "scenery.deploy.registry.v1"

type DeployRegistry struct {
	SchemaVersion string         `json:"schema_version"`
	ACMEEmail     string         `json:"acme_email,omitempty"`
	ACMECA        string         `json:"acme_ca,omitempty"`
	Targets       []DeployTarget `json:"targets"`
}

type DeployTarget struct {
	Domain      string    `json:"domain"`
	AppRoot     string    `json:"app_root"`
	RootService string    `json:"root_service,omitempty"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func EmptyDeployRegistry() DeployRegistry {
	return DeployRegistry{
		SchemaVersion: DeployRegistrySchemaVersion,
		ACMECA:        "production",
		Targets:       []DeployTarget{},
	}
}

func LoadDeployRegistry(path string) (DeployRegistry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return EmptyDeployRegistry(), nil
	}
	if err != nil {
		return DeployRegistry{}, err
	}
	var registry DeployRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return DeployRegistry{}, err
	}
	if registry.SchemaVersion == "" {
		registry.SchemaVersion = DeployRegistrySchemaVersion
	}
	if registry.ACMECA == "" {
		registry.ACMECA = "production"
	}
	for i := range registry.Targets {
		registry.Targets[i].Domain = strings.ToLower(strings.TrimSpace(registry.Targets[i].Domain))
		registry.Targets[i].AppRoot = filepath.Clean(strings.TrimSpace(registry.Targets[i].AppRoot))
		registry.Targets[i].RootService = strings.TrimSpace(registry.Targets[i].RootService)
	}
	sortDeployTargets(registry.Targets)
	if registry.Targets == nil {
		registry.Targets = []DeployTarget{}
	}
	return registry, nil
}

func WriteDeployRegistry(path string, registry DeployRegistry) error {
	if registry.SchemaVersion == "" {
		registry.SchemaVersion = DeployRegistrySchemaVersion
	}
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
