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
	writeTestAppFile(t, root, ".scenery.json", `{"name":"postgres-harness","id":"postgres-harness","envs":{"local":{"default":true}},"storage":{"stores":{"app":{"kind":"local"}}}}`)
	writeSQLTestDeclarations(t, root, "reports", "cache")
	appRoot, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := discoverDBSeedPlans(appRoot, cfg)
	if err != nil {
		t.Fatal(err)
	}
	requirements, err := compileSQLRequirements(root)
	if err != nil || len(plans) != 0 || len(requirements) != 2 {
		t.Fatalf("fixture has %d seeds and %d requirements: %v", len(plans), len(requirements), err)
	}
}

func TestManagedDatabaseEnvUsesExternalDSN(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
	}
	dsn := "postgres://user:secret@localhost/app"
	env, database, err := managedDatabaseEnv(t.Context(), root, cfg, testSQLRequirements(t, "reports"), []string{"DATABASE_URL=" + dsn})
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
	}
	dsn := "postgres://user:secret@localhost/app"
	env, _, err := managedDatabaseEnv(t.Context(), root, cfg, testSQLRequirements(t, "reports"), []string{"DATABASE_URL=" + dsn})
	if err != nil {
		t.Fatalf("managedDatabaseEnv returned error: %v", err)
	}
	if envValueFromList(env, "DATABASE_URL") != dsn || envValueFromList(env, "REPORTS_DATABASE_URL") == "" {
		t.Fatalf("env = %+v", env)
	}
}

func TestValidateHeadlessPostgresEnvRequiresExplicitDSN(t *testing.T) {
	t.Parallel()

	requirements := testSQLRequirements(t, "reports")
	err := validateHeadlessPostgresEnv(requirements, nil)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "scenery up") {
		t.Fatalf("validateHeadlessPostgresEnv error = %v", err)
	}
	if err := validateHeadlessPostgresEnv(requirements, []string{"DATABASE_URL=postgres://user:secret@localhost/reports"}); err != nil {
		t.Fatalf("validateHeadlessPostgresEnv rejected explicit DSN: %v", err)
	}
}
