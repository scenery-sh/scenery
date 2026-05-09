package devdash

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestOpenStoreConfiguresSQLiteForConcurrentDevReaders(t *testing.T) {
	t.Parallel()

	cacheRoot := t.TempDir()
	store, err := OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	db, err := sql.Open("sqlite", storeSQLiteDSN(filepath.Join(cacheRoot, "dev.db")))
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	var journalMode string
	if err := db.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := db.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != sqliteBusyTimeoutMS {
		t.Fatalf("busy_timeout = %d, want %d", busyTimeout, sqliteBusyTimeoutMS)
	}
}

func TestStoreStoredRequestsCRUD(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	created, err := store.CreateStoredRequest(ctx, StoredRequest{
		AppID:  "app-test",
		Title:  "Initial",
		RPC:    "Config",
		Svc:    "tenants",
		Shared: true,
		Data: StoredRequestData{
			Method:     "GET",
			PathParams: json.RawMessage(`{"tenantID":"123"}`),
			Payload:    json.RawMessage(`{"ok":true}`),
		},
	})
	if err != nil {
		t.Fatalf("create stored request: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected stored request id")
	}

	list, err := store.ListStoredRequests(ctx, "app-test")
	if err != nil {
		t.Fatalf("list stored requests: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 stored request, got %d", len(list))
	}
	if got := list[0].Data.PathParams; string(got) != `{"tenantID":"123"}` {
		t.Fatalf("unexpected path params: %s", got)
	}

	updated, err := store.UpdateStoredRequest(ctx, StoredRequest{
		ID:     created.ID,
		AppID:  "app-test",
		Title:  "Updated",
		RPC:    "Config",
		Svc:    "tenants",
		Shared: false,
		Data: StoredRequestData{
			Method:     "POST",
			PathParams: json.RawMessage(`{"tenantID":"456"}`),
			Payload:    json.RawMessage(`{"ok":false}`),
		},
	})
	if err != nil {
		t.Fatalf("update stored request: %v", err)
	}
	if updated.Title != "Updated" {
		t.Fatalf("unexpected updated title: %q", updated.Title)
	}

	list, err = store.ListStoredRequests(ctx, "app-test")
	if err != nil {
		t.Fatalf("list after update: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 stored request after update, got %d", len(list))
	}
	if list[0].Shared {
		t.Fatal("expected shared=false after update")
	}
	if got := list[0].Data.Payload; string(got) != `{"ok":false}` {
		t.Fatalf("unexpected payload after update: %s", got)
	}

	if err := store.DeleteStoredRequest(ctx, "app-test", created.ID); err != nil {
		t.Fatalf("delete stored request: %v", err)
	}
	list, err = store.ListStoredRequests(ctx, "app-test")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 stored requests after delete, got %d", len(list))
	}
}

func TestStorePubSubSnapshot(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	empty, err := store.GetPubSubSnapshot(ctx, "app-test")
	if err != nil {
		t.Fatalf("get empty pubsub snapshot: %v", err)
	}
	if got := string(empty.Topics); got != `[]` {
		t.Fatalf("empty topics = %s, want []", got)
	}

	if err := store.UpsertPubSubSnapshot(ctx, PubSubSnapshot{
		AppID:  "app-test",
		Topics: json.RawMessage(`[{"name":"events","pending":2}]`),
	}); err != nil {
		t.Fatalf("upsert pubsub snapshot: %v", err)
	}
	got, err := store.GetPubSubSnapshot(ctx, "app-test")
	if err != nil {
		t.Fatalf("get pubsub snapshot: %v", err)
	}
	if string(got.Topics) != `[{"name":"events","pending":2}]` {
		t.Fatalf("topics = %s", got.Topics)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("expected updated timestamp")
	}
	history, err := store.ListPubSubSnapshots(ctx, "app-test", time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatalf("list pubsub history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history length = %d, want 1", len(history))
	}
	if string(history[0].Topics) != `[{"name":"events","pending":2}]` {
		t.Fatalf("history topics = %s", history[0].Topics)
	}
}

func TestStorePubSubMessages(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	insertedAt := time.Now().UTC().Add(-2 * time.Minute)
	if err := store.UpsertPubSubMessage(ctx, PubSubMessage{
		AppID:            "app-test",
		MessageID:        "stream:1",
		TopicName:        "events",
		SubscriptionName: "events-sub",
		ServiceName:      "events",
		Status:           "queued",
		TraceID:          "",
		Attempt:          0,
		Payload:          json.RawMessage(`{"value":"hello"}`),
		InsertedAt:       insertedAt,
	}); err != nil {
		t.Fatalf("upsert queued pubsub message: %v", err)
	}
	if err := store.UpsertPubSubMessage(ctx, PubSubMessage{
		AppID:            "app-test",
		MessageID:        "stream:1",
		TopicName:        "events",
		SubscriptionName: "events-sub",
		ServiceName:      "events",
		Status:           "completed",
		TraceID:          "trace-1",
		Attempt:          1,
		Payload:          json.RawMessage(`{"value":"hello"}`),
		Result:           json.RawMessage(`{"status":"completed"}`),
		Deliveries:       1,
		InsertedAt:       insertedAt,
		PickedUpAt:       insertedAt.Add(2 * time.Second),
		FinishedAt:       insertedAt.Add(5 * time.Second),
		DurationMS:       3000,
	}); err != nil {
		t.Fatalf("upsert completed pubsub message: %v", err)
	}
	if err := store.UpsertPubSubMessage(ctx, PubSubMessage{
		AppID:            "app-test",
		MessageID:        "stream:2",
		TopicName:        "events",
		SubscriptionName: "other-sub",
		Status:           "queued",
		Attempt:          0,
		Payload:          json.RawMessage(`{"value":"later"}`),
		InsertedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert second pubsub message: %v", err)
	}

	items, err := store.ListPubSubMessages(ctx, "app-test", time.Now().UTC().Add(-time.Hour), "", "", "", 50)
	if err != nil {
		t.Fatalf("list pubsub messages: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("message count = %d, want 2", len(items))
	}
	if items[1].Status != "completed" {
		t.Fatalf("updated status = %q, want completed", items[1].Status)
	}
	if string(items[1].Result) != `{"status":"completed"}` {
		t.Fatalf("unexpected result json: %s", items[1].Result)
	}

	filtered, err := store.ListPubSubMessages(ctx, "app-test", time.Now().UTC().Add(-time.Hour), "", "events-sub", "completed", 50)
	if err != nil {
		t.Fatalf("list filtered pubsub messages: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("filtered message count = %d, want 1", len(filtered))
	}
	if filtered[0].SubscriptionName != "events-sub" {
		t.Fatalf("filtered subscription = %q", filtered[0].SubscriptionName)
	}
	if filtered[0].TraceID != "trace-1" {
		t.Fatalf("filtered trace id = %q, want trace-1", filtered[0].TraceID)
	}

	if err := store.UpsertPubSubMessageAttempt(ctx, PubSubMessageAttempt{
		AppID:            "app-test",
		MessageID:        "stream:1",
		TopicName:        "events",
		SubscriptionName: "events-sub",
		Status:           "completed",
		TraceID:          "trace-1",
		Attempt:          1,
		Payload:          json.RawMessage(`{"value":"hello"}`),
		Result:           json.RawMessage(`{"status":"completed"}`),
		Deliveries:       1,
		InsertedAt:       insertedAt,
		PickedUpAt:       insertedAt.Add(2 * time.Second),
		FinishedAt:       insertedAt.Add(5 * time.Second),
		DurationMS:       3000,
	}); err != nil {
		t.Fatalf("upsert pubsub attempt: %v", err)
	}
	attempts, err := store.ListPubSubMessageAttempts(ctx, "app-test", "stream:1", "events-sub")
	if err != nil {
		t.Fatalf("list pubsub attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(attempts))
	}
	if attempts[0].TraceID != "trace-1" {
		t.Fatalf("attempt trace id = %q, want trace-1", attempts[0].TraceID)
	}

	if err := store.MarkPubSubMessagesCleared(ctx, "app-test", time.Now().UTC()); err != nil {
		t.Fatalf("mark cleared: %v", err)
	}
	items, err = store.ListPubSubMessages(ctx, "app-test", time.Now().UTC().Add(-time.Hour), "", "", "", 50)
	if err != nil {
		t.Fatalf("list after clear: %v", err)
	}
	if items[0].Status != "cleared" {
		t.Fatalf("queued message status after clear = %q, want cleared", items[0].Status)
	}
	if items[1].Status != "completed" {
		t.Fatalf("completed message status after clear = %q, want completed", items[1].Status)
	}
}

func TestStoreQueryTraceSummariesAndMetrics(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	endpoint := "Search"
	now := time.Now().UTC()
	for _, item := range []struct {
		traceID  string
		service  string
		duration time.Duration
		err      bool
	}{
		{traceID: "trace-1", service: "jobs", duration: 100 * time.Millisecond},
		{traceID: "trace-2", service: "jobs", duration: 2500 * time.Millisecond, err: true},
		{traceID: "trace-3", service: "users", duration: 3 * time.Second},
	} {
		if err := store.AppendTraceSummary(ctx, &TraceSummary{
			AppID:         "app-test",
			TraceID:       item.traceID,
			SpanID:        item.traceID + "-span",
			Type:          "RPC",
			IsRoot:        true,
			IsError:       item.err,
			StartedAt:     now.Add(-time.Minute),
			DurationNanos: uint64(item.duration),
			ServiceName:   item.service,
			EndpointName:  &endpoint,
		}); err != nil {
			t.Fatalf("append trace %s: %v", item.traceID, err)
		}
	}

	items, err := store.QueryTraceSummaries(ctx, TraceQuery{
		AppID:            "app-test",
		ServiceName:      "jobs",
		MinDurationNanos: uint64(2 * time.Second),
		Limit:            10,
	})
	if err != nil {
		t.Fatalf("query traces: %v", err)
	}
	if len(items) != 1 || items[0].TraceID != "trace-2" {
		t.Fatalf("items = %+v", items)
	}

	metrics, err := store.QueryTraceMetrics(ctx, TraceQuery{
		AppID: "app-test",
		Since: now.Add(-time.Hour),
		Limit: 100,
	})
	if err != nil {
		t.Fatalf("query metrics: %v", err)
	}
	if len(metrics) != 3 {
		t.Fatalf("metrics count = %d, want 3", len(metrics))
	}
}
