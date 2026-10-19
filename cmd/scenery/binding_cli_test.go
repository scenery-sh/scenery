package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"scenery.sh/internal/graph"
)

func TestBuildCLIInputAcceptsCanonicalSingletonBlocks(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	mapping := map[string]any{"stdout": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.processed.status"}}}
	got, err := selectCLIOutput(json.RawMessage(`{"status":"complete"}`), "result.processed", mapping)
	if err != nil {
		t.Fatal(err)
	}
	if got != "complete" {
		t.Fatalf("output = %#v, want complete", got)
	}
}

func TestBindingHelpDescribesTheParserGrammarInTheHelpSchema(t *testing.T) {
	operation := graph.Resource{
		Address: "house/operation/process_scene", Kind: "scenery.operation", Name: "process_scene", Module: "house",
		Spec: map[string]any{"input": map[string]any{"$ref": "record.process_scene_input"}},
	}
	record := graph.Resource{
		Address: "house/record/process_scene_input", Kind: "scenery.record", Name: "process_scene_input", Module: "house",
		Spec: map[string]any{"field": []any{
			map[string]any{"name": "scene_id", "type": map[string]any{"$ref": "string"}},
			map[string]any{"name": "dry_run", "type": map[string]any{"$expression": "bool"}},
		}},
	}
	binding := graph.Resource{
		Address: "house/binding/process_scene_cli", Kind: "scenery.binding", Name: "process_scene_cli", Module: "house",
		Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.process_scene"},
			"cli": map[string]any{
				"command":  []any{"scene", "process"},
				"argument": map[string]any{"name": "scene_id", "required": true, "position": map[string]any{"$scalar": "int", "value": "0"}, "to": map[string]any{"$ref": "operation.process_scene.input.scene_id"}},
				"flag":     map[string]any{"name": "dry-run", "short": "n", "to": map[string]any{"$ref": "operation.process_scene.input.dry_run"}},
				"outcome": []any{
					map[string]any{"when": map[string]any{"$ref": "result.processed"}, "exit": map[string]any{"$scalar": "int", "value": "0"}, "stdout": map[string]any{"from": map[string]any{"$ref": "result.processed"}}},
					map[string]any{"when": map[string]any{"$ref": "error.missing"}, "exit": map[string]any{"$scalar": "int", "value": "3"}, "stderr": map[string]any{"from": map[string]any{"$ref": "error.missing"}}},
				},
			},
		},
	}
	entry := bindingHelpEntry([]graph.Resource{operation, record}, binding)
	if entry.Command != "scene process" || entry.Usage[0] != "scenery scene process <scene_id> [--dry-run] [--live] [-o human|json]" {
		t.Fatalf("binding help = %#v", entry)
	}
	described := entry.ApplicationBinding
	if described == nil || described.Operation != operation.Address || len(described.Arguments) != 1 || described.Arguments[0].Type != "string" ||
		len(described.Flags) != 1 || described.Flags[0].Short != "n" || described.Flags[0].Type != "bool" || len(described.Outcomes) != 2 || described.Outcomes[1] != (helpBindingOutcome{When: "error.missing", Exit: 3, Stream: "stderr"}) {
		t.Fatalf("binding descriptor = %#v", described)
	}
	// The grammar the descriptor advertises is the one the parser accepts.
	if _, err := buildCLIInput([]graph.Resource{operation, record}, binding, []string{"scene-1", "--dry-run"}); err != nil {
		t.Fatalf("advertised invocation rejected: %v", err)
	}
	var output bytes.Buffer
	if err := writeScopedHelpJSON(&output, entry); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.help.schema.json")
	var payload helpManifest
	if err := decodeCLIJSON(output.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, payload); len(diagnostics) != 0 {
		t.Fatalf("binding help does not match the help schema: %v", diagnostics)
	}
}
