package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateDataDryRunWritesGeneratedSchema(t *testing.T) {
	root := writeModelDSLAppFixture(t, modelDSLExpectedSchemaHCL)

	var out bytes.Buffer
	if err := runGenerate(context.Background(), &out, []string{"data", "--app-root", root, "--dry-run", "--json"}); err != nil {
		t.Fatalf("runGenerate(data) returned error: %v", err)
	}
	var payload generatorGraphResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if len(payload.Generators) != 1 || payload.Generators[0].ID != "data" || payload.Generators[0].Kind != "model-schema" {
		t.Fatalf("generators = %+v", payload.Generators)
	}
	assertStringSliceContains(t, payload.Generators[0].Outputs, ".scenery/gen/db/tasks/schema.hcl")
	assertStringSliceContains(t, payload.Generators[0].Outputs, ".scenery/gen/db/tasks/seed.sql")
	assertDBArtifact(t, payload.DBArtifacts, "tasks", "generated-schema", "generated-source", ".scenery/gen/db/tasks/schema.hcl")
	assertDBArtifact(t, payload.DBArtifacts, "tasks", "seed", "initial-data", ".scenery/gen/db/tasks/seed.sql")

	data, err := os.ReadFile(filepath.Join(root, ".scenery", "gen", "db", "tasks", "schema.hcl"))
	if err != nil {
		t.Fatalf("read generated schema: %v", err)
	}
	if string(data) != modelDSLExpectedSchemaHCL {
		t.Fatalf("generated schema =\n%s\nwant:\n%s", data, modelDSLExpectedSchemaHCL)
	}
	seed, err := os.ReadFile(filepath.Join(root, ".scenery", "gen", "db", "tasks", "seed.sql"))
	if err != nil {
		t.Fatalf("read generated seed: %v", err)
	}
	if string(seed) != modelDSLExpectedSeedSQL {
		t.Fatalf("generated seed =\n%s\nwant:\n%s", seed, modelDSLExpectedSeedSQL)
	}
}

func TestDBSeedDiscoversGeneratedModelSeed(t *testing.T) {
	root := writeModelDSLAppFixture(t, modelDSLExpectedSchemaHCL)
	writeTestAppFile(t, root, ".env", "DatabaseURL=postgres://localhost/modeldsl\n")
	store := newFakeSeedStore()
	restore := stubSeedStore(t, store)
	defer restore()

	var out bytes.Buffer
	if err := runDBSeed(context.Background(), &out, []string{"--app-root", root, "--dry-run", "--json"}); err != nil {
		t.Fatalf("runDBSeed returned error: %v\n%s", err, out.String())
	}
	var payload dbSeedResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.Summary.Planned != 1 || len(payload.Seeds) != 1 || payload.Seeds[0].Path != ".scenery/gen/db/tasks/seed.sql" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestDBDiffGeneratedReportsSchemaDrift(t *testing.T) {
	root := writeModelDSLAppFixture(t, `schema "public" {}
`)

	var out bytes.Buffer
	err := runDBGeneratedDiff(&out, []string{"--generated", "--app-root", root, "--json"})
	var silent *silentCLIError
	if !errors.As(err, &silent) {
		t.Fatalf("runDBGeneratedDiff drift error = %v", err)
	}
	var payload dbGeneratedDiffResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.OK || len(payload.Drift) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Drift[0].Service != "tasks" || !strings.Contains(payload.Drift[0].Message, "tasks/db/schema.hcl") {
		t.Fatalf("drift = %+v", payload.Drift[0])
	}
}

func TestDBDiffGeneratedPassesWhenSchemaMatches(t *testing.T) {
	root := writeModelDSLAppFixture(t, modelDSLExpectedSchemaHCL)

	var out bytes.Buffer
	if err := runDBGeneratedDiff(&out, []string{"--generated", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runDBGeneratedDiff returned error: %v\n%s", err, out.String())
	}
	var payload dbGeneratedDiffResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if !payload.OK || len(payload.Drift) != 0 || len(payload.Generated) != 1 || payload.Generated[0].GeneratedPath != ".scenery/gen/db/tasks/schema.hcl" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRunSceneryCheckReportsGeneratedSchemaDrift(t *testing.T) {
	root := writeModelDSLAppFixture(t, `schema "public" {}
`)

	var out bytes.Buffer
	err := runSceneryCheck(context.Background(), &out, []string{"--app-root", root, "--json"})
	var silent *silentCLIError
	if !errors.As(err, &silent) {
		t.Fatalf("runSceneryCheck drift error = %v", err)
	}
	var payload checkResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.OK || len(payload.Diagnostics) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Diagnostics[0].Stage != "model-schema" || payload.Diagnostics[0].File != "tasks/db/schema.hcl" {
		t.Fatalf("diagnostic = %+v", payload.Diagnostics[0])
	}
}

func writeModelDSLAppFixture(t *testing.T, schemaHCL string) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"modeldsl","id":"modeldsl-dev","proxy":{"frontends":{"web":{"root":"web"}}}}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/modeldsl\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRootForTest(t)+"\n")
	source, err := os.ReadFile(filepath.Join(repoRootForTest(t), "testdata", "apps", "model-dsl", "tasks", "model.go"))
	if err != nil {
		t.Fatalf("read model fixture: %v", err)
	}
	writeTestAppFile(t, root, "tasks/model.go", string(source))
	component, err := os.ReadFile(filepath.Join(repoRootForTest(t), "testdata", "apps", "model-dsl", "web", "src", "components", "TaskStatusBadge.tsx"))
	if err != nil {
		t.Fatalf("read component fixture: %v", err)
	}
	writeTestAppFile(t, root, "web/src/components/TaskStatusBadge.tsx", string(component))
	writeTestAppFile(t, root, "tasks/db/schema.hcl", schemaHCL)
	return root
}

const modelDSLExpectedSchemaHCL = `// Code generated by scenery generate data; DO NOT EDIT.

schema "public" {}

enum "tasks_status" {
  schema = schema.public
  values = ["todo", "doing", "done"]
}

table "tasks" {
  schema = schema.public

  column "id" {
    null = false
    type = text
  }

  column "title" {
    null = false
    type = text
  }

  column "status" {
    null = false
    type = enum.tasks_status
  }

  column "project_id" {
    null = false
    type = text
  }

  column "created_at" {
    null = false
    type = timestamptz
  }

  primary_key {
    columns = [column.id]
  }
}

`

const modelDSLExpectedSeedSQL = `-- Code generated by scenery generate data; DO NOT EDIT.

insert into "tasks" ("id", "title", "status", "project_id", "created_at")
values ('seed-task-1', 'Seeded task', 'todo', 'seed-project', '2026-06-12T12:00:00Z'::timestamptz)
on conflict ("id") do update set
  "title" = excluded."title",
  "status" = excluded."status",
  "project_id" = excluded."project_id",
  "created_at" = excluded."created_at";

`
