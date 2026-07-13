package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/spec"
)

func TestCLIPayloadSchemaRevisionsMatchCheckedSchemas(t *testing.T) {
	for kind, expected := range cliPayloadSchemaRevisions {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join("..", "..", "docs", "schemas", kind+".schema.json")
			encoded, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := spec.SchemaDocumentRevision(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if string(actual) != expected {
				t.Fatalf("schema revision = %s, want %s; refresh the static CLI payload revision", actual, expected)
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(encoded, &schema); err != nil {
				t.Fatal(err)
			}
			var kindSchema, revisionSchema struct {
				Const string `json:"const"`
			}
			if err := json.Unmarshal(schema.Properties["kind"], &kindSchema); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(schema.Properties["schema_revision"], &revisionSchema); err != nil {
				t.Fatal(err)
			}
			if kindSchema.Const != kind || revisionSchema.Const != expected {
				t.Fatalf("checked schema identity does not match %s at %s", kind, expected)
			}
		})
	}
}
