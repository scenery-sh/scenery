package main

import (
	"context"
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
				for _, input := range node.Inputs {
					if input.Kind == "output_root" {
						roots = append(roots, input.Path)
					}
				}
			}
		}
		if !reflect.DeepEqual(roots, want) {
			t.Fatalf("file producer roots = %v", roots)
		}
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
