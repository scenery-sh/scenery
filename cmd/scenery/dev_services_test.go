package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/sqlitedb"
)

func TestManagedSQLiteEnvExposesServiceAndAlias(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"main": {Kind: "sqlite"},
		}},
	}
	env, services, err := managedSQLiteEnv(t.Context(), root, cfg, nil)
	if err != nil {
		t.Fatalf("managedSQLiteEnv returned error: %v", err)
	}
	wantPath := filepath.Join(root, ".scenery", "sqlite", "local", "main.sqlite")
	wantURL := sqlitedb.URLForPath(wantPath)
	for _, want := range []string{
		"MAIN_DATABASE_URL=" + wantURL,
		"MAIN_DATABASE_PATH=" + wantPath,
		appDatabaseURLEnv + "=" + wantURL,
	} {
		if !containsString(env, want) {
			t.Fatalf("env missing %q: %+v", want, env)
		}
	}
	if len(services) != 1 || services[0].Path != wantPath {
		t.Fatalf("services = %+v", services)
	}
}

func TestManagedSQLiteEnvSkipsAliasForExplicitDatabaseURLEnv(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"main": {Kind: "sqlite", DatabaseURLEnv: "APP_DATABASE_URL"},
		}},
	}
	env, _, err := managedSQLiteEnv(t.Context(), root, cfg, nil)
	if err != nil {
		t.Fatalf("managedSQLiteEnv returned error: %v", err)
	}
	if envValueFromList(env, "APP_DATABASE_URL") == "" {
		t.Fatalf("env missing APP_DATABASE_URL: %+v", env)
	}
	if envValueFromList(env, appDatabaseURLEnv) != "" {
		t.Fatalf("env should not include DatabaseURL alias: %+v", env)
	}
}

func TestManagedSQLiteEnvSkipsAliasWhenPostgresServiceExists(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"cache":   {Kind: "sqlite"},
			"reports": {Kind: "postgres"},
		}},
	}
	env, _, err := managedSQLiteEnv(t.Context(), root, cfg, nil)
	if err != nil {
		t.Fatalf("managedSQLiteEnv returned error: %v", err)
	}
	if envValueFromList(env, appDatabaseURLEnv) != "" {
		t.Fatalf("env should not include DatabaseURL alias for mixed engines: %+v", env)
	}
}

func TestManagedPostgresEnvUsesExternalDSNAndAlias(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"reports": {Kind: "postgres"},
		}},
	}
	dsn := "postgres://user:secret@localhost/reports"
	env, services, err := managedPostgresEnv(t.Context(), root, cfg, nil, []string{"REPORTS_DATABASE_URL=" + dsn})
	if err != nil {
		t.Fatalf("managedPostgresEnv returned error: %v", err)
	}
	for _, want := range []string{
		"REPORTS_DATABASE_URL=" + dsn,
		appDatabaseURLEnv + "=" + dsn,
	} {
		if !containsString(env, want) {
			t.Fatalf("env missing %q: %+v", want, env)
		}
	}
	if registry := envValueFromList(env, postgresdb.RegistryEnv); !strings.Contains(registry, `"source":"external"`) {
		t.Fatalf("registry = %q", registry)
	}
	if len(services) != 1 || services[0].Source != postgresdb.SourceExternal || services[0].URL != dsn {
		t.Fatalf("services = %+v", services)
	}
}

func TestValidateHeadlessPostgresEnvRequiresExplicitDSN(t *testing.T) {
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"reports": {Kind: "postgres"},
		}},
	}
	err := validateHeadlessPostgresEnv(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "REPORTS_DATABASE_URL") || !strings.Contains(err.Error(), "scenery up") {
		t.Fatalf("validateHeadlessPostgresEnv error = %v", err)
	}
	if err := validateHeadlessPostgresEnv(cfg, []string{"REPORTS_DATABASE_URL=postgres://user:secret@localhost/reports"}); err != nil {
		t.Fatalf("validateHeadlessPostgresEnv rejected explicit DSN: %v", err)
	}
}

func TestManagedSQLiteEnvDiscoversSchemaBackedServiceDatabases(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tasks", "db"), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", "db", "schema.sql"), []byte("CREATE TABLE tasks(id TEXT);"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	cfg := app.Config{
		Name: "demo",
		Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{
			"db": {Kind: "sqlite", DatabaseURLEnv: appDatabaseURLEnv},
		}},
	}
	env, services, err := managedSQLiteEnv(t.Context(), root, cfg, nil)
	if err != nil {
		t.Fatalf("managedSQLiteEnv returned error: %v", err)
	}
	wantPath := filepath.Join(root, ".scenery", "sqlite", "local", "tasks.sqlite")
	if envValueFromList(env, "TASKS_DATABASE_URL") != sqlitedb.URLForPath(wantPath) {
		t.Fatalf("TASKS_DATABASE_URL missing from env: %+v", env)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("tasks database was not created: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("services = %+v, want db and tasks", services)
	}
}
