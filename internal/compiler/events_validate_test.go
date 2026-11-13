package compiler

import "testing"

func TestScheduleValidationRejectsUnavailableOrRuntimeInvalidTriggers(t *testing.T) {
	for _, trigger := range []map[string]any{{}, {"every": "0s"}} {
		if err := validateScheduleTrigger(trigger); err == nil {
			t.Fatalf("trigger %#v was accepted", trigger)
		}
	}
	if err := validateScheduleTrigger(map[string]any{"every": "15m"}); err != nil {
		t.Fatalf("valid every trigger rejected: %v", err)
	}
	if err := validateScheduleTrigger(map[string]any{"every": "7d"}); err != nil {
		t.Fatalf("valid multi-day every trigger rejected: %v", err)
	}
}

func TestScheduleValidationTypeChecksCompleteStaticInput(t *testing.T) {
	resources := eventProfileFixtureResources()
	resources = append(resources, Resource{
		Address: "house/schedule/nightly", Module: "house", Name: "nightly", Kind: "scenery.schedule",
		Spec: map[string]any{
			"trigger": map[string]any{"every": "1d"},
			"invoke": map[string]any{
				"operation": map[string]any{"$ref": "operation.process"}, "execution": map[string]any{"$ref": "execution.process"},
				"identity": map[string]any{"$ref": "std.workload_identity.scheduler"}, "authorization": map[string]any{"$ref": "std.authorization.scheduled"},
				"pipeline": map[string]any{"$ref": "std.pipeline.empty"}, "input": map[string]any{"scene_id": "scene-1", "extra": true},
			},
			"overlap": "invalid",
		},
	})
	diagnostics := validateResourceSemantics(resources)
	for _, code := range []string{"SCN2303", "SCN2304"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestScheduleValidationAcceptsOnlyEmptyUnitInput(t *testing.T) {
	operation := Resource{Address: "house/operation/ping", Module: "house", Name: "ping", Kind: "scenery.operation", Spec: map[string]any{"input": map[string]any{"$ref": "std.type.unit"}}}
	resources := map[string]Resource{operation.Address: operation}
	if err := validateStaticOperationInput(resources, operation, map[string]any{}); err != nil {
		t.Fatalf("empty unit input rejected: %v", err)
	}
	if err := validateStaticOperationInput(resources, operation, map[string]any{"extra": true}); err == nil {
		t.Fatal("non-empty unit input was accepted")
	}
}

func eventProfileFixtureResources() []Resource {
	return []Resource{
		{Address: "house/record/process_input", Module: "house", Name: "process_input", Kind: "scenery.record", Spec: map[string]any{"field": []any{
			map[string]any{"name": "tenant_id", "type": map[string]any{"$ref": "string"}}, map[string]any{"name": "scene_id", "type": map[string]any{"$ref": "string"}},
		}}},
		{Address: "house/operation/process", Module: "house", Name: "process", Kind: "scenery.operation", Spec: map[string]any{
			"input": map[string]any{"$ref": "record.process_input"}, "result": map[string]any{"name": "done", "type": map[string]any{"$ref": "record.process_input"}},
		}},
		{Address: "house/execution/process", Module: "house", Name: "process", Kind: "scenery.execution", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.process"}, "mode": "direct"}},
	}
}
