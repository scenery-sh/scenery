package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestParseGenerateArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseGenerateArgs([]string{"client", "demo", "--lang", "typescript", "--output", "src/client.ts", "--app-root", "/tmp/app", "--dry-run", "--json"})
	if err != nil {
		t.Fatalf("parseGenerateArgs returned error: %v", err)
	}
	if opts.Subject != "client" || opts.Target != "demo" || opts.Lang != "typescript" || opts.Output != "src/client.ts" || opts.AppRoot != "/tmp/app" || !opts.DryRun || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseGenerateArgs([]string{"sqlc", "--output", "client.ts"}); err == nil || err.Error() != "--output is only supported for generate client" {
		t.Fatalf("parseGenerateArgs(sqlc --output) error = %v", err)
	}
}

func TestBuildSQLCGeneratorPlanInfersAtlasSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSQLCFixture(t, root)

	plan, ok, err := buildSQLCGeneratorPlan(root, appcfg.Config{Name: "demo"})
	if err != nil {
		t.Fatalf("buildSQLCGeneratorPlan returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected sqlc generator to be configured")
	}
	if plan.ConfigPath != "sqlc.yaml" {
		t.Fatalf("config path = %q", plan.ConfigPath)
	}
	if len(plan.Schemas) != 1 {
		t.Fatalf("schemas = %+v", plan.Schemas)
	}
	if plan.Schemas[0].SQLCSchema != "auth/db/gen/schema.sql" || plan.Schemas[0].AtlasSource != "auth/db/schema.hcl" {
		t.Fatalf("schema plan = %+v", plan.Schemas[0])
	}
	assertStringSliceContains(t, plan.Record.Inputs, "auth/db/schema.hcl")
	assertStringSliceContains(t, plan.Record.Inputs, "auth/db/queries.sql")
	assertStringSliceContains(t, plan.Record.Outputs, "auth/db/gen/schema.sql")
	assertStringSliceContains(t, plan.Record.Outputs, "auth/db/gen")
}

func TestRunGenerateDryRunJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
  "name": "demo",
  "id": "demo-id",
  "generators": {
    "clients": [
      { "id": "web", "kind": "typescript-client", "output": "web/src/client.ts" }
    ],
    "sqlc": { "provider": "sqlc", "config": "sqlc.yaml" }
  }
}`)
	writeSQLCFixture(t, root)

	var out bytes.Buffer
	if err := runGenerate(context.Background(), &out, []string{"--app-root", root, "--dry-run", "--json"}); err != nil {
		t.Fatalf("runGenerate returned error: %v", err)
	}
	var payload generatorGraphResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "scenery.inspect.generators.v1" {
		t.Fatalf("schema_version = %q", payload.SchemaVersion)
	}
	if len(payload.Generators) != 2 {
		t.Fatalf("generators = %+v", payload.Generators)
	}
	if payload.Generators[0].ID != "web" || payload.Generators[1].ID != "sqlc" {
		t.Fatalf("generators = %+v", payload.Generators)
	}
}

func TestSQLCGeneratorIgnoresSeedData(t *testing.T) {
	root := t.TempDir()
	writeSQLCFixture(t, root)
	writeTestAppFile(t, root, "auth/db/seed.sql", `insert into scenery_auth.users(id) values ('dev-user');
`)

	plan, ok, err := buildSQLCGeneratorPlan(root, appcfg.Config{Name: "demo"})
	if err != nil || !ok {
		t.Fatalf("buildSQLCGeneratorPlan ok=%v err=%v", ok, err)
	}
	assertStringSliceNotContains(t, plan.Record.Inputs, "auth/db/seed.sql")
	assertStringSliceNotContains(t, plan.Record.Outputs, "auth/db/seed.sql")

	var ran []lifecycleExecRequest
	restore := stubLifecycleExec(t, func(_ context.Context, req lifecycleExecRequest) error {
		ran = append(ran, req)
		return nil
	}, func(_ context.Context, req lifecycleExecRequest) ([]byte, error) {
		if req.Program != "atlas" {
			t.Fatalf("output command program = %q", req.Program)
		}
		return []byte("create schema scenery_auth;\n"), nil
	})
	defer restore()

	if err := runSQLCGenerator(context.Background(), &bytes.Buffer{}, root, plan, true); err != nil {
		t.Fatalf("runSQLCGenerator returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "auth/db/gen/schema.sql"))
	if err != nil {
		t.Fatalf("read generated schema: %v", err)
	}
	if strings.Contains(string(data), "dev-user") {
		t.Fatalf("generated schema included seed data: %q", string(data))
	}
	if len(ran) != 1 || ran[0].Program != "sqlc" {
		t.Fatalf("ran = %+v", ran)
	}
}

func TestInspectGeneratorsDiscoversServiceDBArtifacts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
  "name": "demo",
  "id": "demo-id",
  "generators": {
    "sqlc": { "provider": "sqlc", "config": "sqlc.yaml" }
  }
}`)
	writeSQLCFixture(t, root)
	writeTestAppFile(t, root, "auth/db/seed.sql", `insert into scenery_auth.users(id) values ('dev-user');
`)
	writeTestAppFile(t, root, "billing/db/schema.hcl", `schema "billing" {}`)
	writeTestAppFile(t, root, "billing/db/queries.sql", `-- name: BillingPing :one
select 1;
`)

	var out bytes.Buffer
	if err := runSceneryInspect([]string{"generators", "--app-root", root, "--json"}, &out); err != nil {
		t.Fatalf("runSceneryInspect(generators) returned error: %v", err)
	}
	var payload generatorGraphResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if len(payload.Generators) != 1 || payload.Generators[0].ID != "sqlc" {
		t.Fatalf("generators = %+v", payload.Generators)
	}
	assertStringSliceNotContains(t, payload.Generators[0].Inputs, "auth/db/seed.sql")
	assertStringSliceNotContains(t, payload.Generators[0].Outputs, "auth/db/seed.sql")
	assertDBArtifact(t, payload.DBArtifacts, "auth", "schema-source", "schema", "auth/db/schema.hcl")
	assertDBArtifact(t, payload.DBArtifacts, "auth", "query", "query-generation-input", "auth/db/queries.sql")
	assertDBArtifact(t, payload.DBArtifacts, "auth", "generated-schema", "generated-source", "auth/db/gen/schema.sql")
	assertDBArtifact(t, payload.DBArtifacts, "auth", "seed", "initial-data", "auth/db/seed.sql")
	assertDBArtifact(t, payload.DBArtifacts, "billing", "schema-source", "schema", "billing/db/schema.hcl")
	assertDBArtifact(t, payload.DBArtifacts, "billing", "query", "query-generation-input", "billing/db/queries.sql")
}

func TestRunSQLCGeneratorUsesAtlasAndSQLC(t *testing.T) {
	root := t.TempDir()
	writeSQLCFixture(t, root)
	plan, ok, err := buildSQLCGeneratorPlan(root, appcfg.Config{Name: "demo"})
	if err != nil || !ok {
		t.Fatalf("buildSQLCGeneratorPlan ok=%v err=%v", ok, err)
	}

	var ran []lifecycleExecRequest
	restore := stubLifecycleExec(t, func(_ context.Context, req lifecycleExecRequest) error {
		ran = append(ran, req)
		return nil
	}, func(_ context.Context, req lifecycleExecRequest) ([]byte, error) {
		if req.Program != "atlas" {
			t.Fatalf("output command program = %q", req.Program)
		}
		return []byte("create schema scenery_auth;\n"), nil
	})
	defer restore()

	var out bytes.Buffer
	if err := runSQLCGenerator(context.Background(), &out, root, plan, false); err != nil {
		t.Fatalf("runSQLCGenerator returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "auth/db/gen/schema.sql"))
	if err != nil {
		t.Fatalf("read generated schema: %v", err)
	}
	if text := string(data); !strings.Contains(text, "scenery generate sqlc") || !strings.Contains(text, "create schema scenery_auth") {
		t.Fatalf("schema text = %q", text)
	}
	if len(ran) != 1 || ran[0].Program != "sqlc" || strings.Join(ran[0].Args, " ") != "generate" {
		t.Fatalf("ran = %+v", ran)
	}
}

func TestDBSyncRunsApplyThenSQLC(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
  "name": "demo",
  "database": {
    "apply": {
      "provider": "exec",
      "command": "./scripts/db-safe-apply.sh",
      "cwd": "scripts",
      "env": { "MIGRATION_MODE": "safe" }
    }
  },
  "generators": {
    "sqlc": { "provider": "sqlc", "config": "sqlc.yaml" }
  }
}`)
	writeSQLCFixture(t, root)

	var ran []lifecycleExecRequest
	restore := stubLifecycleExec(t, func(_ context.Context, req lifecycleExecRequest) error {
		ran = append(ran, req)
		return nil
	}, func(_ context.Context, req lifecycleExecRequest) ([]byte, error) {
		return []byte("create schema scenery_auth;\n"), nil
	})
	defer restore()

	if err := dbSyncCommand([]string{"--app-root", root}); err != nil {
		t.Fatalf("dbSyncCommand returned error: %v", err)
	}
	if len(ran) != 2 {
		t.Fatalf("ran = %+v", ran)
	}
	if ran[0].Dir != filepath.Join(root, "scripts") {
		t.Fatalf("apply dir = %q", ran[0].Dir)
	}
	if runtime.GOOS != "windows" && (ran[0].Program != "/bin/sh" || strings.Join(ran[0].Args, " ") != "-c ./scripts/db-safe-apply.sh") {
		t.Fatalf("apply command = %+v", ran[0])
	}
	if !containsEnv(ran[0].Env, "MIGRATION_MODE=safe") {
		t.Fatalf("apply env missing overlay: %+v", ran[0].Env)
	}
	if ran[1].Program != "sqlc" {
		t.Fatalf("second command = %+v", ran[1])
	}
}

func TestDBApplyRunsApplyWithoutSQLC(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
  "name": "demo",
  "database": {
    "apply": {
      "provider": "exec",
      "command": "./scripts/db-safe-apply.sh",
      "cwd": "scripts",
      "env": { "MIGRATION_MODE": "safe" }
    }
  },
  "generators": {
    "sqlc": { "provider": "sqlc", "config": "sqlc.yaml" }
  }
}`)
	writeSQLCFixture(t, root)

	var ran []lifecycleExecRequest
	restore := stubLifecycleExec(t, func(_ context.Context, req lifecycleExecRequest) error {
		ran = append(ran, req)
		return nil
	}, func(_ context.Context, req lifecycleExecRequest) ([]byte, error) {
		t.Fatalf("db apply must not run output lifecycle exec: %+v", req)
		return nil, nil
	})
	defer restore()

	var out bytes.Buffer
	if err := runDBApply(context.Background(), &out, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("runDBApply returned error: %v", err)
	}
	if len(ran) != 1 {
		t.Fatalf("ran = %+v", ran)
	}
	if ran[0].Dir != filepath.Join(root, "scripts") {
		t.Fatalf("apply dir = %q", ran[0].Dir)
	}
	if runtime.GOOS != "windows" && (ran[0].Program != "/bin/sh" || strings.Join(ran[0].Args, " ") != "-c ./scripts/db-safe-apply.sh") {
		t.Fatalf("apply command = %+v", ran[0])
	}
	if !containsEnv(ran[0].Env, "MIGRATION_MODE=safe") {
		t.Fatalf("apply env missing overlay: %+v", ran[0].Env)
	}

	var payload dbApplyResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "scenery.db.apply.result.v1" || payload.Apply.Provider != "exec" || payload.Apply.Status != "applied" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestDBApplyReportsMissingConfiguration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo"}`)

	err := runDBApply(context.Background(), &bytes.Buffer{}, []string{"--app-root", root})
	if err == nil || err.Error() != "database.apply is not configured" {
		t.Fatalf("runDBApply missing config error = %v", err)
	}
}

func TestDBSeedDryRunPlansSeedWithoutApplying(t *testing.T) {
	root := writeSeedCommandFixture(t)
	store := newFakeSeedStore()
	restore := stubSeedStore(t, store)
	defer restore()

	var out bytes.Buffer
	if err := runDBSeed(context.Background(), &out, []string{"--app-root", root, "--dry-run", "--json"}); err != nil {
		t.Fatalf("runDBSeed returned error: %v", err)
	}
	var payload dbSeedResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if !payload.DryRun || payload.Summary.Planned != 1 || payload.Summary.Applied != 0 {
		t.Fatalf("payload = %+v", payload)
	}
	if len(store.applied) != 0 {
		t.Fatalf("dry run applied seeds: %+v", store.applied)
	}
}

func TestDBSeedAppliesThenSkipsUnchangedSeed(t *testing.T) {
	root := writeSeedCommandFixture(t)
	store := newFakeSeedStore()
	restore := stubSeedStore(t, store)
	defer restore()

	var first bytes.Buffer
	if err := runDBSeed(context.Background(), &first, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("first runDBSeed returned error: %v", err)
	}
	var firstPayload dbSeedResult
	if err := json.Unmarshal(first.Bytes(), &firstPayload); err != nil {
		t.Fatalf("json.Unmarshal first: %v\n%s", err, first.String())
	}
	if firstPayload.Summary.Applied != 1 || len(store.applied) != 1 {
		t.Fatalf("first payload = %+v store = %+v", firstPayload, store.applied)
	}

	var second bytes.Buffer
	if err := runDBSeed(context.Background(), &second, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("second runDBSeed returned error: %v", err)
	}
	var secondPayload dbSeedResult
	if err := json.Unmarshal(second.Bytes(), &secondPayload); err != nil {
		t.Fatalf("json.Unmarshal second: %v\n%s", err, second.String())
	}
	if secondPayload.Summary.Skipped != 1 || len(store.applied) != 1 {
		t.Fatalf("second payload = %+v store = %+v", secondPayload, store.applied)
	}
}

func TestDBSeedChangedSeedFailsClosed(t *testing.T) {
	root := writeSeedCommandFixture(t)
	store := newFakeSeedStore()
	store.ledger["seedapp|auth/db/seed.sql"] = "old-hash"
	restore := stubSeedStore(t, store)
	defer restore()

	var out bytes.Buffer
	err := runDBSeed(context.Background(), &out, []string{"--app-root", root, "--json"})
	if err == nil || !strings.Contains(err.Error(), "changed after it was applied") {
		t.Fatalf("runDBSeed changed seed error = %v", err)
	}
	var payload dbSeedResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.Summary.Changed != 1 || payload.Seeds[0].Status != "changed" || len(store.applied) != 0 {
		t.Fatalf("payload = %+v store = %+v", payload, store.applied)
	}
}

func TestDBSeedApplyFailureReportsFailed(t *testing.T) {
	root := writeSeedCommandFixture(t)
	store := newFakeSeedStore()
	store.applyErr = errors.New("boom")
	restore := stubSeedStore(t, store)
	defer restore()

	var out bytes.Buffer
	err := runDBSeed(context.Background(), &out, []string{"--app-root", root, "--json"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("runDBSeed apply error = %v", err)
	}
	var payload dbSeedResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.Summary.Failed != 1 || payload.Seeds[0].Status != "failed" || len(store.ledger) != 0 {
		t.Fatalf("payload = %+v store = %+v", payload, store.ledger)
	}
}

func TestDBSeedSafetyAllowsIdempotentInsertsAndUpserts(t *testing.T) {
	root := writeSeedCommandFixture(t)
	writeTestAppFile(t, root, "auth/db/seed.sql", `insert into scenery_auth.users(id) values ('dev-user');
insert into scenery_auth.users(id) values ('dev-user') on conflict (id) do update set id = excluded.id;
delete from scenery_auth.temp_users where id = 'dev-user';
`)
	store := newFakeSeedStore()
	restore := stubSeedStore(t, store)
	defer restore()

	var out bytes.Buffer
	if err := runDBSeed(context.Background(), &out, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("runDBSeed safe seed returned error: %v", err)
	}
	var payload dbSeedResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.Summary.Applied != 1 || payload.Summary.Failed != 0 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestDBSeedSafetyRejectsDestructiveStatements(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		message string
	}{
		{name: "drop", sql: "drop table scenery_auth.users;\n", message: "DROP"},
		{name: "truncate", sql: "truncate table scenery_auth.users;\n", message: "TRUNCATE"},
		{name: "delete without where", sql: "delete from scenery_auth.users;\n", message: "broad DELETE"},
		{name: "delete where true", sql: "delete from scenery_auth.users where true;\n", message: "broad DELETE"},
		{name: "delete one equals one", sql: "delete from scenery_auth.users where 1 = 1;\n", message: "broad DELETE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeSeedCommandFixture(t)
			writeTestAppFile(t, root, "auth/db/seed.sql", tt.sql)
			store := newFakeSeedStore()
			restore := stubSeedStore(t, store)
			defer restore()

			var out bytes.Buffer
			err := runDBSeed(context.Background(), &out, []string{"--app-root", root, "--json"})
			if err == nil || !strings.Contains(err.Error(), tt.message) || !strings.Contains(err.Error(), "auth/db/seed.sql") {
				t.Fatalf("runDBSeed error = %v", err)
			}
			var payload dbSeedResult
			if unmarshalErr := json.Unmarshal(out.Bytes(), &payload); unmarshalErr != nil {
				t.Fatalf("json.Unmarshal: %v\n%s", unmarshalErr, out.String())
			}
			if payload.Summary.Failed != 1 || len(payload.Seeds) != 1 || len(payload.Seeds[0].Diagnostics) == 0 {
				t.Fatalf("payload = %+v", payload)
			}
			if payload.Seeds[0].Path != "auth/db/seed.sql" || payload.Seeds[0].Diagnostics[0].Line != 1 {
				t.Fatalf("diagnostic = %+v", payload.Seeds[0].Diagnostics)
			}
			if len(store.applied) != 0 {
				t.Fatalf("unsafe seed was applied: %+v", store.applied)
			}
		})
	}
}

func TestDBSeedSafetyIgnoresCommentsAndStrings(t *testing.T) {
	root := writeSeedCommandFixture(t)
	writeTestAppFile(t, root, "auth/db/seed.sql", `-- drop table scenery_auth.users;
/* truncate table scenery_auth.users; */
insert into scenery_auth.audit(message) values ('delete from scenery_auth.users;');
insert into scenery_auth.audit(message) values ($$drop table scenery_auth.users;$$);
`)
	store := newFakeSeedStore()
	restore := stubSeedStore(t, store)
	defer restore()

	var out bytes.Buffer
	if err := runDBSeed(context.Background(), &out, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("runDBSeed comments/strings returned error: %v", err)
	}
	var payload dbSeedResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.Summary.Applied != 1 || payload.Summary.Failed != 0 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestDBSeedSafetyHasNoForceEscapeHatch(t *testing.T) {
	t.Parallel()

	if _, err := parseDBSeedArgs([]string{"--force"}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("parseDBSeedArgs --force error = %v", err)
	}
	if _, err := parseDBSeedArgs([]string{"--reseed"}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("parseDBSeedArgs --reseed error = %v", err)
	}
}

func TestDBSetupRunsApplyThenSeed(t *testing.T) {
	root := writeSetupCommandFixture(t)
	store := newFakeSeedStore()
	var events []string
	restoreSeed := stubSeedStore(t, store)
	defer restoreSeed()
	restoreExec := stubLifecycleExec(t, func(_ context.Context, req lifecycleExecRequest) error {
		events = append(events, "apply:"+req.Program)
		return nil
	}, nil)
	defer restoreExec()

	var out bytes.Buffer
	if err := runDBSetup(context.Background(), &out, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("runDBSetup returned error: %v", err)
	}
	var payload dbSetupResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.Apply.Status != "applied" || payload.Seed.Summary.Applied != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	if len(events) != 1 || len(store.applied) != 1 {
		t.Fatalf("events = %+v applied = %+v", events, store.applied)
	}
}

func TestDBSetupApplyUsesManagedPostgresBranchDatabaseURL(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	t.Setenv(devPostgresAdminURLEnv, "postgres://scenery:secret@127.0.0.1:55432/postgres?sslmode=disable")
	root := t.TempDir()
	writeTestAppFile(t, root, ".env", "DatabaseURL=postgres://stale:stale@127.0.0.1:5433/postgres\nDATABASE_URL=postgres://legacy/poison\n")
	writeTestAppFile(t, root, ".scenery.json", `{
  "name": "managedsetup",
  "dev": {
    "services": {
      "postgres": {
        "kind": "postgres",
        "mode": "local",
        "isolation": "database",
        "branch_strategy": "template_database",
        "project": "managedsetup",
        "parent_database": "managedsetup_main",
        "branch_policy": "manual",
        "database": "managedsetup"
      }
    }
  },
  "database": {
    "apply": {
      "provider": "exec",
      "command": "true"
    }
  }
}`)
	pin := worktreeDBPin{
		SchemaVersion: dbBranchPinSchemaVersion,
		Provider:      postgresBranchProviderName,
		Project:       "managedsetup",
		ParentBranch:  "main",
		Branch:        "feature",
		BranchID:      "br-local-feature",
		Database:      "managedsetup_feature",
		Role:          "scenery",
		CreatedBy:     "scenery",
	}
	if err := writeWorktreeDBPin(root, pin); err != nil {
		t.Fatalf("writeWorktreeDBPin: %v", err)
	}
	endpoint := dbBranchEndpoint{Host: "127.0.0.1", Port: 55432, Database: pin.Database, Role: "scenery", SSLMode: "disable", Source: "postgres"}
	if err := upsertPostgresBranchLease(pin, &endpoint, "ready"); err != nil {
		t.Fatalf("upsertPostgresBranchLease: %v", err)
	}
	var applyEnv []string
	restoreExec := stubLifecycleExec(t, func(_ context.Context, req lifecycleExecRequest) error {
		applyEnv = req.Env
		return nil
	}, nil)
	defer restoreExec()
	var out bytes.Buffer
	if err := runDBSetup(context.Background(), &out, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("runDBSetup returned error: %v\n%s", err, out.String())
	}
	wantURL := "postgres://scenery:secret@127.0.0.1:55432/managedsetup_feature?sslmode=disable"
	if !containsEnv(applyEnv, appDatabaseURLEnv+"="+wantURL) || !containsEnv(applyEnv, "SCENERY_MANAGED_DATABASE_URL="+wantURL) || !containsEnv(applyEnv, "SCENERY_MANAGED_DATABASE_NAME=managedsetup_feature") {
		t.Fatalf("apply env missing managed database values: %+v", applyEnv)
	}
	if containsEnv(applyEnv, legacyDatabaseURLEnv+"=postgres://legacy/poison") {
		t.Fatalf("apply env leaked stale %s: %+v", legacyDatabaseURLEnv, applyEnv)
	}
}

func TestDBSetupStopsWhenApplyFails(t *testing.T) {
	root := writeSetupCommandFixture(t)
	store := newFakeSeedStore()
	restoreSeed := stubSeedStore(t, store)
	defer restoreSeed()
	restoreExec := stubLifecycleExec(t, func(context.Context, lifecycleExecRequest) error {
		return errors.New("apply failed")
	}, nil)
	defer restoreExec()

	var out bytes.Buffer
	err := runDBSetup(context.Background(), &out, []string{"--app-root", root, "--json"})
	if err == nil || !strings.Contains(err.Error(), "apply failed") {
		t.Fatalf("runDBSetup apply error = %v", err)
	}
	var payload dbSetupResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.Apply.Status != "failed" || len(store.applied) != 0 {
		t.Fatalf("payload = %+v store = %+v", payload, store.applied)
	}
}

func TestDBSetupReportsSeedFailure(t *testing.T) {
	root := writeSetupCommandFixture(t)
	store := newFakeSeedStore()
	store.applyErr = errors.New("seed failed")
	restoreSeed := stubSeedStore(t, store)
	defer restoreSeed()
	restoreExec := stubLifecycleExec(t, func(context.Context, lifecycleExecRequest) error {
		return nil
	}, nil)
	defer restoreExec()

	var out bytes.Buffer
	err := runDBSetup(context.Background(), &out, []string{"--app-root", root, "--json"})
	if err == nil || !strings.Contains(err.Error(), "seed failed") {
		t.Fatalf("runDBSetup seed error = %v", err)
	}
	var payload dbSetupResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.Apply.Status != "applied" || payload.Seed.Summary.Failed != 1 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestDBSetupRepeatedRunSkipsUnchangedSeed(t *testing.T) {
	root := writeSetupCommandFixture(t)
	store := newFakeSeedStore()
	restoreSeed := stubSeedStore(t, store)
	defer restoreSeed()
	restoreExec := stubLifecycleExec(t, func(context.Context, lifecycleExecRequest) error {
		return nil
	}, nil)
	defer restoreExec()

	if err := runDBSetup(context.Background(), &bytes.Buffer{}, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("first runDBSetup returned error: %v", err)
	}
	var out bytes.Buffer
	if err := runDBSetup(context.Background(), &out, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("second runDBSetup returned error: %v", err)
	}
	var payload dbSetupResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.Seed.Summary.Skipped != 1 || len(store.applied) != 1 {
		t.Fatalf("payload = %+v store = %+v", payload, store.applied)
	}
}

func TestTaskGraphAndRun(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
  "name": "demo",
  "tasks": {
    "echo": {
      "cwd": "tools",
      "run": "echo hello",
      "env": { "TASK_MODE": "test" }
    }
  }
}`)

	var graphOut bytes.Buffer
	if err := runTaskCommand(context.Background(), &graphOut, []string{"graph", "--json", "--app-root", root}); err != nil {
		t.Fatalf("runTaskCommand graph returned error: %v", err)
	}
	var graph taskGraphResponse
	if err := json.Unmarshal(graphOut.Bytes(), &graph); err != nil {
		t.Fatalf("json.Unmarshal graph: %v\n%s", err, graphOut.String())
	}
	if len(graph.Tasks) != 1 || graph.Tasks[0].Name != "echo" || graph.Tasks[0].EnvKeys[0] != "TASK_MODE" {
		t.Fatalf("graph = %+v", graph)
	}

	var ran []lifecycleExecRequest
	restore := stubLifecycleExec(t, func(_ context.Context, req lifecycleExecRequest) error {
		ran = append(ran, req)
		return nil
	}, nil)
	defer restore()
	if err := runTaskCommand(context.Background(), &bytes.Buffer{}, []string{"run", "echo", "--app-root", root}); err != nil {
		t.Fatalf("runTaskCommand run returned error: %v", err)
	}
	if len(ran) != 1 || ran[0].Dir != filepath.Join(root, "tools") {
		t.Fatalf("ran = %+v", ran)
	}
	if !containsEnv(ran[0].Env, "TASK_MODE=test") {
		t.Fatalf("task env missing overlay: %+v", ran[0].Env)
	}
}

func writeSQLCFixture(t *testing.T, root string) {
	t.Helper()
	writeTestAppFile(t, root, "sqlc.yaml", `version: "2"
sql:
  - engine: "postgresql"
    schema:
      - "auth/db/gen/schema.sql"
    queries:
      - "auth/db/queries.sql"
    gen:
      go:
        out: "auth/db/gen"
`)
	writeTestAppFile(t, root, "auth/db/schema.hcl", `schema "scenery_auth" {}`)
	writeTestAppFile(t, root, "auth/db/queries.sql", `-- name: Ping :one
select 1;
`)
}

func writeSeedCommandFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"seedapp"}`)
	writeTestAppFile(t, root, ".env", "DATABASE_URL=postgres://localhost/seedapp\n")
	writeTestAppFile(t, root, "auth/db/seed.sql", `insert into scenery_auth.users(id) values ('dev-user');
`)
	return root
}

func writeSetupCommandFixture(t *testing.T) string {
	t.Helper()
	root := writeSeedCommandFixture(t)
	writeTestAppFile(t, root, ".scenery.json", `{
  "name": "seedapp",
  "database": {
    "apply": {
      "provider": "exec",
      "command": "true"
    }
  }
}`)
	return root
}

func stubLifecycleExec(t *testing.T, run func(context.Context, lifecycleExecRequest) error, output func(context.Context, lifecycleExecRequest) ([]byte, error)) func() {
	t.Helper()
	oldRun := runLifecycleExec
	oldOutput := outputLifecycleExec
	if run != nil {
		runLifecycleExec = run
	}
	if output != nil {
		outputLifecycleExec = output
	}
	return func() {
		runLifecycleExec = oldRun
		outputLifecycleExec = oldOutput
	}
}

func assertStringSliceContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%q not found in %+v", want, values)
}

func assertStringSliceNotContains(t *testing.T, values []string, unwanted string) {
	t.Helper()
	for _, value := range values {
		if value == unwanted {
			t.Fatalf("%q unexpectedly found in %+v", unwanted, values)
		}
	}
}

func assertDBArtifact(t *testing.T, artifacts []databaseArtifactRecord, service, kind, role, path string) {
	t.Helper()
	for _, artifact := range artifacts {
		if artifact.Service == service && artifact.Kind == kind && artifact.Role == role && artifact.Path == path {
			return
		}
	}
	t.Fatalf("artifact %s %s %s %s not found in %+v", service, kind, role, path, artifacts)
}

type fakeSeedStore struct {
	ledger    map[string]string
	applied   []string
	applyErr  error
	closed    bool
	ensureRan bool
}

func newFakeSeedStore() *fakeSeedStore {
	return &fakeSeedStore{ledger: map[string]string{}}
}

func (s *fakeSeedStore) Close(context.Context) error {
	s.closed = true
	return nil
}

func (s *fakeSeedStore) EnsureLedger(context.Context) error {
	s.ensureRan = true
	return nil
}

func (s *fakeSeedStore) LookupSeed(_ context.Context, appID, path string) (string, bool, error) {
	hash, ok := s.ledger[appID+"|"+path]
	return hash, ok, nil
}

func (s *fakeSeedStore) ApplySeed(_ context.Context, appID, path, hash, _ string) error {
	if s.applyErr != nil {
		return s.applyErr
	}
	s.ledger[appID+"|"+path] = hash
	s.applied = append(s.applied, path)
	return nil
}

func stubSeedStore(t *testing.T, store *fakeSeedStore) func() {
	t.Helper()
	oldOpen := openDatabaseSeedStore
	openDatabaseSeedStore = func(context.Context, string) (databaseSeedStore, error) {
		return store, nil
	}
	return func() {
		openDatabaseSeedStore = oldOpen
	}
}
