package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestRunSceneryInspectStorage(t *testing.T) {
	t.Parallel()

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
					"kind": "zerofs",
					"access": "private"
				},
				"app": {
					"kind": "zerofs",
					"access": "auth",
					"tenant_scoped": true,
					"max_object_bytes": 1048576
				}
			}
		},
		"dev": {
			"services": {
				"storage": {
					"kind": "zerofs",
					"mode": "local",
					"route": "storage",
					"image": "zerofs:dev",
					"env": {
						"ZEROFS_STORAGE_URL": "s3://secret-bucket",
						"AWS_SECRET_ACCESS_KEY": "secret"
					}
				}
			}
		}
	}`)
	var out bytes.Buffer
	if err := runSceneryInspect([]string{"storage", "--app-root", root, "--json"}, &out); err != nil {
		t.Fatalf("runSceneryInspect(storage) error = %v", err)
	}
	var payload struct {
		SchemaVersion string `json:"schema_version"`
		Storage       struct {
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
		} `json:"stores"`
		DevService struct {
			Name  string            `json:"name"`
			Kind  string            `json:"kind"`
			Route string            `json:"route"`
			Image string            `json:"image"`
			Env   map[string]string `json:"env"`
		} `json:"dev_service"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(storage) error = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "scenery.storage.inspect.v1" {
		t.Fatalf("schema_version = %q", payload.SchemaVersion)
	}
	if !payload.Storage.Configured || payload.Storage.CellID != "onlv" || payload.Storage.Share != "worktree" || payload.Storage.Default != "app" || payload.Storage.Readiness != "configured" {
		t.Fatalf("storage = %+v", payload.Storage)
	}
	if len(payload.Stores) != 2 || payload.Stores[0].Name != "app" || payload.Stores[1].Name != "logs" {
		t.Fatalf("stores = %+v", payload.Stores)
	}
	if payload.Stores[0].Kind != "zerofs" || payload.Stores[0].Access != "auth" || !payload.Stores[0].TenantScoped || payload.Stores[0].MaxObjectBytes != 1048576 {
		t.Fatalf("store app = %+v", payload.Stores[0])
	}
	if payload.DevService.Name != "storage" || payload.DevService.Kind != "zerofs" || payload.DevService.Route != "storage" || payload.DevService.Image != "zerofs:dev" {
		t.Fatalf("dev service = %+v", payload.DevService)
	}
	if payload.DevService.Env["AWS_SECRET_ACCESS_KEY"] != "<redacted>" || payload.DevService.Env["ZEROFS_STORAGE_URL"] != "<redacted>" {
		t.Fatalf("env was not redacted: %+v", payload.DevService.Env)
	}
}

func TestRunSceneryInspectStorageReportsAgentRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)
	defer func() {
		cancel()
		waitForTestAgentServer(t, agentDone)
	}()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "storageapp",
		"id": "storage-id",
		"storage": {
			"cell_id": "onlv",
			"default": "app",
			"stores": {"app": {"kind": "zerofs", "access": "auth"}}
		},
		"dev": {"services": {"storage": {"kind": "zerofs", "route": "storage"}}}
	}`)
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     managedZeroFSSubstrateKind("onlv"),
		Status:   "running",
		OwnerPID: os.Getpid(),
		URLs: map[string]string{
			"webui": "https://storage.dev.local/",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "storage-id",
		AppRoot:   root,
		SessionID: "storage-session",
		Status:    "running",
		OwnerPID:  os.Getpid(),
		Owner:     localagent.CaptureOwner(os.Getpid(), "test"),
		Backends: map[string]localagent.Backend{
			"storage": {Network: "tcp", Addr: "127.0.0.1:49152"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runSceneryInspect([]string{"storage", "--app-root", root, "--json"}, &out); err != nil {
		t.Fatalf("runSceneryInspect(storage) error = %v", err)
	}
	var payload struct {
		Storage struct {
			Readiness string `json:"readiness"`
			Runtime   struct {
				SubstrateKind   string `json:"substrate_kind"`
				SubstrateStatus string `json:"substrate_status"`
				Route           string `json:"route"`
				WebUIURL        string `json:"webui_url"`
				Attached        bool   `json:"attached"`
			} `json:"runtime"`
		} `json:"storage"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(storage) error = %v\n%s", err, out.String())
	}
	if payload.Storage.Readiness != "ready" {
		t.Fatalf("readiness = %q, payload = %s", payload.Storage.Readiness, out.String())
	}
	if payload.Storage.Runtime.SubstrateKind != managedZeroFSSubstrateKind("onlv") || payload.Storage.Runtime.SubstrateStatus != "running" || payload.Storage.Runtime.Route != "storage" || !payload.Storage.Runtime.Attached || payload.Storage.Runtime.WebUIURL == "" {
		t.Fatalf("runtime = %+v", payload.Storage.Runtime)
	}
}
