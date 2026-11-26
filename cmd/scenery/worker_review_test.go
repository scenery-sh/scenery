package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/jsbuild"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/storageconfig"
	"scenery.sh/internal/workerabi"
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
		"SCENERY_ROLE=api",
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
		"SCENERY_ROLE=", "SCENERY_ASSISTANT_RUNTIME_CONFIG=", "SCENERY_ASSISTANT_TOKEN_KEY=", "SCENERY_ASSISTANT_TOKEN_KEY_FILE=",
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
		"SCENERY_ROLE=", "DATABASE_URL=", "SCENERY_DATABASE_JSON=", "SCENERY_DEV_REPORT_URL=", "SCENERY_DEV_REPORT_TOKEN=",
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

func TestReconcileExplicitJSDependenciesPreservesRetainedPreBuildDigest(t *testing.T) {
	root := t.TempDir()
	keptNode := filepath.Join("web", "node_modules", "pkg", "kept.js")
	droppedNode := filepath.Join("web", "node_modules", "pkg", "dropped.js")
	wsFile := filepath.Join("web", "shared.ts")
	writeWatchFile(t, root, keptNode, "export const kept = 1;\n")
	writeWatchFile(t, root, droppedNode, "export const dropped = 1;\n")
	writeWatchFile(t, root, wsFile, "export const ws = 1;\n")

	watch, err := openWorkerInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watch.fs.Close() }()
	prevDeps := []string{droppedNode, keptNode}
	nextDeps := []string{keptNode}
	watch.setExplicit(prevDeps, false)
	current, err := watch.capture(nil, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an edit to the retained dependency after bundling but before
	// reconcileExplicitJSDependencies updates the explicit dependency set.
	writeWatchFile(t, root, keptNode, "export const kept = 2;\n")
	updated, err := reconcileExplicitJSDependencies(watch, current, prevDeps, nextDeps)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := updated[droppedNode]; exists {
		t.Fatalf("dropped node_modules dependency %s remained in updated snapshot", droppedNode)
	}
	if updated[keptNode] != current[keptNode] {
		t.Fatalf("retained dependency digest was overwritten during refresh")
	}
	if updated[wsFile] != current[wsFile] {
		t.Fatalf("workspace dependency digest was overwritten during refresh")
	}
	if err := (&workerDevelopment{watch: watch}).verify(updated); !errors.Is(err, errWorkerInputsChanged) {
		t.Fatalf("verify after concurrent edit to retained dependency = %v, want errWorkerInputsChanged", err)
	}

	// Without concurrent edits, removing a skipped dependency passes verify, and
	// removing a tracked workspace file retains its snapshot entry for verify.
	watch.setExplicit(prevDeps, false)
	cleanCurrent, err := watch.capture(nil, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	cleanUpdated, err := reconcileExplicitJSDependencies(watch, cleanCurrent, []string{droppedNode, keptNode, wsFile}, []string{keptNode})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cleanUpdated[wsFile]; !exists {
		t.Fatalf("workspace dependency %s should remain in updated snapshot after unimport", wsFile)
	}
	if err := (&workerDevelopment{watch: watch}).verify(cleanUpdated); err != nil {
		t.Fatalf("verify after clean dependency removal = %v", err)
	}
}

func TestProjectWorkerAssistantsAndReserveFrontendPaths(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeWatchFile(t, root, "web/package.json", "{}\n")
	result := &compiler.Result{
		Root:              root,
		WorkspaceRevision: "ws-rev-1",
		ImplementationRevisions: map[string]string{
			"app/assistant/support": "runtime-7",
		},
		Manifest: &graph.Manifest{
			ContractRevision: "contract-1",
			Resources: []graph.Resource{
				{Address: "std.authorization.public", Kind: "scenery.authorization", Name: "public", Module: "std", Spec: map[string]any{"strategy": "public"}},
				{Address: "app/http_gateway/api", Kind: "scenery.http-gateway", Name: "api", Module: "app", Spec: map[string]any{"base_path": "/api"}},
				{Address: "app/mcp_server/support", Kind: "scenery.mcp-server", Name: "support", Module: "app", Spec: map[string]any{"max_input_bytes": 1024, "max_result_bytes": 4096}},
				{
					Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support", Module: "app",
					Spec: map[string]any{
						"mcp_server": map[string]any{"$ref": "mcp_server.support"},
						"surface": map[string]any{
							"path":          "/support-chat",
							"gateway":       map[string]any{"$ref": "http_gateway.api"},
							"authorization": map[string]any{"$ref": "std.authorization.public"},
						},
					},
				},
			},
		},
	}
	plans, err := projectWorkerAssistants(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans len = %d, want 1", len(plans))
	}
	plan := plans[0]
	if plan.Address != "app/assistant/support" || plan.Path != "/support-chat" || plan.RuntimeRevision != "runtime-7" || plan.CapabilityRevision == "" {
		t.Fatalf("unexpected assistant plan: %+v", plan)
	}
	manifest, err := mcpcontract.Parse(plan.MCPManifest)
	if err != nil || manifest.ContractRevision != plan.CapabilityRevision {
		t.Fatalf("invalid assistant MCP manifest (%v): %+v", err, manifest)
	}
	frontend, err := selectWorkerFrontend(root, app.Config{
		Root:      "web",
		Frontends: map[string]app.FrontendConfig{"web": {Root: "web", Adapter: "vite", Mode: "static"}},
	}, result)
	if err != nil {
		t.Fatal(err)
	}
	if frontend == nil || !slices.Contains(frontend.Reserved, "/support-chat") {
		t.Fatalf("expected assistant path in frontend reserved list: %+v", frontend)
	}
}

func TestStartupReconcileExplicitJSDependenciesPreservesPreBuildDigest(t *testing.T) {
	root := t.TempDir()
	wsFile := filepath.Join("web", "shared.ts")
	nodeFile := filepath.Join("web", "node_modules", "pkg", "index.js")
	writeWatchFile(t, root, wsFile, "export const shared = 1;\n")
	writeWatchFile(t, root, nodeFile, "export const pkg = 1;\n")

	watch, err := openWorkerInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watch.fs.Close() }()

	inputs, err := watch.capture(nil, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an edit to wsFile right as startup JS bundling ran, before
	// reconcileExplicitJSDependencies registers explicit dependencies.
	writeWatchFile(t, root, wsFile, "export const shared = 2;\n")
	updated, err := reconcileExplicitJSDependencies(watch, inputs, nil, []string{wsFile, nodeFile})
	if err != nil {
		t.Fatal(err)
	}
	if updated[wsFile] != inputs[wsFile] {
		t.Fatal("startup reconcile overwrote pre-build workspace dependency digest")
	}
	if _, exists := updated[nodeFile]; !exists {
		t.Fatalf("startup reconcile missing newly discovered node_modules dependency %s", nodeFile)
	}
	if err := (&workerDevelopment{watch: watch}).verify(updated); !errors.Is(err, errWorkerInputsChanged) {
		t.Fatalf("verify after concurrent edit = %v, want errWorkerInputsChanged", err)
	}
	wrappedErr := &workerJSBuildError{service: "app/service/web", err: errWorkerInputsChanged}
	if !errors.Is(wrappedErr, errWorkerInputsChanged) {
		t.Fatalf("workerJSBuildError must unwrap errWorkerInputsChanged: %v", wrappedErr)
	}
}

func TestWorkerAssistantSourceDigestSkipsNodeModulesAndRejectsSymlinks(t *testing.T) {
	sourceRoot := t.TempDir()
	writeWatchFile(t, sourceRoot, "index.ts", "export const v = 1;\n")
	writeWatchFile(t, sourceRoot, filepath.Join("node_modules", "pkg", "index.js"), "module.exports = 1;\n")
	binDir := filepath.Join(sourceRoot, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../pkg/index.js", filepath.Join(binDir, "helper")); err != nil {
		t.Fatal(err)
	}

	digest1, err := workerAssistantSourceDigest(sourceRoot)
	if err != nil {
		t.Fatalf("workerAssistantSourceDigest with node_modules symlinks = %v", err)
	}
	// Edits inside node_modules do not invalidate authored assistant sources.
	writeWatchFile(t, sourceRoot, filepath.Join("node_modules", "pkg", "index.js"), "module.exports = 2;\n")
	digest2, err := workerAssistantSourceDigest(sourceRoot)
	if err != nil || digest2 != digest1 {
		t.Fatalf("digest after node_modules change = (%q, %v), want unchanged %q", digest2, err, digest1)
	}

	writeWatchFile(t, sourceRoot, "index.ts", "export const v = 2;\n")
	digest3, err := workerAssistantSourceDigest(sourceRoot)
	if err != nil || digest3 == digest1 {
		t.Fatalf("digest after index.ts edit = (%q, %v), want different from %q", digest3, err, digest1)
	}

	if err := os.Symlink("index.ts", filepath.Join(sourceRoot, "escape.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := workerAssistantSourceDigest(sourceRoot); err == nil {
		t.Fatal("expected symlink outside node_modules to return error")
	}
}

func TestWorkerConfigIdentityAndLiveReloadSelectivelyMatchesWorkers(t *testing.T) {
	baseA := &workerruntime.ConfigSnapshot{
		Kind: workerruntime.ConfigSnapshotKind, AppID: "app", Environment: "development", Revision: "rev-1",
		Consumer: "mod/service/a",
		Values:   map[string]json.RawMessage{"a.greeting": json.RawMessage(`"hello"`)},
		Secrets:  map[string][]byte{"a.token": []byte("secret-1")},
	}
	reorderedRevA := &workerruntime.ConfigSnapshot{
		Kind: workerruntime.ConfigSnapshotKind, AppID: "app", Environment: "development", Revision: "rev-2",
		Consumer: "mod/service/a",
		Values:   map[string]json.RawMessage{"a.greeting": json.RawMessage(`"hello"`)},
		Secrets:  map[string][]byte{"a.token": []byte("secret-1")},
	}
	changedSecretA := &workerruntime.ConfigSnapshot{
		Kind: workerruntime.ConfigSnapshotKind, AppID: "app", Environment: "development", Revision: "rev-2",
		Consumer: "mod/service/a",
		Values:   map[string]json.RawMessage{"a.greeting": json.RawMessage(`"hello"`)},
		Secrets:  map[string][]byte{"a.token": []byte("secret-2")},
	}
	if workerConfigIdentity(baseA) != workerConfigIdentity(reorderedRevA) {
		t.Fatal("workerConfigIdentity must ignore revision-only bumps when values and secrets match")
	}
	if workerConfigIdentity(baseA) == workerConfigIdentity(changedSecretA) {
		t.Fatal("workerConfigIdentity must change when consumed secret bytes change")
	}

	identity := workerProcessIdentity{ContractRevision: "c1", ImplementationRevision: "i1", BuildInputDigest: "b1", Target: "go"}
	procA := &workerProcess{
		program: workerProgram{Service: "mod/service/a", Runtime: "go", Digest: "sha256:1", Identity: identity, config: baseA},
		done:    make(chan struct{}),
	}
	progUnchanged := workerProgram{Service: "mod/service/a", Runtime: "go", Digest: "sha256:1", Identity: identity, config: reorderedRevA}
	if !workerStillMatches(procA, progUnchanged) {
		t.Fatal("workerStillMatches returned false when consumed config stayed identical")
	}
	progChanged := workerProgram{Service: "mod/service/a", Runtime: "go", Digest: "sha256:1", Identity: identity, config: changedSecretA}
	if workerStillMatches(procA, progChanged) {
		t.Fatal("workerStillMatches returned true when consumed secret changed")
	}

	hostSnap := &workerruntime.ConfigSnapshot{
		Kind: workerruntime.ConfigSnapshotKind, AppID: "app", Environment: "development", Revision: "rev-2",
		Consumer: hostConsumer, Values: map[string]json.RawMessage{"public.flag": json.RawMessage(`true`)},
		Public: []string{"public.flag"},
	}
	workerruntime.UpdateWorkerHostConfig(hostSnap)
	loaded, err := workerruntime.LoadConfigSnapshot()
	if err != nil || loaded == nil || loaded.Revision != "rev-2" {
		t.Fatalf("LoadConfigSnapshot after UpdateWorkerHostConfig = (%+v, %v), want rev-2", loaded, err)
	}
}

func TestWorkerVerifyDatabaseSetupSkipsUnchangedAndBlocksPendingMigration(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeWatchFile(t, root, ".scenery.json", `{"name":"db-test","id":"db-test","envs":{"local":{"default":true}},"database":{"migrations":[{"service":"main","directory":"db/migrations"}]}}`)
	writeWatchFile(t, root, filepath.Join("db", "migrations", "0001_init.sql"), "CREATE TABLE items (id int);\n")

	_, cfg, err := app.DiscoverRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	compiled := &compiler.Result{
		Root:            root,
		Manifest:        &graph.Manifest{ContractRevision: "contract-1"},
		SQLRequirements: compiler.SQLRequirements{{Name: "main", Schema: "public"}},
	}
	initialSetup, hasWork, err := buildDevDatabaseSetup(root, cfg, compiled)
	if err != nil || !hasWork {
		t.Fatalf("buildDevDatabaseSetup(initial) = (%+v, %v, %v)", initialSetup, hasWork, err)
	}

	called := 0
	var observedStatusOnly bool
	dev := &workerDevelopment{
		watch:              &workerInputWatch{root: root},
		compiled:           compiled,
		envName:            "local",
		dbSetupFingerprint: initialSetup.Fingerprint,
		dbSetupApply: func(_ context.Context, _ devDatabaseSetup, statusOnly bool) error {
			called++
			observedStatusOnly = statusOnly
			return &pendingMigrationError{Service: "main"}
		},
	}

	t.Setenv(storageconfig.RuntimeConfigEnv, "/tmp/storage.json")
	t.Setenv("SCENERY_PROCESS_LINK", "/tmp/link.json")
	t.Setenv(workerruntime.AssistantRuntimeConfigEnv, "/tmp/assistant.json")
	dbEnv := workerDatabaseEnv(root, cfg, "local")
	if !slices.Contains(dbEnv, storageconfig.RuntimeConfigEnv+"=/tmp/storage.json") {
		t.Fatalf("workerDatabaseEnv stripped %s: %v", storageconfig.RuntimeConfigEnv, dbEnv)
	}
	for _, entry := range dbEnv {
		if strings.HasPrefix(entry, "SCENERY_PROCESS_LINK=") || strings.HasPrefix(entry, workerruntime.AssistantRuntimeConfigEnv+"=") {
			t.Fatalf("workerDatabaseEnv leaked restricted variable %q", entry)
		}
	}

	gotSetup, dbChanged, err := dev.checkPendingMigrations(context.Background())
	if err != nil || dbChanged || gotSetup.Fingerprint != initialSetup.Fingerprint || called != 0 {
		t.Fatalf("checkPendingMigrations(unchanged) = (%+v, %v, %v), called=%d", gotSetup, dbChanged, err, called)
	}

	writeWatchFile(t, root, filepath.Join("db", "migrations", "0002_add_col.sql"), "ALTER TABLE items ADD COLUMN name text;\n")
	_, _, err = dev.checkPendingMigrations(context.Background())
	var pendingErr *pendingMigrationError
	if !errors.As(err, &pendingErr) || cliExitCode(err) != 3 {
		t.Fatalf("checkPendingMigrations(pending) = %v (exit %d), want *pendingMigrationError exit 3", err, cliExitCode(err))
	}
	if called != 1 || !observedStatusOnly {
		t.Fatalf("dbSetupApply called=%d, statusOnly=%v; want 1, true", called, observedStatusOnly)
	}
	if applyErr := dev.applyVerifiedDatabaseSetup(context.Background(), gotSetup); !errors.As(applyErr, &pendingErr) {
		t.Fatalf("applyVerifiedDatabaseSetup = %v, want *pendingMigrationError from stub", applyErr)
	}
	if called != 2 || observedStatusOnly {
		t.Fatalf("dbSetupApply after verify = (%d, statusOnly=%v), want (2, false)", called, observedStatusOnly)
	}
}

func newTestWorkerControl(t *testing.T, handler http.HandlerFunc) (string, *http.Client) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wrev-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Protocols: workerabi.Protocols(), ReadHeaderTimeout: time.Second, Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client := workerControlClient(sock)
	t.Cleanup(client.CloseIdleConnections)
	return dir, client
}

func newTestDummyWorkerProcess(dir string, program workerProgram) *workerProcess {
	cmd := exec.Command("true")
	cmd.Process = &os.Process{Pid: os.Getpid()}
	return &workerProcess{program: program, cmd: cmd, socket: filepath.Join(dir, "w1.sock"), token: strings.Repeat("a", 32), done: make(chan struct{})}
}

func TestWorkerWatcherRepublishesGenerationOnHostOnlyConfigChangeAndForcedRetire(t *testing.T) {
	var mu sync.Mutex
	var published []map[string]any
	var forcedAttempts int
	dir, client := newTestWorkerControl(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodPut:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			published = append(published, body)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/__scenery/process/v1/generations/") && r.Method == http.MethodDelete:
			if r.URL.Query().Get("force") != "true" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			mu.Lock()
			forcedAttempts++
			attempt := forcedAttempts
			mu.Unlock()
			if attempt == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	identity := workerProcessIdentity{ContractRevision: "c1", ImplementationRevision: "i1", BuildInputDigest: "b1", Target: "go"}
	workerCfg := &workerruntime.ConfigSnapshot{Kind: workerruntime.ConfigSnapshotKind, AppID: "app", Environment: "local", Revision: "rev-1", Consumer: "mod/service/a", Values: map[string]json.RawMessage{}, Secrets: map[string][]byte{}}
	hostCfg1 := &workerruntime.ConfigSnapshot{Kind: workerruntime.ConfigSnapshotKind, AppID: "app", Environment: "local", Revision: "rev-1", Consumer: "$host", Values: map[string]json.RawMessage{"public.flag": json.RawMessage(`false`)}, Public: []string{"public.flag"}, Secrets: map[string][]byte{}}
	hostCfg2 := &workerruntime.ConfigSnapshot{Kind: workerruntime.ConfigSnapshotKind, AppID: "app", Environment: "local", Revision: "rev-2", Consumer: "$host", Values: map[string]json.RawMessage{"public.flag": json.RawMessage(`false`)}, Public: []string{"public.flag"}, Secrets: map[string][]byte{}}

	aliveProc := newTestDummyWorkerProcess(dir, workerProgram{Service: "mod/service/a", Process: "a", Runtime: "go", Digest: "sha256:1", Identity: identity, config: workerCfg})
	bundle1 := workerBundle{
		ContractRevision: "c1",
		Host:             workerruntime.ProcessHostConfig{DevelopmentCall: true, WorkerConfig: map[string]*workerruntime.ConfigSnapshot{"mod/service/a": workerCfg}},
		Programs:         []workerProgram{aliveProc.program},
		HostConfigKeys:   []string{"public.flag"},
		config:           hostCfg1,
	}
	bundle2 := bundle1
	bundle2.config = hostCfg2

	watch := &workerWatcher{token: "tok", client: client, bundle: bundle1, active: map[string]*workerProcess{"a": aliveProc}, generation: 1, sequence: 1, retireDrainTimeout: 10 * time.Millisecond, retireForceInterval: 5 * time.Millisecond}
	watch.generations.add(1, watch.active)
	if err := watch.replace(context.Background(), bundle2, nil); err != nil {
		t.Fatalf("watch.replace(host revision bump) = %v", err)
	}
	watch.retirements.Wait()
	if watch.generation != 2 || watch.active["a"] != aliveProc {
		t.Fatalf("watch state after host-only change = (gen=%d, active=%p), want gen=2 and reused process", watch.generation, watch.active["a"])
	}
	mu.Lock()
	defer mu.Unlock()
	if forcedAttempts < 2 || len(published) != 1 {
		t.Fatalf("forcedAttempts=%d, published=%d; want >=2, 1", forcedAttempts, len(published))
	}
	if gen, _ := published[0]["generation"].(float64); gen != 2 {
		t.Fatalf("published generation = %v, want 2", published[0]["generation"])
	}
	if hostPayload, _ := published[0]["host_config"].(map[string]any); hostPayload == nil || hostPayload["revision"] != "rev-2" {
		t.Fatalf("published host_config = %#v, want revision rev-2", published[0]["host_config"])
	}
}

func TestWorkerWatcherCancelsActiveRetirementsOnHostExitAndRetriesFailedConfigApply(t *testing.T) {
	var mu sync.Mutex
	allowPublish := false
	retireForcedSeen := make(chan struct{})
	var retireForcedOnce sync.Once
	dir, client := newTestWorkerControl(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodPut:
			mu.Lock()
			ok := allowPublish
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/__scenery/process/v1/generations/") && r.Method == http.MethodDelete:
			if strings.HasSuffix(r.URL.Path, "/1") || strings.HasSuffix(r.URL.Path, "/2") {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.URL.Query().Get("force") == "true" {
				retireForcedOnce.Do(func() { close(retireForcedSeen) })
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusConflict)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	root := filepath.Join(dir, "app")
	writeWatchFile(t, root, ".scenery.json", `{"name":"cfg-retry","id":"cfg-retry","envs":{"local":{"default":true}}}`)
	store, err := devConfigStore(app.Config{Name: "cfg-retry", ID: "cfg-retry"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(store.Dir()) })
	mutateFlag := func(val string) {
		t.Helper()
		if _, err := store.Mutate(context.Background(), "local", appconfig.Mutation{Key: "a.flag", Value: json.RawMessage(val)}); err != nil {
			t.Fatal(err)
		}
	}
	mutateFlag("false")
	_, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		t.Fatal(err)
	}
	compiled := &compiler.Result{
		Root: root,
		Manifest: &graph.Manifest{
			ContractRevision: "c1",
			Resources: []graph.Resource{{
				Address: "mod/service/a", Module: "mod", Kind: "scenery.service", Name: "a",
				Spec: map[string]any{"config_schema": []any{map[string]any{"name": "flag", "type": "bool", "public": true}}},
			}},
		},
	}
	inputWatch, err := openWorkerInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inputWatch.fs.Close() }()

	identity := workerProcessIdentity{ContractRevision: "c1", ImplementationRevision: "i1", BuildInputDigest: "b1", Target: "go"}
	bundle1 := workerBundle{
		ContractRevision: "c1",
		Host:             workerruntime.ProcessHostConfig{DevelopmentCall: true},
		Programs:         []workerProgram{{Service: "mod/service/a", Process: "a", Runtime: "go", Digest: "sha256:1", Identity: identity}},
		HostConfigKeys:   []string{"a.flag"},
	}
	if err := bundle1.resolveDevelopmentConfig(context.Background(), compiled, cfg, "local"); err != nil {
		t.Fatal(err)
	}
	bundle1.setWorkerConfig()
	aliveProc := newTestDummyWorkerProcess(dir, bundle1.Programs[0])
	watch := &workerWatcher{
		token: "tok", client: client, dev: &workerDevelopment{watch: inputWatch, compiled: compiled, envName: "local"},
		bundle: bundle1, active: map[string]*workerProcess{"a": aliveProc}, generation: 1, sequence: 1,
		retireDrainTimeout: 5 * time.Millisecond, retireForceInterval: 5 * time.Millisecond,
	}

	mutateFlag("true")
	if watch.applyConfigurationChange(context.Background()) {
		t.Fatal("applyConfigurationChange must return false when publish fails")
	}
	mu.Lock()
	allowPublish = true
	mu.Unlock()
	if !watch.applyConfigurationChange(context.Background()) {
		t.Fatal("applyConfigurationChange must return true once Host accepts publish")
	}

	// Bump config revision so watch.run's startup check triggers a real replacement
	// and starts a stuck forced-retirement goroutine under runCtx, then exit via hostDone.
	mutateFlag("false")
	hostDone := make(chan error, 1)
	sentinel := errors.New("host exited")
	runDone := make(chan error, 1)
	go func() { runDone <- watch.run(context.Background(), hostDone) }()
	select {
	case <-retireForcedSeen:
	case <-time.After(time.Second):
		hostDone <- sentinel
		t.Fatal("forced retire did not start within 1s")
	}
	hostDone <- sentinel
	select {
	case runErr := <-runDone:
		if !errors.Is(runErr, sentinel) {
			t.Fatalf("watcher.run = %v, want %v", runErr, sentinel)
		}
	case <-time.After(time.Second):
		t.Fatal("watcher.run deadlocked on active retirements after hostDone")
	}
}
