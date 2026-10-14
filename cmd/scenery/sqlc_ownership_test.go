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

func TestSQLCRejectsOverlappingCodegenRootsWhilePlanning(t *testing.T) {
	t.Parallel()
	for _, out := range []string{"auth/db/gen", "auth/db/gen/plugin"} {
		t.Run(out, func(t *testing.T) {
			root := t.TempDir()
			writeSQLCFixture(t, root)
			writeTestAppFile(t, root, ".scenery.json", `{"name":"demo","id":"sqlc-overlap"}`)
			writeTestAppFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n - engine: sqlite\n   schema: auth/db/gen/schema.sql\n   queries: auth/db/queries.sql\n   gen:\n     go:\n       out: auth/db/gen\n     json:\n       out: "+out+"\n")
			for _, inspect := range []bool{false, true} {
				var output strings.Builder
				var err error
				if inspect {
					err = runSceneryInspect([]string{"generators", "--app-root", root, "-o", "json"}, &output)
				} else {
					err = runGenerate(t.Context(), &output, []string{"sqlc", "--dry-run", "--app-root", root, "-o", "json"})
				}
				if err == nil || cliErrorDiagnostic(err).Code != "SCN8001" || !strings.Contains(err.Error(), "overlapping SQLC output roots") {
					t.Fatalf("overlapping roots accepted: %v", err)
				}
			}
		})
	}
}

func TestGeneratorInspectionRejectsSQLCTypeScriptScopeCollision(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{"", "/client"} {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			writeSQLCFixture(t, root)
			writeTestAppFile(t, root, ".scenery.json", `{"name":"demo","id":"generator-overlap"}`)
			writeTestAppFile(t, root, "app.scn", `application "demo" {}
workspace { managed_generated_roots = ["auth/db/gen"] }
http_gateway "api" {
 exposure = "internet"
 base_path = "/"
 cors = std.cors.none
 trusted_proxies = std.trusted_proxies.none
 forwarded = std.forwarded_headers.reject
}
typescript_client "public_api" {
 gateways = [http_gateway.api]
 package = "@example/public"
 module = "esm"
 runtime = "fetch"
 output_root = "auth/db/gen`+suffix+`"
}
`)
			var output strings.Builder
			err := runSceneryInspect([]string{"generators", "--app-root", root, "-o", "json"}, &output)
			if err == nil || cliErrorDiagnostic(err).Code != "SCN8001" || !strings.Contains(err.Error(), "overlapping lifecycle output owners") {
				t.Fatalf("different generator owners admitted: %v", err)
			}
		})
	}
}

func TestSQLCLocalProcessPluginUsesCapturedConfigRelativeExecutable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestAppFile(t, root, "app.scn", "application \"demo\" {}\nworkspace { managed_generated_roots = [\"config/gen\"] }\n")
	writeTestAppFile(t, root, "config/sqlc.yaml", "version: \"2\"\nplugins:\n - name: local\n   process:\n     cmd: ./tools/plugin\nsql:\n - engine: sqlite\n   schema: schema.sql\n   queries: queries.sql\n   codegen:\n    - plugin: local\n      out: gen\n")
	writeTestAppFile(t, root, "config/schema.sql", "create table items (id integer primary key);\n")
	writeTestAppFile(t, root, "config/queries.sql", "-- name: ListItems :many\nselect id from items;\n")
	writeTestAppFile(t, root, "config/tools/plugin", "captured executable bytes")
	if err := os.Chmod(filepath.Join(root, "config/tools/plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := appcfg.Config{Name: "demo"}
	cfg.Generators.SQLC.Config = "config/sqlc.yaml"
	plan, _, err := buildSQLCGeneratorPlan(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	assertStringSliceContains(t, plan.Record.Inputs, "config/tools/plugin")
	hooks := stubLifecycleExec(t, func(_ context.Context, req lifecycleExecRequest) error {
		path := filepath.Join(req.Dir, "tools/plugin")
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "captured executable bytes" {
			t.Fatalf("staged plugin: %q %v", data, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("staged plugin is not executable: %v", err)
		}
		return nil
	}, nil)
	hooks.toolIdentity = func(program string) (string, error) {
		if program != "sqlc" {
			t.Fatalf("local plugin resolved through ambient tool lookup: %q", program)
		}
		return sqlcDigest([]byte("sqlc fixture")), nil
	}
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
	writeTestAppFile(t, root, "config/tools/plugin", "changed executable bytes")
	if err := op.verify(false); err == nil || !strings.Contains(err.Error(), "revision_conflict") {
		t.Fatalf("edited plugin accepted: %v", err)
	}
}
