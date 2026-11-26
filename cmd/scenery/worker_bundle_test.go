package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	workerruntime "scenery.sh/runtime"
)

func TestWorkerHTTPParametersMapOpenAndClosedEnumsToString(t *testing.T) {
	enum := func(name string, open bool) graph.Resource {
		return graph.Resource{Address: "orders/enum/" + name, Module: "orders", Kind: "scenery.enum", Name: name, Spec: map[string]any{
			"open": open, "value": []any{map[string]any{"name": "ready"}, map[string]any{"name": "done"}},
		}}
	}
	projection := workerPlanProjection{resources: map[string]graph.Resource{
		"orders/enum/status":   enum("status", true),
		"orders/enum/priority": enum("priority", false),
		"orders/record/Query": {Address: "orders/record/Query", Module: "orders", Kind: "scenery.record", Name: "Query", Spec: map[string]any{"field": []any{
			map[string]any{"name": "status", "type": map[string]any{"$ref": "enum.status"}},
			map[string]any{"name": "history", "type": map[string]any{"$expression": "optional(list(enum.status))"}},
			map[string]any{"name": "priority", "type": map[string]any{"$ref": "enum.priority"}},
		}}},
	}}
	operation := graph.Resource{Address: "orders/operation/list", Module: "orders", Kind: "scenery.operation", Name: "list", Spec: map[string]any{
		"input": map[string]any{"$ref": "record.Query"},
	}}
	parameter := func(name string) map[string]any {
		return map[string]any{"name": name, "to": map[string]any{"$ref": "operation.list.input." + name}}
	}
	schema, err := projection.httpRequest(map[string]any{"query_parameter": []any{parameter("status"), parameter("history"), parameter("priority")}}, operation)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	for _, mapping := range schema.Mappings {
		types[mapping.Name] = mapping.Type
		if closed := mapping.Name == "priority"; closed != (len(mapping.EnumValues) > 0) {
			t.Fatalf("%s enum values = %v", mapping.Name, mapping.EnumValues)
		}
	}
	if types["status"] != "string" || types["history"] != "optional(list(string))" || types["priority"] != "string" {
		t.Fatalf("mapped types = %v", types)
	}
	// An open enum accepts a value outside its declared set; a closed one does not.
	decoded, err := workerruntime.DecodeContractInput[map[string]json.RawMessage](httptest.NewRequest("GET", "/orders?status=archived&history=ready&history=later&priority=done", nil), nil, schema)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded["status"]) != `"archived"` || string(decoded["history"]) != `["ready","later"]` || string(decoded["priority"]) != `"done"` {
		t.Fatalf("decoded input = %s", decoded)
	}
	_, err = workerruntime.DecodeContractInput[map[string]json.RawMessage](httptest.NewRequest("GET", "/orders?status=ready&priority=urgent", nil), nil, schema)
	if err == nil || !strings.Contains(err.Error(), "priority") {
		t.Fatalf("closed enum accepted an undeclared value: %v", err)
	}
}

func TestDeclaredDatabaseSetupRunsApplyThenSeedsBeforeWorkers(t *testing.T) {
	t.Parallel()

	root := writeDBSeedCommandFixture(t)
	_, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database.Apply = appcfg.DatabaseApplyConfig{Command: "schema-apply"}
	contract, err := compileSQLContract(root)
	if err != nil {
		t.Fatal(err)
	}
	const dsn = "postgres://user:secret@localhost/seedapp?search_path=catalog%2Cscenery"
	var phases []string
	store := newFakeSeedStore()
	setup := declaredDatabaseSetup{
		root: root, cfg: cfg, contract: contract, env: []string{"CATALOG_DATABASE_URL=" + dsn},
		lifecycle: lifecycleHooks{runExec: func(_ context.Context, request lifecycleExecRequest) error {
			phases = append(phases, "apply "+envValueFromList(request.Env, "CATALOG_DATABASE_URL"))
			return nil
		}},
		seed: dbSeedHooks{
			openStore: func(context.Context, string) (databaseSeedStore, error) { return store, nil },
			runCommand: func(_ context.Context, request lifecycleExecRequest) (dbSeedCommandOutput, error) {
				phases = append(phases, "seed "+envValueFromList(request.Env, appDatabaseURLEnv))
				return dbSeedCommandOutput{}, nil
			},
		},
	}
	if err := setup.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"apply " + dsn, "seed " + dsn}; !slices.Equal(phases, want) {
		t.Fatalf("setup phases = %q, want %q", phases, want)
	}
	// An application that declares no setup starts without touching a database.
	setup.cfg.Database, phases = appcfg.DatabaseConfig{}, nil
	if err := setup.run(context.Background()); err != nil || len(phases) != 0 {
		t.Fatalf("undeclared setup ran %q: %v", phases, err)
	}
}

func TestWorkerApplicationsRejectFrontendsTheHostDoesNotServe(t *testing.T) {
	vite := appcfg.FrontendConfig{Root: "web", Adapter: "vite", Mode: "static"}
	envs := map[string]appcfg.EnvConfig{
		"local": {Default: true, Frontends: map[string]appcfg.EnvFrontendConfig{"blog": {Serve: "disabled"}}},
		"all":   {},
	}
	for _, test := range []struct {
		name      string
		root, env string
		frontends map[string]appcfg.FrontendConfig
		want      string
	}{
		{name: "no frontend"},
		{name: "static vite root", frontends: map[string]appcfg.FrontendConfig{"web": vite}},
		{name: "managed root", frontends: map[string]appcfg.FrontendConfig{"web": {Root: "web"}}, want: `declare frontends.web with "adapter": "vite" and "mode": "static"`},
		{name: "shared upstream", frontends: map[string]appcfg.FrontendConfig{"web": {Upstream: "127.0.0.1:5173", AllowSharedUpstream: true}}, want: `declare frontends.web with "adapter": "vite"`},
		{name: "disabled secondary", root: "web", frontends: map[string]appcfg.FrontendConfig{"web": vite, "blog": {Root: "blog"}}},
		{name: "served secondary", root: "web", env: "all", frontends: map[string]appcfg.FrontendConfig{"web": vite, "blog": {Root: "blog"}}, want: `set envs.all.frontends.blog.serve to "disabled"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := requireWorkerFrontends(appcfg.Config{Root: test.root, Frontends: test.frontends, Envs: envs}, test.env)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) || cliExitCode(err) != 3 {
				t.Fatalf("error = %v (exit %d), want %q", err, cliExitCode(err), test.want)
			}
		})
	}
}

func TestWorkerBundleBuildResultConformsToDeclaredSchema(t *testing.T) {
	// `help build -o json` declares scenery.build.result for an application
	// build, whether it produces one Go executable or a worker bundle.
	payload := workerBundleBuildResult("/tmp/app/dist/bundle")
	if payload["kind"] != "scenery.build.result" || payload["output_path"] != "/tmp/app/dist/bundle" {
		t.Fatalf("payload = %v", payload)
	}
	schemas := filepath.Join(repoRootForTest(t), "docs", "schemas")
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(schemas, "scenery.build.result.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("worker bundle build result diagnostics = %s", strings.Join(diagnostics, "\n"))
	}
	var stdout bytes.Buffer
	if err := writeCLIJSON(&stdout, payload); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(schemas, "scenery.cli.schema.json"), envelope); len(diagnostics) != 0 {
		t.Fatalf("worker bundle build CLI envelope diagnostics = %s", strings.Join(diagnostics, "\n"))
	}
}

func TestWorkerBundleBuildRejectsUndeclaredGoTargetBeforeBuilding(t *testing.T) {
	fixture := filepath.Join(repoRootForTest(t), "testdata", "apps", "ts-only")
	err := buildCommand(io.Discard, []string{"--app-root", fixture, "--target", "missing"})
	if err == nil || cliExitCode(err) != 2 || !strings.Contains(err.Error(), `go target "missing" is unavailable; declare a go_target`) {
		t.Fatalf("error = %v (exit %d)", err, cliExitCode(err))
	}
}

func TestWorkerBundleLinksGoServicesFromTheSelectedTarget(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"targets"}`)
	compiled := &compiler.Result{Root: root, ContractStatus: "valid", Manifest: &graph.Manifest{Resources: []graph.Resource{
		{Address: "app/go_target/development", Kind: "scenery.go-target", Name: "development", Spec: map[string]any{"role": "development"}},
		{Address: "jobs/service/jobs", Module: "jobs", Kind: "scenery.service", Name: "jobs", Spec: map[string]any{"runtime": "go"}},
	}}}
	bundle := workerBundle{Host: workerHostPlan{Workers: []workerruntime.WorkerServicePlan{{Address: "jobs/service/jobs", Process: "worker-jobs"}}}}
	// A production bundle asks for the artifact target; the named target wins.
	for _, test := range []struct{ role, target string }{{"artifact", ""}, {"artifact", "linux"}} {
		_, err := bundle.addGoWorkers(context.Background(), compiled, test.role, test.target, nil, nil)
		selection, ok := errors.AsType[*compiler.GoTargetSelectionError](err)
		if !ok || selection.DefaultRole != test.role || selection.Name != test.target {
			t.Fatalf("role %q target %q: %v", test.role, test.target, err)
		}
	}
	// An application without Go services needs no go_target at all.
	compiled.Manifest.Resources[1].Spec["runtime"] = "rust"
	if preparation, err := bundle.addGoWorkers(context.Background(), compiled, "artifact", "", nil, nil); preparation != nil || err != nil {
		t.Fatalf("no Go service: %v, %v", preparation, err)
	}
}

func TestWorkerBundleRejectsCrossPlatformGoTarget(t *testing.T) {
	otherOS := "linux"
	if goruntime.GOOS == "linux" {
		otherOS = "darwin"
	}
	otherArch := "amd64"
	if goruntime.GOARCH == "amd64" {
		otherArch = "arm64"
	}
	manifest := &graph.Manifest{Resources: []graph.Resource{
		{Address: "app/go_target/cross", Kind: "scenery.go-target", Name: "cross", Spec: map[string]any{
			"role": "artifact", "goos": otherOS, "goarch": otherArch, "cgo": "disabled",
		}},
		{Address: "jobs/service/jobs", Module: "jobs", Kind: "scenery.service", Name: "jobs", Origin: graph.Origin{Kind: "authored"}, Spec: map[string]any{
			"runtime": "go", "implementation": map[string]any{"kind": "go"},
		}},
		{Address: "jobs/operation/run", Module: "jobs", Kind: "scenery.operation", Name: "run", Spec: map[string]any{
			"service": map[string]any{"$ref": "service.jobs"}, "handler": map[string]any{"method": "Run"},
		}},
	}}
	compiled := &compiler.Result{Root: t.TempDir(), ContractStatus: "valid", Manifest: manifest}

	for _, target := range []string{"cross", ""} {
		err := validateWorkerBundleTargetPlatform(compiled, "artifact", target)
		if err == nil || cliExitCode(err) != 3 || !strings.Contains(err.Error(), "does not match host platform") {
			t.Fatalf("target=%q: error = %v (exit %d), want exit 3 cross-platform error", target, err, cliExitCode(err))
		}
		_, buildErr := buildWorkerBundle(context.Background(), compiled, "artifact", target, false)
		if buildErr == nil || cliExitCode(buildErr) != 3 || !strings.Contains(buildErr.Error(), "does not match host platform") {
			t.Fatalf("buildWorkerBundle(target=%q) = %v (exit %d), want exit 3 cross-platform error", target, buildErr, cliExitCode(buildErr))
		}
	}

	// An explicit cross-platform Go target is rejected even without Go services.
	compiled.Manifest.Resources[1].Spec["runtime"] = "rust"
	if err := validateWorkerBundleTargetPlatform(compiled, "artifact", "cross"); err == nil || cliExitCode(err) != 3 {
		t.Fatalf("explicit cross target without Go service: %v (exit %d)", err, cliExitCode(err))
	}
	if err := validateWorkerBundleTargetPlatform(compiled, "artifact", ""); err != nil {
		t.Fatalf("implicit empty target without Go service should pass: %v", err)
	}
}
