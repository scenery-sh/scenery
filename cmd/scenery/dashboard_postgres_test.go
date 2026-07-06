package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDashboardPostgresRPCsRequireDatabase(t *testing.T) {
	server := newTestDashboardServer(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"postgres-test","dev":{"services":{"main":{}}}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	params, _ := json.Marshal(map[string]any{"app_id": "missing"})
	if _, err := server.dispatchRPC(context.Background(), "postgres/tables", params); err == nil {
		t.Fatal("postgres/tables succeeded without a registered app")
	}
}
