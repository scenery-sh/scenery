package devdash

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenStorePersistsJSONState(t *testing.T) {
	t.Parallel()

	cacheRoot := t.TempDir()
	store, err := OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	if err := store.UpsertApp(ctx, AppRecord{ID: "app-test", Name: "app-test", Root: "/tmp/app", Running: true}); err != nil {
		t.Fatalf("upsert app: %v", err)
	}
	if _, err := store.CreateStoredRequest(ctx, StoredRequest{
		AppID: "app-test",
		Title: "Persisted",
		Data:  StoredRequestData{Method: "GET"},
	}); err != nil {
		t.Fatalf("create stored request: %v", err)
	}
	if _, err := filepath.Abs(filepath.Join(cacheRoot, "devdash.json")); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})
	app, err := reopened.GetApp(ctx, "app-test")
	if err != nil {
		t.Fatalf("get persisted app: %v", err)
	}
	if !app.Running || app.Name != "app-test" {
		t.Fatalf("persisted app = %+v", app)
	}
	requests, err := reopened.ListStoredRequests(ctx, "app-test")
	if err != nil {
		t.Fatalf("list persisted requests: %v", err)
	}
	if len(requests) != 1 || requests[0].Title != "Persisted" {
		t.Fatalf("persisted requests = %+v", requests)
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

func TestStorePersistsSessionIdentityAndFiltersOutput(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	if err := store.UpsertApp(ctx, AppRecord{
		ID:           "app-test",
		BaseAppID:    "app-test",
		RuntimeAppID: "app-test--feature-a",
		SessionID:    "feature-a-123abc",
		Name:         "app-test",
		Root:         "/tmp/app",
		UpdatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert app: %v", err)
	}
	app, err := store.GetApp(ctx, "app-test")
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if app.SessionID != "feature-a-123abc" || app.RuntimeAppID != "app-test--feature-a" || app.BaseAppID != "app-test" {
		t.Fatalf("app identity = %+v", app)
	}

	for _, output := range []ProcessOutput{
		{AppID: "app-test", SessionID: "feature-a-123abc", PID: "1", Stream: "stdout", Output: []byte("a\n")},
		{AppID: "app-test", SessionID: "feature-b-456def", PID: "2", Stream: "stdout", Output: []byte("b\n")},
	} {
		if err := store.WriteProcessOutput(ctx, output); err != nil {
			t.Fatalf("write process output: %v", err)
		}
	}
	all, err := store.ListProcessOutput(ctx, "app-test", 10)
	if err != nil {
		t.Fatalf("list output: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all output count = %d, want 2", len(all))
	}
	filtered, err := store.ListProcessOutputForSession(ctx, "app-test", "feature-a-123abc", 10)
	if err != nil {
		t.Fatalf("list filtered output: %v", err)
	}
	if len(filtered) != 1 || filtered[0].SessionID != "feature-a-123abc" || string(filtered[0].Output) != "a\n" {
		t.Fatalf("filtered output = %+v", filtered)
	}
}

func TestStoreDevEventsRoundTripAndFilters(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	now := time.Now().UTC()
	events := []DevEvent{
		DevEventFromOutput("app-test", "session-a", DevSource{ID: "api", Kind: "app", Name: "api", PID: "111", Stream: "stdout", Status: "running"}, []byte(`{"level":"info","msg":"registered","activity":"SendEmail"}`+"\n"), now),
		DevEventFromOutput("app-test", "session-a", DevSource{ID: "worker:typescript", Kind: "worker", Name: "typescript", PID: "222", Stream: "stderr", Status: "running"}, []byte(`2026-05-31T12:00:00Z ERROR activity failed activity=SyncUser attempt=2`+"\n"), now.Add(time.Second)),
		DevEventFromOutput("app-test", "session-b", DevSource{ID: "api", Kind: "app", Name: "api", PID: "333", Stream: "stdout", Status: "running"}, []byte("other session\n"), now.Add(2*time.Second)),
	}
	for _, event := range events {
		if err := store.WriteDevEvent(ctx, event); err != nil {
			t.Fatalf("write dev event: %v", err)
		}
	}

	filtered, err := store.ListDevEvents(ctx, DevEventQuery{
		AppID:     "app-test",
		SessionID: "session-a",
		SourceID:  "worker:typescript",
		Level:     "error",
		Grep:      "SyncUser",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("list filtered dev events: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("filtered events = %d, want 1: %+v", len(filtered), filtered)
	}
	event := filtered[0]
	if event.Source.ID != "worker:typescript" || event.Source.Kind != "worker" || event.Level != "error" || event.Message != "activity failed" {
		t.Fatalf("filtered event = %+v", event)
	}
	if string(event.Fields) != `{"activity":"SyncUser","attempt":2}` {
		t.Fatalf("fields = %s", event.Fields)
	}

	sources, err := store.ListDevSources(ctx, "app-test", "session-a")
	if err != nil {
		t.Fatalf("list dev sources: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %d, want 2: %+v", len(sources), sources)
	}
}

func TestStoreDevEventIDsAreAssignedBeforeInsert(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	first := NewDevEvent("app-test", "session-a", DevSource{ID: "api", Kind: "app"}, "info", "first", nil, time.Now().UTC())
	firstID, err := store.WriteDevEventReturningID(ctx, first)
	if err != nil {
		t.Fatalf("write first event: %v", err)
	}
	if firstID <= 0 {
		t.Fatalf("first id = %d, want positive", firstID)
	}

	explicit := NewDevEvent("app-test", "session-a", DevSource{ID: "api", Kind: "app"}, "info", "explicit", nil, time.Now().UTC())
	explicit.ID = firstID + 10
	explicitID, err := store.WriteDevEventReturningID(ctx, explicit)
	if err != nil {
		t.Fatalf("write explicit event: %v", err)
	}
	if explicitID != explicit.ID {
		t.Fatalf("explicit id = %d, want %d", explicitID, explicit.ID)
	}

	next := NewDevEvent("app-test", "session-a", DevSource{ID: "api", Kind: "app"}, "info", "next", nil, time.Now().UTC())
	nextID, err := store.WriteDevEventReturningID(ctx, next)
	if err != nil {
		t.Fatalf("write next event: %v", err)
	}
	if nextID != explicit.ID+1 {
		t.Fatalf("next id = %d, want %d", nextID, explicit.ID+1)
	}

	followed, err := store.ListDevEvents(ctx, DevEventQuery{AppID: "app-test", SessionID: "session-a", AfterID: explicit.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list after id: %v", err)
	}
	if len(followed) != 1 || followed[0].ID != nextID || followed[0].Message != "next" {
		t.Fatalf("followed events = %+v, want next id %d", followed, nextID)
	}
}

func TestStoreKeepsDistinctAppSessionsForSameApp(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	now := time.Now().UTC()
	for _, rec := range []AppRecord{
		{
			ID:           "app-test",
			BaseAppID:    "app-test",
			RuntimeAppID: "app-test--session-a",
			SessionID:    "session-a",
			Name:         "app-test",
			Root:         "/tmp/worktree-a",
			Running:      true,
			PID:          "111",
			UpdatedAt:    now.Add(-time.Minute),
		},
		{
			ID:           "app-test",
			BaseAppID:    "app-test",
			RuntimeAppID: "app-test--session-b",
			SessionID:    "session-b",
			Name:         "app-test",
			Root:         "/tmp/worktree-b",
			Running:      true,
			PID:          "222",
			UpdatedAt:    now,
		},
	} {
		if err := store.UpsertApp(ctx, rec); err != nil {
			t.Fatalf("upsert app session %q: %v", rec.SessionID, err)
		}
	}

	latest, err := store.GetApp(ctx, "app-test")
	if err != nil {
		t.Fatalf("get latest app: %v", err)
	}
	if latest.SessionID != "session-b" || latest.RouteID != "app-test" {
		t.Fatalf("latest app = %+v, want session-b with legacy route id", latest)
	}

	sessions, err := store.ListAppSessions(ctx)
	if err != nil {
		t.Fatalf("list app sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("session count = %d, want 2: %+v", len(sessions), sessions)
	}
	byRoute := map[string]AppRecord{}
	for _, session := range sessions {
		byRoute[session.RouteID] = session
	}
	if byRoute["session-a"].Root != "/tmp/worktree-a" || byRoute["session-b"].Root != "/tmp/worktree-b" {
		t.Fatalf("session records = %+v", sessions)
	}

	sessionA, err := store.GetAppSession(ctx, "session-a")
	if err != nil {
		t.Fatalf("get session-a: %v", err)
	}
	if sessionA.ID != "app-test" || sessionA.SessionID != "session-a" || sessionA.RouteID != "session-a" {
		t.Fatalf("session-a record = %+v", sessionA)
	}

	sessionB, err := store.GetAppForSession(ctx, "app-test", "session-b")
	if err != nil {
		t.Fatalf("get app session-b: %v", err)
	}
	if sessionB.PID != "222" || sessionB.RouteID != "session-b" {
		t.Fatalf("session-b record = %+v", sessionB)
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

func TestStoreFiltersObservabilityBySession(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	now := time.Now().UTC()
	for _, item := range []struct {
		traceID   string
		sessionID string
		level     string
	}{
		{traceID: "trace-a", sessionID: "session-a", level: "INFO"},
		{traceID: "trace-b", sessionID: "session-b", level: "ERR"},
	} {
		if err := store.AppendTraceSummary(ctx, &TraceSummary{
			AppID:         "app-test",
			SessionID:     item.sessionID,
			TraceID:       item.traceID,
			SpanID:        item.traceID + "-span",
			Type:          "REQUEST",
			IsRoot:        true,
			StartedAt:     now,
			DurationNanos: uint64(time.Second),
			ServiceName:   "svc",
		}); err != nil {
			t.Fatalf("append trace %s: %v", item.traceID, err)
		}
		if err := store.AppendTraceEvent(ctx, &TraceEvent{
			AppID:       "app-test",
			SessionID:   item.sessionID,
			AppRootHash: "root123",
			Branch:      "feature/a",
			Worktree:    "onlv-a",
			TraceID:     item.traceID,
			SpanID:      item.traceID + "-span",
			EventID:     1,
			EventTime:   now,
			Event:       map[string]any{"span_start": map[string]any{}},
		}); err != nil {
			t.Fatalf("append event %s: %v", item.traceID, err)
		}
		if err := store.WriteLogEvent(ctx, &LogEvent{
			AppID:     "app-test",
			SessionID: item.sessionID,
			TraceID:   item.traceID,
			SpanID:    item.traceID + "-span",
			Level:     item.level,
			Message:   item.traceID,
			Timestamp: now,
		}); err != nil {
			t.Fatalf("write log %s: %v", item.traceID, err)
		}
	}

	items, err := store.QueryTraceSummaries(ctx, TraceQuery{
		AppID:     "app-test",
		SessionID: "session-a",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("query traces: %v", err)
	}
	if len(items) != 1 || items[0].TraceID != "trace-a" || items[0].SessionID != "session-a" {
		t.Fatalf("session traces = %+v", items)
	}

	events, err := store.GetTraceEventsForSession(ctx, "app-test", "session-a", "trace-a", "trace-a-span")
	if err != nil {
		t.Fatalf("get trace events: %v", err)
	}
	if len(events) != 1 || events[0].SessionID != "session-a" || events[0].AppRootHash != "root123" || events[0].Branch != "feature/a" || events[0].Worktree != "onlv-a" {
		t.Fatalf("session events = %+v", events)
	}

	eventCount, err := store.CountTraceEventsForSession(ctx, "app-test", "session-a", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("event count = %d, want 1", eventCount)
	}

	logs, err := store.CountLogsByLevelForSession(ctx, "app-test", "session-a", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if len(logs) != 1 || logs[0].Level != "INFO" || logs[0].Count != 1 {
		t.Fatalf("session logs = %+v", logs)
	}
}

func TestStoreKeepsTraceSummariesDistinctBySession(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	now := time.Now().UTC()
	for _, sessionID := range []string{"session-a", "session-b"} {
		if err := store.AppendTraceSummary(ctx, &TraceSummary{
			AppID:         "app-test",
			SessionID:     sessionID,
			TraceID:       "trace-replay",
			SpanID:        "span-root",
			Type:          "REQUEST",
			IsRoot:        true,
			StartedAt:     now,
			DurationNanos: uint64(time.Second),
			ServiceName:   "svc",
		}); err != nil {
			t.Fatalf("append trace for %s: %v", sessionID, err)
		}
	}

	all, err := store.GetTraceSummaries(ctx, "app-test", "trace-replay")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("trace summary count = %d, want 2", len(all))
	}
	for _, sessionID := range []string{"session-a", "session-b"} {
		items, err := store.GetTraceSummariesForSession(ctx, "app-test", sessionID, "trace-replay")
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].SessionID != sessionID {
			t.Fatalf("items for %s = %+v", sessionID, items)
		}
	}
}
