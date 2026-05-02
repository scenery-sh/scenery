package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"onlava.com/internal/app"
	"onlava.com/internal/devdash"
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

func TestDashboardPubSubStatusRPCAndReport(t *testing.T) {
	server := newTestDashboardServer(t)
	if err := server.supervisor.store.UpsertPubSubSnapshot(context.Background(), devdash.PubSubSnapshot{
		AppID:     "app-test",
		Topics:    json.RawMessage(`[{"name":"old-events","published":1,"pending":0,"subscriptions":[]}]`),
		UpdatedAt: time.Now().UTC().Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("seed old pubsub snapshot: %v", err)
	}
	body := []byte(`{"type":"pubsub","app_id":"app-test","pubsub":[{"name":"events","published":1,"pending":0,"subscriptions":[]}]}`)
	req := httptest.NewRequest(http.MethodPost, devdash.ReportPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+server.supervisor.reportToken)
	rec := httptest.NewRecorder()

	server.handleReport(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("handleReport status = %d body=%s", rec.Code, rec.Body.String())
	}

	raw, err := json.Marshal(map[string]any{"app_id": "app-test", "period": "5m"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := server.dispatchRPC(context.Background(), "pubsub/status", raw)
	if err != nil {
		t.Fatalf("pubsub/status: %v", err)
	}
	payload := result.(map[string]any)
	topicsRaw, ok := payload["topics"].(json.RawMessage)
	if !ok {
		t.Fatalf("topics type = %T", payload["topics"])
	}
	if !bytes.Contains(topicsRaw, []byte(`"events"`)) {
		t.Fatalf("unexpected topics: %s", topicsRaw)
	}
	history, ok := payload["history"].([]map[string]any)
	if !ok || len(history) != 1 {
		t.Fatalf("history = %#v", payload["history"])
	}
	raw, err = json.Marshal(map[string]any{"app_id": "app-test", "period": "15m"})
	if err != nil {
		t.Fatal(err)
	}
	result, err = server.dispatchRPC(context.Background(), "pubsub/status", raw)
	if err != nil {
		t.Fatalf("pubsub/status 15m: %v", err)
	}
	payload = result.(map[string]any)
	history, ok = payload["history"].([]map[string]any)
	if !ok || len(history) != 2 {
		t.Fatalf("15m history = %#v", payload["history"])
	}
}

func TestDashboardPubSubMessagesRPCAndReport(t *testing.T) {
	server := newTestDashboardServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	body := []byte(fmt.Sprintf(`{"type":"pubsub-message","app_id":"app-test","pubsub_message":{"message_id":"stream:1","topic_name":"events","subscription_name":"events-sub","service_name":"events","status":"completed","trace_id":"trace-1","attempt":1,"payload":{"value":"ok"},"result":{"status":"completed"},"deliveries":1,"inserted_at":"%s","picked_up_at":"%s","finished_at":"%s","duration_ms":1000}}`, now, now, now))
	req := httptest.NewRequest(http.MethodPost, devdash.ReportPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+server.supervisor.reportToken)
	rec := httptest.NewRecorder()

	server.handleReport(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("handleReport status = %d body=%s", rec.Code, rec.Body.String())
	}

	raw, err := json.Marshal(map[string]any{
		"app_id":     "app-test",
		"period":     "15m",
		"queue_name": "events-sub",
		"status":     "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := server.dispatchRPC(context.Background(), "pubsub/messages", raw)
	if err != nil {
		t.Fatalf("pubsub/messages: %v", err)
	}
	payload := result.(map[string]any)
	messages, ok := payload["messages"].([]devdash.PubSubMessage)
	if !ok {
		t.Fatalf("messages type = %T", payload["messages"])
	}
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	if messages[0].SubscriptionName != "events-sub" {
		t.Fatalf("subscription = %q", messages[0].SubscriptionName)
	}
	if messages[0].Status != "completed" {
		t.Fatalf("status = %q", messages[0].Status)
	}
	attemptResult, err := server.dispatchRPC(context.Background(), "pubsub/message/attempts", mustJSON(t, map[string]any{
		"app_id":            "app-test",
		"message_id":        "stream:1",
		"subscription_name": "events-sub",
	}))
	if err != nil {
		t.Fatalf("pubsub/message/attempts: %v", err)
	}
	attemptPayload := attemptResult.(map[string]any)
	attempts, ok := attemptPayload["attempts"].([]devdash.PubSubMessageAttempt)
	if !ok || len(attempts) != 1 {
		t.Fatalf("attempts = %#v", attemptPayload["attempts"])
	}
	if attempts[0].TraceID != "trace-1" {
		t.Fatalf("attempt trace id = %q", attempts[0].TraceID)
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

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}
