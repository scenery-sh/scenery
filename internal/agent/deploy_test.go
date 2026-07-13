package agent

import (
	"os"
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
	if got.Kind != DeployRegistryKind || got.SchemaRevision == "" || got.ACMEEmail != "ops@example.com" || got.ACMECA != "staging" {
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
	if got.Kind != DeployRegistryKind || got.SchemaRevision == "" || got.ACMECA != "production" || len(got.Targets) != 0 {
		t.Fatalf("registry = %+v", got)
	}
}

func TestLoadDeployRegistryMigratesLegacyWithoutLosingOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy.json")
	legacy := []byte(`{"schema_version":"scenery.deploy.registry.v1","acme_email":"ops@example.com","targets":[{"domain":"onlv.dev","app_root":"/repo/onlv","enabled":true}]}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDeployRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Targets) != 1 || got.Targets[0].AppRoot != "/repo/onlv" || got.Kind != DeployRegistryKind {
		t.Fatalf("migrated registry = %+v", got)
	}
	backup, err := os.ReadFile(path + ".legacy.bak")
	if err != nil || string(backup) != string(legacy) {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	if info, err := os.Stat(path + ".legacy.bak"); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %v, %v", info, err)
	}
	if _, err := os.Stat(path + ".legacy.migrated"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDeployRegistry(path); err != nil {
		t.Fatalf("idempotent reload: %v", err)
	}
}
