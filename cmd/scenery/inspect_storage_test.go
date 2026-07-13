package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRunSceneryInspectStorage(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"id": "storage-id",
		"storage": {
			"cell_id": "onlv",
			"share": "worktree",
			"default": "app",
			"stores": {
				"logs": {
					"kind": "local",
					"access": "private"
				},
				"app": {
					"kind": "local",
					"access": "auth",
					"tenant_scoped": true,
					"max_object_bytes": 1048576
				}
			}
		}
	}`)
	var out bytes.Buffer
	if err := runSceneryInspect([]string{"storage", "--app-root", root, "-o", "json"}, &out); err != nil {
		t.Fatalf("runSceneryInspect(storage) error = %v", err)
	}
	var payload struct {
		Kind           string `json:"kind"`
		SchemaRevision string `json:"schema_revision"`
		Storage        struct {
			Configured bool   `json:"configured"`
			CellID     string `json:"storage_cell_id"`
			Share      string `json:"share"`
			Default    string `json:"default"`
			Readiness  string `json:"readiness"`
		} `json:"storage"`
		Stores []struct {
			Name           string `json:"name"`
			Kind           string `json:"kind"`
			Access         string `json:"access"`
			TenantScoped   bool   `json:"tenant_scoped"`
			MaxObjectBytes int64  `json:"max_object_bytes"`
			ObjectCount    int    `json:"object_count"`
			TotalBytes     int64  `json:"total_bytes"`
		} `json:"stores"`
	}
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON(storage) error = %v\n%s", err, out.String())
	}
	if payload.Kind != "scenery.storage.inspect" || payload.SchemaRevision != newCLIPayloadIdentity("scenery.storage.inspect").SchemaRevision {
		t.Fatalf("identity = %q %q", payload.Kind, payload.SchemaRevision)
	}
	if !payload.Storage.Configured || payload.Storage.CellID != "onlv" || payload.Storage.Share != "worktree" || payload.Storage.Default != "app" || payload.Storage.Readiness != "configured" {
		t.Fatalf("storage = %+v", payload.Storage)
	}
	if len(payload.Stores) != 2 || payload.Stores[0].Name != "app" || payload.Stores[1].Name != "logs" {
		t.Fatalf("stores = %+v", payload.Stores)
	}
	if payload.Stores[0].Kind != "local" || payload.Stores[0].Access != "auth" || !payload.Stores[0].TenantScoped || payload.Stores[0].MaxObjectBytes != 1048576 {
		t.Fatalf("store app = %+v", payload.Stores[0])
	}
}

func TestRunSceneryInspectStorageReportsLocalCellUsage(t *testing.T) {
	agentHome := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", agentHome)

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"id": "storage-id",
		"storage": {
			"cell_id": "onlv",
			"default": "app",
			"stores": {"app": {"kind": "local", "access": "auth"}}
		}
	}`)

	// Seed the cell with one object plus a Scenery-owned metadata sidecar that
	// must be excluded from the object count.
	appObjects := filepath.Join(agentHome, "agent", "storage", "onlv", "objects", "app")
	if err := os.MkdirAll(filepath.Join(appObjects, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appObjects, "reports", "report.txt"), []byte("storage report"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(appObjects, "__scenery", "metadata", "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appObjects, "__scenery", "metadata", "reports", "report.txt.json"), []byte(`{"content_type":"text/plain"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runSceneryInspect([]string{"storage", "--app-root", root, "-o", "json"}, &out); err != nil {
		t.Fatalf("runSceneryInspect(storage) error = %v", err)
	}
	var payload struct {
		Storage struct {
			Readiness string `json:"readiness"`
			Runtime   struct {
				CellRoot   string `json:"cell_root"`
				ObjectsDir string `json:"objects_dir"`
				Exists     bool   `json:"exists"`
			} `json:"runtime"`
		} `json:"storage"`
		Stores []struct {
			Name        string `json:"name"`
			ObjectCount int    `json:"object_count"`
			TotalBytes  int64  `json:"total_bytes"`
		} `json:"stores"`
	}
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON(storage) error = %v\n%s", err, out.String())
	}
	if payload.Storage.Readiness != "ready" {
		t.Fatalf("readiness = %q, payload = %s", payload.Storage.Readiness, out.String())
	}
	if !payload.Storage.Runtime.Exists || payload.Storage.Runtime.CellRoot == "" || payload.Storage.Runtime.ObjectsDir == "" {
		t.Fatalf("runtime = %+v", payload.Storage.Runtime)
	}
	if len(payload.Stores) != 1 || payload.Stores[0].Name != "app" {
		t.Fatalf("stores = %+v", payload.Stores)
	}
	if payload.Stores[0].ObjectCount != 1 || payload.Stores[0].TotalBytes != int64(len("storage report")) {
		t.Fatalf("store usage = %+v", payload.Stores[0])
	}
}
