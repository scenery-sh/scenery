package compiler

import "testing"

func TestGoGeneratedNameCollisionsAreCompileDiagnostics(t *testing.T) {
	module := Resource{Address: "app/module/house", Module: "app", Kind: "scenery.module", Name: "house", Spec: map[string]any{
		"package": map[string]any{"go_contract": map[string]any{"import_path": "example.test/house"}},
	}}
	resources := []Resource{
		module,
		{Address: "house/record/foo_bar", Module: "house", Kind: "scenery.record", Name: "foo_bar", Spec: map[string]any{}},
		{Address: "house/record/foo__bar", Module: "house", Kind: "scenery.record", Name: "foo__bar", Spec: map[string]any{}},
		{Address: "house/record/preserved", Module: "house", Kind: "scenery.record", Name: "preserved", Spec: map[string]any{
			"unknown_fields": "preserve", "field": map[string]any{"name": "unknown_fields", "type": map[string]any{"$ref": "string"}},
		}},
		{Address: "house/service/house", Module: "house", Kind: "scenery.service", Name: "house", Spec: map[string]any{}},
		{Address: "house/operation/process_scene", Module: "house", Kind: "scenery.operation", Name: "process_scene", Spec: map[string]any{"service": map[string]any{"$ref": "service.house"}, "input": map[string]any{"$ref": "string"}}},
		{Address: "house/record/clone_process_scene_input", Module: "house", Kind: "scenery.record", Name: "clone_process_scene_input", Spec: map[string]any{}},
	}
	diagnostics := validateGoGeneratedNames(resources)
	if count := diagnosticCount(diagnostics, "SCN4012"); count != 3 {
		t.Fatalf("SCN4012 count = %d, diagnostics=%#v", count, diagnostics)
	}
}

func TestCompilerServiceGoClientsReservesLocalPackageSymbolsAcrossServices(t *testing.T) {
	resources := []Resource{
		{Address: "app/module/consumer", Module: "app", Kind: "scenery.module", Name: "consumer", Spec: map[string]any{
			"package": map[string]any{"go_contract": map[string]any{"import_path": "example.test/consumer"}},
		}},
		{Address: "consumer/record/echo_internal_client", Module: "consumer", Kind: "scenery.record", Name: "echo_internal_client", Spec: map[string]any{}},
		{Address: "consumer/operation/quote", Module: "consumer", Kind: "scenery.operation", Name: "quote", Spec: map[string]any{
			"service": map[string]any{"$ref": "service.alpha"}, "input": "string",
			"result": []any{map[string]any{"name": "outcome", "type": "string"}},
		}},
		{Address: "consumer/service/alpha", Module: "consumer", Kind: "scenery.service", Name: "alpha", Spec: map[string]any{
			"client": []any{map[string]any{"name": "echo", "binding": map[string]any{"$ref": "provider/binding/echo_internal"}}},
		}},
		{Address: "consumer/service/beta", Module: "consumer", Kind: "scenery.service", Name: "beta", Spec: map[string]any{
			"client": []any{map[string]any{"name": "echo", "binding": map[string]any{"$ref": "provider/binding/echo_internal"}}},
		}},
		{Address: "provider/service/provider", Module: "provider", Kind: "scenery.service", Name: "provider", Spec: map[string]any{}},
		{Address: "provider/operation/echo", Module: "provider", Kind: "scenery.operation", Name: "echo", Spec: map[string]any{
			"service": map[string]any{"$ref": "service.provider"}, "input": "string",
			"result": []any{map[string]any{"name": "ok", "type": "string"}},
		}},
		{Address: "provider/binding/echo_internal", Module: "provider", Kind: "scenery.binding", Name: "echo_internal", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.echo"}, "protocol": "internal", "delivery": "call",
		}},
	}
	alphaClients, err := serviceGoClients(resources, resources[3])
	if err != nil || len(alphaClients) != 1 || alphaClients[0].InterfaceName != "AlphaEchoInternalClient" {
		t.Fatalf("alpha clients = %+v, %v; want AlphaEchoInternalClient", alphaClients, err)
	}
	betaClients, err := serviceGoClients(resources, resources[4])
	if err != nil || len(betaClients) != 1 || betaClients[0].InterfaceName != "BetaEchoInternalClient" {
		t.Fatalf("beta clients = %+v, %v; want BetaEchoInternalClient", betaClients, err)
	}
	if diags := validateGoGeneratedNames(resources); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diags)
	}
	withCollision := append(resources, Resource{
		Address: "consumer/record/quote_result_variant_outcome", Module: "consumer", Kind: "scenery.record", Name: "quote_result_variant_outcome", Spec: map[string]any{},
	})
	if count := diagnosticCount(validateGoGeneratedNames(withCollision), "SCN4012"); count != 1 {
		t.Fatalf("expected SCN4012 on QuoteResultVariantOutcome collision, got %#v", validateGoGeneratedNames(withCollision))
	}
}
