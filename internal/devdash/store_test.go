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
	const sessionCount = 5
	// If metadata were still stored inline, these sessions would exceed the
	// soft store budget. Keep the fixture only just large enough to prove that
	// boundary so the test does not spend time hashing throwaway megabytes.
	metadata := largeAppMetadata(t, "rev-shared", 2*1024*1024/sessionCount+1024)
	apiEncoding := json.RawMessage(`{"rpc":"shape"}`)
	now := time.Now().UTC()
	for i := range sessionCount {
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
	if len(sessions) != sessionCount {
		t.Fatalf("session count = %d, want %d", len(sessions), sessionCount)
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

func TestStorePersistsSessionIdentity(t *testing.T) {
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
}

func TestDevEventFromOutputKeepsShortLevelAboveStderr(t *testing.T) {
	event := DevEventFromOutput(
		"app-test",
		"session-a",
		DevSource{ID: "api", Stream: "stderr"},
		[]byte("9:29PM INF health.console_probe log 5 step=done\n"),
		time.Now().UTC(),
	)
	if event.Level != "info" {
		t.Fatalf("level = %q, want info", event.Level)
	}
	if event.Parse.Format != "level-text" {
		t.Fatalf("parse format = %q, want level-text", event.Parse.Format)
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
