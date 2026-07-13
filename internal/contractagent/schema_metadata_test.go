package contractagent

import (
	"slices"
	"strings"
	"testing"
)

func TestAgentSchemasReportRuntimeExpressionPhases(t *testing.T) {
	for _, test := range []struct{ revision, field string }{
		{"scenery.authorization.rule", "allow"},
		{"scenery.record.validation", "when"},
		{"scenery.operation.idempotency", "key"},
		{"scenery.binding.http.response-body", "from"},
		{"scenery.binding.cli.outcome", "when"},
		{"scenery.event-emission.from", "payload"},
	} {
		t.Run(test.revision+"/"+test.field, func(t *testing.T) {
			if field := agentSchemaField(t, test.revision, test.field); field["phase"] != "runtime" {
				t.Fatalf("phase = %#v, want runtime", field["phase"])
			}
		})
	}
}

func TestAgentSchemaReportsOrderedCompositeIdempotencyKey(t *testing.T) {
	field := agentSchemaField(t, "scenery.operation.idempotency", "key")
	typeDefinition, _ := field["type"].(map[string]any)
	items, _ := typeDefinition["items"].(map[string]any)
	constraints, _ := field["constraints"].(map[string]any)
	schema, _ := AgentSchema(sourceSchemaRevisionForInternalName("scenery.operation.idempotency"))
	conditional, _ := schema["conditional_requirements"].([]any)
	if field["phase"] != "runtime" || field["ordered"] != true || typeDefinition["collection"] != "list" || items["typed_reference"] != "schema_path" || constraints["min_items"] != 1 || len(conditional) != 1 {
		t.Fatalf("idempotency key metadata = %#v", field)
	}
}

func TestAgentSchemasExposeCompilerEnums(t *testing.T) {
	for _, test := range []struct {
		revision, field string
		values          []string
	}{
		{"scenery.source.execution", "mode", []string{"direct", "durable", "workflow"}},
		{"scenery.source.binding", "protocol", []string{"cli", "event", "http", "internal"}},
		{"scenery.source.fixture", "mode", []string{"insert", "replace", "upsert"}},
		{"scenery.source.typescript_client", "runtime", []string{"fetch"}},
	} {
		t.Run(test.revision+"/"+test.field, func(t *testing.T) {
			field := agentSchemaField(t, test.revision, test.field)
			constraints, _ := field["constraints"].(map[string]any)
			values, _ := constraints["enum"].([]string)
			if !slices.Equal(values, test.values) {
				t.Fatalf("enum = %#v, want %#v", values, test.values)
			}
		})
	}
}

func agentSchemaField(t *testing.T, revision, name string) map[string]any {
	t.Helper()
	if strings.HasPrefix(revision, "scenery.") {
		revision = sourceSchemaRevisionForInternalName(revision)
	}
	schema, ok := AgentSchema(revision)
	if !ok {
		t.Fatalf("schema %s is unavailable", revision)
	}
	fields, _ := schema["fields"].(map[string]any)
	field, _ := fields[name].(map[string]any)
	if field == nil {
		t.Fatalf("schema %s has no field %s", revision, name)
	}
	return field
}
