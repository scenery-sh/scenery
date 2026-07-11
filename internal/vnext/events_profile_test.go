package vnext

import "testing"

func TestScheduleValidationRejectsUnavailableOrRuntimeInvalidTriggers(t *testing.T) {
	for _, trigger := range []map[string]any{{"calendar": "business_days"}, {"every": "0s"}} {
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
	if err := validateScheduleTrigger(map[string]any{"calendar": "FREQ=WEEKLY;BYDAY=MO,FR;BYHOUR=2", "timezone": "Europe/Prague"}); err != nil {
		t.Fatalf("valid calendar trigger rejected: %v", err)
	}
}

func TestScheduleValidationTypeChecksCompleteStaticInput(t *testing.T) {
	resources := eventProfileFixtureResources()
	resources = append(resources, Resource{
		Address: "house/schedule/nightly", Module: "house", Name: "nightly", Kind: "scenery.schedule/v1",
		Spec: map[string]any{
			"trigger": map[string]any{"cron": "0 2 * * *", "timezone": "Europe/Prague"},
			"invoke": map[string]any{
				"operation": map[string]any{"$ref": "operation.process"}, "execution": map[string]any{"$ref": "execution.process"},
				"identity": map[string]any{"$ref": "std.workload_identity.scheduler"}, "authorization": map[string]any{"$ref": "std.authorization.scheduled"},
				"pipeline": map[string]any{"$ref": "std.pipeline.empty"}, "input": map[string]any{"scene_id": "scene-1", "extra": true},
			},
			"overlap": "invalid",
		},
	})
	diagnostics := validateProfileResources(resources)
	for _, code := range []string{"SCN2303", "SCN2304"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestScheduleValidationAcceptsOnlyEmptyUnitInput(t *testing.T) {
	operation := Resource{Address: "house/operation/ping", Module: "house", Name: "ping", Kind: "scenery.operation/v1", Spec: map[string]any{"input": map[string]any{"$ref": "std.type.unit"}}}
	resources := map[string]Resource{operation.Address: operation}
	if err := validateStaticOperationInput(resources, operation, map[string]any{}); err != nil {
		t.Fatalf("empty unit input rejected: %v", err)
	}
	if err := validateStaticOperationInput(resources, operation, map[string]any{"extra": true}); err == nil {
		t.Fatal("non-empty unit input was accepted")
	}
}

func TestEventValidationChecksBusContractMappingAndDeliveryPolicy(t *testing.T) {
	resources := eventProfileFixtureResources()
	resources = append(resources,
		Resource{Address: "app/event_bus/events", Module: "app", Name: "events", Kind: "scenery.event-bus/v1", Spec: map[string]any{"provider": map[string]any{"$ref": "provider.kafka"}}},
		Resource{Address: "house/event/registered", Module: "house", Name: "registered", Kind: "scenery.event/v1", Spec: map[string]any{"payload": map[string]any{"$ref": "record.event_payload"}, "version": "1"}},
		Resource{Address: "house/binding/consume", Module: "house", Name: "consume", Kind: "scenery.binding/v1", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.process"}, "execution": map[string]any{"$ref": "execution.process"}, "protocol": "event", "delivery": "call",
			"authentication": map[string]any{"$ref": "std.authentication.service_identity"}, "authorization": map[string]any{"$ref": "std.authorization.application"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"event": map[string]any{"direction": "publish", "bus": map[string]any{"$ref": "app/event_bus/events"}, "channel": "house.registered", "contract": map[string]any{"$ref": "event.registered"}, "guarantee": "sometimes", "map": map[string]any{"from": map[string]any{"$ref": "message.payload"}, "to": map[string]any{"$ref": "operation.process.input"}}},
		}},
	)
	diagnostics := validateProfileResources(resources)
	for _, code := range []string{"SCN2704", "SCN2705"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestEventEmissionValidationChecksOutcomePayloadType(t *testing.T) {
	resources := eventProfileFixtureResources()
	resources = append(resources,
		Resource{Address: "app/event_bus/events", Module: "app", Name: "events", Kind: "scenery.event-bus/v1", Spec: map[string]any{"provider": map[string]any{"$ref": "provider.kafka"}}},
		Resource{Address: "house/event/registered", Module: "house", Name: "registered", Kind: "scenery.event/v1", Spec: map[string]any{"payload": map[string]any{"$ref": "record.event_payload"}, "version": "1"}},
		Resource{Address: "house/event_emission/registered", Module: "house", Name: "registered", Kind: "scenery.event-emission/v1", Spec: map[string]any{
			"bus": map[string]any{"$ref": "app/event_bus/events"}, "channel": "house.registered", "contract": map[string]any{"$ref": "event.registered"}, "guarantee": "at_least_once",
			"from": map[string]any{"operation": map[string]any{"$ref": "operation.process"}, "when": map[string]any{"$ref": "result.done"}, "payload": map[string]any{"$ref": "result.done"}},
		}},
	)
	diagnostics := validateProfileResources(resources)
	if !hasDiagnostic(diagnostics, "SCN2706") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func eventProfileFixtureResources() []Resource {
	return []Resource{
		{Address: "house/record/process_input", Module: "house", Name: "process_input", Kind: "scenery.record/v1", Spec: map[string]any{"field": []any{
			map[string]any{"name": "tenant_id", "type": map[string]any{"$ref": "string"}}, map[string]any{"name": "scene_id", "type": map[string]any{"$ref": "string"}},
		}}},
		{Address: "house/record/event_payload", Module: "house", Name: "event_payload", Kind: "scenery.record/v1", Spec: map[string]any{"field": map[string]any{"name": "event_id", "type": map[string]any{"$ref": "string"}}}},
		{Address: "house/operation/process", Module: "house", Name: "process", Kind: "scenery.operation/v1", Spec: map[string]any{
			"input": map[string]any{"$ref": "record.process_input"}, "result": map[string]any{"name": "done", "type": map[string]any{"$ref": "record.process_input"}},
		}},
		{Address: "house/execution/process", Module: "house", Name: "process", Kind: "scenery.execution/v1", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.process"}, "mode": "direct"}},
	}
}
