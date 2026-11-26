package main

import (
	"context"
	"slices"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	workerruntime "scenery.sh/runtime"
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
