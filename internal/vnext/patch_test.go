package vnext

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestCompileAppliesEffectiveDefaultsBeforeExactPatches(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "house"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scenery.scn"), []byte(`language {
  edition = "2027"
  require_profiles = ["scenery.compiler-core/v1", "scenery.inspection-core/v1", "scenery.compatibility-core/v1", "scenery.patches/v1"]
}
application "patch_defaults" { version = "1.0.0" }
module "house" { source = "./house" }
patch "record_openness" {
  target         = module.house.model
  module_version = ">= 1.0.0, < 2.0.0"
  schema         = "scenery.record/v1"
  expect {
    path  = "/spec/unknown_fields"
    value = "reject"
  }
  set {
    path  = "/spec/unknown_fields"
    value = "preserve"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "house", "scenery.package.scn"), []byte(`package "house" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
record "model" {
  field "value" { type = string }
}
export "model" {
  value     = record.model
  patchable = ["/spec/unknown_fields"]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	source := resourcesByAddress(result.ViewManifests["source"])["house/record/model"]
	effective := resourcesByAddress(result.ViewManifests["effective"])["house/record/model"]
	if source.Spec["unknown_fields"] != nil || effective.Spec["unknown_fields"] != "preserve" {
		t.Fatalf("source=%#v effective=%#v", source.Spec, effective.Spec)
	}
	field := effective.Origin.FieldProvenance["/spec/unknown_fields"]
	if field.Kind != "patch" || field.ProvidedBy != "app/patch/record_openness" {
		t.Fatalf("patch provenance = %#v", field)
	}
}
