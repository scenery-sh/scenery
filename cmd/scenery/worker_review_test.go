package main

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/jsbuild"
	"scenery.sh/internal/storageconfig"
	workerruntime "scenery.sh/runtime"
	publicstorage "scenery.sh/storage"
)

func TestWorkerGenerationsRetainSharedDrainingWorker(t *testing.T) {
	a1, a2 := new(workerProcess), new(workerProcess)
	b1, b2 := new(workerProcess), new(workerProcess)

	var generations workerGenerations
	generations.add(1, map[string]*workerProcess{"a": a1, "b": b1})
	generations.add(2, map[string]*workerProcess{"a": a2, "b": b1})
	generations.add(3, map[string]*workerProcess{"a": a2, "b": b2})
	if stopped := generations.remove(2); len(stopped) != 0 {
		t.Fatalf("retiring generation 2 would stop shared workers: %v", stopped)
	}
	if stopped := generations.remove(1); len(stopped) != 2 || !slices.Contains(stopped, a1) || !slices.Contains(stopped, b1) {
		t.Fatalf("retiring generation 1 should stop A1 and B1: %v", stopped)
	}
}

func TestWorkerFieldDefaultsUseDeclaredWireTypes(t *testing.T) {
	integer := map[string]any{"$scalar": "int", "value": "1"}
	duration := map[string]any{"$scalar": "duration", "nanoseconds": "2000000000"}
	uuid := map[string]any{"$scalar": "uuid", "value": "2a8b8956-ac42-4948-9f25-816a5f92047f"}
	decimal := map[string]any{"$scalar": "decimal", "coefficient": "125", "scale": 2}
	fields := []any{
		map[string]any{"name": "count", "type": "int64", "default": integer},
		map[string]any{"name": "delay", "type": "duration", "default": duration},
		map[string]any{"name": "id", "type": "uuid", "default": uuid},
		map[string]any{"name": "price", "type": "decimal", "default": decimal},
		map[string]any{"name": "limits", "type": "map(list(int64))", "default": map[string]any{"daily": []any{integer}}},
		map[string]any{"name": "ratio", "type": "float64", "default": map[string]any{"$scalar": "int", "value": "1000000"}},
		map[string]any{"name": "options", "type": "json", "default": map[string]any{"retries": integer}},
	}
	const address = "pricing/record/Request"
	types, err := projectWorkerTypes(&graph.Manifest{Resources: []graph.Resource{{
		Address: address, Module: "pricing", Kind: "scenery.record", Spec: map[string]any{"field": fields},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := types.Normalize(address, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"count":"1","delay":"PT2S","id":"2a8b8956-ac42-4948-9f25-816a5f92047f","limits":{"daily":["1"]},"options":{"retries":1},"price":"1.25","ratio":1e+06}`
	if string(got) != want {
		t.Fatalf("normalized input = %s, want %s", got, want)
	}
}

func TestWorkerPlanNamesDurableTasksAndKeepsHTTPAuthorization(t *testing.T) {
	projection := workerPlanProjection{resources: map[string]graph.Resource{}}
	service := workerruntime.WorkerServicePlan{Name: "jobs"}
	operation := graph.Resource{Address: "jobs/operation/run", Module: "jobs", Kind: "scenery.operation"}
	for external, want := range map[string]string{"": "jobs/execution/run", "jobs.run.v1": "jobs.run.v1"} {
		execution := graph.Resource{Address: "jobs/execution/run", Module: "jobs", Kind: "scenery.execution", Spec: map[string]any{"mode": "durable"}}
		if external != "" {
			execution.Spec["external_name"] = external
		}
		plan, err := projection.durable(service, execution, operation)
		if err != nil || plan.Registration.ExternalName != want {
			t.Fatalf("external name %q projected as %q, %v", external, plan.Registration.ExternalName, err)
		}
	}
	for _, test := range []struct {
		kind, protocol, strategy, want string
	}{
		{"scenery.binding", "http", "application", "application"},
		{"scenery.binding", "http", "scheduled", "scheduled"},
		{"scenery.binding", "internal", "application", "public"},
		{"scenery.binding", "mcp", "application", "public"},
		{"scenery.schedule", "", "scheduled", "public"},
	} {
		source := graph.Resource{Address: "jobs/binding/run", Module: "jobs", Kind: test.kind, Spec: map[string]any{
			"authorization": map[string]any{"$ref": "std.authorization." + test.strategy},
		}}
		if test.protocol != "" {
			source.Spec["protocol"] = test.protocol
		}
		if got := projection.policy(service, source, operation).AuthorizationStrategy; got != test.want {
			t.Errorf("%s %s %s authorization = %s, want %s", test.kind, test.protocol, test.strategy, got, test.want)
		}
	}
}

func TestConfigureWorkerFrontendRespectsDisabledEnvFrontend(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{
		Name: "polyglot",
		Frontends: map[string]app.FrontendConfig{
			"web": {Root: "web", Adapter: "vite", Mode: "static"},
		},
		Envs: map[string]app.EnvConfig{
			"backend": {
				Frontends: map[string]app.EnvFrontendConfig{
					"web": {Serve: "disabled"},
				},
			},
		},
	}
	handler, clean, err := configureWorkerFrontend(context.Background(), root, cfg, &compiler.Result{}, "backend")
	if err != nil {
		t.Fatalf("configureWorkerFrontend returned error for disabled frontend: %v", err)
	}
	if handler != nil {
		t.Fatalf("configureWorkerFrontend returned non-nil handler %T for disabled frontend", handler)
	}
	clean()
}

func TestWorkerProcessEnvStripsHostAndNonGoSecrets(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"DATABASE_URL=postgres://user:pass@localhost/db",
		"SCENERY_DATABASE_JSON={}",
		"SCENERY_ASSISTANT_RUNTIME_CONFIG=/tmp/a.json",
		"SCENERY_ASSISTANT_TOKEN_KEY=secret",
		"SCENERY_ASSISTANT_TOKEN_KEY_FILE=/tmp/k",
		"SCENERY_ASSISTANT_CONTROL_ADDR=http://127.0.0.1:9",
		"SCENERY_ASSISTANT_CONTROL_TOKEN=ctrl-secret",
		"SCENERY_MCP_URL=http://127.0.0.1:10",
		"SCENERY_DEV_REPORT_URL=http://127.0.0.1:1/report",
		"SCENERY_DEV_REPORT_TOKEN=report-secret",
	}
	goEnv := strings.Join(workerProcessEnv(base, "go"), "\n")
	for _, forbidden := range []string{
		"SCENERY_ASSISTANT_RUNTIME_CONFIG=", "SCENERY_ASSISTANT_TOKEN_KEY=", "SCENERY_ASSISTANT_TOKEN_KEY_FILE=",
		"SCENERY_ASSISTANT_CONTROL_ADDR=", "SCENERY_ASSISTANT_CONTROL_TOKEN=", "SCENERY_MCP_URL=",
	} {
		if strings.Contains(goEnv, forbidden) {
			t.Fatalf("Go worker leaked %s", forbidden)
		}
	}
	if !strings.Contains(goEnv, "SCENERY_DEV_REPORT_TOKEN=report-secret") {
		t.Fatalf("Go worker lost dev report token: %s", goEnv)
	}
	rustEnv := strings.Join(workerProcessEnv(base, "rust"), "\n")
	if !strings.Contains(rustEnv, "PATH=") {
		t.Fatalf("Rust worker lost PATH: %s", rustEnv)
	}
	for _, forbidden := range []string{
		"DATABASE_URL=", "SCENERY_DATABASE_JSON=", "SCENERY_DEV_REPORT_URL=", "SCENERY_DEV_REPORT_TOKEN=",
		"SCENERY_ASSISTANT_TOKEN_KEY=", "SCENERY_ASSISTANT_CONTROL_TOKEN=", "SCENERY_ASSISTANT_CONTROL_ADDR=", "SCENERY_MCP_URL=",
	} {
		if strings.Contains(rustEnv, forbidden) {
			t.Fatalf("Rust worker leaked %s", forbidden)
		}
	}

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".scenery"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "devctrl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("SCENERY_DEV_REPORT_URL", "")
	t.Setenv("SCENERY_DEV_REPORT_TOKEN", "")
	t.Setenv("SCENERY_APP_ID", "")
	t.Setenv("SCENERY_SESSION_ID", "")
	dev := &workerDevelopment{
		watch:    &workerInputWatch{root: root},
		compiled: &compiler.Result{Manifest: &graph.Manifest{Application: graph.ApplicationIdentity{Name: "app"}}},
	}
	_, stopCtrl, err := startWorkerDevControl(dev, dir, filepath.Join(dir, "control.sock"), "host-control-token")
	if err != nil {
		t.Fatal(err)
	}
	defer stopCtrl()
	reportURL := os.Getenv("SCENERY_DEV_REPORT_URL")
	reportToken := os.Getenv("SCENERY_DEV_REPORT_TOKEN")
	if reportURL == "" || reportToken == "" || reportToken == "host-control-token" {
		t.Fatalf("unexpected dev report env url=%q token=%q", reportURL, reportToken)
	}
	req, _ := http.NewRequest(http.MethodPost, reportURL, strings.NewReader(`{"reports":[]}`))
	req.Header.Set("Authorization", "Bearer host-control-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/report accepted Host control token: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, reportURL, strings.NewReader(`{"reports":[]}`))
	req.Header.Set("Authorization", "Bearer "+reportToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("/report rejected dedicated report token: %d", resp.StatusCode)
	}
}

func TestWorkerPlanProjectsMCPToolsAndStreamFields(t *testing.T) {
	manifest := &graph.Manifest{Resources: []graph.Resource{
		{Address: "house/service/house", Kind: "scenery.service", Name: "house", Module: "house", Spec: map[string]any{"runtime": "rust"}},
		{Address: "house/operation/export", Kind: "scenery.operation", Name: "export", Module: "house", Spec: map[string]any{
			"service": map[string]any{"$ref": "service.house"}, "input": "std.type.unit",
			"result": []any{map[string]any{"name": "ok", "type": "bytes"}},
		}},
		{Address: "house/execution/export_direct", Kind: "scenery.execution", Name: "export_direct", Module: "house", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.export"}, "mode": "direct",
		}},
		{Address: "app/http_gateway/api", Kind: "scenery.http-gateway", Name: "api", Module: "app", Spec: map[string]any{"base_path": "/api"}},
		{Address: "house/binding/export_http", Kind: "scenery.binding", Name: "export_http", Module: "house", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.export"}, "execution": map[string]any{"$ref": "execution.export_direct"},
			"protocol": "http", "delivery": "stream", "gateway": map[string]any{"$ref": "http_gateway.api"},
			"authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"http": map[string]any{
				"method": "GET", "path": "/export",
				"response": []any{map[string]any{"name": "ok", "when": "result.ok", "status": "200", "body": map[string]any{"codec": "bytes", "from": "result.ok"}}},
			},
		}},
		{Address: "house/binding/export_mcp", Kind: "scenery.binding", Name: "export_mcp", Module: "house", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.export"}, "execution": map[string]any{"$ref": "execution.export_direct"},
			"protocol": "mcp", "delivery": "call",
			"authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"mcp": map[string]any{"read_only": true},
		}},
		{Address: "app/mcp_server/support", Kind: "scenery.mcp-server", Name: "support", Module: "app", Spec: map[string]any{
			"max_input_bytes": 1024, "max_result_bytes": 4096,
			"capability": []any{map[string]any{"name": "house__export", "binding": map[string]any{"$ref": "house/binding/export_mcp"}, "approval": "never"}},
		}},
		{Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support", Module: "app", Spec: map[string]any{
			"mcp_server": map[string]any{"$ref": "mcp_server.support"},
		}},
	}}
	services, err := projectWorkerServices(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || len(services[0].MCPTools) != 1 {
		t.Fatalf("services = %+v", services)
	}
	tool := services[0].MCPTools[0]
	if tool.Name != "house__export" || tool.ID != "app/assistant/support#house/binding/export_mcp" || !tool.Effect.ReadOnly {
		t.Fatalf("unexpected MCP tool plan: %+v", tool)
	}
	if _, ok := tool.StreamFields["result.ok"]; !ok {
		t.Fatalf("missing result.ok stream field mapping: %+v", tool.StreamFields)
	}
}

func TestAgentCallRanksLiteralRoutesAheadOfParameters(t *testing.T) {
	service := graph.Resource{Address: "items/service/items", Kind: "scenery.service", Name: "items", Module: "items"}
	opParam := graph.Resource{Address: "items/operation/get", Kind: "scenery.operation", Name: "get", Module: "items", Spec: map[string]any{"service": map[string]any{"$ref": "service.items"}}}
	opLiteral := graph.Resource{Address: "items/operation/latest", Kind: "scenery.operation", Name: "latest", Module: "items", Spec: map[string]any{"service": map[string]any{"$ref": "service.items"}}}
	gateway := graph.Resource{Address: "app/http_gateway/api", Kind: "scenery.http-gateway", Spec: map[string]any{"base_path": "/api"}}
	paramBinding := graph.Resource{Address: "items/binding/get", Kind: "scenery.binding", Module: "items", Spec: map[string]any{
		"protocol": "http", "gateway": map[string]any{"$ref": "http_gateway.api"}, "operation": map[string]any{"$ref": "operation.get"},
		"authentication": map[string]any{"$ref": "std.authentication.none"}, "http": map[string]any{"method": "GET", "path": "/items/{id}"},
	}}
	literalBinding := graph.Resource{Address: "items/binding/latest", Kind: "scenery.binding", Module: "items", Spec: map[string]any{
		"protocol": "http", "gateway": map[string]any{"$ref": "http_gateway.api"}, "operation": map[string]any{"$ref": "operation.latest"},
		"authentication": map[string]any{"$ref": "std.authentication.none"}, "http": map[string]any{"method": "GET", "path": "/items/latest"},
	}}
	resources := []graph.Resource{service, opParam, opLiteral, gateway, paramBinding, literalBinding}
	g := agentGraph{Result: &compiler.Result{Manifest: &graph.Manifest{Resources: resources}}, resources: map[string]graph.Resource{}}
	for _, r := range resources {
		g.resources[r.Address] = r
	}
	binding, _, err := g.callTarget("GET /api/items/latest", false)
	if err != nil || binding.Address != literalBinding.Address {
		t.Fatalf("got binding %s (%v), want %s", binding.Address, err, literalBinding.Address)
	}
}

func TestDBQueryBindingDistinguishesDBLessServiceFromSQLBinding(t *testing.T) {
	compiled := &compiler.Result{
		Manifest: &graph.Manifest{Resources: []graph.Resource{
			{Address: "auth/service/auth", Name: "auth", Module: "auth", Kind: "scenery.service", Origin: graph.Origin{Kind: "authored"}, Spec: map[string]any{"runtime": "go", "implementation": map[string]any{"kind": "go"}}},
			{Address: "auth/operation/ping", Name: "ping", Module: "auth", Kind: "scenery.operation", Spec: map[string]any{"service": map[string]any{"$ref": "service.auth"}, "handler": map[string]any{"method": "Ping"}}},
		}},
		SQLRequirements: []compiler.SQLRequirement{{Kind: compiler.SQLDataSource, Name: "ledger_db", Schema: "ledger"}},
	}
	if _, err := dbQueryBinding(compiled, "auth"); err == nil || !strings.Contains(err.Error(), "has no Postgres binding") {
		t.Fatalf("expected DB-less error, got %v", err)
	}
	got, err := dbQueryBinding(compiled, "ledger_db")
	if err != nil || got.Name != "ledger_db" {
		t.Fatalf("ledger_db resolution = %+v, %v", got, err)
	}
}

func TestJSBuildPruneAndExplicitDependencyWatch(t *testing.T) {
	root := t.TempDir()
	jsDir := filepath.Join(root, ".scenery", "js")
	if err := os.MkdirAll(jsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cachedDigest := strings.Repeat("a", 64)
	activeDigest := strings.Repeat("b", 64)
	staleDigest := strings.Repeat("c", 64)
	for _, digest := range []string{cachedDigest, activeDigest, staleDigest} {
		writeWatchFile(t, root, filepath.Join(".scenery", "js", "worker-"+digest), "bin")
	}
	writeWatchFile(t, root, filepath.Join(".scenery", "js", "build-test.json"), `{"Inputs":"x","Executable":"`+cachedDigest+`"}`)
	if err := jsbuild.Prune(root, []string{filepath.Join(jsDir, "worker-"+activeDigest)}); err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{cachedDigest, activeDigest} {
		if _, err := os.Stat(filepath.Join(jsDir, "worker-"+kept)); err != nil {
			t.Fatalf("expected kept binary %s: %v", kept, err)
		}
	}
	if _, err := os.Stat(filepath.Join(jsDir, "worker-"+staleDigest)); !os.IsNotExist(err) {
		t.Fatalf("expected stale binary removed, stat err = %v", err)
	}

	depRel := filepath.Join("web", "node_modules", "pkg", "index.js")
	writeWatchFile(t, root, depRel, "export const v = 1;\n")
	watch, err := openWorkerInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watch.fs.Close() }()
	watch.setExplicit([]string{depRel}, false)
	if !watch.eventRelevant(filepath.Join(root, depRel)) {
		t.Fatal("expected explicit node_modules dependency event to be relevant")
	}
	inputs := make(workerInputs)
	if err := watch.scan(root, inputs, false); err != nil {
		t.Fatal(err)
	}
	if err := watch.scanExplicit(inputs, false); err != nil {
		t.Fatal(err)
	}
	writeWatchFile(t, root, depRel, "export const v = 2;\n")
	updated, err := watch.capture(inputs, map[string]struct{}{filepath.Join(root, depRel): {}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed := changedWorkerInputs(inputs, updated); !slices.Equal(changed, []string{depRel}) {
		t.Fatalf("changed = %v, want [%s]", changed, depRel)
	}
	// Removing an explicit dependency must not wedge verification when the file still exists in node_modules.
	watch.setExplicit(nil, false)
	dropped := maps.Clone(updated)
	delete(dropped, depRel)
	if err := (&workerDevelopment{watch: watch}).verify(dropped); err != nil {
		t.Fatalf("verify after dropping explicit dependency = %v", err)
	}
}

func TestStorageProxyVersionConditionsAndWorkerDeletePrefix(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "store")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rawCfg := storageconfig.RuntimeConfig{
		ArtifactIdentity: storageconfig.NewRuntimeIdentity(),
		Default:          "app",
		Stores:           map[string]storageconfig.RuntimeStoreConfig{"app": {Kind: "local", Root: root}},
	}
	cfgBytes, err := json.Marshal(rawCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(storageconfig.RuntimeConfigEnv, string(cfgBytes))
	store, err := publicstorage.Default(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	obj, err := store.Put(context.Background(), "tmp/a.txt", strings.NewReader("alpha"), publicstorage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), "keep/b.txt", strings.NewReader("beta"), publicstorage.PutOptions{}); err != nil {
		t.Fatal(err)
	}

	handler, err := prepareWorkerStorage([]workerruntime.WorkerServicePlan{{Address: "app/service/api", Stores: map[string]string{"app": "app"}}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/host/v1/storage/read?store=app&key=tmp%2Fa.txt", nil)
	req.Header.Set("If-Match", `"wrong-etag"`)
	handler("app", rec, req)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("GET If-Match mismatch = %d, want 412", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodHead, "/host/v1/storage/read?store=app&key=tmp%2Fa.txt", nil)
	req.Header.Set("If-Match", obj.ETag)
	handler("app", rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD If-Match match = %d, want 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/host/v1/storage/read?store=app&key=tmp%2Fa.txt", nil)
	req.Header.Set("If-None-Match", `"bad"`)
	handler("app", rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET invalid If-None-Match = %d, want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/host/v1/storage/delete?store=app&key=tmp%2F&recursive=1", nil)
	handler("app", rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("recursive DELETE = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if _, err := store.Head(context.Background(), "tmp/a.txt"); err == nil {
		t.Fatal("tmp/a.txt survived recursive worker delete")
	}
	if _, err := store.Head(context.Background(), "keep/b.txt"); err != nil {
		t.Fatalf("keep/b.txt should remain: %v", err)
	}
}
