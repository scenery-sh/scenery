package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/workerplan"
)

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
	service := workerplan.WorkerServicePlan{Name: "jobs"}
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

// The Rust Host records runtime failure reports itself. The bundle hands it
// the identity `scenery.failure.report` requires, so `scenery report` and the
// call and durable-run results that embed a report stay schema-valid.
func TestWorkerBundleCarriesFailureReportIdentity(t *testing.T) {
	identity := currentWorkerReportIdentity()
	schema, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", "scenery.failure.report.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Properties struct {
			SchemaRevision struct {
				Const string `json:"const"`
			} `json:"schema_revision"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &document); err != nil {
		t.Fatal(err)
	}
	if identity.SchemaRevision == "" || identity.SchemaRevision != document.Properties.SchemaRevision.Const || identity.ProducerVersion == "" {
		t.Fatalf("report identity = %+v, schema revision %q", identity, document.Properties.SchemaRevision.Const)
	}
	encoded, err := json.Marshal(workerBundle{Reports: identity})
	if err != nil {
		t.Fatal(err)
	}
	if want := `"reports":{"schema_revision":"` + identity.SchemaRevision + `","producer_version":"`; !strings.Contains(string(encoded), want) {
		t.Fatalf("bundle manifest %s lacks %s", encoded, want)
	}
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
		"SCENERY_MCP_BRIDGE_SECRET=bridge-secret",
		"SCENERY_MCP_URL=http://127.0.0.1:10",
		"SCENERY_DEV_REPORT_URL=http://127.0.0.1:1/report",
		"SCENERY_DEV_REPORT_TOKEN=report-secret",
	}
	goEnv := strings.Join(workerProcessEnv(base, "go"), "\n")
	for _, forbidden := range []string{
		"SCENERY_ROLE=", "SCENERY_ASSISTANT_RUNTIME_CONFIG=", "SCENERY_ASSISTANT_TOKEN_KEY=", "SCENERY_ASSISTANT_TOKEN_KEY_FILE=",
		"SCENERY_ASSISTANT_CONTROL_ADDR=", "SCENERY_ASSISTANT_CONTROL_TOKEN=", "SCENERY_MCP_BRIDGE_SECRET=", "SCENERY_MCP_URL=",
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
		"SCENERY_ASSISTANT_TOKEN_KEY=", "SCENERY_ASSISTANT_CONTROL_TOKEN=", "SCENERY_ASSISTANT_CONTROL_ADDR=", "SCENERY_MCP_BRIDGE_SECRET=", "SCENERY_MCP_URL=",
	} {
		if strings.Contains(rustEnv, forbidden) {
			t.Fatalf("Rust worker leaked %s", forbidden)
		}
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
