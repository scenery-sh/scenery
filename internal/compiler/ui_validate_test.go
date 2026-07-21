package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUIValidationRequiresTypedInternalBindings(t *testing.T) {
	resources := uiProfileFixtureResources()
	for index := range resources {
		if resources[index].Address == "house/binding/load_scene" {
			resources[index].Spec["protocol"] = "http"
		}
	}
	diagnostics := validateUISemantics("", resources)
	if !hasDiagnostic(diagnostics, "SCN2603") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestUIValidationAllowsPageWithoutLoadButRejectsExplicitInvalidLoad(t *testing.T) {
	page := Resource{Address: "house/page/static", Module: "house", Name: "static", Kind: "scenery.page", Spec: map[string]any{"path": "/static"}}
	if diagnostics := validateUISemantics("", []Resource{page}); hasErrors(diagnostics) {
		t.Fatalf("static page diagnostics = %#v", diagnostics)
	}

	page.Spec["load"] = ""
	if diagnostics := validateUISemantics("", []Resource{page}); !hasDiagnostic(diagnostics, "SCN2603") {
		t.Fatalf("explicit invalid load diagnostics = %#v", diagnostics)
	}
}

func TestUIValidationRejectsDuplicatePageRoutesAndMissingRendererModule(t *testing.T) {
	resources := uiProfileFixtureResources()
	resources = append(resources, Resource{Address: "house/page/duplicate", Module: "house", Name: "duplicate", Kind: "scenery.page", Spec: map[string]any{"path": "/house/scenes/{scene_id}", "load": map[string]any{"$ref": "binding.load_scene"}}})
	diagnostics := validateUISemantics(t.TempDir(), resources)
	for _, code := range []string{"SCN2604", "SCN2605"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestRendererImplementationDigestTracksDeclaredModule(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "house")
	if err := os.MkdirAll(filepath.Join(moduleRoot, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "ui", "SceneDetail.tsx"), []byte("export default {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resources := uiProfileFixtureResources()
	resources = append(resources, Resource{Address: "app/module/house", Module: "app", Name: "house", Kind: "scenery.module", Spec: map[string]any{"source": "./house"}})
	resolved, diagnostics := enrichUIImplementationDigests(root, resources)
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	renderer := resourcesByAddress(&Manifest{Resources: resolved})["house/renderer/scene_detail_web"]
	if stringValue(renderer.Spec["implementation_digest"]) == "" {
		t.Fatalf("renderer = %#v", renderer)
	}
}

func uiProfileFixtureResources() []Resource {
	return []Resource{
		{Address: "house/record/load_input", Module: "house", Name: "load_input", Kind: "scenery.record", Spec: map[string]any{"field": map[string]any{"name": "scene_id", "type": map[string]any{"$ref": "string"}}}},
		{Address: "house/operation/load", Module: "house", Name: "load", Kind: "scenery.operation", Spec: map[string]any{"input": map[string]any{"$ref": "record.load_input"}}},
		{Address: "house/binding/load_scene", Module: "house", Name: "load_scene", Kind: "scenery.binding", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.load"}, "protocol": "internal", "delivery": "call", "internal": map[string]any{"visibility": "application", "principal": "inherit"}}},
		{Address: "house/page/scene_detail", Module: "house", Name: "scene_detail", Kind: "scenery.page", Spec: map[string]any{"path": "/house/scenes/{scene_id}", "load": map[string]any{"$ref": "binding.load_scene"}, "action": map[string]any{"name": "refresh", "invoke": map[string]any{"$ref": "binding.load_scene"}}}},
		{Address: "house/renderer/scene_detail_web", Module: "house", Name: "scene_detail_web", Kind: "scenery.renderer", Spec: map[string]any{"page": map[string]any{"$ref": "page.scene_detail"}, "runtime": "web", "module": "ui/SceneDetail.tsx"}},
	}
}
