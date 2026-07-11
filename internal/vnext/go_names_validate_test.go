package vnext

import "testing"

func TestGoGeneratedNameCollisionsAreCompileDiagnostics(t *testing.T) {
	module := Resource{Address: "app/module/house", Module: "app", Kind: "scenery.module/v1", Name: "house", Spec: map[string]any{
		"package": map[string]any{"go_contract": map[string]any{"import_path": "example.test/house"}},
	}}
	resources := []Resource{
		module,
		{Address: "house/record/foo_bar", Module: "house", Kind: "scenery.record/v1", Name: "foo_bar", Spec: map[string]any{}},
		{Address: "house/record/foo__bar", Module: "house", Kind: "scenery.record/v1", Name: "foo__bar", Spec: map[string]any{}},
		{Address: "house/record/preserved", Module: "house", Kind: "scenery.record/v1", Name: "preserved", Spec: map[string]any{
			"unknown_fields": "preserve", "field": map[string]any{"name": "unknown_fields", "type": map[string]any{"$ref": "string"}},
		}},
		{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1", Name: "house", Spec: map[string]any{}},
		{Address: "house/operation/process_scene", Module: "house", Kind: "scenery.operation/v1", Name: "process_scene", Spec: map[string]any{"service": map[string]any{"$ref": "service.house"}, "input": map[string]any{"$ref": "string"}}},
		{Address: "house/record/clone_process_scene_input", Module: "house", Kind: "scenery.record/v1", Name: "clone_process_scene_input", Spec: map[string]any{}},
	}
	diagnostics := validateGoGeneratedNames(resources)
	if count := diagnosticCount(diagnostics, "SCN4012"); count != 3 {
		t.Fatalf("SCN4012 count = %d, diagnostics=%#v", count, diagnostics)
	}
}
