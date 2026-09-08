package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDashboardPostgresRPCsRequireDatabase(t *testing.T) {
	t.Parallel()

	server := newTestDashboardServer(t)
	params, _ := json.Marshal(map[string]any{"app_id": "missing"})
	if _, err := server.dispatchRPC(context.Background(), "postgres/tables", params); err == nil {
		t.Fatal("postgres/tables succeeded without a registered app")
	}
}
