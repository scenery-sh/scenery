package vnext

import "testing"

func TestApplyPatchesRequiresExactPrecondition(t *testing.T) {
	resources := []Resource{
		{Address: "house/execution/process", Module: "house", Kind: "scenery.execution/v1", Name: "process", Spec: map[string]any{"timeout": "40m"}},
		{Address: "app/module/house", Module: "app", Kind: "scenery.module/v1", Name: "house", Spec: map[string]any{
			"package":         map[string]any{"version": "1.2.3"},
			"exports":         map[string]any{"process_execution": map[string]any{"$ref": "execution.process"}},
			"export_metadata": map[string]any{"process_execution": map[string]any{"value": map[string]any{"$ref": "execution.process"}, "patchable": []any{"/spec/timeout"}}},
		}},
		{Address: "app/patch/timeout", Module: "app", Kind: "scenery.patch/v1", Name: "timeout", Spec: map[string]any{
			"target": map[string]any{"$ref": "module.house.process_execution"}, "module_version": ">= 1.0.0, < 2.0.0", "schema": "scenery.execution/v1",
			"expect": map[string]any{"path": "/spec/timeout", "value": "40m"},
			"set":    map[string]any{"path": "/spec/timeout", "value": "45m"},
		}},
	}
	patched, diagnostics := applyPatches(resources)
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics: %#v", diagnostics)
	}
	if patched[0].Spec["timeout"] != "45m" {
		t.Fatalf("target = %#v", patched[0])
	}
	if len(patched[0].Origin.Patches) != 1 || patched[0].Origin.Patches[0] != "app/patch/timeout" {
		t.Fatalf("patch provenance = %#v", patched[0].Origin)
	}
	field := patched[0].Origin.FieldProvenance["/spec/timeout"]
	if field.Kind != "patch" || field.ProvidedBy != "app/patch/timeout" || field.SourceAddress != "house/execution/process" {
		t.Fatalf("patch field provenance = %#v", field)
	}
	resources[2].Spec["expect"].(map[string]any)["value"] = "30m"
	_, diagnostics = applyPatches(resources)
	if !hasDiagnostic(diagnostics, "SCN2903") {
		t.Fatalf("diagnostics: %#v", diagnostics)
	}
}

func TestApplyPatchesEnforcesVersionExportAndSingleWriter(t *testing.T) {
	base := []Resource{
		{Address: "house/execution/process", Module: "house", Kind: "scenery.execution/v1", Name: "process", Spec: map[string]any{"timeout": "40m"}},
		{Address: "app/module/house", Module: "app", Kind: "scenery.module/v1", Name: "house", Spec: map[string]any{
			"package":         map[string]any{"version": "1.2.3"},
			"exports":         map[string]any{"process_execution": map[string]any{"$ref": "execution.process"}},
			"export_metadata": map[string]any{"process_execution": map[string]any{"value": map[string]any{"$ref": "execution.process"}, "patchable": []any{"/spec/timeout"}}},
		}},
	}
	patch := Resource{Address: "app/patch/timeout", Module: "app", Kind: "scenery.patch/v1", Name: "timeout", Spec: map[string]any{
		"target": map[string]any{"$ref": "module.house.process_execution"}, "module_version": ">= 2.0.0, < 3.0.0", "schema": "scenery.execution/v1",
		"expect": map[string]any{"path": "/spec/timeout", "value": "40m"}, "set": map[string]any{"path": "/spec/timeout", "value": "45m"},
	}}
	_, diagnostics := applyPatches(append(append([]Resource(nil), base...), patch))
	if !hasDiagnostic(diagnostics, "SCN2905") {
		t.Fatalf("version diagnostics = %#v", diagnostics)
	}
	patch.Spec["module_version"] = ">= 1.0.0, < 2.0.0"
	patch.Spec["set"].(map[string]any)["path"] = "/spec/lease"
	_, diagnostics = applyPatches(append(append([]Resource(nil), base...), patch))
	if !hasDiagnostic(diagnostics, "SCN2906") {
		t.Fatalf("patchability diagnostics = %#v", diagnostics)
	}
	patch.Spec["set"].(map[string]any)["path"] = "/spec/timeout"
	duplicate := patch
	duplicate.Address, duplicate.Name = "app/patch/timeout_again", "timeout_again"
	_, diagnostics = applyPatches(append(append(append([]Resource(nil), base...), patch), duplicate))
	if !hasDiagnostic(diagnostics, "SCN2907") {
		t.Fatalf("single-writer diagnostics = %#v", diagnostics)
	}
	patch.Spec["target"] = map[string]any{"$ref": "house/execution/process"}
	_, diagnostics = applyPatches(append(append([]Resource(nil), base...), patch))
	if !hasDiagnostic(diagnostics, "SCN2906") {
		t.Fatalf("private target diagnostics = %#v", diagnostics)
	}
}
