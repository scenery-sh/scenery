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
	if len(payload.Generators) != 2 || payload.Generators[0].ID != "data" || payload.Generators[0].Kind != "model-schema" {
		t.Fatalf("generators = %+v", payload.Generators)
	}
	if payload.Generators[1].ID != "web:web" || payload.Generators[1].Kind != "model-web" {
		t.Fatalf("web generator = %+v", payload.Generators)
	}
	assertStringSliceContains(t, payload.Generators[0].Outputs, ".scenery/gen/db/tasks/schema.hcl")
	assertStringSliceContains(t, payload.Generators[0].Outputs, ".scenery/gen/db/tasks/seed.sql")
	assertStringSliceContains(t, payload.Generators[1].Outputs, ".scenery/gen/web/web/index.ts")
	assertStringSliceContains(t, payload.Generators[1].Outputs, ".scenery/gen/web/web/routes.tsx")
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

func TestGenerateDataWritesDeterministicGeneratedWebPackage(t *testing.T) {
	root := writeModelDSLAppFixture(t, modelDSLExpectedSchemaHCL)

	var out bytes.Buffer
	if err := runGenerate(context.Background(), &out, []string{"data", "--app-root", root, "--dry-run", "--json"}); err != nil {
		t.Fatalf("runGenerate(data) returned error: %v", err)
	}

	webRoot := filepath.Join(root, ".scenery", "gen", "web", "web")
	wantFiles := []string{"collections.ts", "index.ts", "models.ts", "package.json", "projections.ts", "routes.tsx", "runtime.ts", "shapes.ts"}
	first := map[string]string{}
	for _, name := range wantFiles {
		data, err := os.ReadFile(filepath.Join(webRoot, name))
		if err != nil {
			t.Fatalf("read generated web file %s: %v", name, err)
		}
		first[name] = string(data)
	}
	for name, data := range map[string]string{
		"models.ts":      "export interface TaskRow",
		"shapes.ts":      "export const taskSource",
		"projections.ts": "export interface TaskListRecord",
		"collections.ts": "export interface CollectionDefinition",
		"routes.tsx":     "registerGeneratedRoutes",
		"runtime.ts":     "export function createTaskListRuntime",
		"index.ts":       "export * from \"./routes\"",
		"package.json":   "\"name\": \"@scenery/generated-web\"",
	} {
		if !strings.Contains(first[name], data) {
			t.Fatalf("%s missing %q:\n%s", name, data, first[name])
		}
	}
	if !strings.Contains(first["models.ts"], "tenant_id: string") ||
		!strings.Contains(first["models.ts"], "status: TaskStatus") ||
		!strings.Contains(first["models.ts"], "priority: TaskPriority") ||
		!strings.Contains(first["projections.ts"], "export function materializeTaskList(row: TaskRow): TaskListRecord") ||
		!strings.Contains(first["projections.ts"], `due_at: row["due_at"]`) ||
		!strings.Contains(first["projections.ts"], `created_at: row["created_at"]`) ||
		!strings.Contains(first["collections.ts"], "CollectionDefinition<TaskListRecord, TaskRow>") ||
		!strings.Contains(first["collections.ts"], `display: "badge"`) ||
		!strings.Contains(first["collections.ts"], `{ field: "status", op: "neq", value: "done" }`) ||
		!strings.Contains(first["collections.ts"], `{ field: "due_at", direction: "asc" }`) ||
		!strings.Contains(first["collections.ts"], "materialize: materializeTaskListCollection") ||
		!strings.Contains(first["runtime.ts"], "taskList?: RuntimeRows<TaskRow>") ||
		!strings.Contains(first["runtime.ts"], "export type TaskListRuntime = CollectionRuntime<TaskListRecord, TaskRow>") ||
		!strings.Contains(first["runtime.ts"], "materialize: () => definition.materialize(rows())") ||
		!strings.Contains(first["routes.tsx"], "export function TaskListPage(props: { rows?: readonly TaskListRecord[]; runtime?: GeneratedRuntime[\"collections\"][\"taskList\"] } = {})") ||
		!strings.Contains(first["routes.tsx"], "satisfies Record<\"TaskStatusBadge\", ComponentSlot<TaskListRecord>>") ||
		!strings.Contains(first["routes.tsx"], "rows: props.runtime?.materialize() ?? props.rows ?? []") ||
		!strings.Contains(first["index.ts"], "export * from \"./routes\"") {
		t.Fatalf("generated web projection boundary missing:\nmodels:\n%s\nprojections:\n%s\ncollections:\n%s\nruntime:\n%s\nroutes:\n%s", first["models.ts"], first["projections.ts"], first["collections.ts"], first["runtime.ts"], first["routes.tsx"])
	}
	if strings.Contains(first["models.ts"], "export interface TaskCreate {\n  id: string\n  tenant_id: string") || strings.Contains(first["models.ts"], "export interface TaskPatch {\n  tenant_id?: string") {
		t.Fatalf("generated web create/patch should not expose tenant_id as client-writable:\n%s", first["models.ts"])
	}

	if err := os.RemoveAll(webRoot); err != nil {
		t.Fatalf("remove generated web root: %v", err)
	}
	out.Reset()
	if err := runGenerate(context.Background(), &out, []string{"data", "--app-root", root, "--dry-run", "--json"}); err != nil {
		t.Fatalf("second runGenerate(data) returned error: %v", err)
	}
	for _, name := range wantFiles {
		data, err := os.ReadFile(filepath.Join(webRoot, name))
		if err != nil {
			t.Fatalf("read regenerated web file %s: %v", name, err)
		}
		if string(data) != first[name] {
			t.Fatalf("regenerated %s changed:\n%s\nwant:\n%s", name, data, first[name])
		}
	}
}

func TestGenerateDataExistingTableWritesWebWithoutGeneratedDBArtifacts(t *testing.T) {
	root := writeExistingTableDSLAppFixture(t)

	var out bytes.Buffer
	if err := runGenerate(context.Background(), &out, []string{"data", "--app-root", root, "--dry-run", "--json"}); err != nil {
		t.Fatalf("runGenerate(data) returned error: %v", err)
	}
	var payload generatorGraphResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	for _, artifact := range payload.DBArtifacts {
		if artifact.Path == ".scenery/gen/db/legacy/schema.hcl" || artifact.Path == ".scenery/gen/db/legacy/seed.sql" || artifact.Kind == "generated-schema" {
			t.Fatalf("unexpected generated db artifact = %+v (all %+v)", artifact, payload.DBArtifacts)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".scenery", "gen", "db")); !os.IsNotExist(err) {
		t.Fatalf("generated db dir stat error = %v, want not exist", err)
	}
	webRoot := filepath.Join(root, ".scenery", "gen", "web", "web")
	for _, name := range []string{"collections.ts", "index.ts", "models.ts", "projections.ts", "routes.tsx", "runtime.ts", "shapes.ts"} {
		if _, err := os.Stat(filepath.Join(webRoot, name)); err != nil {
			t.Fatalf("expected generated web file %s: %v", name, err)
		}
	}
	shapes, err := os.ReadFile(filepath.Join(webRoot, "shapes.ts"))
	if err != nil {
		t.Fatalf("read shapes: %v", err)
	}
	if !strings.Contains(string(shapes), `schema: "legacy"`) || !strings.Contains(string(shapes), `table: "customers"`) || !strings.Contains(string(shapes), `qualifiedTable: "legacy.customers"`) {
		t.Fatalf("existing table shape metadata missing:\n%s", shapes)
	}
	projections, err := os.ReadFile(filepath.Join(webRoot, "projections.ts"))
	if err != nil {
		t.Fatalf("read projections: %v", err)
	}
	if !strings.Contains(string(projections), "export interface CustomerListRecord") || !strings.Contains(string(projections), "materializeCustomerList(row: CustomerRow): CustomerListRecord") {
		t.Fatalf("existing table projection missing:\n%s", projections)
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

func TestDBDiffGeneratedAcceptsCollisionSafeSchemaLabels(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"modelsafe","id":"modelsafe-dev"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/modelsafe\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRootForTest(t)+"\n")
	writeTestAppFile(t, root, "tasksnew/model.go", "package tasksnew\n\n"+
		"import \"scenery.sh/model\"\n\n"+
		"//scenery:model\n"+
		"type Task struct {\n"+
		"\tID string `db:\"id\"`\n"+
		"\tStatus string `db:\"status\"`\n"+
		"}\n\n"+
		"var _ = model.Entity[Task](\n"+
		"\tmodel.Table(\"tasks\"),\n"+
		"\tmodel.Field(\"Status\", model.EnumValues(\"todo\", \"done\")),\n"+
		")\n")
	const safeSchemaHCL = `// Code generated by scenery generate data; DO NOT EDIT.

schema "tasksnew" {}

enum "tasksnew" "tasks_status" {
  schema = schema.tasksnew
  values = ["todo", "done"]
}

table "tasksnew" "tasks" {
  schema = schema.tasksnew

  column "id" {
    null = false
    type = text
  }

  column "status" {
    null = false
    type = enum.tasksnew.tasks_status
  }

  primary_key {
    columns = [column.id]
  }
}

`
	writeTestAppFile(t, root, "tasksnew/db/schema.hcl", safeSchemaHCL)

	var out bytes.Buffer
	if err := runDBGeneratedDiff(&out, []string{"--generated", "--app-root", root, "--json"}); err != nil {
		t.Fatalf("runDBGeneratedDiff returned error: %v\n%s", err, out.String())
	}
	var payload dbGeneratedDiffResult
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if !payload.OK || len(payload.Drift) != 0 || len(payload.Generated) != 1 || payload.Generated[0].GeneratedPath != ".scenery/gen/db/tasksnew/schema.hcl" {
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
	writeTestAppFile(t, root, ".scenery.json", `{"name":"modeldsl","id":"modeldsl-dev","proxy":{"frontends":{"web":{"root":"web"}}},"auth":{"enabled":true,"dev_bootstrap":{"enabled":true}}}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/modeldsl\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRootForTest(t)+"\n")
	source, err := os.ReadFile(filepath.Join(repoRootForTest(t), "testdata", "apps", "model-dsl", "tasks", "model.go"))
	if err != nil {
		t.Fatalf("read model fixture: %v", err)
	}
	writeTestAppFile(t, root, "tasks/model.go", string(source))
	copyModelDSLWebFixture(t, root)
	writeTestAppFile(t, root, "tasks/db/schema.hcl", schemaHCL)
	return root
}

func copyModelDSLWebFixture(t *testing.T, root string) {
	t.Helper()
	webRoot := filepath.Join(repoRootForTest(t), "testdata", "apps", "model-dsl", "web")
	if err := filepath.WalkDir(webRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		rel, err := filepath.Rel(webRoot, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		writeTestAppFile(t, root, filepath.Join("web", rel), string(data))
		return nil
	}); err != nil {
		t.Fatalf("copy web fixture: %v", err)
	}
}

func writeExistingTableDSLAppFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	sourceRoot := filepath.Join(repoRootForTest(t), "testdata", "apps", "existing-table-dsl")
	if err := filepath.WalkDir(sourceRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".scenery", "node_modules":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := strings.ReplaceAll(string(data), "../../..", repoRootForTest(t))
		writeTestAppFile(t, root, rel, text)
		return nil
	}); err != nil {
		t.Fatalf("copy existing-table fixture: %v", err)
	}
	return root
}

const modelDSLExpectedSchemaHCL = `// Code generated by scenery generate data; DO NOT EDIT.

schema "tasks" {}

enum "tasks" "tasks_status" {
  schema = schema.tasks
  values = ["todo", "doing", "done"]
}

enum "tasks" "tasks_priority" {
  schema = schema.tasks
  values = ["low", "normal", "high"]
}

table "tasks" "tasks" {
  schema = schema.tasks

  column "id" {
    null = false
    type = text
  }

  column "tenant_id" {
    null = false
    type = text
  }

  column "title" {
    null = false
    type = text
  }

  column "status" {
    null = false
    type = enum.tasks.tasks_status
  }

  column "priority" {
    null = false
    type = enum.tasks.tasks_priority
  }

  column "assignee_name" {
    null = false
    type = text
  }

  column "due_at" {
    null = false
    type = timestamptz
  }

  column "project_id" {
    null = false
    type = text
  }

  column "created_at" {
    null = false
    type = timestamptz
  }

  column "updated_at" {
    null = false
    type = timestamptz
  }

  primary_key {
    columns = [column.id]
  }
}

`

const modelDSLExpectedSeedSQL = `-- Code generated by scenery generate data; DO NOT EDIT.

insert into "tasks"."tasks" ("id", "tenant_id", "title", "status", "priority", "assignee_name", "due_at", "project_id", "created_at", "updated_at")
values ('seed-task-1', '00000000-0000-0000-0000-000000000001', 'Seeded task', 'todo', 'normal', 'Dev User', '2026-06-18T09:00:00Z'::timestamptz, 'seed-project', '2026-06-12T12:00:00Z'::timestamptz, '2026-06-13T12:00:00Z'::timestamptz)
on conflict ("id") do update set
  "tenant_id" = excluded."tenant_id",
  "title" = excluded."title",
  "status" = excluded."status",
  "priority" = excluded."priority",
  "assignee_name" = excluded."assignee_name",
  "due_at" = excluded."due_at",
  "project_id" = excluded."project_id",
  "created_at" = excluded."created_at",
  "updated_at" = excluded."updated_at";

`
