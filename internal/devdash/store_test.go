package devdash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if err := store.UpsertApp(ctx, AppRecord{
		ID:                  "app-test",
		SessionID:           "session-test",
		Name:                "app-test",
		Root:                "/tmp/app",
		Running:             false,
		SessionStatus:       "degraded",
		SessionStatusReason: "app process is not running",
	}); err != nil {
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
	if app.Running || app.Name != "app-test" || app.SessionStatus != "degraded" || app.SessionStatusReason == "" {
		t.Fatalf("persisted app = %+v", app)
	}
	session, err := reopened.GetAppSession(ctx, "session-test")
	if err != nil {
		t.Fatalf("get persisted session: %v", err)
	}
	if session.SessionStatus != "degraded" || session.SessionStatusReason == "" {
		t.Fatalf("persisted session = %+v", session)
	}
	requests, err := reopened.ListStoredRequests(ctx, "app-test")
	if err != nil {
		t.Fatalf("list persisted requests: %v", err)
	}
	if len(requests) != 1 || requests[0].Title != "Persisted" {
		t.Fatalf("persisted requests = %+v", requests)
	}
}

func TestStoreSaveStateCompactsAndPrunesLocalHistory(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	state := newStoreState()
	now := time.Now().UTC()
	for i := range maxStoredProcessOutput + 100 {
		state.ProcessOutput = append(state.ProcessOutput, ProcessOutput{
			AppID:     "app-test",
			SessionID: "session-a",
			PID:       fmt.Sprintf("%d", i),
			Stream:    "stdout",
			Output:    []byte(strings.Repeat("x", 256)),
			CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		})
	}

	if err := store.saveState(state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if bytes.Contains(data, []byte("\n  \"")) {
		t.Fatalf("state was written with pretty indentation")
	}

	var saved storeState
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal saved state: %v", err)
	}
	if len(saved.ProcessOutput) > maxStoredProcessOutput {
		t.Fatalf("process output count = %d, want <= %d", len(saved.ProcessOutput), maxStoredProcessOutput)
	}
	if len(saved.ProcessOutput) == 0 {
		t.Fatal("byte budget pruned all process output, want recent history retained")
	}
	if len(data) > softStoreFileBytes {
		t.Fatalf("saved state size = %d, want <= soft budget %d", len(data), softStoreFileBytes)
	}
}

func TestStoreDropsLegacyObservabilityArraysOnSave(t *testing.T) {
	t.Parallel()

	cacheRoot := t.TempDir()
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cacheRoot, "devdash.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"trace_summaries":[{"app_id":"app-test","trace_id":"trace-a"}],"trace_events":[{"app_id":"app-test","trace_id":"trace-a"}],"log_events":[{"app_id":"app-test","level":"INFO","message":"hello"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	if err := store.UpsertApp(context.Background(), AppRecord{ID: "app-test", Name: "app-test", Root: "/tmp/app"}); err != nil {
		t.Fatalf("upsert app: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"trace_summaries"`, `"trace_events"`, `"log_events"`} {
		if bytes.Contains(data, []byte(key)) {
			t.Fatalf("legacy observability key %s survived save: %s", key, data)
		}
	}
}

func TestStoreBudgetRejectsLargeSessions(t *testing.T) {
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
	metadata := largeAppMetadata(t, "rev-shared", 2*1024*1024)
	apiEncoding := json.RawMessage(`{"rpc":"shape"}`)
	now := time.Now().UTC()
	for i := range 20 {
		if err := store.UpsertApp(ctx, AppRecord{
			ID:           "app-test",
			BaseAppID:    "app-test",
			RuntimeAppID: fmt.Sprintf("app-test--session-%02d", i),
			SessionID:    fmt.Sprintf("session-%02d", i),
			Name:         "app-test",
			Root:         fmt.Sprintf("/tmp/worktree-%02d", i),
			ListenAddr:   fmt.Sprintf("127.0.0.1:%d", 4100+i),
			Metadata:     metadata,
			APIEncoding:  apiEncoding,
			Running:      true,
			UpdatedAt:    now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("upsert session %d: %v", i, err)
		}
	}
	if err := store.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(cacheRoot, "devdash.json"))
	if err != nil {
		t.Fatalf("read devdash.json: %v", err)
	}
	if len(data) > hardStoreFileBytes {
		t.Fatalf("devdash.json size = %d, want <= %d", len(data), hardStoreFileBytes)
	}
	if bytes.Contains(data, []byte("svc_payload")) {
		t.Fatalf("devdash.json still contains inline app metadata")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw store: %v", err)
	}
	if got := len(raw["app_sessions"]); got > 128*1024 {
		t.Fatalf("app_sessions serialized size = %d, want compact", got)
	}
	metadataBlobs, err := filepath.Glob(filepath.Join(cacheRoot, "app-model", "metadata", "sha256", "*.json"))
	if err != nil {
		t.Fatalf("glob metadata blobs: %v", err)
	}
	if len(metadataBlobs) != 1 {
		t.Fatalf("metadata blob count = %d, want 1: %v", len(metadataBlobs), metadataBlobs)
	}

	sessions, err := store.ListAppSessions(ctx)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 20 {
		t.Fatalf("session count = %d, want 20", len(sessions))
	}
	if bytes.Contains(sessions[0].Metadata, []byte("svc_payload")) {
		t.Fatalf("list sessions hydrated large metadata")
	}
	session, err := store.GetAppSession(ctx, "session-00")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !bytes.Contains(session.Metadata, []byte("svc_payload")) {
		t.Fatalf("detail session did not hydrate metadata")
	}
	if string(session.APIEncoding) != string(apiEncoding) {
		t.Fatalf("hydrated api encoding = %s, want %s", session.APIEncoding, apiEncoding)
	}
}

func TestLegacyFatSessionMetadataMigratesToRefs(t *testing.T) {
	t.Parallel()

	cacheRoot := t.TempDir()
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatalf("mkdir cache root: %v", err)
	}
	metadata := largeAppMetadata(t, "legacy-rev", 512*1024)
	legacyApp := AppRecord{
		ID:          "legacy-app",
		SessionID:   "legacy-session",
		Name:        "legacy-app",
		Root:        "/tmp/legacy",
		Metadata:    metadata,
		APIEncoding: json.RawMessage(`{"legacy":true}`),
		Running:     true,
		UpdatedAt:   time.Now().UTC(),
	}
	legacyData, err := json.Marshal(map[string]any{
		"version": 1,
		"apps": map[string]AppRecord{
			"legacy-app": legacyApp,
		},
		"app_sessions": map[string]AppRecord{
			"legacy-session": legacyApp,
		},
	})
	if err != nil {
		t.Fatalf("marshal legacy store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, "devdash.json"), append(legacyData, '\n'), 0o644); err != nil {
		t.Fatalf("write legacy store: %v", err)
	}

	store, err := OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	if err := store.Flush(context.Background()); err != nil {
		t.Fatalf("flush migrated store: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(cacheRoot, "devdash.json"))
	if err != nil {
		t.Fatalf("read migrated store: %v", err)
	}
	if bytes.Contains(data, []byte("svc_payload")) || bytes.Contains(data, []byte("Metadata")) || bytes.Contains(data, []byte("APIEncoding")) {
		t.Fatalf("migrated store still contains legacy inline app model: %s", data)
	}
	if !bytes.Contains(data, []byte("metadata_ref")) || !bytes.Contains(data, []byte("api_encoding_ref")) {
		t.Fatalf("migrated store missing app model refs: %s", data)
	}
	session, err := store.GetAppSession(context.Background(), "legacy-session")
	if err != nil {
		t.Fatalf("get migrated session: %v", err)
	}
	if !bytes.Contains(session.Metadata, []byte("svc_payload")) {
		t.Fatalf("migrated session did not hydrate metadata")
	}
	if string(session.APIEncoding) != `{"legacy":true}` {
		t.Fatalf("migrated api encoding = %s", session.APIEncoding)
	}
}

func TestStoreStateSerializedSizeUnderBudget(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	state := newStoreState()
	now := time.Now().UTC()
	for i := range 300 {
		state.ProcessOutput = append(state.ProcessOutput, ProcessOutput{
			ID:        int64(i + 1),
			AppID:     "app-test",
			SessionID: "session-a",
			PID:       "123",
			Stream:    "stdout",
			Output:    bytes.Repeat([]byte("x"), 64*1024),
			CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		})
	}
	state.NextProcessOutputID = 301

	if err := store.saveState(state); err != nil {
		t.Fatalf("save oversized state: %v", err)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read saved state: %v", err)
	}
	if len(data) > softStoreFileBytes {
		t.Fatalf("saved store size = %d, want <= soft budget %d", len(data), softStoreFileBytes)
	}
	var saved storeState
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal saved state: %v", err)
	}
	if len(saved.ProcessOutput) >= 300 {
		t.Fatalf("process output count = %d, want byte-pruned below 300", len(saved.ProcessOutput))
	}
}

func TestStoreObservabilityWritesAreCompatibilityNoops(t *testing.T) {
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
	now := time.Now().UTC()
	if err := store.AppendTraceSummaryDeferred(ctx, &TraceSummary{
		AppID:         "app-test",
		SessionID:     "session-a",
		TraceID:       "trace-deferred",
		SpanID:        "root",
		Type:          "REQUEST",
		IsRoot:        true,
		StartedAt:     now,
		DurationNanos: uint64(time.Millisecond),
		ServiceName:   "tasks",
	}); err != nil {
		t.Fatalf("append deferred summary: %v", err)
	}
	if err := store.AppendTraceEventDeferred(ctx, &TraceEvent{
		AppID:     "app-test",
		SessionID: "session-a",
		TraceID:   "trace-deferred",
		SpanID:    "root",
		EventID:   1,
		EventTime: now,
		Event:     map[string]any{"request": map[string]any{"path": "/tasks"}},
	}); err != nil {
		t.Fatalf("append deferred event: %v", err)
	}

	if err := store.Flush(ctx); err != nil {
		t.Fatalf("flush deferred state: %v", err)
	}
	if err := store.WriteLogEventDeferred(ctx, &LogEvent{
		AppID:     "app-test",
		SessionID: "session-a",
		Level:     "INFO",
		Message:   "hello",
		Timestamp: now,
	}); err != nil {
		t.Fatalf("write deferred log: %v", err)
	}
	if err := store.Flush(ctx); err != nil {
		t.Fatalf("flush deferred log: %v", err)
	}
	events, err := store.GetTraceEventsForSession(ctx, "app-test", "session-a", "trace-deferred", "root")
	if err != nil {
		t.Fatalf("get trace events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("stored trace events = %d, want 0", len(events))
	}
	summaries, err := store.GetTraceSummariesForSession(ctx, "app-test", "session-a", "trace-deferred")
	if err != nil {
		t.Fatalf("get trace summaries: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("stored trace summaries = %d, want 0", len(summaries))
	}
	logs, err := store.CountLogsByLevelForSession(ctx, "app-test", "session-a", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("stored log counts = %+v, want empty", logs)
	}
}

func largeAppMetadata(t *testing.T, revision string, payloadBytes int) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"app_revision": revision,
		"svcs": map[string]any{
			"svc": map[string]any{
				"svc_payload": strings.Repeat("x", payloadBytes),
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal large metadata: %v", err)
	}
	return data
}

func TestStoreFlushErrorAllowsDeferredRetry(t *testing.T) {
	t.Parallel()

	store := &Store{
		path: filepath.Join(t.TempDir(), "missing", "devdash.json"),
		shared: &storeShared{
			state:       newStoreState(),
			dirty:       true,
			savePending: true,
		},
	}
	if err := store.Flush(context.Background()); err == nil {
		t.Fatal("Flush returned nil for unwritable store path")
	}
	if !store.shared.dirty {
		t.Fatal("failed flush cleared dirty state")
	}
	if store.shared.savePending {
		t.Fatal("failed flush left savePending set, blocking retry scheduling")
	}
}

func TestStoreDeferredFlushDoesNotOverwriteExternalClear(t *testing.T) {
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
	if err := store.AppendTraceSummary(ctx, &TraceSummary{
		AppID:         "app-test",
		SessionID:     "session-a",
		TraceID:       "trace-old",
		SpanID:        "root",
		Type:          "REQUEST",
		IsRoot:        true,
		StartedAt:     time.Now().UTC(),
		DurationNanos: uint64(time.Millisecond),
		ServiceName:   "tasks",
	}); err != nil {
		t.Fatalf("append old trace: %v", err)
	}
	if err := store.AppendTraceSummaryDeferred(ctx, &TraceSummary{
		AppID:         "app-test",
		SessionID:     "session-a",
		TraceID:       "trace-deferred",
		SpanID:        "root",
		Type:          "REQUEST",
		IsRoot:        true,
		StartedAt:     time.Now().UTC(),
		DurationNanos: uint64(time.Millisecond),
		ServiceName:   "tasks",
	}); err != nil {
		t.Fatalf("append deferred trace: %v", err)
	}

	external := &Store{path: store.path, shared: &storeShared{}}
	if err := external.ClearTraces(ctx, "app-test"); err != nil {
		t.Fatalf("external clear traces: %v", err)
	}
	if err := store.Flush(ctx); err != nil {
		t.Fatalf("flush after external clear: %v", err)
	}

	reopened := &Store{path: store.path, shared: &storeShared{}}
	summaries, err := reopened.GetTraceSummaries(ctx, "app-test", "trace-old")
	if err != nil {
		t.Fatalf("get old trace: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("external clear was overwritten, old summaries = %+v", summaries)
	}
}

func TestStoreHandlesForSamePathShareState(t *testing.T) {
	t.Parallel()

	cacheRoot := t.TempDir()
	first, err := OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	t.Cleanup(func() {
		_ = first.Close()
	})
	second, err := OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	t.Cleanup(func() {
		_ = second.Close()
	})

	ctx := context.Background()
	if err := first.AppendTraceSummary(ctx, &TraceSummary{
		AppID:         "app-test",
		SessionID:     "session-a",
		TraceID:       "trace-shared",
		SpanID:        "root",
		Type:          "REQUEST",
		IsRoot:        true,
		StartedAt:     time.Now().UTC(),
		DurationNanos: uint64(time.Millisecond),
		ServiceName:   "tasks",
	}); err != nil {
		t.Fatalf("append trace: %v", err)
	}
	if err := second.ClearTracesForSession(ctx, "app-test", "session-a"); err != nil {
		t.Fatalf("clear traces: %v", err)
	}
	summaries, err := first.GetTraceSummariesForSession(ctx, "app-test", "session-a", "trace-shared")
	if err != nil {
		t.Fatalf("get traces: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("first store saw stale summaries after second store cleared: %+v", summaries)
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

func TestStoreTraceSummaryQueriesReturnNoPersistedHistory(t *testing.T) {
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
	if len(items) != 0 {
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
	if len(metrics) != 0 {
		t.Fatalf("metrics count = %d, want 0", len(metrics))
	}
}

func TestStoreObservabilitySessionQueriesReturnNoPersistedHistory(t *testing.T) {
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
	if len(items) != 0 {
		t.Fatalf("session traces = %+v", items)
	}

	events, err := store.GetTraceEventsForSession(ctx, "app-test", "session-a", "trace-a", "trace-a-span")
	if err != nil {
		t.Fatalf("get trace events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("session events = %+v", events)
	}

	eventCount, err := store.CountTraceEventsForSession(ctx, "app-test", "session-a", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("event count = %d, want 0", eventCount)
	}

	logs, err := store.CountLogsByLevelForSession(ctx, "app-test", "session-a", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("session logs = %+v", logs)
	}
}

func TestStoreTraceSummarySessionQueriesStayEmpty(t *testing.T) {
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
	if len(all) != 0 {
		t.Fatalf("trace summary count = %d, want 0", len(all))
	}
	for _, sessionID := range []string{"session-a", "session-b"} {
		items, err := store.GetTraceSummariesForSession(ctx, "app-test", sessionID, "trace-replay")
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 {
			t.Fatalf("items for %s = %+v", sessionID, items)
		}
	}
}

func TestWriteProcessEventTruncatesOversizedPayloads(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	huge := map[string]any{"meta": strings.Repeat("x", maxProcessEventPayloadBytes+1)}
	if err := store.WriteProcessEvent(ctx, "app-test", "process/reload", huge); err != nil {
		t.Fatalf("write oversized process event: %v", err)
	}
	if err := store.WriteProcessEvent(ctx, "app-test", "process/start", map[string]any{"pid": "42"}); err != nil {
		t.Fatalf("write small process event: %v", err)
	}

	events, err := store.ListProcessEvents(ctx, "app-test", 10)
	if err != nil {
		t.Fatalf("list process events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	for _, event := range events {
		if len(event.PayloadJSON) > maxProcessEventPayloadBytes {
			t.Fatalf("stored payload for %s is %d bytes, want <= %d", event.Kind, len(event.PayloadJSON), maxProcessEventPayloadBytes)
		}
	}
	var truncated struct {
		Truncated     bool `json:"truncated"`
		OriginalBytes int  `json:"original_bytes"`
	}
	for _, event := range events {
		if event.Kind != "process/reload" {
			continue
		}
		if err := json.Unmarshal(event.PayloadJSON, &truncated); err != nil {
			t.Fatalf("unmarshal truncated payload: %v", err)
		}
	}
	if !truncated.Truncated || truncated.OriginalBytes <= maxProcessEventPayloadBytes {
		t.Fatalf("truncated marker = %+v", truncated)
	}
}

func TestPruneTruncatesOversizedProcessEventPayloadsFromOlderWriters(t *testing.T) {
	t.Parallel()

	huge, err := json.Marshal(map[string]any{"meta": strings.Repeat("x", maxProcessEventPayloadBytes+1)})
	if err != nil {
		t.Fatal(err)
	}
	state := &storeState{
		Version: 1,
		ProcessEvents: []ProcessEvent{
			{ID: 1, AppID: "app-test", Kind: "compile-start", PayloadJSON: huge, CreatedAt: time.Now().UTC()},
			{ID: 2, AppID: "app-test", Kind: "process/start", PayloadJSON: json.RawMessage(`{"pid":"42"}`), CreatedAt: time.Now().UTC()},
		},
	}
	pruneStoreState(state)
	if got := len(state.ProcessEvents[0].PayloadJSON); got > maxProcessEventPayloadBytes {
		t.Fatalf("oversized payload survived prune: %d bytes", got)
	}
	var marker struct {
		Truncated     bool `json:"truncated"`
		OriginalBytes int  `json:"original_bytes"`
	}
	if err := json.Unmarshal(state.ProcessEvents[0].PayloadJSON, &marker); err != nil {
		t.Fatalf("unmarshal truncation marker: %v", err)
	}
	if !marker.Truncated || marker.OriginalBytes != len(huge) {
		t.Fatalf("marker = %+v, want truncated with original_bytes %d", marker, len(huge))
	}
	if string(state.ProcessEvents[1].PayloadJSON) != `{"pid":"42"}` {
		t.Fatalf("small payload was modified: %s", state.ProcessEvents[1].PayloadJSON)
	}
}
