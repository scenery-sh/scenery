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
	const expected = "sha256:c510c09edae970695642f4d6a805fcba8f6497c99c217486393968c41a1428dc"
	if string(revision) != expected {
		t.Fatalf("agent capabilities schema revision = %s, want %s", revision, expected)
	}
}
