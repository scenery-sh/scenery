package agent

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDeployRegistryLoadWriteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent", "deploy.json")
	created := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	registry := EmptyDeployRegistry()
	registry.ACMEEmail = "ops@example.com"
	registry.ACMECA = "staging"
	registry.Targets = []DeployTarget{{
		Domain:      "onlv.dev",
		AppRoot:     "/repo/onlv",
		RootService: "web",
		Enabled:     true,
		CreatedAt:   created,
		UpdatedAt:   created,
	}}

	if err := WriteDeployRegistry(path, registry); err != nil {
		t.Fatalf("WriteDeployRegistry: %v", err)
	}
	got, err := LoadDeployRegistry(path)
	if err != nil {
		t.Fatalf("LoadDeployRegistry: %v", err)
	}
	if got.SchemaVersion != DeployRegistrySchemaVersion || got.ACMEEmail != "ops@example.com" || got.ACMECA != "staging" {
		t.Fatalf("registry metadata = %+v", got)
	}
	if len(got.Targets) != 1 || got.Targets[0].Domain != "onlv.dev" || !got.Targets[0].Enabled {
		t.Fatalf("targets = %+v", got.Targets)
	}
}

func TestLoadDeployRegistryMissingReturnsEmpty(t *testing.T) {
	got, err := LoadDeployRegistry(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("LoadDeployRegistry missing: %v", err)
	}
	if got.SchemaVersion != DeployRegistrySchemaVersion || got.ACMECA != "production" || len(got.Targets) != 0 {
		t.Fatalf("registry = %+v", got)
	}
}
