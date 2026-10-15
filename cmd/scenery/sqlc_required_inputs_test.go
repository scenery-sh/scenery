package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestSQLCPlanningRequiresEverySQLSelection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"demo","id":"sqlc-required"}`)
	config, err := os.ReadFile(filepath.Join(root, "sqlc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, original := range []string{"auth/db/queries.sql", "auth/db/gen/schema.sql"} {
		for _, missing := range []string{"missing.sql", "empty", "empty/*.sql"} {
			if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestAppFile(t, root, "sqlc.yaml", strings.Replace(string(config), original, missing, 1))
			if _, _, err := buildSQLCGeneratorPlan(root, appcfg.Config{Name: "demo"}); err == nil || !strings.Contains(err.Error(), "required declared input") {
				t.Fatalf("missing %s accepted for %s: %v", missing, original, err)
			}
		}
	}
	var output strings.Builder
	if err := runSceneryInspect([]string{"generators", "--app-root", root, "-o", "json"}, &output); err == nil {
		t.Fatal("inspection accepted a missing required selection")
	}
	if err := runGenerate(t.Context(), &output, []string{"sqlc", "--dry-run", "--app-root", root, "-o", "json"}); err == nil {
		t.Fatal("dry-run accepted a missing required selection")
	}
}

func TestSQLCPluginLosingExecutableModeRefusesPublication(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSQLCFixture(t, root)
	config, err := os.ReadFile(filepath.Join(root, "sqlc.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(config), "    gen:\n", "    codegen:\n      - plugin: local\n        out: auth/db/gen/plugin\n    gen:\n", 1)
	source = strings.Replace(source, `out: "auth/db/gen"`, `out: "auth/db/gen/go"`, 1)
	writeTestAppFile(t, root, "sqlc.yaml", source+"\nplugins:\n - name: local\n   process:\n     cmd: ./tools/plugin\n")
	writeTestAppFile(t, root, "tools/plugin", "executable bytes")
	pluginPath := filepath.Join(root, "tools/plugin")
	if err := os.Chmod(pluginPath, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, _, err := buildSQLCGeneratorPlan(root, appcfg.Config{Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	hooks := stubLifecycleExec(t, func(context.Context, lifecycleExecRequest) error { return nil }, nil)
	op, err := newSQLCOperation(root, plan, hooks, io.Discard, true)
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
	if err := os.Chmod(pluginPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := op.verify(false); err == nil || cliErrorDiagnostic(err).Code != "SCN8002" {
		t.Fatalf("mode change accepted before publication: %v", err)
	}
	// This is the callback used under the real publication owner, without
	// invoking OS transaction ownership in an ordinary test.
	if _, err := op.publicationFiles(); err == nil || cliErrorDiagnostic(err).Code != "SCN8002" {
		t.Fatalf("publication callback accepted lost executable mode: %v", err)
	}
	if err := plan.verifyInputs(root); err == nil || cliErrorDiagnostic(err).Code != "SCN8002" {
		t.Fatalf("response boundary accepted lost executable mode: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".scenery/artifacts/sqlc.json")); !os.IsNotExist(err) {
		t.Fatalf("refused operation published ownership metadata: %v", err)
	}
}
