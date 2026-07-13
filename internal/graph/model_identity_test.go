package graph

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/spec"
)

func TestManifestSchemaRevisionMatchesCheckedSchema(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", "scenery.manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := spec.SchemaDocumentRevision(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(revision) != ManifestSchemaRevision {
		t.Fatalf("manifest schema revision = %s, want %s", revision, ManifestSchemaRevision)
	}
}
