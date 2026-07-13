package generate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestOpenAPIArtifactDescriptorCoversProducedShape(t *testing.T) {
	var shape map[string]any
	if err := json.Unmarshal([]byte(openAPISchemaDocument), &shape); err != nil {
		t.Fatal(err)
	}
	properties, _ := shape["properties"].(map[string]any)
	got := make([]string, 0, len(properties))
	for name := range properties {
		got = append(got, name)
	}
	slices.Sort(got)
	want := []string{"content_digest", "contract_revision", "gateway", "generator", "http_surface_revision", "kind", "openapi_revision", "openapi_version", "producer", "schema_revision", "spec_revision"}
	if !slices.Equal(got, want) {
		t.Fatalf("descriptor fields = %v, want %v", got, want)
	}
	revision, err := spec.SchemaDocumentRevision([]byte(openAPISchemaDocument))
	if err != nil {
		t.Fatal(err)
	}
	if string(revision) != string(openAPISchemaDescriptor) {
		t.Fatalf("OpenAPI schema revision = %s, want %s", openAPISchemaDescriptor, revision)
	}
}
