package main

import (
	"context"
	"encoding/json"
	"testing"

	"scenery.sh/internal/devdash"
)

func TestDashboardRPCStoredRequestsCRUD(t *testing.T) {
	t.Parallel()

	server := newTestDashboardServer(t)
	ctx := context.Background()

	createID, err := dispatchStoredRequestRPC[string](ctx, server, "stored-requests/create", map[string]any{
		"app_id": "app-test",
		"input": map[string]any{
			"title":   "Tenant Config",
			"rpcName": "Config",
			"svcName": "tenants",
			"shared":  true,
			"data": map[string]any{
				"method":     "GET",
				"pathParams": map[string]any{"tenantID": "123"},
				"payload":    map[string]any{"include": "all"},
				"authPayload": map[string]any{
					"uid": "user_123",
				},
			},
			"authToken": "secret",
		},
	})
	if err != nil {
		t.Fatalf("create stored request rpc: %v", err)
	}
	if createID == "" {
		t.Fatal("expected stored request id")
	}

	list, err := dispatchStoredRequestRPC[[]devdash.StoredRequest](ctx, server, "stored-requests/list", map[string]any{
		"app_id": "app-test",
	})
	if err != nil {
		t.Fatalf("list stored requests rpc: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 stored request, got %d", len(list))
	}
	stored := list[0]
	if stored.Title != "Tenant Config" {
		t.Fatalf("unexpected stored request title: %q", stored.Title)
	}
	if stored.Data.Method != "GET" {
		t.Fatalf("unexpected stored request method: %q", stored.Data.Method)
	}
	if string(stored.Data.Payload) != `{"include":"all"}` {
		t.Fatalf("unexpected stored request payload: %s", stored.Data.Payload)
	}

	updatedID, err := dispatchStoredRequestRPC[string](ctx, server, "stored-requests/update", map[string]any{
		"app_id": "app-test",
		"id":     createID,
		"input": map[string]any{
			"title":   "Tenant Config Updated",
			"rpcName": "Config",
			"svcName": "tenants",
			"shared":  false,
			"data": map[string]any{
				"method":     "POST",
				"pathParams": map[string]any{"tenantID": "456"},
				"payload":    map[string]any{"include": "minimal"},
			},
		},
	})
	if err != nil {
		t.Fatalf("update stored request rpc: %v", err)
	}
	if updatedID != createID {
		t.Fatalf("unexpected updated id: %q", updatedID)
	}

	list, err = dispatchStoredRequestRPC[[]devdash.StoredRequest](ctx, server, "stored-requests/list", map[string]any{
		"app_id": "app-test",
	})
	if err != nil {
		t.Fatalf("list stored requests after update: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 stored request after update, got %d", len(list))
	}
	if list[0].Title != "Tenant Config Updated" {
		t.Fatalf("unexpected updated title: %q", list[0].Title)
	}
	if list[0].Shared {
		t.Fatal("expected updated shared flag to be false")
	}

	deleted, err := dispatchStoredRequestRPC[bool](ctx, server, "stored-requests/delete", map[string]any{
		"app_id": "app-test",
		"id":     createID,
	})
	if err != nil {
		t.Fatalf("delete stored request rpc: %v", err)
	}
	if !deleted {
		t.Fatal("expected delete response true")
	}

	list, err = dispatchStoredRequestRPC[[]devdash.StoredRequest](ctx, server, "stored-requests/list", map[string]any{
		"app_id": "app-test",
	})
	if err != nil {
		t.Fatalf("list stored requests after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 stored requests after delete, got %d", len(list))
	}
}

func dispatchStoredRequestRPC[T any](ctx context.Context, server *dashboardServer, method string, params map[string]any) (T, error) {
	var zero T
	raw, err := json.Marshal(params)
	if err != nil {
		return zero, err
	}
	result, err := server.dispatchRPC(ctx, method, raw)
	if err != nil {
		return zero, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return zero, err
	}
	if err := json.Unmarshal(encoded, &zero); err != nil {
		return zero, err
	}
	return zero, nil
}
