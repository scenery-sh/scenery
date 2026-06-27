package main

import (
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
