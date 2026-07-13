package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"scenery.sh/internal/graph"
)

func TestBuildCLIInputAcceptsCanonicalSingletonBlocks(t *testing.T) {
	operation := graph.Resource{
		Address: "house/operation/process_scene", Kind: "scenery.operation", Name: "process_scene", Module: "house",
		Spec: map[string]any{"input": map[string]any{"$ref": "record.process_scene_input"}},
	}
	record := graph.Resource{
		Address: "house/record/process_scene_input", Kind: "scenery.record", Name: "process_scene_input", Module: "house",
		Spec: map[string]any{"field": map[string]any{"name": "scene_id", "type": map[string]any{"$ref": "string"}}},
	}
	binding := graph.Resource{
		Address: "house/binding/process_scene_cli", Kind: "scenery.binding", Name: "process_scene_cli", Module: "house",
		Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.process_scene"},
			"cli": map[string]any{"argument": map[string]any{
				"name": "scene_id", "position": map[string]any{"$scalar": "int", "value": "0"}, "to": map[string]any{"$ref": "operation.process_scene.input.scene_id"},
			}},
		},
	}

	got, err := buildCLIInput([]graph.Resource{operation, record}, binding, []string{"scene-42"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"scene_id": "scene-42"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("input = %#v, want %#v", got, want)
	}
}

func TestSelectCLIOutputUsesOutcomeRelativePath(t *testing.T) {
	mapping := map[string]any{"stdout": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.processed.status"}}}
	got, err := selectCLIOutput(json.RawMessage(`{"status":"complete"}`), "result.processed", mapping)
	if err != nil {
		t.Fatal(err)
	}
	if got != "complete" {
		t.Fatalf("output = %#v, want complete", got)
	}
}
