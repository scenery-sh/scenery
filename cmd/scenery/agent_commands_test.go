package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestDBQueryBoundsSessionAndFormatsUUIDs(t *testing.T) {
	t.Parallel()
	opts, err := parseDBQueryArgs([]string{"--service", "inventory", "SELECT 1"})
	if err != nil || opts.timeout != 30*time.Second {
		t.Fatalf("default timeout = %s, %v", opts.timeout, err)
	}
	for _, timeout := range []string{"0s", "2h"} {
		if _, err := parseDBQueryArgs([]string{"--service", "inventory", "--timeout", timeout, "SELECT 1"}); cliExitCode(err) != 2 {
			t.Errorf("timeout %s: expected usage error, got %v", timeout, err)
		}
	}
	for timeout, lock := range map[time.Duration]string{30 * time.Second: "5000", 2 * time.Second: "2000"} {
		config, err := dbQueryConnConfig("postgres://app@localhost:5432/app?search_path=inventory", timeout)
		if err != nil || config.RuntimeParams["statement_timeout"] != strconv.FormatInt(timeout.Milliseconds(), 10) ||
			config.RuntimeParams["lock_timeout"] != lock || config.RuntimeParams["search_path"] != "inventory" {
			t.Fatalf("session for %s = %#v, %v", timeout, config.RuntimeParams, err)
		}
	}
	id := [16]byte{0x12, 15: 0xff}
	encoded, err := json.Marshal([]any{dbQueryValue(id), dbQueryValue([]any{id, nil}), dbQueryValue(int64(7))})
	if err != nil || string(encoded) != `["12000000-0000-0000-0000-0000000000ff",["12000000-0000-0000-0000-0000000000ff",null],7]` {
		t.Fatalf("query values = %s, %v", encoded, err)
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
	for _, name := range []string{"../inventory", "Inventory", "type", "scenery_inventory", "app", "std", "main", "target", "dist", "out", "node_modules"} {
		if _, err := parseAddModuleArgs([]string{name, "--runtime", "go"}); cliExitCode(err) != 2 {
			t.Errorf("name %q: expected usage error, got %v", name, err)
		}
	}
	if _, err := parseAddModuleArgs([]string{"main", "--runtime", "ts"}); err != nil {
		t.Errorf("TypeScript module main: %v", err)
	}
	manifest := []byte("[workspace]\nresolver = \"3\"\nmembers = [\n  \"old\", # Keep this comment.\n]\n\n[workspace.package]\nversion = \"1.2.3\"\n")
	updated, err := addCargoMember(manifest, "inventory")
	if err != nil || string(updated) != strings.Replace(string(manifest), "members = [", `members = ["inventory", `, 1) {
		t.Fatalf("Cargo edit = %s, error = %v", updated, err)
	}
}

func TestAgentCallSendsInvocationAuthorizationOnlyWithBearer(t *testing.T) {
	t.Parallel()
	for bearer, want := range map[string][]string{"": nil, "Bearer user": {"Bearer user"}} {
		var seen http.Header
		connection := agentCallConnection{controlToken: "control", client: &http.Client{Transport: routerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			seen = req.Header.Clone()
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}")), Request: req}, nil
		})}}
		response, err := connection.invoke(context.Background(), "", "", graph.Resource{Address: "echo/binding/echo_internal", Module: "echo"}, bearer, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if got := seen.Values("X-App-Invocation-Authorization"); !slices.Equal(got, want) || seen.Get("Authorization") != "Bearer control" {
			t.Errorf("bearer %q sent invocation authorization %q with control %q", bearer, got, seen.Get("Authorization"))
		}
	}
}
