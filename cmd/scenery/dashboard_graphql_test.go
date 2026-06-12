package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
)

func TestDashboardGraphQLStoredRequestsCRUD(t *testing.T) {
	t.Parallel()

	server := newTestDashboardServer(t)

	createResp := postGraphQL(t, server, map[string]any{
		"operationName": "createStoredRequest",
		"variables": map[string]any{
			"appSlug": "app-test",
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
		},
	})
	createData := createResp["data"].(map[string]any)
	createApp := createData["app"].(map[string]any)
	createStoredRequest := createApp["createStoredRequest"].(map[string]any)
	storedRequestID, _ := createStoredRequest["id"].(string)
	if storedRequestID == "" {
		t.Fatal("expected stored request id from create mutation")
	}

	listResp := postGraphQL(t, server, map[string]any{
		"operationName": "getStoredRequests",
		"variables": map[string]any{
			"appSlug": "app-test",
		},
	})
	listData := listResp["data"].(map[string]any)
	listApp := listData["app"].(map[string]any)
	storedRequests := listApp["storedRequests"].([]any)
	if len(storedRequests) != 1 {
		t.Fatalf("expected 1 stored request, got %d", len(storedRequests))
	}
	stored := storedRequests[0].(map[string]any)
	if stored["title"] != "Tenant Config" {
		t.Fatalf("unexpected stored request title: %v", stored["title"])
	}
	data := stored["data"].(map[string]any)
	if _, ok := data["authPayload"]; ok {
		t.Fatal("auth payload should not be persisted in stored request data")
	}
	if _, ok := stored["authToken"]; ok {
		t.Fatal("auth token should not be persisted in stored request")
	}

	updateResp := postGraphQL(t, server, map[string]any{
		"operationName": "updateStoredRequest",
		"variables": map[string]any{
			"appSlug":         "app-test",
			"storedRequestID": storedRequestID,
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
		},
	})
	updateData := updateResp["data"].(map[string]any)
	updateApp := updateData["app"].(map[string]any)
	updateStored := updateApp["storedRequest"].(map[string]any)
	updated := updateStored["update"].(map[string]any)
	if updated["id"] != storedRequestID {
		t.Fatalf("unexpected updated id: %v", updated["id"])
	}

	listResp = postGraphQL(t, server, map[string]any{
		"operationName": "getStoredRequests",
		"variables": map[string]any{
			"appSlug": "app-test",
		},
	})
	listData = listResp["data"].(map[string]any)
	listApp = listData["app"].(map[string]any)
	storedRequests = listApp["storedRequests"].([]any)
	stored = storedRequests[0].(map[string]any)
	if stored["title"] != "Tenant Config Updated" {
		t.Fatalf("unexpected updated title from list: %v", stored["title"])
	}
	if stored["shared"] != false {
		t.Fatalf("unexpected shared flag from list: %v", stored["shared"])
	}

	deleteResp := postGraphQL(t, server, map[string]any{
		"operationName": "deleteStoredRequest",
		"variables": map[string]any{
			"appSlug":         "app-test",
			"storedRequestID": storedRequestID,
		},
	})
	deleteData := deleteResp["data"].(map[string]any)
	deleteApp := deleteData["app"].(map[string]any)
	deleteStored := deleteApp["storedRequest"].(map[string]any)
	if deleteStored["delete"] != true {
		t.Fatalf("unexpected delete response: %v", deleteStored["delete"])
	}

	listResp = postGraphQL(t, server, map[string]any{
		"operationName": "getStoredRequests",
		"variables": map[string]any{
			"appSlug": "app-test",
		},
	})
	listData = listResp["data"].(map[string]any)
	listApp = listData["app"].(map[string]any)
	storedRequests = listApp["storedRequests"].([]any)
	if len(storedRequests) != 0 {
		t.Fatalf("expected 0 stored requests after delete, got %d", len(storedRequests))
	}
}

func TestDashboardGraphQLUnsupportedOperation(t *testing.T) {
	t.Parallel()

	server := newTestDashboardServer(t)
	body, err := json.Marshal(map[string]any{
		"operationName": "unsupportedOperation",
		"variables":     map[string]any{},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/__graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleGraphQL(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	errorsValue, ok := resp["errors"].([]any)
	if !ok || len(errorsValue) != 1 {
		t.Fatalf("expected one graphql error, got %#v", resp["errors"])
	}
}

func newTestDashboardServer(t *testing.T) *dashboardServer {
	t.Helper()

	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	supervisor := &devSupervisor{
		cfg:         app.Config{Name: "app-test"},
		store:       store,
		reportToken: "test-token",
	}
	return newDashboardServer(supervisor, "")
}

func postGraphQL(t *testing.T, server *dashboardServer, body map[string]any) map[string]any {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal graphql request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/__graphql", bytes.NewReader(encoded))
	req = req.WithContext(context.Background())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleGraphQL(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode graphql response: %v", err)
	}
	return resp
}
