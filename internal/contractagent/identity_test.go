package contractagent

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/spec"
)

func TestAgentCapabilitiesSchemaRevisionMatchesCheckedSchema(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", "scenery.agent.capabilities.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := spec.SchemaDocumentRevision(encoded)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "sha256:2039fe024772db03d46bb431ccb58381ea24a7452c58fc999db08824f90415c8"
	if string(revision) != expected {
		t.Fatalf("agent capabilities schema revision = %s, want %s", revision, expected)
	}
}
