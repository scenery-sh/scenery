package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
)

func TestParseLogsArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseLogsArgs([]string{"--app-root", "/tmp/app", "--limit", "50", "--stream", "stderr", "--follow", "--jsonl", "--source", "api", "--kind", "app", "--level", "error", "--grep", "boom", "--since", "15m", "--backend", "victoria"})
	if err != nil {
		t.Fatalf("parseLogsArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" || opts.Limit != 50 || opts.Stream != "stderr" || opts.Session != "" || !opts.Follow || !opts.JSONL || opts.Source != "api" || opts.Kind != "app" || opts.Level != "error" || opts.Grep != "boom" || opts.Since != 15*time.Minute || opts.Backend != logsBackendVictoria {
		t.Fatalf("unexpected logs options: %#v", opts)
	}
}

func TestParseLogsArgsTreatsJSONAsAliasForJSONL(t *testing.T) {
	t.Parallel()

	opts, err := parseLogsArgs([]string{"--json"})
	if err != nil {
		t.Fatalf("parseLogsArgs returned error: %v", err)
	}
	if !opts.JSONL {
		t.Fatalf("expected JSONL mode, got %#v", opts)
	}
}

func TestAttachLogArgsDefaultsToCurrentSessionFollow(t *testing.T) {
	t.Parallel()

	args, err := attachLogArgs([]string{"--app-root", "/tmp/app", "--limit", "25", "--stream", "stderr", "--json"})
	if err != nil {
		t.Fatalf("attachLogArgs returned error: %v", err)
	}
	want := []string{"--follow", "--limit", "25", "--stream", "stderr", "--app-root", "/tmp/app", "--jsonl"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("attach args = %#v, want %#v", args, want)
	}
}

func TestAttachCommandUsesLogsFollow(t *testing.T) {
	prev := runSceneryLogsFunc
	defer func() { runSceneryLogsFunc = prev }()
	called := false
	runSceneryLogsFunc = func(ctx context.Context, stdout io.Writer, args []string) error {
		called = true
		want := []string{"--follow", "--limit", "200", "--stream", "all", "--source", "api"}
		if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("logs args = %#v, want %#v", args, want)
		}
		return nil
	}
	if err := attachCommand([]string{"--source", "api"}); err != nil {
		t.Fatalf("attachCommand returned error: %v", err)
	}
	if !called {
		t.Fatal("expected logs runner to be called")
	}
}

func TestRunSceneryLogsReadsStoredOutput(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"logsapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/logsapp\n\ngo 1.26.3\n")

	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.UpsertApp(ctx, devdash.AppRecord{
		ID:         "logsapp",
		Name:       "logsapp",
		Root:       root,
		ListenAddr: "127.0.0.1:4000",
		Running:    true,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertApp: %v", err)
	}
	installLogsVictoriaStack(t,
		testOutputEvent("logsapp", "", 1, "stdout", "first line\n"),
		testOutputEvent("logsapp", "", 2, "stderr", "second line\n"),
	)

	var buf bytes.Buffer
	if err := runSceneryLogs(ctx, &buf, []string{"--app-root", root, "--limit", "10"}); err != nil {
		t.Fatalf("runSceneryLogs returned error: %v", err)
	}
	if got := buf.String(); got != "first line\nsecond line\n" {
		t.Fatalf("logs output = %q", got)
	}
}

func TestRunSceneryLogsFiltersStream(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"logsapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/logsapp\n\ngo 1.26.3\n")

	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.UpsertApp(ctx, devdash.AppRecord{
		ID:         "logsapp",
		Name:       "logsapp",
		Root:       root,
		ListenAddr: "127.0.0.1:4000",
		Running:    true,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertApp: %v", err)
	}
	installLogsVictoriaStack(t,
		testOutputEvent("logsapp", "", 1, "stdout", "out\n"),
		testOutputEvent("logsapp", "", 2, "stderr", "err\n"),
	)

	var buf bytes.Buffer
	if err := runSceneryLogs(ctx, &buf, []string{"--app-root", root, "--stream", "stderr"}); err != nil {
		t.Fatalf("runSceneryLogs returned error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "err" {
		t.Fatalf("stderr logs output = %q", got)
	}
}

func TestRunSceneryLogsFiltersAppRootRuntime(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"logsapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/logsapp\n\ngo 1.26.3\n")

	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.UpsertApp(ctx, devdash.AppRecord{
		ID:         "logsapp",
		SessionID:  "session-a",
		Name:       "logsapp",
		Root:       root,
		ListenAddr: "127.0.0.1:4000",
		Running:    true,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertApp: %v", err)
	}
	installLogsVictoriaStack(t,
		testOutputEvent("logsapp", "session-a", 1, "stdout", "a\n"),
		testOutputEvent("logsapp", "session-b", 2, "stdout", "b\n"),
	)

	var buf bytes.Buffer
	if err := runSceneryLogs(ctx, &buf, []string{"--app-root", root}); err != nil {
		t.Fatalf("runSceneryLogs returned error: %v", err)
	}
	if got := buf.String(); got != "a\n" {
		t.Fatalf("session logs output = %q", got)
	}
}

func TestRunSceneryLogsUsesSessionAppRecordWhenLatestAppRootDiffers(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"logsapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/logsapp\n\ngo 1.26.3\n")

	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	for _, rec := range []devdash.AppRecord{
		{ID: "logsapp", SessionID: "session-a", Name: "logsapp", Root: root, Running: true, UpdatedAt: time.Now().UTC().Add(-time.Minute)},
		{ID: "logsapp", SessionID: "session-b", Name: "logsapp", Root: otherRoot, Running: true, UpdatedAt: time.Now().UTC()},
	} {
		if err := store.UpsertApp(ctx, rec); err != nil {
			t.Fatalf("UpsertApp: %v", err)
		}
	}
	installLogsVictoriaStack(t, testOutputEvent("logsapp", "session-a", 1, "stdout", "session-a\n"))

	var buf bytes.Buffer
	if err := runSceneryLogs(ctx, &buf, []string{"--app-root", root}); err != nil {
		t.Fatalf("runSceneryLogs returned error: %v", err)
	}
	if got := buf.String(); got != "session-a\n" {
		t.Fatalf("session logs output = %q", got)
	}
}

func TestRunSceneryLogsJSONL(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"logsapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/logsapp\n\ngo 1.26.3\n")

	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.UpsertApp(ctx, devdash.AppRecord{
		ID:         "logsapp",
		Name:       "logsapp",
		Root:       root,
		ListenAddr: "127.0.0.1:4000",
		Running:    true,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertApp: %v", err)
	}
	installLogsVictoriaStack(t, testOutputEvent("logsapp", "session-json", 1, "stdout", "json line\n"))

	var buf bytes.Buffer
	if err := runSceneryLogs(ctx, &buf, []string{"--app-root", root, "--jsonl"}); err != nil {
		t.Fatalf("runSceneryLogs returned error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("jsonl lines = %d\n%s", len(lines), buf.String())
	}
	var payload struct {
		SchemaVersion string `json:"schema_version"`
		App           struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Root string `json:"root"`
		} `json:"app"`
		SessionID string `json:"session_id"`
		Source    struct {
			ID     string `json:"id"`
			PID    string `json:"pid"`
			Stream string `json:"stream"`
		} `json:"source"`
		Level   string `json:"level"`
		Message string `json:"message"`
		Raw     string `json:"raw"`
		Time    string `json:"time"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &payload); err != nil {
		t.Fatalf("json.Unmarshal(jsonl): %v\n%s", err, lines[0])
	}
	if payload.SchemaVersion != "scenery.dev.event.v1" {
		t.Fatalf("schema_version = %q", payload.SchemaVersion)
	}
	if payload.App.ID != "logsapp" || payload.App.Name != "logsapp" || payload.App.Root != root {
		t.Fatalf("app = %+v", payload.App)
	}
	if payload.Source.PID != "123" || payload.Source.Stream != "stdout" || payload.Raw != "json line" || payload.Message != "json line" || payload.Level != "info" {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.SessionID != "session-json" {
		t.Fatalf("session_id = %q", payload.SessionID)
	}
	if payload.Time == "" {
		t.Fatal("expected time")
	}
}

func TestRunSceneryLogsFiltersStructuredEvents(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"logsapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/logsapp\n\ngo 1.26.3\n")

	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.UpsertApp(ctx, devdash.AppRecord{
		ID:        "logsapp",
		SessionID: "session-a",
		Name:      "logsapp",
		Root:      root,
		Running:   true,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertApp: %v", err)
	}
	installLogsVictoriaStack(t, []devdash.DevEvent{
		devdash.DevEventFromOutput("logsapp", "session-a", devdash.DevSource{ID: "api", Kind: "app", Name: "api", Stream: "stdout"}, []byte("INFO request ok path=/health\n"), time.Now().UTC().Add(-time.Second)),
		devdash.DevEventFromOutput("logsapp", "session-a", devdash.DevSource{ID: "worker:typescript", Kind: "worker", Name: "typescript", Stream: "stderr"}, []byte("ERROR activity failed activity=SyncUser\n"), time.Now().UTC()),
	}...)

	var buf bytes.Buffer
	if err := runSceneryLogs(ctx, &buf, []string{"--app-root", root, "--source", "worker:typescript", "--level", "error", "--grep", "SyncUser"}); err != nil {
		t.Fatalf("runSceneryLogs returned error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "ERROR activity failed activity=SyncUser" {
		t.Fatalf("filtered structured logs output = %q", got)
	}
}

func TestVictoriaDevEventExportUsesJSONLineShape(t *testing.T) {
	t.Parallel()

	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/insert/jsonline" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("_time_field") != "" || r.URL.Query().Get("_msg_field") != "message" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	stack := &victoriaStack{components: []*victoriaComponent{{spec: victoriaComponentSpec{Name: "logs"}, baseURL: server.URL}}}
	event := devdash.NewDevEvent("logsapp", "session-a", devdash.DevSource{
		ID:     "worker:typescript",
		Kind:   "worker",
		Name:   "typescript",
		Role:   "temporal-activity-worker",
		PID:    "123",
		Stream: "stderr",
	}, "error", "activity failed", map[string]any{"activity": "SyncUser"}, time.Date(2026, 5, 31, 12, 44, 1, 223000000, time.UTC))
	event.ID = 42

	if err := stack.ExportDevEvent(context.Background(), event); err != nil {
		t.Fatalf("ExportDevEvent: %v", err)
	}
	if got[victoriaDevEventSchemaField] != devdash.DevEventSchemaVersion || got[victoriaDevEventAppField] != "logsapp" || got["id"].(float64) != 42 || got[victoriaDevEventCreatedAt] == "" {
		t.Fatalf("unexpected exported record: %+v", got)
	}
	if got["source_id"] != "worker:typescript" || got["source_stream"] != "stderr" || got["level"] != "error" || got["message"] != "activity failed" {
		t.Fatalf("unexpected exported fields: %+v", got)
	}
	if !strings.Contains(got["fields_json"].(string), "SyncUser") {
		t.Fatalf("fields_json = %v", got["fields_json"])
	}
}

func TestVictoriaListDevEventsReconstructsStructuredEvents(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/select/logsql/query" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		query := r.Form.Get("query")
		for _, want := range []string{
			`scenery_app_id="logsapp"`,
			`scenery_dev_schema="scenery.dev.event.v1"`,
			`source_id="worker:typescript"`,
		} {
			if !strings.Contains(query, want) {
				t.Fatalf("query %q does not contain %q", query, want)
			}
		}
		_, _ = io.WriteString(w, `{"_msg":"ERROR activity failed activity=SyncUser","created_at":"2026-05-31T12:44:01.223Z","scenery_dev_schema":"scenery.dev.event.v1","scenery_app_id":"logsapp","scenery_session_id":"session-a","id":"42","source_id":"worker:typescript","source_kind":"worker","source_name":"typescript","source_role":"temporal-activity-worker","source_pid":"123","source_stream":"stderr","level":"error","fields_json":"{\"activity\":\"SyncUser\"}","raw":"ERROR activity failed activity=SyncUser","parse_format":"logfmt","parse_ok":"true"}`+"\n")
		_, _ = io.WriteString(w, `{"_msg":"INFO other","created_at":"2026-05-31T12:44:02Z","scenery_dev_schema":"scenery.dev.event.v1","scenery_app_id":"logsapp","scenery_session_id":"session-a","id":"43","source_id":"api","source_kind":"app","source_stream":"stdout","level":"info","fields_json":"{}","raw":"INFO other","parse_format":"raw","parse_ok":"false"}`+"\n")
	}))
	defer server.Close()

	stack := &victoriaStack{components: []*victoriaComponent{{spec: victoriaComponentSpec{Name: "logs"}, baseURL: server.URL}}}
	items, err := stack.ListDevEvents(context.Background(), devdash.DevEventQuery{
		AppID:     "logsapp",
		SessionID: "session-a",
		SourceID:  "worker:typescript",
		Level:     "error",
		Grep:      "SyncUser",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("ListDevEvents: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d: %+v", len(items), items)
	}
	item := items[0]
	if item.ID != 42 || item.AppID != "logsapp" || item.SessionID != "session-a" || item.Source.ID != "worker:typescript" || item.Source.Stream != "stderr" || item.Level != "error" {
		t.Fatalf("unexpected event: %+v", item)
	}
	if item.Message != "ERROR activity failed activity=SyncUser" || item.Raw != "ERROR activity failed activity=SyncUser" || !item.Parse.OK || item.Parse.Format != "logfmt" {
		t.Fatalf("unexpected event payload: %+v", item)
	}
}

func installLogsVictoriaStack(t *testing.T, events ...devdash.DevEvent) {
	t.Helper()
	for i := range events {
		if events[i].ID == 0 {
			events[i].ID = int64(i + 1)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/select/logsql/query" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		for _, event := range events {
			data, err := json.Marshal(victoriaDevEventRecord(event))
			if err != nil {
				t.Fatalf("marshal event: %v", err)
			}
			_, _ = w.Write(append(data, '\n'))
		}
	}))
	t.Cleanup(server.Close)
	stack := &victoriaStack{components: []*victoriaComponent{{spec: victoriaComponentSpec{Name: "logs"}, baseURL: server.URL}}}
	prev := resolveLogsVictoriaStackFunc
	resolveLogsVictoriaStackFunc = func(ctx context.Context, allowDefault bool) *victoriaStack {
		return stack
	}
	t.Cleanup(func() {
		resolveLogsVictoriaStackFunc = prev
	})
}

func testOutputEvent(appID, sessionID string, id int64, stream, text string) devdash.DevEvent {
	event := devdash.DevEventFromOutput(appID, sessionID, devdash.DevSource{
		ID:     "process:123",
		Kind:   "process",
		Name:   "process",
		PID:    "123",
		Stream: stream,
	}, []byte(text), time.Date(2026, 6, 1, 12, 0, int(id), 0, time.UTC))
	event.ID = id
	return event
}
