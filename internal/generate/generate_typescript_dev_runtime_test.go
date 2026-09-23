package generate

import (
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/machine"
)

func TestTypeScriptDevRuntimeClientIsOptInAndPinsTheStatusRevision(t *testing.T) {
	t.Parallel()

	if typeScriptDevRuntimeEnabled(Resource{Spec: map[string]any{}}) {
		t.Fatal("a target without dev_runtime enabled the development runtime client")
	}
	if !typeScriptDevRuntimeEnabled(Resource{Spec: map[string]any{"dev_runtime": true}}) {
		t.Fatal("dev_runtime = true did not enable the development runtime client")
	}
	file := renderTypeScriptDevRuntimeFile("clients/public")
	if file.Path != filepath.Join("clients/public", "dev-runtime.ts") {
		t.Fatalf("path = %q", file.Path)
	}
	revision, _ := machine.PayloadSchemaRevision("scenery.dev-runtime.status")
	source := string(file.Bytes)
	if strings.Contains(source, devRuntimeStatusRevisionPlaceholder) || !strings.Contains(source, `DEV_RUNTIME_STATUS_SCHEMA_REVISION = "`+revision+`"`) {
		t.Fatalf("generated client does not pin the status schema revision %s", revision)
	}
	// A runtime without this status kind is an older Scenery: the remedy is a
	// restart, not regenerating the client against that runtime.
	for _, message := range []string{"does not serve ${DEV_RUNTIME_STATUS_KIND}; restart scenery up", "restart scenery up if the app's Scenery version changed"} {
		if !strings.Contains(source, message) {
			t.Errorf("generated client does not explain status mismatches with %q", message)
		}
	}
	for _, method := range []string{`"status"`, `"postgres/tables"`, `"postgres/schema"`, `"postgres/rows"`, `"db/query"`, `"storage/inspect"`, `"storage/list"`, `"storage/stat"`, `"storage/delete"`, `"storage/delete-preview"`, `"storage/delete-selection"`, `"/runtime/storage"`} {
		if !strings.Contains(source, method) {
			t.Errorf("generated client does not call %s", method)
		}
	}
}
