package generateddata

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
)

func TestBuildAndWriteModelArtifacts(t *testing.T) {
	t.Parallel()
	root := copyAppFixture(t, "model-dsl")
	plan := buildAndWrite(t, root)

	if plan.Record.ID != "data" || plan.Record.Kind != "model-schema" || len(plan.WebRecords) != 1 || plan.WebRecords[0].ID != "web:web" {
		t.Fatalf("plan records = %+v / %+v", plan.Record, plan.WebRecords)
	}
	for _, output := range []string{".scenery/gen/db/tasks/schema.hcl", ".scenery/gen/db/tasks/seed.sql"} {
		if !contains(plan.Record.Outputs, output) {
			t.Fatalf("data outputs %v do not contain %q", plan.Record.Outputs, output)
		}
	}
	for _, output := range []string{".scenery/gen/web/web/index.ts", ".scenery/gen/web/web/routes.tsx"} {
		if !contains(plan.WebRecords[0].Outputs, output) {
			t.Fatalf("web outputs %v do not contain %q", plan.WebRecords[0].Outputs, output)
		}
	}
	wantSchema, err := os.ReadFile(filepath.Join(root, "tasks", "db", "schema.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	assertFileContents(t, filepath.Join(root, ".scenery", "gen", "db", "tasks", "schema.hcl"), string(wantSchema))
	assertFileContents(t, filepath.Join(root, ".scenery", "gen", "db", "tasks", "seed.sql"), expectedSeedSQL)
}

func TestWriteProducesDeterministicWebPackage(t *testing.T) {
	t.Parallel()
	root := copyAppFixture(t, "model-dsl")
	plan := buildAndWrite(t, root)

	webRoot := filepath.Join(root, ".scenery", "gen", "web", "web")
	wantFiles := []string{"collections.ts", "index.ts", "models.ts", "package.json", "projections.ts", "routes.tsx", "runtime.ts", "shapes.ts"}
	first := make(map[string]string, len(wantFiles))
	for _, name := range wantFiles {
		data, err := os.ReadFile(filepath.Join(webRoot, name))
		if err != nil {
			t.Fatalf("read generated web file %s: %v", name, err)
		}
		first[name] = string(data)
	}
	for name, fragment := range map[string]string{
		"models.ts": "export interface TaskRow", "shapes.ts": "export const taskSource",
		"projections.ts": "export interface TaskListRecord", "collections.ts": "export interface CollectionDefinition",
		"routes.tsx": "registerGeneratedRoutes", "runtime.ts": "export function createTaskListRuntime",
		"index.ts": "export * from \"./routes\"", "package.json": "\"name\": \"@scenery/generated-web\"",
	} {
		if !strings.Contains(first[name], fragment) {
			t.Fatalf("%s missing %q:\n%s", name, fragment, first[name])
		}
	}
	for _, fragment := range []string{
		"tenant_id: string", "status: TaskStatus", "priority: TaskPriority",
		"export function materializeTaskList(row: TaskRow): TaskListRecord",
		`due_at: row["due_at"]`, `created_at: row["created_at"]`,
		"CollectionDefinition<TaskListRecord, TaskRow>", `display: "badge"`,
		`{ field: "status", op: "neq", value: "done" }`, `{ field: "due_at", direction: "asc" }`,
		"materialize: materializeTaskListCollection", "taskList?: RuntimeRows<TaskRow>",
		"export type TaskListRuntime = CollectionRuntime<TaskListRecord, TaskRow>",
		"materialize: () => definition.materialize(rows())",
		"satisfies Record<\"TaskStatusBadge\", ComponentSlot<TaskListRecord>>",
		"rows: props.runtime?.materialize() ?? props.rows ?? []",
	} {
		if !webOutputContains(first, fragment) {
			t.Fatalf("generated web package missing %q", fragment)
		}
	}
	if strings.Contains(first["models.ts"], "export interface TaskCreate {\n  id: string\n  tenant_id: string") || strings.Contains(first["models.ts"], "export interface TaskPatch {\n  tenant_id?: string") {
		t.Fatalf("generated web create/patch exposes tenant_id as client-writable:\n%s", first["models.ts"])
	}

	if err := os.RemoveAll(webRoot); err != nil {
		t.Fatal(err)
	}
	if err := Write(root, plan); err != nil {
		t.Fatal(err)
	}
	for _, name := range wantFiles {
		data, err := os.ReadFile(filepath.Join(webRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != first[name] {
			t.Fatalf("regenerated %s changed", name)
		}
	}
}

func TestExistingTableWritesOnlyWebArtifacts(t *testing.T) {
	t.Parallel()
	root := copyAppFixture(t, "existing-table-dsl")
	plan := buildAndWrite(t, root)
	if len(plan.Schemas) != 0 || len(plan.Seeds) != 0 {
		t.Fatalf("existing-table plan has generated database artifacts: %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(root, ".scenery", "gen", "db")); !os.IsNotExist(err) {
		t.Fatalf("generated db dir stat error = %v, want not exist", err)
	}
	webRoot := filepath.Join(root, ".scenery", "gen", "web", "web")
	shapes, err := os.ReadFile(filepath.Join(webRoot, "shapes.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shapes), `schema: "legacy"`) || !strings.Contains(string(shapes), `table: "customers"`) || !strings.Contains(string(shapes), `qualifiedTable: "legacy.customers"`) {
		t.Fatalf("existing table shape metadata missing:\n%s", shapes)
	}
	projections, err := os.ReadFile(filepath.Join(webRoot, "projections.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projections), "export interface CustomerListRecord") || !strings.Contains(string(projections), "materializeCustomerList(row: CustomerRow): CustomerListRecord") {
		t.Fatalf("existing table projection missing:\n%s", projections)
	}
}

func TestBuildDiffResultReportsDriftAndMatch(t *testing.T) {
	t.Parallel()
	root := copyAppFixture(t, "model-dsl")
	_, appModel := loadApp(t, root)

	matched, err := BuildDiff(root, appModel)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched.Drift) != 0 || len(matched.Schemas) != 1 || matched.Schemas[0].GeneratedPath != ".scenery/gen/db/tasks/schema.hcl" {
		t.Fatalf("matching result = %+v", matched)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", "db", "schema.hcl"), []byte("schema \"public\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	drifted, err := BuildDiff(root, appModel)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifted.Drift) != 1 || drifted.Drift[0].Service != "tasks" || !strings.Contains(drifted.Drift[0].Message, "tasks/db/schema.hcl") {
		t.Fatalf("drift result = %+v", drifted)
	}
}

func TestBuildDiffResultAcceptsCollisionSafeLabels(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(t, root, ".scenery.json", `{"name":"modelsafe","id":"modelsafe-dev"}`)
	writeTestFile(t, root, "go.mod", "module example.com/modelsafe\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRoot(t)+"\n")
	writeTestFile(t, root, "tasksnew/model.go", "package tasksnew\n\nimport \"scenery.sh/model\"\n\n//scenery:model\ntype Task struct {\n\tID string `db:\"id\"`\n\tStatus string `db:\"status\"`\n}\n\nvar _ = model.Entity[Task](model.Table(\"tasks\"), model.Field(\"Status\", model.EnumValues(\"todo\", \"done\")))\n")
	writeTestFile(t, root, "tasksnew/db/schema.hcl", safeSchemaHCL)
	_, appModel := loadApp(t, root)
	result, err := BuildDiff(root, appModel)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Drift) != 0 || len(result.Schemas) != 1 || result.Schemas[0].GeneratedPath != ".scenery/gen/db/tasksnew/schema.hcl" {
		t.Fatalf("result = %+v", result)
	}
}

func buildAndWrite(t *testing.T, root string) *Plan {
	t.Helper()
	cfg, appModel := loadApp(t, root)
	plan, ok, err := Build(root, cfg, appModel)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("Build reported no generated data")
	}
	if err := Write(root, plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func loadApp(t *testing.T, root string) (appcfg.Config, *model.App) {
	t.Helper()
	_, cfg, err := appcfg.DiscoverRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	appModel, err := parse.App(root, cfg.Name)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, appModel
}

func copyAppFixture(t *testing.T, name string) string {
	t.Helper()
	sourceRoot := filepath.Join(repoRoot(t), "testdata", "apps", name)
	root := t.TempDir()
	if err := filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
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
		contents := strings.ReplaceAll(string(data), "../../..", repoRoot(t))
		writeTestFile(t, root, rel, contents)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return root
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate generateddata test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func writeTestFile(t *testing.T, root, relativePath, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != strings.TrimSpace(want) {
		t.Fatalf("%s contents differ:\n%s\nwant:\n%s", path, data, want)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func webOutputContains(files map[string]string, fragment string) bool {
	for _, contents := range files {
		if strings.Contains(contents, fragment) {
			return true
		}
	}
	return false
}

const expectedSeedSQL = `-- Code generated by scenery generate data; DO NOT EDIT.

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
