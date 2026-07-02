package main

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/app"
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
