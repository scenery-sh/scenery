package generate

import (
	"strings"
	"testing"
)

func TestGeneratedHTTPByteStreamKeepsBodyOutOfOutcomeClone(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	for index := range result.Manifest.Resources {
		resource := &result.Manifest.Resources[index]
		switch resource.Kind {
		case "scenery.record":
			if resource.Name == "process_scene_result" {
				resource.Spec["field"] = map[string]any{"name": "content", "type": map[string]any{"$ref": "bytes"}}
			}
		case "scenery.binding":
			resource.Spec["delivery"] = "stream"
			httpSpec := resource.Spec["http"].(map[string]any)
			httpSpec["response"] = map[string]any{
				"name": "processed", "when": map[string]any{"$ref": "result.processed"}, "status": "200",
				"body": map[string]any{"codec": "bytes", "from": map[string]any{"$ref": "result.processed.content"}, "produced_media_types": []any{"model/gltf-binary"}},
			}
		}
	}
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources))
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(files, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{
		"ProcessScene(context.Context, contract.ProcessSceneInput) (contract.ProcessSceneOutcome, scenery.ByteStream, error)",
		"outcome, stream, err := service.ProcessScene(ctx, copied)",
		"if len(typed.Value.Content) != 0",
		"typed.Value.Content = nil",
		"outcome = typed",
		"contract.CloneProcessSceneOutcome(outcome)",
		"sceneryruntime.NewContractStreamOutcome(cloned, stream)",
		"stream, err := streamed.TakeStream()",
		"sceneryruntime.EncodeContractByteStreamWithOptions(request, 200, stream, []string{\"model/gltf-binary\"}",
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("streaming adapter missing %q:\n%s", fragment, adapter)
		}
	}
	if strings.Contains(adapter, "EncodeContractRepresentationWithOptions(request, 200, typed.Value.Content") {
		t.Fatalf("streaming adapter buffered its response body:\n%s", adapter)
	}
}
