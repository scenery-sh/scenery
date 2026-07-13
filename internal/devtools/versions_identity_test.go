package devtools

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/spec"
)

func TestPinnedVersionsSchemaRevisionMatchesCheckedSchema(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", pinnedVersionsKind+".schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := spec.SchemaDocumentRevision(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(revision) != pinnedVersionsSchemaRevision {
		t.Fatalf("pinned versions schema revision = %s, want %s", revision, pinnedVersionsSchemaRevision)
	}
}
