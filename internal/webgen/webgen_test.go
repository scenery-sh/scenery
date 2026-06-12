package webgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

func TestBuildGeneratesFrontendBundle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "web", "src", "components", "TaskStatusBadge.tsx"), "export function TaskStatusBadge() { return null }\n")

	bundles, err := Build(root, testAppModel(), map[string]appcfg.FrontendConfig{"web": {Root: "web"}})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(bundles) != 1 || bundles[0].Frontend != "web" || bundles[0].GeneratedDir != ".scenery/gen/web/web" {
		t.Fatalf("bundles = %+v", bundles)
	}
	files := map[string]string{}
	for _, file := range bundles[0].Files {
		files[file.Path] = file.Contents
	}
	for _, path := range []string{
		".scenery/gen/web/web/models.ts",
		".scenery/gen/web/web/shapes.ts",
		".scenery/gen/web/web/collections.ts",
		".scenery/gen/web/web/runtime.ts",
		".scenery/gen/web/web/routes.tsx",
		".scenery/gen/web/web/index.ts",
		".scenery/gen/web/web/package.json",
	} {
		if files[path] == "" {
			t.Fatalf("missing generated file %s in %+v", path, bundles[0].Files)
		}
	}
	if !strings.Contains(files[".scenery/gen/web/web/routes.tsx"], `satisfies Record<"TaskStatusBadge", ComponentSlot<TaskRow>>`) {
		t.Fatalf("routes missing slot assertion:\n%s", files[".scenery/gen/web/web/routes.tsx"])
	}
	if !strings.Contains(files[".scenery/gen/web/web/runtime.ts"], `export function createTaskListRuntime`) {
		t.Fatalf("runtime missing collection adapter factory:\n%s", files[".scenery/gen/web/web/runtime.ts"])
	}
	if !strings.Contains(files[".scenery/gen/web/web/routes.tsx"], `export function registerGeneratedRoutes`) {
		t.Fatalf("routes missing registration helper:\n%s", files[".scenery/gen/web/web/routes.tsx"])
	}
}

func TestBuildReportsMissingFrontendSlot(t *testing.T) {
	root := t.TempDir()
	if _, err := Build(root, testAppModel(), map[string]appcfg.FrontendConfig{"web": {Root: "web"}}); err == nil || !strings.Contains(err.Error(), "TaskStatusBadge") {
		t.Fatalf("Build() error = %v, want missing slot diagnostic", err)
	}
}

func testAppModel() *model.App {
	pkg := &model.Package{RelDir: "tasks"}
	entity := &model.Entity{
		Package: pkg,
		Name:    "Task",
		Table:   "tasks",
		Fields: []model.EntityField{
			{Name: "ID", TypeExpr: "string", Kind: model.EntityFieldStored, Column: "id"},
			{Name: "Title", TypeExpr: "string", Kind: model.EntityFieldStored, Column: "title"},
			{Name: "Status", TypeExpr: "string", Kind: model.EntityFieldStored, Column: "status", EnumValues: []string{"todo", "done"}},
		},
	}
	view := &model.View{
		Package: pkg,
		Name:    "TaskList",
		Kind:    "collection",
		Entity:  "Task",
		Route:   "/tasks",
		Title:   "Tasks",
		Columns: []string{"Title", "Status"},
		Slots:   []model.ViewSlot{{Name: "TaskStatusBadge"}},
	}
	return &model.App{Entities: []*model.Entity{entity}, Views: []*model.View{view}}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
