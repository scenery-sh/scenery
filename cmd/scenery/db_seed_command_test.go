package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestDBSeedCommandJSONCapturesCommandOutput(t *testing.T) {
	t.Parallel()

	root := writeDBSeedCommandFixture(t)
	_, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeSeedStore()
	hooks := dbSeedHooks{
		openStore: func(context.Context, string) (databaseSeedStore, error) { return store, nil },
		runCommand: func(context.Context, lifecycleExecRequest) (dbSeedCommandOutput, error) {
			return dbSeedCommandOutput{Stdout: `{"rows":1}` + "\n"}, nil
		},
	}
	dsn := "postgres://user:secret@localhost/seedapp?search_path=catalog%2Cscenery"
	result, err := buildDBSeedResultWithEnvHooks(context.Background(), root, cfg, dbSeedOptions{}, []string{"CATALOG_DATABASE_URL=" + dsn}, false, hooks)
	if err != nil {
		t.Fatalf("build seed result: %v", err)
	}
	var output bytes.Buffer
	if err := writeInspectJSON(&output, result); err != nil {
		t.Fatalf("write CLI JSON: %v", err)
	}
	var payload dbSeedResult
	if err := decodeCLIJSON(output.Bytes(), &payload); err != nil {
		t.Fatalf("decode CLI JSON: %v\n%s", err, output.String())
	}
	if payload.Summary.Applied != 1 || payload.Seeds[0].Output != `{"rows":1}` {
		t.Fatalf("payload = %+v", payload)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.db.seed.result.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("seed result schema diagnostics = %+v", diagnostics)
	}
}

func TestDBSeedCommandAppliesSkipsAndRerunsChangedInput(t *testing.T) {
	t.Parallel()

	root := writeDBSeedCommandFixture(t)
	_, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeSeedStore()
	dsn := "postgres://user:secret@localhost/seedapp?search_path=catalog%2Cscenery"
	runs := 0
	hooks := dbSeedHooks{
		openStore: func(_ context.Context, got string) (databaseSeedStore, error) {
			if got != dsn {
				t.Fatalf("seed store DSN = %q, want %q", got, dsn)
			}
			return store, nil
		},
		runCommand: func(_ context.Context, req lifecycleExecRequest) (dbSeedCommandOutput, error) {
			runs++
			if got := envValueFromList(req.Env, appDatabaseURLEnv); got != dsn {
				t.Fatalf("command DATABASE_URL = %q, want %q", got, dsn)
			}
			if got := envValueFromList(req.Env, "IMPORT_MODE"); got != "atomic" {
				t.Fatalf("command IMPORT_MODE = %q", got)
			}
			if req.Dir != root {
				t.Fatalf("command dir = %q, want %q", req.Dir, root)
			}
			return dbSeedCommandOutput{Stdout: `{"rows":1}` + "\n"}, nil
		},
	}
	env := []string{"CATALOG_DATABASE_URL=" + dsn, appDatabaseURLEnv + "=postgres://wrong"}

	first, err := buildDBSeedResultWithEnvHooks(context.Background(), root, cfg, dbSeedOptions{}, env, false, hooks)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if first.Summary.Applied != 1 || len(first.Seeds) != 1 || first.Seeds[0].Kind != dbSeedPlanKindCommand || first.Seeds[0].Output != `{"rows":1}` {
		t.Fatalf("first result = %+v", first)
	}
	firstHash := first.Seeds[0].SHA256
	if runs != 1 || len(store.commandRecorded) != 1 {
		t.Fatalf("runs = %d recorded = %+v", runs, store.commandRecorded)
	}

	second, err := buildDBSeedResultWithEnvHooks(context.Background(), root, cfg, dbSeedOptions{}, env, false, hooks)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if second.Summary.Skipped != 1 || runs != 1 {
		t.Fatalf("second result = %+v runs = %d", second, runs)
	}

	writeTestAppFile(t, root, "catalog/data.csv", "id,name\n1,updated\n")
	third, err := buildDBSeedResultWithEnvHooks(context.Background(), root, cfg, dbSeedOptions{}, env, false, hooks)
	if err != nil {
		t.Fatalf("third seed: %v", err)
	}
	if third.Summary.Applied != 1 || third.Seeds[0].SHA256 == firstHash || runs != 2 || len(store.commandRecorded) != 2 {
		t.Fatalf("third result = %+v runs = %d recorded = %+v", third, runs, store.commandRecorded)
	}
}

func TestDBSeedCommandFailureDoesNotAdvanceLedger(t *testing.T) {
	t.Parallel()

	root := writeDBSeedCommandFixture(t)
	_, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeSeedStore()
	hooks := dbSeedHooks{
		openStore: func(context.Context, string) (databaseSeedStore, error) { return store, nil },
		runCommand: func(context.Context, lifecycleExecRequest) (dbSeedCommandOutput, error) {
			return dbSeedCommandOutput{Stderr: "invalid catalog"}, errors.New("exit status 1: invalid catalog")
		},
	}
	dsn := "postgres://user:secret@localhost/seedapp?search_path=catalog%2Cscenery"
	result, err := buildDBSeedResultWithEnvHooks(context.Background(), root, cfg, dbSeedOptions{}, []string{"CATALOG_DATABASE_URL=" + dsn}, false, hooks)
	if err == nil || !strings.Contains(err.Error(), "invalid catalog") {
		t.Fatalf("seed error = %v", err)
	}
	if result.Summary.Failed != 1 || len(store.ledger) != 0 || len(store.commandRecorded) != 0 {
		t.Fatalf("result = %+v ledger = %+v recorded = %+v", result, store.ledger, store.commandRecorded)
	}
}

func TestDBSeedCommandDryRunPlansChangedInputWithoutExecuting(t *testing.T) {
	t.Parallel()

	root := writeDBSeedCommandFixture(t)
	_, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := discoverDBSeedPlans(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeSeedStore()
	store.ledger[cfg.AppID()+"|"+plans[0].Path] = "older-hash"
	runs := 0
	hooks := dbSeedHooks{
		openStore: func(context.Context, string) (databaseSeedStore, error) { return store, nil },
		runCommand: func(context.Context, lifecycleExecRequest) (dbSeedCommandOutput, error) {
			runs++
			return dbSeedCommandOutput{}, nil
		},
	}
	dsn := "postgres://user:secret@localhost/seedapp?search_path=catalog%2Cscenery"
	result, err := buildDBSeedResultWithEnvHooks(context.Background(), root, cfg, dbSeedOptions{DryRun: true}, []string{"CATALOG_DATABASE_URL=" + dsn}, false, hooks)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result.Summary.Planned != 1 || result.Summary.Changed != 0 || runs != 0 {
		t.Fatalf("result = %+v runs = %d", result, runs)
	}
}

func TestDiscoverDBSeedCommandPlansRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, "catalog/data.csv", "id\n1\n")
	writeTestAppFile(t, root, "catalog/import.go", "package main\n")
	if err := os.Mkdir(filepath.Join(root, "catalog", "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.csv")
	if err := os.WriteFile(outside, []byte("id\n2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "catalog", "linked.csv")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		commands []appcfg.DatabaseSeedCommandConfig
		want     string
	}{
		{name: "invalid name", commands: []appcfg.DatabaseSeedCommandConfig{seedCommandConfig("Bad Name", "catalog/data.csv")}, want: "must use lowercase"},
		{name: "duplicate name", commands: []appcfg.DatabaseSeedCommandConfig{seedCommandConfig("catalog", "catalog/data.csv"), seedCommandConfig("catalog", "catalog/import.go")}, want: "duplicate name"},
		{name: "unknown service", commands: []appcfg.DatabaseSeedCommandConfig{{Name: "catalog", Service: "missing", Command: "true", Inputs: []string{"catalog/data.csv"}}}, want: "no matching dev.services"},
		{name: "no inputs", commands: []appcfg.DatabaseSeedCommandConfig{{Name: "catalog", Service: "catalog", Command: "true"}}, want: "at least one file"},
		{name: "duplicate input", commands: []appcfg.DatabaseSeedCommandConfig{seedCommandConfig("catalog", "catalog/data.csv", "catalog/data.csv")}, want: "duplicate input"},
		{name: "traversal", commands: []appcfg.DatabaseSeedCommandConfig{seedCommandConfig("catalog", "../outside.csv")}, want: "escapes the app workspace"},
		{name: "absolute", commands: []appcfg.DatabaseSeedCommandConfig{seedCommandConfig("catalog", outside)}, want: "workspace-relative"},
		{name: "directory", commands: []appcfg.DatabaseSeedCommandConfig{seedCommandConfig("catalog", "catalog/directory")}, want: "not a regular file"},
		{name: "symlink", commands: []appcfg.DatabaseSeedCommandConfig{seedCommandConfig("catalog", "catalog/linked.csv")}, want: "contains symlink"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := appcfg.Config{
				Name:     "seedapp",
				Dev:      appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{"catalog": {}}},
				Database: appcfg.DatabaseConfig{Seed: appcfg.DatabaseSeedConfig{Commands: test.commands}},
			}
			_, err := discoverDBSeedCommandPlans(root, cfg)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDeleteDBSeedLedgerForServiceOnlyDeletesMatchingPlans(t *testing.T) {
	t.Parallel()

	store := newFakeSeedStore()
	store.ledger["demo|auth/db/seed.sql"] = "auth"
	store.ledger["demo|.scenery.json#/database/seed/commands/catalog"] = "catalog"
	store.ledger["demo|reports/db/seed.sql"] = "reports"
	plans := []dbSeedPlan{
		{Kind: dbSeedPlanKindSQL, Service: "auth", Path: "auth/db/seed.sql"},
		{Kind: dbSeedPlanKindCommand, Service: "auth", Path: ".scenery.json#/database/seed/commands/catalog"},
		{Kind: dbSeedPlanKindSQL, Service: "reports", Path: "reports/db/seed.sql"},
	}
	hooks := dbSeedHooks{openStore: func(context.Context, string) (databaseSeedStore, error) { return store, nil }}
	if err := deleteDBSeedLedgerForService(context.Background(), "postgres://localhost/demo", "demo", "auth", plans, hooks); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 2 || len(store.ledger) != 1 || store.ledger["demo|reports/db/seed.sql"] != "reports" {
		t.Fatalf("deleted = %+v ledger = %+v", store.deleted, store.ledger)
	}
}

func writeDBSeedCommandFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
  "name": "seedapp",
  "dev": {"services": {"catalog": {}}},
  "database": {
    "seed": {
      "commands": [{
        "name": "catalog",
        "service": "catalog",
        "command": "seed-import --file catalog/data.csv",
        "inputs": ["catalog/data.csv", "catalog/import.go"],
        "env": {"IMPORT_MODE": "atomic"}
      }]
    }
  }
}`)
	writeTestAppFile(t, root, "catalog/data.csv", "id,name\n1,first\n")
	writeTestAppFile(t, root, "catalog/import.go", "package main\n")
	return root
}

func seedCommandConfig(name string, inputs ...string) appcfg.DatabaseSeedCommandConfig {
	return appcfg.DatabaseSeedCommandConfig{Name: name, Service: "catalog", Command: "true", Inputs: inputs}
}
