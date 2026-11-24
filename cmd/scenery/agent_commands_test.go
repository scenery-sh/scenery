package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
)

func TestDBQueryOptionsAndServiceBinding(t *testing.T) {
	t.Parallel()
	opts, err := parseDBQueryArgs([]string{"--service", "inventory", "SELECT $1, $2", "--param", "quoted ' text", "--param=-4", "-o", "json"})
	if err != nil || opts.limit != 200 || !opts.json || opts.sql != "SELECT $1, $2" || !slices.Equal(opts.params, []any{"quoted ' text", "-4"}) {
		t.Fatalf("options = %+v, error = %v", opts, err)
	}
	for _, args := range [][]string{
		{"--service", "inventory", "--sql", "SELECT 1", "SELECT 2"},
		{"--service", "inventory", "--sql", "SELECT 1", "--limit", "0"},
		{"SELECT 1"},
	} {
		if _, err := parseDBQueryArgs(args); cliExitCode(err) != 2 {
			t.Errorf("args %v: expected usage error, got %v", args, err)
		}
	}
	compiled := &compiler.Result{Manifest: &graph.Manifest{Resources: []graph.Resource{
		{Address: "inventory/service/items", Module: "inventory", Name: "items", Kind: "scenery.service",
			Origin: graph.Origin{Kind: "authored"}, Spec: map[string]any{"implementation": map[string]any{}}},
		{Address: "inventory/operation/read", Module: "inventory", Kind: "scenery.operation",
			Spec: map[string]any{"service": map[string]any{"$ref": "service.items"}, "handler": map[string]any{}}},
	}}, SQLRequirements: compiler.SQLRequirements{
		{Kind: compiler.SQLDataSource, Name: "warehouse", Schema: "warehouse", Consumers: []string{"inventory/service/items"}},
	}}
	binding, err := dbQueryBinding(compiled, "inventory")
	if err != nil || binding.Name != "warehouse" || binding.Schema != "warehouse" {
		t.Fatalf("module SQL binding = %+v, error = %v", binding, err)
	}
	if _, err := dbQueryBinding(compiled, "missing"); cliExitCode(err) != 2 {
		t.Fatalf("unknown service error = %v", err)
	}
	compiled.SQLRequirements = append(compiled.SQLRequirements, compiler.SQLRequirement{
		Kind: compiler.SQLDataSource, Name: "audit", Schema: "audit", Consumers: []string{"inventory/service/items"},
	})
	if _, err := dbQueryBinding(compiled, "inventory"); err == nil || !strings.Contains(err.Error(), "multiple Postgres bindings") {
		t.Fatalf("ambiguous service error = %v", err)
	}
}

func TestDurableRunOptions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		flags []string
		wait  bool
	}{
		{nil, true},
		{[]string{"--wait"}, true},
		{[]string{"--wait=false"}, false},
	} {
		opts, err := parseDurableRunArgs(append([]string{"inventory.restock", "--input", `{"sku":"x"}`}, test.flags...))
		if err != nil || opts.wait != test.wait || opts.target != "inventory.restock" || opts.timeout.Seconds() != 30 {
			t.Fatalf("options = %+v, error = %v", opts, err)
		}
	}
	for _, args := range [][]string{{}, {"target", "--input", "{"}, {"target", "--timeout", "0s"}, {"target", "--wait=maybe"}} {
		if _, err := parseDurableRunArgs(args); cliExitCode(err) != 2 {
			t.Errorf("args %v: expected usage error, got %v", args, err)
		}
	}
}

const scaffoldTestApp = `application "fixture" {}

workspace {
  managed_generated_roots = [
    "internal/scenerygen", # Keep the existing comment.
  ]
}

go_module "application" {
  root = "."
  import_path = "example.test/fixture"
}

go_toolchain "application" { version = "1.27.0" }

go_target "development" {
  role = "development"
  platform = "host"
  toolchain = go_toolchain.application
  module = go_module.application
  packages = ["./..."]
  cgo = "disabled"
}
`

func TestModuleScaffoldPublishesValidDeclarations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		runtime, starter, unit string
	}{
		{"go", "service.go", "service_test.go"},
		{"rust", "Cargo.toml", "src/lib.rs"},
		{"ts", "service.ts", "service.test.ts"},
		{"js", "service.js", "service.test.js"},
	} {
		t.Run(test.runtime, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, contents := range map[string]string{"app.scn": scaffoldTestApp, "go.mod": "module example.test/fixture\n\ngo 1.27.0\n"} {
				if err := os.WriteFile(filepath.Join(root, path), []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := compiler.Compile(root)
			if err != nil || !before.Valid() {
				t.Fatalf("compile before = %+v, %v", before, err)
			}
			opts, err := parseAddModuleArgs([]string{"inventory", "--runtime", test.runtime})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := planModuleScaffold(root, opts, before.Manifest.Resources)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range plan.files {
				path := filepath.Join(root, file.Path)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, file.Bytes, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{test.starter, test.unit} {
				if !slices.Contains(plan.CreatedFiles, "inventory/"+name) {
					t.Fatalf("expected starter file %s in %v", name, plan.CreatedFiles)
				}
			}
			after, err := compiler.Compile(root)
			if err != nil || !after.Valid() {
				t.Fatalf("compile after = %+v, %v", after.Diagnostics, err)
			}
			if err := validateModuleDirectory(root, "inventory", after.Manifest.Resources); err == nil || !strings.Contains(err.Error(), "already declared") {
				t.Fatalf("duplicate module error = %v", err)
			}
			app, err := os.ReadFile(filepath.Join(root, "app.scn"))
			if err != nil || !bytes.Contains(app, []byte("# Keep the existing comment.")) {
				t.Fatalf("comment was lost: %s (%v)", app, err)
			}
		})
	}
}

func TestModuleScaffoldRejectsDirectoryAndEditsCargoMembership(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "inventory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateModuleDirectory(root, "inventory", nil); cliExitCode(err) != 3 {
		t.Fatalf("directory collision error = %v", err)
	}
	for _, name := range []string{"../inventory", "Inventory", "type", "scenery_inventory"} {
		if _, err := parseAddModuleArgs([]string{name, "--runtime", "go"}); cliExitCode(err) != 2 {
			t.Errorf("name %q: expected usage error, got %v", name, err)
		}
	}
	manifest := []byte("[workspace]\nresolver = \"3\"\nmembers = [\n  \"old\", # Keep this comment.\n]\n\n[workspace.package]\nversion = \"1.2.3\"\n")
	updated, err := addCargoMember(manifest, "inventory")
	if err != nil || string(updated) != strings.Replace(string(manifest), "members = [", `members = ["inventory", `, 1) {
		t.Fatalf("Cargo edit = %s, error = %v", updated, err)
	}
}
