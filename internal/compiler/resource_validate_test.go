package compiler

import "testing"

func TestLibraryContractRequiresPkgRootAndRecordOperations(t *testing.T) {
	module := Resource{Address: "app/module/maps3d", Module: "app", Name: "maps3d", Kind: "scenery.module", Spec: map[string]any{
		"source": map[string]any{"value": "./pkg/maps3d"}, "workspace_package_root": "pkg/maps3d",
	}}
	library := Resource{Address: "maps3d/library/maps3d", Module: "maps3d", Name: "maps3d", Kind: "scenery.library", Spec: map[string]any{
		"runtime": "go", "package": "example.test/pkg/maps3d", "version": "v1.0.0", "artifact": map[string]any{"name": "maps3d"},
	}}
	input := Resource{Address: "maps3d/record/process_input", Module: "maps3d", Name: "process_input", Kind: "scenery.record", Spec: map[string]any{}}
	output := Resource{Address: "maps3d/record/process_output", Module: "maps3d", Name: "process_output", Kind: "scenery.record", Spec: map[string]any{}}
	operation := Resource{Address: "maps3d/operation/process", Module: "maps3d", Name: "process", Kind: "scenery.operation", Spec: map[string]any{
		"library": map[string]any{"$ref": "library.maps3d"}, "input": map[string]any{"$ref": "record.process_input"},
		"handler": map[string]any{"method": "Process"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "record.process_output"}},
	}}
	resources := []Resource{module, library, input, output, operation}
	if diagnostics := validateResourceSemantics(resources); hasDiagnostic(diagnostics, "SCN2004") || hasDiagnostic(diagnostics, "SCN2005") {
		t.Fatalf("valid library diagnostics = %#v", diagnostics)
	}

	broken := operation
	broken.Spec = cloneMapValue(operation.Spec)
	broken.Spec["service"] = map[string]any{"$ref": "service.other"}
	broken.Spec["input"] = map[string]any{"$ref": "string"}
	diagnostics := validateResourceSemantics([]Resource{module, library, input, output, broken})
	if !hasDiagnostic(diagnostics, "SCN2004") || !hasDiagnostic(diagnostics, "SCN2005") {
		t.Fatalf("broken library diagnostics = %#v", diagnostics)
	}
}

func TestOperationIdempotencyRequiresOrderedTypedKeyComponents(t *testing.T) {
	tests := []struct {
		name string
		spec map[string]any
		want bool
	}{
		{name: "absent", spec: map[string]any{}, want: true},
		{name: "wrong block shape", spec: map[string]any{"idempotency": "keyed"}},
		{name: "none", spec: map[string]any{"idempotency": map[string]any{"mode": "none"}}, want: true},
		{
			name: "keyed",
			spec: map[string]any{"idempotency": map[string]any{
				"mode": "keyed",
				"key":  []any{map[string]any{"$expression": "input.tenant_id"}, map[string]any{"$expression": "input.scene_id"}},
			}},
			want: true,
		},
		{name: "missing key", spec: map[string]any{"idempotency": map[string]any{"mode": "keyed"}}},
		{name: "empty key", spec: map[string]any{"idempotency": map[string]any{"mode": "keyed", "key": []any{}}}},
		{name: "scalar key", spec: map[string]any{"idempotency": map[string]any{"mode": "keyed", "key": map[string]any{"$expression": "input.scene_id"}}}},
		{name: "literal component", spec: map[string]any{"idempotency": map[string]any{"mode": "keyed", "key": []any{"scene"}}}},
		{name: "computed component", spec: map[string]any{"idempotency": map[string]any{"mode": "keyed", "key": []any{map[string]any{"$expression": "input.scene_id + input.tenant_id"}}}}},
		{name: "non-input component", spec: map[string]any{"idempotency": map[string]any{"mode": "keyed", "key": []any{map[string]any{"$expression": "principal.uid"}}}}},
		{name: "missing input field", spec: map[string]any{"idempotency": map[string]any{"mode": "keyed", "key": []any{map[string]any{"$expression": "input.missing"}}}}},
		{name: "nested input field", spec: map[string]any{"idempotency": map[string]any{"mode": "keyed", "key": []any{map[string]any{"$expression": "input.scene_id.value"}}}}},
		{name: "unit input", spec: map[string]any{"input": map[string]any{"$ref": "std.type.unit"}, "idempotency": map[string]any{"mode": "keyed", "key": []any{map[string]any{"$expression": "input.scene_id"}}}}},
		{name: "none with key", spec: map[string]any{"idempotency": map[string]any{"mode": "none", "key": []any{map[string]any{"$expression": "input.scene_id"}}}}},
	}
	input := Resource{Address: "house/record/process_input", Module: "house", Name: "process_input", Kind: "scenery.record", Spec: map[string]any{"field": []any{
		map[string]any{"name": "tenant_id", "type": map[string]any{"$ref": "string"}},
		map[string]any{"name": "scene_id", "type": map[string]any{"$ref": "string"}},
	}}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := cloneMapValue(test.spec)
			if spec["input"] == nil {
				spec["input"] = map[string]any{"$ref": "record.process_input"}
			}
			operation := Resource{Address: "house/operation/process", Module: "house", Name: "process", Kind: "scenery.operation", Spec: spec}
			diagnostics := validateResourceSemantics([]Resource{input, operation})
			if got := !hasDiagnostic(diagnostics, "SCN2003"); got != test.want {
				t.Fatalf("valid = %t, want %t; diagnostics=%#v", got, test.want, diagnostics)
			}
		})
	}
}

func TestProfileValidationRejectsIncompleteDurableAndTriggerResources(t *testing.T) {
	resources := []Resource{
		{Address: "house/execution/process", Module: "house", Kind: "scenery.execution", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.process"}, "mode": "durable", "revision": "0"}},
		{Address: "house/schedule/nightly", Module: "house", Kind: "scenery.schedule", Spec: map[string]any{"trigger": map[string]any{"cron": "* * * * *", "every": "1m"}, "invoke": map[string]any{}}},
		{Address: "house/binding/internal", Module: "house", Kind: "scenery.binding", Spec: map[string]any{"protocol": "internal", "delivery": "enqueue", "internal": map[string]any{"visibility": "application"}}},
	}
	diagnostics := validateResourceSemantics(resources)
	for _, code := range []string{"SCN2201", "SCN2202", "SCN2203", "SCN2301", "SCN2302", "SCN2401", "SCN2402"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestProfileValidationRejectsIncompatibleExecutionDelivery(t *testing.T) {
	resources := []Resource{
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation", Name: "process", Spec: map[string]any{}},
		{Address: "house/operation/other", Module: "house", Kind: "scenery.operation", Name: "other", Spec: map[string]any{}},
		{Address: "house/execution/process", Module: "house", Kind: "scenery.execution", Name: "process", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.process"}, "mode": "direct"}},
		{Address: "house/binding/process", Module: "house", Kind: "scenery.binding", Name: "process", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.other"}, "execution": map[string]any{"$ref": "execution.process"}, "protocol": "internal", "delivery": "enqueue",
			"authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
			"internal": map[string]any{"visibility": "application", "principal": "inherit"},
		}},
	}
	diagnostics := validateResourceSemantics(resources)
	for _, code := range []string{"SCN2403", "SCN2404"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestProfileValidationEnforcesDurableEngineAndKeySemantics(t *testing.T) {
	resources := []Resource{
		{Address: "app/data_source/not_an_engine", Module: "app", Kind: "scenery.data-source", Name: "not_an_engine", Spec: map[string]any{}},
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation", Name: "process", Spec: map[string]any{
			"idempotency": map[string]any{"mode": "keyed", "key": []any{map[string]any{"$expression": "input.id"}}},
		}},
		{Address: "house/execution/process", Module: "house", Kind: "scenery.execution", Name: "process", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.process"}, "mode": "durable", "engine": map[string]any{"$ref": "app/data_source/not_an_engine"},
			"revision": "1", "timeout": "10s", "lease": "20s", "attempts": "0",
			"retry":       map[string]any{"strategy": "exponential", "initial": "2s", "factor": "1", "maximum": "1s"},
			"retention":   map[string]any{"success": "0s", "failure": "1d"},
			"concurrency": map[string]any{"key": map[string]any{"$expression": "principal.uid"}, "limit": "0"},
		}},
	}
	diagnostics := validateResourceSemantics(resources)
	for _, code := range []string{"SCN2205", "SCN2206", "SCN2207", "SCN2208", "SCN2209"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestProfileValidationRejectsIncompleteDataAndUIResources(t *testing.T) {
	resources := []Resource{
		{Address: "house/entity/scene", Module: "house", Kind: "scenery.entity", Spec: map[string]any{}},
		{Address: "house/view/recent", Module: "house", Kind: "scenery.view", Spec: map[string]any{}},
		{Address: "house/crud/scenes", Module: "house", Kind: "scenery.crud", Spec: map[string]any{}},
		{Address: "house/page/detail", Module: "house", Kind: "scenery.page", Spec: map[string]any{"path": "/house/{id}"}},
		{Address: "house/renderer/detail", Module: "house", Kind: "scenery.renderer", Spec: map[string]any{}},
	}
	diagnostics := validateResourceSemantics(resources)
	for _, code := range []string{"SCN2501", "SCN2502", "SCN2503", "SCN2504", "SCN2601", "SCN2602"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}
