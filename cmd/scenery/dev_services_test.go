package main

import (
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/postgresdb"
)

func TestPostgresHarnessFixtureSupportsDatabaseDiscovery(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := writePostgresHarnessConfig(root); err != nil {
		t.Fatal(err)
	}
	appRoot, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := discoverDBSeedPlans(appRoot, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 0 || len(cfg.Dev.Services) != 2 {
		t.Fatalf("fixture has %d seeds and %d services", len(plans), len(cfg.Dev.Services))
	}
}

func TestManagedDatabaseEnvUsesExternalDSN(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"reports": {},
		}},
	}
	dsn := "postgres://user:secret@localhost/app"
	env, database, err := managedDatabaseEnv(t.Context(), root, cfg, []string{"DATABASE_URL=" + dsn})
	if err != nil {
		t.Fatalf("managedDatabaseEnv returned error: %v", err)
	}
	serviceURL := envValueFromList(env, "REPORTS_DATABASE_URL")
	if envValueFromList(env, "DATABASE_URL") != dsn || !strings.Contains(serviceURL, "search_path=reports%2Cscenery") {
		t.Fatalf("env = %+v", env)
	}
	if registry := envValueFromList(env, postgresdb.RegistryEnv); !strings.Contains(registry, `"source":"external"`) {
		t.Fatalf("registry = %q", registry)
	}
	if len(database.Schemas) != 1 || database.Schemas[0].Name != "reports" || database.Schemas[0].Schema != "reports" {
		t.Fatalf("database = %+v", database)
	}
}

func TestManagedDatabaseEnvUsesCanonicalAppURLEnv(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{
		Name:     "demo",
		Database: app.DatabaseConfig{},
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"reports": {},
		}},
	}
	dsn := "postgres://user:secret@localhost/app"
	env, _, err := managedDatabaseEnv(t.Context(), root, cfg, []string{"DATABASE_URL=" + dsn})
	if err != nil {
		t.Fatalf("managedDatabaseEnv returned error: %v", err)
	}
	if envValueFromList(env, "DATABASE_URL") != dsn || envValueFromList(env, "REPORTS_DATABASE_URL") == "" {
		t.Fatalf("env = %+v", env)
	}
}

func TestValidateHeadlessPostgresEnvRequiresExplicitDSN(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"reports": {},
		}},
	}
	err := validateHeadlessPostgresEnv(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "scenery up") {
		t.Fatalf("validateHeadlessPostgresEnv error = %v", err)
	}
	if err := validateHeadlessPostgresEnv(cfg, []string{"DATABASE_URL=postgres://user:secret@localhost/reports"}); err != nil {
		t.Fatalf("validateHeadlessPostgresEnv rejected explicit DSN: %v", err)
	}
}
