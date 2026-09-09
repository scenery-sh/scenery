package main

import (
	"bytes"
	"errors"
	"os"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestRunSceneryInspectStorageIsReadOnlyWithOptionalTotals(t *testing.T) {
	home := t.TempDir()
	_ = isolateCommandAgentHomeAt(t, home)
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"storageapp","id":"storage-id","storage":{"default":"app","stores":{"logs":{"kind":"local","access":"private"},"app":{"kind":"local","tenant_scoped":true,"max_object_bytes":1048576}}}}`)
	for _, stats := range []bool{false, true} {
		var out bytes.Buffer
		args := []string{"storage", "--app-root", root, "-o", "json"}
		if stats {
			args = append(args, "--stats")
		}
		if err := runSceneryInspect(args, &out); err != nil {
			t.Fatal(err)
		}
		var payload inspectStorageResponse
		if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Storage.Readiness != "uninitialized" || payload.Storage.Scope.AppID != "storage-id" || payload.Storage.Scope.Incarnation != nil || len(payload.Stores) != 2 || payload.Stores[0].Name != "app" {
			t.Fatalf("bad inspect: %+v", payload)
		}
		if (payload.Storage.Totals != nil) != stats || (payload.Stores[0].ObjectCount != nil) != stats {
			t.Fatal("invented or omitted totals")
		}
		if !payload.Stores[0].TenantScoped || payload.Stores[0].MaxObjectBytes != 1048576 {
			t.Fatal("lost store policy")
		}
	}
	plan, err := resolveStorageNamespacePlan(appcfg.Config{Name: "storageapp", ID: "storage-id"}, root, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plan.Worktree.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspect allocated state: %v", err)
	}
}
