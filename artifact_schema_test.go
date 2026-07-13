package scenery

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

func TestApprovalTokenSchemaRevisionMatchesCheckedSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("docs", "schemas", "scenery.approval-token.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := spec.SchemaDocumentRevision(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := machine.ArtifactSchemaRevision(approvalTokenSchemaRevision); got != string(revision) {
		t.Fatalf("approval token schema revision = %s, want %s", got, revision)
	}
}
