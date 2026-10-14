package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
)

func TestSQLCConcurrentSourceEditPublishesNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	plan, _, err := buildSQLCGeneratorPlan(root, appcfg.Config{Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	hooks := stubLifecycleExec(t, func(_ context.Context, req lifecycleExecRequest) error {
		if req.Program == "sqlc" {
			writeTestAppFile(t, root, "auth/db/queries.sql", "-- name: Ping :one\nselect 2;\n")
		}
		return nil
	}, nil)
	err = runSQLCGeneratorWithHooks(context.Background(), io.Discard, root, plan, true, hooks)
	if err == nil || !strings.Contains(err.Error(), "revision_conflict") {
		t.Fatalf("concurrent SQL edit accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "auth/db/gen/db.go")); !os.IsNotExist(err) {
		t.Fatalf("superseded code published: %v", err)
	}
	assertSQLCStageClean(t, root)
}

func TestGeneratorInspectionRejectsConfigEditAfterPlanning(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	compiled, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := buildInspectGeneratorsResponseWithContract(root, appcfg.Config{Name: "demo"}, compiled)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "sqlc.yaml")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(source, []byte("\n# edited after planning\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeSourceResult(io.Discard, "json", false, compiled, graph); err == nil || cliErrorDiagnostic(err).Code != "SCN8002" {
		t.Fatalf("stale generator metadata returned successfully: %v", err)
	}
}

func TestGeneratorInspectionRejectsConfigRemovalAfterPlanning(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	compiled, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := buildInspectGeneratorsResponseWithContract(root, appcfg.Config{Name: "demo"}, compiled)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "sqlc.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := writeSourceResult(io.Discard, "json", false, compiled, graph); err == nil || cliErrorDiagnostic(err).Code != "SCN8002" {
		t.Fatalf("missing captured configuration did not return a revision conflict: %v", err)
	}
}

func TestSQLCPluginSelectionFollowsCodegenReferences(t *testing.T) {
	t.Parallel()
	config := sqlcConfigFile{Plugins: []sqlcPlugin{{Name: "unused"}, {Name: "process"}, {Name: "wasm"}}}
	var block sqlcConfigBlock
	block.Codegen = append(block.Codegen, struct {
		Out    string `yaml:"out"`
		Plugin string `yaml:"plugin"`
	}{Out: "gen/process", Plugin: "process"}, struct {
		Out    string `yaml:"out"`
		Plugin string `yaml:"plugin"`
	}{Out: "gen/wasm", Plugin: "wasm"})
	config.SQL = []sqlcConfigBlock{block, block}
	got := selectedSQLCPlugins(config)
	if len(got) != 2 || got[0].Name != "process" || got[1].Name != "wasm" {
		t.Fatalf("selected plugins = %+v", got)
	}
}

func TestSQLCInspectionReportsEveryOutputRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo","id":"sqlc-output-roots"}`)
	writeTestAppFile(t, root, "sqlc.yaml", `version: "2"
plugins:
  - name: process
    process:
      cmd: unavailable-during-inspection
  - name: wasm
    wasm:
      url: file://plugin.wasm
      sha256: abc
sql:
  - engine: postgresql
    schema: auth/db/gen/schema.sql
    queries: auth/db/queries.sql
    gen:
      go:
        out: auth/db/gen/go
      json:
        out: auth/db/gen/json
    codegen:
      - plugin: process
        out: auth/db/gen/process
      - plugin: wasm
        out: auth/db/gen/wasm
`)
	want := []string{"auth/db/gen/go", "auth/db/gen/json", "auth/db/gen/process", "auth/db/gen/wasm"}
	for _, inspect := range []bool{false, true} {
		var output strings.Builder
		var err error
		if inspect {
			err = runSceneryInspect([]string{"generators", "--app-root", root, "-o", "json"}, &output)
		} else {
			err = runGenerate(t.Context(), &output, []string{"sqlc", "--dry-run", "--app-root", root, "-o", "json"})
		}
		if err != nil {
			t.Fatal(err)
		}
		var graph generatorGraphResponse
		if err := decodeCLIJSON([]byte(output.String()), &graph); err != nil {
			t.Fatal(err)
		}
		var outputs []string
		for _, generator := range graph.Generators {
			if generator.ID == "sqlc" {
				outputs = generator.Outputs
			}
		}
		if !reflect.DeepEqual(outputs, want) {
			t.Fatalf("reported roots = %+v", graph.Generators)
		}
		var roots []string
		for _, node := range graph.Nodes {
			if node.ID == "sqlc" {
				for _, output := range node.Outputs {
					if output.Kind == "output_root" {
						roots = append(roots, output.Path)
					}
				}
			}
		}
		if !reflect.DeepEqual(roots, want) {
			t.Fatalf("file producer roots = %v", roots)
		}
	}
}

func TestSQLCAtlasSchemaHasOneProducer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo","id":"sqlc-atlas","generators":{"sqlc":{"provider":"sqlc","dev_url":"postgres://scratch","schemas":[{"sqlc_schema":"auth/db/gen/schema.sql","atlas_source":"auth/db/schema.hcl"}]}}}`)
	if err := os.Remove(filepath.Join(root, "auth/db/gen/schema.sql")); err != nil {
		t.Fatal(err)
	}
	for _, inspect := range []bool{false, true} {
		var output strings.Builder
		var err error
		if inspect {
			err = runSceneryInspect([]string{"generators", "--app-root", root, "-o", "json"}, &output)
		} else {
			err = runGenerate(t.Context(), &output, []string{"sqlc", "--dry-run", "--app-root", root, "-o", "json"})
		}
		if err != nil {
			t.Fatal(err)
		}
		var graph generatorGraphResponse
		if err := decodeCLIJSON([]byte(output.String()), &graph); err != nil {
			t.Fatal(err)
		}
		var roots []string
		var schemaOwners, schemaInputs int
		for _, node := range graph.Nodes {
			for _, output := range node.Outputs {
				if output.Path == "auth/db/gen/schema.sql" && output.Kind == "sql_schema" {
					schemaOwners++
				}
			}
			if node.ID == "sqlc" {
				for _, output := range node.Outputs {
					if output.Kind == "output_root" {
						roots = append(roots, output.Path)
					}
				}
				for _, input := range node.Inputs {
					if input.Kind == "sql_schema" && input.Path == "auth/db/gen/schema.sql" && input.Producer == "sqlc.schema.render.0" {
						schemaInputs++
					}
				}
			}
		}
		if !reflect.DeepEqual(roots, []string{"auth/db/gen"}) || schemaOwners != 1 || schemaInputs != 1 {
			t.Fatalf("roots = %v, schema owners = %d, schema inputs = %d", roots, schemaOwners, schemaInputs)
		}
	}
}

func TestSQLCRunsFromSelectedConfigDirectory(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"sqlc.yaml", "queries.yaml"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeTestAppFile(t, root, "app.scn", "application \"demo\" {}\nworkspace { managed_generated_roots = [\"config/gen\"] }\n")
			writeTestAppFile(t, root, "config/"+name, "version: \"2\"\nsql:\n - engine: sqlite\n   schema: schema.sql\n   queries: queries.sql\n   gen:\n     go:\n       out: gen\n")
			writeTestAppFile(t, root, "config/schema.sql", "create table items (id integer primary key);\n")
			writeTestAppFile(t, root, "config/queries.sql", "-- name: ListItems :many\nselect id from items;\n")
			cfg := appcfg.Config{Name: "demo"}
			cfg.Generators.SQLC.Config = "config/" + name
			plan, _, err := buildSQLCGeneratorPlan(root, cfg)
			if err != nil {
				t.Fatal(err)
			}
			var op *sqlcOperation
			hooks := stubLifecycleExec(t, func(_ context.Context, req lifecycleExecRequest) error {
				wantArgs := []string{"generate"}
				if name != "sqlc.yaml" {
					wantArgs = append(wantArgs, "-f", name)
				}
				if req.Dir != filepath.Join(op.stage, "config") || !reflect.DeepEqual(req.Args, wantArgs) {
					t.Fatalf("SQLC directory = %q, args = %v", req.Dir, req.Args)
				}
				return nil
			}, nil)
			op, err = newSQLCOperation(root, plan, hooks, io.Discard, true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.RemoveAll(op.stage); err != nil {
					t.Error(err)
				}
			}()
			if err := op.capture(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := op.renderSQLC(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, ok := op.files["config/gen/db.go"]; !ok {
				t.Fatalf("staged output = %v", op.files)
			}
		})
	}
}

func TestSQLCFailureDoesNotPublishRenderedSchema(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	if err := os.Remove(filepath.Join(root, "auth/db/gen/schema.sql")); err != nil {
		t.Fatal(err)
	}
	plan := &sqlcGeneratorPlan{ConfigPath: "sqlc.yaml", Schemas: []sqlcSchemaPlan{{SQLCSchema: "auth/db/gen/schema.sql", AtlasSource: "auth/db/schema.hcl", AtlasDevURL: "postgres://scratch"}}}
	want := errors.New("SQLC failed")
	hooks := stubLifecycleExec(t, func(context.Context, lifecycleExecRequest) error { return want }, func(context.Context, lifecycleExecRequest) ([]byte, error) {
		return []byte("create schema scenery_auth;\n"), nil
	})
	if err := runSQLCGeneratorWithHooks(context.Background(), io.Discard, root, plan, true, hooks); !errors.Is(err, want) {
		t.Fatalf("failure = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "auth/db/gen/schema.sql")); !os.IsNotExist(err) {
		t.Fatalf("partial schema published: %v", err)
	}
	assertSQLCStageClean(t, root)
}

func TestSQLCPlanningRejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo","id":"sqlc-version"}`)
	writeTestAppFile(t, root, "sqlc.yaml", "version: \"1\"\npackages: []\n")
	var out strings.Builder
	if err := runGenerate(t.Context(), &out, []string{"sqlc", "--dry-run", "--app-root", root, "-o", "json"}); err == nil || cliErrorDiagnostic(err).Code != "SCN8001" {
		t.Fatalf("unsupported dry-run succeeded: %v", err)
	}
	if err := runSceneryInspect([]string{"generators", "--app-root", root, "-o", "json"}, &out); err == nil || cliErrorDiagnostic(err).Code != "SCN8001" {
		t.Fatalf("unsupported inspection succeeded: %v", err)
	}
}

func TestSQLCInspectionBindsAndRevalidatesToolInputs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	config, err := os.ReadFile(filepath.Join(root, "sqlc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, "sqlc.yaml", string(config)+"\nplugins:\n - name: wasm\n   wasm:\n     url: file://plugin.wasm\n     sha256: abc\n")
	// Select the local WASM input without executing a plugin during inspection.
	source, err := os.ReadFile(filepath.Join(root, "sqlc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configWithPlugin := strings.Replace(string(source), "    gen:\n", "    codegen:\n      - plugin: wasm\n        out: auth/db/gen/wasm\n    gen:\n", 1)
	writeTestAppFile(t, root, "sqlc.yaml", strings.Replace(configWithPlugin, `out: "auth/db/gen"`, `out: "auth/db/gen/go"`, 1))
	writeTestAppFile(t, root, "plugin.wasm", "wasm-original")
	compiled, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	build := func() generatorGraphResponse {
		t.Helper()
		graph, err := buildInspectGeneratorsResponseWithContract(root, appcfg.Config{Name: "demo"}, compiled)
		if err != nil {
			t.Fatal(err)
		}
		if graph.sqlcPlan.InputRevision == "" {
			t.Fatal("input revision missing from inspection")
		}
		for _, node := range graph.Nodes {
			if node.ID == "sqlc.source.capture" && node.Outputs[0].Digest != graph.sqlcPlan.InputRevision {
				t.Fatal("inspection omitted the captured input digest")
			}
		}
		return graph
	}
	for _, path := range []string{"auth/db/gen/schema.sql", "auth/db/queries.sql", "plugin.wasm"} {
		before := build()
		file := filepath.Join(root, path)
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		writeTestAppFile(t, root, path, string(data)+"\nchanged")
		if err := writeSourceResult(io.Discard, "json", false, compiled, before); err == nil || cliErrorDiagnostic(err).Code != "SCN8002" {
			t.Fatalf("input edit escaped response validation: %s %v", path, err)
		}
		after := build()
		if before.sqlcPlan.InputRevision == after.sqlcPlan.InputRevision {
			t.Fatalf("input edit did not change plan identity: %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".scenery/staging")); !os.IsNotExist(err) {
		t.Fatalf("inspection created staging: %v", err)
	}
}

func writeSQLCTestReceipt(t *testing.T, root string, files map[string]string, roots []string) {
	t.Helper()
	receipt := sqlcArtifactReceipt{Kind: "scenery.sqlc-generated", SchemaRevision: compiler.SQLCReceiptSchemaRevision(), Owner: root, InputRevision: sqlcDigest([]byte("inputs")), Tools: map[string]string{"sqlc": sqlcDigest([]byte("tool"))}, Roots: roots, Files: files}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, sqlcReceiptPath, string(data))
}

func TestSQLCRequiresOutputInEveryRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	writeTestAppFile(t, root, "sqlc.yaml", `version: "2"
sql:
 - engine: postgresql
   schema: auth/db/gen/schema.sql
   queries: auth/db/queries.sql
   gen:
     go:
       out: auth/db/gen/go
     json:
       out: auth/db/gen/json
`)
	oldJSON := "old JSON output"
	writeTestAppFile(t, root, "auth/db/gen/json/output.json", oldJSON)
	writeSQLCTestReceipt(t, root, map[string]string{"auth/db/gen/json/output.json": sqlcDigest([]byte(oldJSON))}, []string{"auth/db/gen/json"})
	plan, _, err := buildSQLCGeneratorPlan(root, appcfg.Config{Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	hooks := defaultLifecycleHooks()
	hooks.toolIdentity = func(string) (string, error) { return sqlcDigest([]byte("tool")), nil }
	hooks.runExec = func(_ context.Context, req lifecycleExecRequest) error {
		writeTestAppFile(t, req.Dir, "auth/db/gen/go/db.go", "package db\n")
		return nil
	}
	if err := runSQLCGeneratorWithHooks(t.Context(), io.Discard, root, plan, true, hooks); err == nil || !strings.Contains(err.Error(), "auth/db/gen/json") {
		t.Fatalf("partial output accepted: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "auth/db/gen/json/output.json"))
	if err != nil || string(data) != oldJSON {
		t.Fatalf("missing output retired existing files: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "auth/db/gen/go/db.go")); !os.IsNotExist(err) {
		t.Fatalf("partial Go output published: %v", err)
	}
	assertSQLCStageClean(t, root)
}

func TestSQLCPublicationBindsVerifiedOutputAndKeepsRevokedRoots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	owned := map[string]string{}
	for _, path := range []string{"auth/db/gen/db.go", "auth/db/gen/retired.go", "auth/db/old/db.go"} {
		writeTestAppFile(t, root, path, "verified")
		owned[path] = sqlcDigest([]byte("verified"))
	}
	writeSQLCTestReceipt(t, root, owned, []string{"auth/db/gen", "auth/db/old"})
	plan, _, err := buildSQLCGeneratorPlan(root, appcfg.Config{Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	hooks := stubLifecycleExec(t, nil, nil)
	op, err := newSQLCOperation(root, plan, hooks, io.Discard, true)
	if err != nil {
		t.Fatal(err)
	}
	op.inputs, op.selections, op.excluded = plan.InputSnapshot.capture, plan.InputSnapshot.selections, plan.InputSnapshot.excluded
	op.files = map[string][]byte{"auth/db/gen/db.go": []byte("replacement"), "auth/db/gen/new.go": []byte("new")}
	op.tools = map[string]string{"sqlc": sqlcDigest([]byte("in-process tool fixture"))}
	files, err := op.publicationFiles()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"auth/db/gen/db.go": sqlcDigest([]byte("verified")), "auth/db/gen/retired.go": sqlcDigest([]byte("verified")), "auth/db/gen/new.go": "absent"}
	for _, file := range files {
		if file.Path == "auth/db/old/db.go" {
			t.Fatal("revoked output scope admitted for deletion")
		}
		if want, ok := expected[file.Path]; ok {
			if file.ExpectedBeforeDigest != want {
				t.Fatalf("publication lost verified digest for %s: %s", file.Path, file.ExpectedBeforeDigest)
			}
			delete(expected, file.Path)
		}
	}
	if len(expected) != 0 {
		t.Fatalf("missing publication preconditions: %v", expected)
	}
}

func TestSQLCReceiptStructuralDescriptorCoversFields(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[sqlcArtifactReceipt]()
	want := []string{"kind:string", "schema_revision:string", "owner:string", "input_revision:string", "tools:map[string]string", "roots:[]string", "files:map[string]string"}
	if typ.NumField() != len(want) {
		t.Fatal("receipt shape changed without revising its structural schema")
	}
	for i, field := range want {
		got := typ.Field(i)
		if got.Tag.Get("json")+":"+got.Type.String() != field {
			t.Fatalf("receipt descriptor field %d changed: %s", i, got.Name)
		}
	}
}

func assertSQLCStageClean(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".scenery/staging"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("SQLC left staging files: %v", entries)
	}
}
