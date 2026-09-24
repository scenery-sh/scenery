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
	for _, fragment := range []string{`readonly "house.maps_key"?: string;`, `readonly "house.zoom"?: number;`, `export async function loadPublicConfig(`, `"__scenery/public-config"`} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("public configuration missing %q:\n%s", fragment, source)
		}
	}
	if strings.Contains(source, "private_path") {
		t.Fatal("a non-public input reached the browser projection")
	}
}
