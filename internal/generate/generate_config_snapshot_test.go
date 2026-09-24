package generate

import (
	"strings"
	"testing"
)

func TestGeneratedConstructorReadsDeploymentInputsFromTheSnapshot(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Kind != "scenery.service" {
			continue
		}
		spec := result.Manifest.Resources[index].Spec
		spec["config"] = map[string]any{
			"process_concurrency": map[string]any{"$scalar": "int", "value": "4"},
			"page_size":           map[string]any{"$scalar": "int", "value": "50"},
		}
		spec["config_schema"] = []any{
			map[string]any{"name": "page_size", "input": "page_size", "type": "uint32", "phase": "contract", "sensitive": false},
			map[string]any{"name": "process_concurrency", "input": "process_concurrency", "type": "uint32", "phase": "deployment", "sensitive": false},
			map[string]any{"name": "provider_token", "input": "provider_token", "type": `resource_ref("secret")`, "phase": "deployment", "sensitive": true},
			map[string]any{"name": "weather_pack_root", "input": "weather_pack_root", "type": "optional(host_path)", "phase": "deployment", "sensitive": false},
		}
	}
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(files, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{
		`sceneryruntime.ResolveDeploymentConfig("house.process_concurrency", &input.Config.ProcessConcurrency, "uint32", false)`,
		`sceneryruntime.ResolveDeploymentConfig("house.weather_pack_root", &input.Config.WeatherPackRoot, "optional(host_path)", true)`,
		`input.Config.ProviderToken = sceneryruntime.ConfigSecretRef("house.provider_token")`,
		`scenery.UnmarshalContractValue([]byte("50"), &input.Config.PageSize, "uint32")`,
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("adapter missing %q:\n%s", fragment, adapter)
		}
	}
	if strings.Contains(adapter, `[]byte("4")`) {
		t.Fatal("a deployment value was compiled into the adapter")
	}
}
