package generate

import (
	"strings"
	"testing"
)

func TestTypeScriptClientProjectsPublicConfiguration(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	root := t.TempDir()
	if file, err := renderTypeScriptPublicConfig(result, root); err != nil || file != nil {
		t.Fatalf("an application without public inputs rendered %v %v", file, err)
	}
	for index := range result.Manifest.Resources {
		resource := &result.Manifest.Resources[index]
		if resource.Kind != "scenery.module" {
			continue
		}
		inputs, _ := resource.Spec["interface_inputs"].(map[string]any)
		if inputs == nil {
			inputs = map[string]any{}
			resource.Spec["interface_inputs"] = inputs
		}
		inputs["maps_key"] = map[string]any{"type": map[string]any{"$ref": "string"}, "phase": "deployment", "public": true}
		inputs["zoom"] = map[string]any{"type": map[string]any{"$ref": "uint32"}, "phase": "deployment", "public": true, "default": map[string]any{"$scalar": "int", "value": "3"}}
		inputs["private_path"] = map[string]any{"type": map[string]any{"$ref": "host_path"}, "phase": "deployment"}
	}
	file, err := renderTypeScriptPublicConfig(result, root)
	if err != nil || file == nil {
		t.Fatalf("public configuration = %v %v", file, err)
	}
	source := string(file.Bytes)
	for _, fragment := range []string{`readonly "house.maps_key"?: string;`, `readonly "house.zoom"?: number;`, `export async function loadPublicConfig(`, `"_app/public-config"`} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("public configuration missing %q:\n%s", fragment, source)
		}
	}
	if strings.Contains(source, "private_path") || strings.Contains(strings.ToLower(source), "scenery") {
		t.Fatal("a private input or framework branding reached the browser projection")
	}
}

func TestBrowserClientSourcesUseNeutralNames(t *testing.T) {
	t.Parallel()
	result := nativeApplicationGenerationFixture(t.TempDir())
	target := Resource{Address: "app/typescript_client/public_api", Name: "public_api", Spec: map[string]any{}}
	files, err := renderTypeScriptTargetWithCatalog(result, target, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Path, ".ts") && strings.Contains(strings.ToLower(string(file.Bytes)), "scenery") {
			t.Fatalf("browser client %s contains framework branding", file.Path)
		}
	}
	if strings.Contains(strings.ToLower(renderTSRuntime(allTSRuntimeCapabilities())), "scenery") {
		t.Fatal("the complete browser runtime contains framework branding")
	}
}
