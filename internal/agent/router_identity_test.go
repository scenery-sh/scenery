package agent

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/spec"
)

func TestLocalResponseSchemaRevisionsMatchCheckedSchemas(t *testing.T) {
	for kind, expected := range map[string]string{
		"scenery.local.health": "sha256:af2ff38e2a1d33b3657300d2a12b8d249a94cb13be536dec4680b030a5275569",
		"scenery.local.routes": "sha256:cd25c08078a7d79950f1bd1dbf06670499e2c4882ee28a0d0ae04cbfb96402c9",
	} {
		encoded, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", kind+".schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		revision, err := spec.SchemaDocumentRevision(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(revision) != expected {
			t.Fatalf("%s schema revision = %s, want %s", kind, revision, expected)
		}
	}
}
