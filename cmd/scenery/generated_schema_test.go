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
	wantSchema := modelDSLSchemaHCL(t)
	root := writeModelDSLAppFixture(t, wantSchema)

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
	if strings.TrimSpace(string(data)) != strings.TrimSpace(wantSchema) {
		t.Fatalf("generated schema =\n%s\nwant:\n%s", data, wantSchema)
	}
	seed, err := os.ReadFile(filepath.Join(root, ".scenery", "gen", "db", "tasks", "seed.sql"))
	if err != nil {
		t.Fatalf("read generated seed: %v", err)
	}
	if text := string(seed); !strings.Contains(text, `insert into "tasks"."tasks"`) || !strings.Contains(text, `Seeded task`) || !strings.Contains(text, `on conflict ("id") do update`) {
		t.Fatalf("generated seed missing expected insert/upsert:\n%s", seed)
	}
}

func TestGenerateDataWritesDeterministicGeneratedWebPackage(t *testing.T) {
	root := writeModelDSLAppFixture(t, modelDSLSchemaHCL(t))

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
}

func TestDBSeedDiscoversGeneratedModelSeed(t *testing.T) {
	root := writeModelDSLAppFixture(t, modelDSLSchemaHCL(t))
	writeTestAppFile(t, root, ".env", "DatabaseURL=sqlite:///tmp/modeldsl.sqlite\n")
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
	root := writeModelDSLAppFixture(t, modelDSLSchemaHCL(t))

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
	if err := runGenerate(context.Background(), ioDiscardWriter{}, []string{"data", "--app-root", root, "--dry-run"}); err != nil {
		t.Fatalf("runGenerate(data) returned error: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(root, ".scenery", "gen", "db", "tasksnew", "schema.hcl"))
	if err != nil {
		t.Fatalf("read generated schema: %v", err)
	}
	writeTestAppFile(t, root, "tasksnew/db/schema.hcl", string(generated))

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
	writeTestAppFile(t, root, ".scenery.json", `{"name":"modeldsl","id":"modeldsl-dev","dev":{"services":{"main":{"kind":"sqlite"}}},"proxy":{"frontends":{"web":{"root":"web"}}},"auth":{"enabled":true,"dev_bootstrap":{"enabled":true}}}`)
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

func modelDSLSchemaHCL(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRootForTest(t), "testdata", "apps", "model-dsl", "tasks", "db", "schema.hcl"))
	if err != nil {
		t.Fatalf("read model schema fixture: %v", err)
	}
	return string(data)
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
