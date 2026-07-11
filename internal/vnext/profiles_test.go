package vnext

import "testing"

func TestProfileValidationRejectsIncompleteDurableAndTriggerResources(t *testing.T) {
	resources := []Resource{
		{Address: "house/execution/process", Module: "house", Kind: "scenery.execution/v1", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.process"}, "mode": "durable", "revision": "0"}},
		{Address: "house/schedule/nightly", Module: "house", Kind: "scenery.schedule/v1", Spec: map[string]any{"trigger": map[string]any{"cron": "* * * * *", "every": "1m"}, "invoke": map[string]any{}}},
		{Address: "house/binding/internal", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{"protocol": "internal", "delivery": "enqueue", "internal": map[string]any{"visibility": "application"}}},
	}
	diagnostics := validateProfileResources(resources)
	for _, code := range []string{"SCN2201", "SCN2202", "SCN2203", "SCN2301", "SCN2302", "SCN2401", "SCN2402"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestProfileValidationRejectsIncompatibleExecutionDelivery(t *testing.T) {
	resources := []Resource{
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Name: "process", Spec: map[string]any{}},
		{Address: "house/operation/other", Module: "house", Kind: "scenery.operation/v1", Name: "other", Spec: map[string]any{}},
		{Address: "house/execution/process", Module: "house", Kind: "scenery.execution/v1", Name: "process", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.process"}, "mode": "direct"}},
		{Address: "house/binding/process", Module: "house", Kind: "scenery.binding/v1", Name: "process", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.other"}, "execution": map[string]any{"$ref": "execution.process"}, "protocol": "internal", "delivery": "enqueue",
			"authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"internal": map[string]any{"visibility": "application", "principal": "inherit"},
		}},
	}
	diagnostics := validateProfileResources(resources)
	for _, code := range []string{"SCN2403", "SCN2404"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestProfileValidationEnforcesDurableEngineAndKeySemantics(t *testing.T) {
	resources := []Resource{
		{Address: "app/data_source/not_an_engine", Module: "app", Kind: "scenery.data-source/v1", Name: "not_an_engine", Spec: map[string]any{}},
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Name: "process", Spec: map[string]any{
			"idempotency": map[string]any{"mode": "keyed", "key": []any{map[string]any{"$expression": "input.id"}}},
		}},
		{Address: "house/execution/process", Module: "house", Kind: "scenery.execution/v1", Name: "process", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.process"}, "mode": "durable", "engine": map[string]any{"$ref": "app/data_source/not_an_engine"},
			"revision": "1", "timeout": "10s", "lease": "20s", "attempts": "0",
			"retry":       map[string]any{"strategy": "exponential", "initial": "2s", "factor": "1", "maximum": "1s"},
			"retention":   map[string]any{"success": "0s", "failure": "1d"},
			"concurrency": map[string]any{"key": map[string]any{"$expression": "principal.uid"}, "limit": "0"},
		}},
	}
	diagnostics := validateProfileResources(resources)
	for _, code := range []string{"SCN2205", "SCN2206", "SCN2207", "SCN2208", "SCN2209"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestProfileValidationRejectsIncompleteDataAndUIResources(t *testing.T) {
	resources := []Resource{
		{Address: "house/entity/scene", Module: "house", Kind: "scenery.entity/v1", Spec: map[string]any{}},
		{Address: "house/view/recent", Module: "house", Kind: "scenery.view/v1", Spec: map[string]any{}},
		{Address: "house/crud/scenes", Module: "house", Kind: "scenery.crud/v1", Spec: map[string]any{}},
		{Address: "house/page/detail", Module: "house", Kind: "scenery.page/v1", Spec: map[string]any{"path": "/house/{id}"}},
		{Address: "house/renderer/detail", Module: "house", Kind: "scenery.renderer/v1", Spec: map[string]any{}},
	}
	diagnostics := validateProfileResources(resources)
	for _, code := range []string{"SCN2501", "SCN2502", "SCN2503", "SCN2504", "SCN2601", "SCN2602"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}
