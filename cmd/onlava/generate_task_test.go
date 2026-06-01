package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	appcfg "github.com/pbrazdil/onlava/internal/app"
)

func TestParseGenerateArgs(t *testing.T) {
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
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{
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
	if payload.SchemaVersion != "onlava.inspect.generators.v1" {
		t.Fatalf("schema_version = %q", payload.SchemaVersion)
	}
	if len(payload.Generators) != 2 {
		t.Fatalf("generators = %+v", payload.Generators)
	}
	if payload.Generators[0].ID != "web" || payload.Generators[1].ID != "sqlc" {
		t.Fatalf("generators = %+v", payload.Generators)
	}
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
		return []byte("create schema onlava_auth;\n"), nil
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
	if text := string(data); !strings.Contains(text, "onlava generate sqlc") || !strings.Contains(text, "create schema onlava_auth") {
		t.Fatalf("schema text = %q", text)
	}
	if len(ran) != 1 || ran[0].Program != "sqlc" || strings.Join(ran[0].Args, " ") != "generate" {
		t.Fatalf("ran = %+v", ran)
	}
}

func TestDBSyncRunsApplyThenSQLC(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{
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
		return []byte("create schema onlava_auth;\n"), nil
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

func TestTaskGraphAndRun(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{
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
	writeTestAppFile(t, root, "auth/db/schema.hcl", `schema "onlava_auth" {}`)
	writeTestAppFile(t, root, "auth/db/queries.sql", `-- name: Ping :one
select 1;
`)
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
