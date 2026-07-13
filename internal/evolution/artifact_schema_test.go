package evolution

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

func TestEvolutionArtifactSchemaRevisionsMatchCheckedSchemas(t *testing.T) {
	for _, test := range []struct {
		name     string
		revision machine.ExactSchemaRevision
	}{
		{"scenery.change-plan.schema.json", changePlanSchemaDescriptor},
		{"scenery.change-receipt.schema.json", changeReceiptSchemaDescriptor},
		{"scenery.approval-trust.schema.json", approvalTrustSchemaDescriptor},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertCheckedArtifactSchema(t, test.name, test.revision)
		})
	}
}

func TestPrivateEvolutionArtifactDescriptorsCoverTypeShapes(t *testing.T) {
	assertDescriptorFields(t, changeTransactionSchemaDescriptor, changeTransactionJournal{})
	assertDescriptorFields(t, changeTransactionLockDescriptor, changeTransactionLock{})
	assertDescriptorFields(t, semanticDiffSchemaDescriptor, SemanticDiff{})
}

func assertDescriptorFields(t *testing.T, descriptor string, value any) {
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
	want := jsonFieldNames(reflect.TypeOf(value))
	if !slices.Equal(got, want) {
		t.Fatalf("descriptor fields = %v, want %v", got, want)
	}
}

func jsonFieldNames(value reflect.Type) []string {
	fields := []string{}
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		if field.Anonymous {
			fields = append(fields, jsonFieldNames(field.Type)...)
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

func assertCheckedArtifactSchema(t *testing.T, name string, revision machine.ExactSchemaRevision) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", name))
	if err != nil {
		t.Fatal(err)
	}
	want, err := spec.SchemaDocumentRevision(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(revision) != string(want) {
		t.Fatalf("schema revision = %s, want %s", revision, want)
	}
}
