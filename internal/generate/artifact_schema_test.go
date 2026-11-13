package generate

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

func TestGeneratedArtifactSchemaRevisionsMatchCheckedSchemas(t *testing.T) {
	for _, test := range []struct {
		name     string
		revision machine.ExactSchemaRevision
	}{
		{"scenery.generated.schema.json", goApplicationSchemaDescriptor},
		{"scenery.package-generated.schema.json", goPackageSchemaDescriptor},
		{"scenery.typescript-client-generated.schema.json", typeScriptSchemaDescriptor},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", test.name))
			if err != nil {
				t.Fatal(err)
			}
			want, err := spec.SchemaDocumentRevision(data)
			if err != nil {
				t.Fatal(err)
			}
			if string(test.revision) != string(want) {
				t.Fatalf("schema revision = %s, want %s", test.revision, want)
			}
		})
	}
}
