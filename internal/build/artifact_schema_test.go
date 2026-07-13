package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

func TestBuildArtifactSchemaRevisionsMatchCheckedSchemas(t *testing.T) {
	for _, test := range []struct {
		name     string
		revision machine.ExactSchemaRevision
	}{
		{"scenery.go-build-input-manifest.schema.json", buildInputSchemaDescriptor},
		{"scenery.runtime-bundle.schema.json", runtimeBundleSchemaDescriptor},
		{"scenery.build.latest.schema.json", latestBuildSchemaDescriptor},
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

func TestPrivateBuildArtifactDescriptorsCoverTypeShapes(t *testing.T) {
	assertBuildDescriptorFields(t, generatorFingerprintCacheSchemaDescriptor, generatorFingerprintCache{})
	assertBuildDescriptorFields(t, frameworkFingerprintCacheSchemaDescriptor, frameworkFingerprintCache{})
}

func assertBuildDescriptorFields(t *testing.T, descriptor string, value any) {
	t.Helper()
	var shape map[string]any
	if err := json.Unmarshal([]byte(descriptor), &shape); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(shape))
	for name := range shape {
		got = append(got, name)
	}
	slices.Sort(got)
	want := buildJSONFieldNames(reflect.TypeOf(value))
	if !slices.Equal(got, want) {
		t.Fatalf("descriptor fields = %v, want %v", got, want)
	}
}

func buildJSONFieldNames(value reflect.Type) []string {
	fields := []string{}
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		if field.Anonymous {
			fields = append(fields, buildJSONFieldNames(field.Type)...)
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	return fields
}
