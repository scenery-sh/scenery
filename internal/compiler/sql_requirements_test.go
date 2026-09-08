package compiler

import (
	"reflect"
	"strings"
	"testing"
)

func sqlRequirementFixture(module, source, binding, lifecycle string) []Resource {
	serviceAddress := module + "/service/api"
	sourceAddress := "app/data_source/" + source
	return []Resource{
		{Address: sourceAddress, Kind: "scenery.data-source", Name: source, Module: "app", Origin: Origin{Kind: "authored", ModuleChain: []string{module}}, Spec: map[string]any{
			"provider": map[string]any{"$ref": "app/provider/postgres"}, "lifecycle": lifecycle,
			"require_capabilities": []any{"sql.transaction/v1", "sql.query/v1"}, "config": map[string]any{"database": binding},
		}},
		{Address: serviceAddress, Kind: "scenery.service", Name: "api", Module: module, Origin: Origin{Kind: "authored"}, Spec: map[string]any{
			"implementation": map[string]any{"constructor": "New"}, "dependency": map[string]any{"name": "database", "instance": map[string]any{"$ref": sourceAddress}},
		}},
		{Address: module + "/operation/get", Kind: "scenery.operation", Name: "get", Module: module, Spec: map[string]any{
			"service": map[string]any{"$ref": serviceAddress}, "handler": map[string]any{"method": "Get"},
		}},
	}
}

func TestSQLRequirementsPreserveBindingsAndModuleIdentity(t *testing.T) {
	resources := sqlRequirementFixture("first", "first", "orders", "managed")
	resources = append(resources, sqlRequirementFixture("second", "second", "billing-data", "external")...)
	shared := sqlRequirementFixture("third", "first", "orders", "managed")
	resources = append(resources, shared[1:]...)
	resources = append(resources, Resource{Address: "app/data_source/unused", Kind: "scenery.data-source", Name: "unused", Spec: map[string]any{
		"require_capabilities": []any{"sql.query/v1"}, "config": map[string]any{"database": "unused"},
	}})
	got, diagnostics := ProjectSQLRequirements(resources, false)
	if len(diagnostics) != 0 || len(got) != 2 {
		t.Fatalf("requirements=%+v diagnostics=%+v", got, diagnostics)
	}
	if got[0].Address != "app/data_source/first" || got[0].Name != "orders" || got[0].Schema != "orders" || !reflect.DeepEqual(got[0].Consumers, []string{"first/service/api", "third/service/api"}) {
		t.Fatalf("shared binding lost canonical consumers: %+v", got[0])
	}
	if !reflect.DeepEqual(got[0].Origin.ModuleChain, []string{"first"}) || !reflect.DeepEqual(got[0].Capabilities, []string{"sql.query/v1", "sql.transaction/v1"}) {
		t.Fatalf("declaration evidence lost: %+v", got[0])
	}
	if got[1].Schema != "billing_data" || got[1].Lifecycle != "external" || got[1].Consumers[0] != "second/service/api" {
		t.Fatalf("second instance collapsed: %+v", got[1])
	}
	if bindings := got.Bindings(false); !reflect.DeepEqual(bindings, []SQLBinding{{Name: "billing-data", Schema: "billing_data"}, {Name: "orders", Schema: "orders"}}) {
		t.Fatalf("bindings=%+v", bindings)
	}
	for _, service := range RuntimeServices(resources) {
		dependencies, err := ServiceGoDependencies(resourcesByAddress(&Manifest{Resources: resources}), service)
		if err != nil || len(dependencies) != 1 {
			t.Fatalf("generated dependency=%+v err=%v", dependencies, err)
		}
		binding, ok := got.Binding(dependencies[0].RuntimeName)
		if !ok || binding.Name != dependencies[0].RuntimeName || dependencies[0].GoType != "datasource.SQL" {
			t.Fatalf("generation and runtime disagree: %+v %+v", binding, dependencies)
		}
	}
}

func TestSQLRequirementsRejectInvalidSchemasBeforeSupply(t *testing.T) {
	for _, name := range []string{"scenery", "public", "information-schema", "pg-users", "pg_catalog", "not valid", "UPPERCASE", strings.Repeat("a", 64)} {
		_, diagnostics := ProjectSQLRequirements(sqlRequirementFixture("app", "db", name, "managed"), false)
		if len(diagnostics) == 0 || diagnostics[0].Code != "SCN2505" {
			t.Fatalf("invalid binding %q accepted: %+v", name, diagnostics)
		}
	}
	for _, names := range [][2]string{{"billing-data", "billing.data"}, {"foo-bar", "foo_bar"}} {
		resources := append(sqlRequirementFixture("one", "one", names[0], "managed"), sqlRequirementFixture("two", "two", names[1], "managed")...)
		if _, diagnostics := ProjectSQLRequirements(resources, false); len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "both map") || !strings.Contains(diagnostics[0].Message, names[0]) || !strings.Contains(diagnostics[0].Message, names[1]) {
			t.Fatalf("schema collision not rejected with both identities: %+v", diagnostics)
		}
	}
}

func TestSQLRequirementsFrameworkAndUnusedCapabilities(t *testing.T) {
	resources := sqlRequirementFixture("app", "objects", "uploads", "managed")
	resources[0].Spec["require_capabilities"] = []any{"object.read/v1"}
	resources = append(resources,
		Resource{Address: "app/execution_engine/tasks", Kind: "scenery.execution-engine", Name: "tasks", Spec: map[string]any{
			"provider": map[string]any{"$ref": "app/provider/durable"}, "lifecycle": "managed", "require_capabilities": []any{"execution.durable/v1"},
		}},
		Resource{Address: "app/execution/task", Kind: "scenery.execution", Name: "task", Spec: map[string]any{
			"mode": "durable", "operation": map[string]any{"$ref": "app/operation/get"}, "engine": map[string]any{"$ref": "app/execution_engine/tasks"},
		}},
	)
	got, diagnostics := ProjectSQLRequirements(resources, true)
	if len(diagnostics) != 0 || len(got) != 2 || got[0].Kind != SQLDurable || got[1].Kind != SQLStandardAuth {
		t.Fatalf("framework requirements=%+v diagnostics=%+v", got, diagnostics)
	}
	if got[0].Address != "app/execution_engine/tasks" || got[0].Consumers[0] != "app/execution/task" || got[1].ConfigPath != ".scenery.json#auth.enabled" {
		t.Fatalf("framework provenance=%+v", got)
	}
	if len(got.Bindings(false)) != 1 || len(got.Bindings(true)) != 1 {
		t.Fatalf("auth must retain reserved SQL even with a remote durable endpoint: %+v", got)
	}
	got, diagnostics = ProjectSQLRequirements(resources, false)
	if len(diagnostics) != 0 || len(got) != 1 || len(got.Bindings(true)) != 0 {
		t.Fatalf("remote durable-only program needs no local SQL: %+v %+v", got, diagnostics)
	}
	resources = resources[:len(resources)-1]
	got, diagnostics = ProjectSQLRequirements(resources, false)
	if len(diagnostics) != 0 || len(got) != 0 {
		t.Fatalf("unused engine or object capability eagerly requires SQL: %+v %+v", got, diagnostics)
	}
}
