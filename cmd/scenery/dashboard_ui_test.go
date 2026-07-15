package main

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	dashboardstatic "scenery.sh/cmd/scenery/dashboard_static"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/victoria"
)

func TestDashboardServesUIAssetsFromOverrideDir(t *testing.T) {
	t.Setenv("SCENERY_DEV_DASHBOARD_UI_DIR", t.TempDir())
	uiDir := os.Getenv("SCENERY_DEV_DASHBOARD_UI_DIR")
	if err := os.WriteFile(filepath.Join(uiDir, "index.html"), []byte(`<!doctype html><html><body>ui shell __APP_ID__</body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(uiDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "assets", "app.js"), []byte(`console.log("scenery-ui")`), 0o644); err != nil {
		t.Fatal(err)
	}

	server := newTestDashboardServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/app-test", nil)
	server.handleRoot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if body := rec.Body.String(); body != "<!doctype html><html><body>ui shell app-test</body></html>" {
		t.Fatalf("unexpected index body: %q", body)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	server.handleRoot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected asset status: %d", rec.Code)
	}
	if body := rec.Body.String(); body != `console.log("scenery-ui")` {
		t.Fatalf("unexpected asset body: %q", body)
	}
}

func TestDashboardFallbackWhenUIDirMissing(t *testing.T) {
	t.Setenv("SCENERY_DEV_DASHBOARD_UI_DIR", filepath.Join(t.TempDir(), "missing"))
	server := newTestDashboardServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/app-test", nil)
	server.handleRoot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "dashboard UI build is not available") {
		t.Fatalf("unexpected fallback body: %q", body)
	}
}

// stubEmbeddedDashboardAssets swaps the embedded dashboard bundle for a fake
// so tests pass on fresh checkouts where dashboard_static/dist holds only the
// tracked placeholder. Callers must not use t.Parallel: the seam is a package
// global shared with every other test reading the bundle.
func stubEmbeddedDashboardAssets(t *testing.T, fsys fs.FS) {
	t.Helper()
	old := embeddedDashboardAssetFS
	embeddedDashboardAssetFS = func() fs.FS { return fsys }
	t.Cleanup(func() {
		embeddedDashboardAssetFS = old
	})
}

func TestDashboardServesEmbeddedUIAssets(t *testing.T) {
	stubEmbeddedDashboardAssets(t, fstest.MapFS{
		"index.html":    {Data: []byte(`<!doctype html><html><body>embedded __APP_ID__</body></html>`)},
		"assets/app.js": {Data: []byte(`console.log("embedded-dashboard")`)},
	})

	server := newTestDashboardServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/app-test", nil)
	server.handleRoot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if body := rec.Body.String(); body != "<!doctype html><html><body>embedded app-test</body></html>" {
		t.Fatalf("unexpected index body: %q", body)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	server.handleRoot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected asset status: %d", rec.Code)
	}
	if body := rec.Body.String(); body != `console.log("embedded-dashboard")` {
		t.Fatalf("unexpected asset body: %q", body)
	}
}

func TestDashboardResponsesIncludeBundleIdentity(t *testing.T) {
	stubEmbeddedDashboardAssets(t, fstest.MapFS{
		"index.html": {Data: []byte(`<!doctype html><html><head></head><body>bundle</body></html>`)},
	})

	server := newTestDashboardServer(t)
	server.assets = fstest.MapFS{
		"index.html": {Data: []byte(`<!doctype html><html><head></head><body>app __APP_ID__</body></html>`)},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/app-test", nil)
	server.handleRoot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	hash := rec.Header().Get("X-Scenery-Dashboard-Bundle-Hash")
	if hash == "" {
		t.Fatalf("missing dashboard bundle hash header: headers=%v", rec.Header())
	}
	if body := rec.Body.String(); !strings.Contains(body, `name="scenery-dashboard-bundle-hash" content="`+hash+`"`) {
		t.Fatalf("missing dashboard bundle meta tag: %s", body)
	}
}

func TestDashboardRealEmbeddedBundleHasIdentity(t *testing.T) {
	t.Parallel()

	if dashboardstatic.FS() == nil {
		t.Skip("no real dashboard bundle embedded in this checkout; run ./scripts/build-dashboard-ui-embed.sh to cover the real-bundle path")
	}
	hash, err := dashboardBundleHash(dashboardstatic.FS())
	if err != nil {
		t.Fatalf("hash real embedded bundle: %v", err)
	}
	if hash == "" {
		t.Fatal("real embedded bundle produced an empty hash")
	}
}

func TestDashboardProcessOutputListRPC(t *testing.T) {
	t.Parallel()

	server := newTestDashboardServer(t)
	ctx := context.Background()

	if err := server.supervisor.store.WriteProcessOutput(ctx, devdash.ProcessOutput{
		AppID:     "app-test",
		PID:       "100",
		Stream:    "stdout",
		Output:    []byte("first"),
		CreatedAt: time.Date(2026, time.April, 15, 8, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("write first output: %v", err)
	}
	if err := server.supervisor.store.WriteProcessOutput(ctx, devdash.ProcessOutput{
		AppID:     "app-test",
		PID:       "100",
		Stream:    "stderr",
		Output:    []byte("second"),
		CreatedAt: time.Date(2026, time.April, 15, 8, 0, 1, 0, time.UTC),
	}); err != nil {
		t.Fatalf("write second output: %v", err)
	}

	raw, err := json.Marshal(map[string]any{
		"app_id": "app-test",
		"limit":  10,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	result, err := server.dispatchRPC(ctx, "process/output/list", raw)
	if err != nil {
		t.Fatalf("dispatchRPC: %v", err)
	}
	items, ok := result.([]devdash.ProcessOutput)
	if !ok {
		t.Fatalf("unexpected result type: %T", result)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if string(items[0].Output) != "first" || string(items[1].Output) != "second" {
		t.Fatalf("unexpected output order: %+v", items)
	}
}

func TestDashboardAPICallRPCDialsUnixAppBackend(t *testing.T) {
	t.Parallel()

	server := newTestDashboardServer(t)
	socketDir, err := os.MkdirTemp("/tmp", "scenery-api-call-")
	if err != nil {
		t.Fatalf("mkdir temp socket dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(socketDir)
	})
	socketPath := filepath.Join(socketDir, "api.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})
	go func() {
		_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/platform.Stats" {
				t.Errorf("path = %s", r.URL.Path)
			}
			if r.Method != http.MethodGet {
				t.Errorf("method = %s", r.Method)
			}
			w.Header().Set("X-Scenery-Trace-Id", "trace-unix")
			_, _ = io.WriteString(w, `{"ok":true}`)
		}))
	}()
	upsertDashboardAPITestApp(t, server, socketPath)

	result := dispatchDashboardAPICall(t, server, map[string]any{
		"app_id":   "app-test",
		"service":  "platform",
		"endpoint": "Stats",
		"path":     "/platform.Stats",
		"method":   "GET",
	})
	if result["status_code"] != http.StatusOK {
		t.Fatalf("status_code = %v", result["status_code"])
	}
	if body := string(result["body"].([]byte)); body != `{"ok":true}` {
		t.Fatalf("body = %q", body)
	}
	if result["trace_id"] != "trace-unix" {
		t.Fatalf("trace_id = %v", result["trace_id"])
	}
}

func TestDashboardAPICallRPCDialsTCPAppBackend(t *testing.T) {
	t.Parallel()

	appServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service.Context" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("X-Scenery-Trace-Id", "trace-tcp")
		_, _ = io.WriteString(w, `{"message":"svc"}`)
	}))
	defer appServer.Close()

	server := newTestDashboardServer(t)
	upsertDashboardAPITestApp(t, server, strings.TrimPrefix(appServer.URL, "http://"))

	result := dispatchDashboardAPICall(t, server, map[string]any{
		"app_id":   "app-test",
		"service":  "service",
		"endpoint": "Context",
		"path":     "/service.Context",
		"method":   "GET",
	})
	if result["status_code"] != http.StatusOK {
		t.Fatalf("status_code = %v", result["status_code"])
	}
	if body := string(result["body"].([]byte)); body != `{"message":"svc"}` {
		t.Fatalf("body = %q", body)
	}
	if result["trace_id"] != "trace-tcp" {
		t.Fatalf("trace_id = %v", result["trace_id"])
	}
}

func TestDashboardLogsListRPCUsesVictoriaLogs(t *testing.T) {
	t.Parallel()

	logsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/select/logsql/query" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		query := r.Form.Get("query")
		for _, want := range []string{
			`scenery_app_id="app-test"`,
			`scenery_session_id="session-a"`,
			`scenery_dev_kind="` + devdash.DevEventKind + `"`,
			`scenery_dev_schema_revision="` + devdash.DevEventSchemaRevision + `"`,
		} {
			if !strings.Contains(query, want) {
				t.Fatalf("query %q does not contain %q", query, want)
			}
		}
		_, _ = io.WriteString(w, `{"_msg":"INFO ready","created_at":"2026-05-31T12:44:01.223Z","scenery_dev_kind":"`+devdash.DevEventKind+`","scenery_dev_schema_revision":"`+devdash.DevEventSchemaRevision+`","scenery_app_id":"app-test","scenery_session_id":"session-a","id":"42","source_id":"api","source_kind":"app","source_name":"api","source_stream":"stdout","level":"info","fields_json":"{}","raw":"INFO ready","parse_format":"raw","parse_ok":"false"}`+"\n")
	}))
	defer logsServer.Close()

	server := newTestDashboardServer(t)
	server.supervisor.victoria = victoria.NewStack(victoria.ExternalComponent{Name: "logs", BaseURL: logsServer.URL})
	if err := server.supervisor.store.UpsertApp(context.Background(), devdash.AppRecord{
		ID:        "app-test",
		BaseAppID: "app-test",
		SessionID: "session-a",
		Name:      "app-test",
		Root:      "/tmp/app-test",
		Running:   true,
	}); err != nil {
		t.Fatalf("upsert app: %v", err)
	}

	raw, err := json.Marshal(map[string]any{
		"app_id": "app-test",
		"limit":  10,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	result, err := server.dispatchRPC(context.Background(), "logs/list", raw)
	if err != nil {
		t.Fatalf("dispatchRPC: %v", err)
	}
	items, ok := result.([]dashboardLogEvent)
	if !ok {
		t.Fatalf("unexpected result type: %T", result)
	}
	if len(items) != 1 || items[0].ID != 42 || items[0].Source.ID != "api" || items[0].Message != "INFO ready" || items[0].Time == "" {
		t.Fatalf("unexpected log events: %+v", items)
	}
}

func upsertDashboardAPITestApp(t *testing.T, server *dashboardServer, listenAddr string) {
	t.Helper()
	if err := server.supervisor.store.UpsertApp(context.Background(), devdash.AppRecord{
		ID:         "app-test",
		BaseAppID:  "app-test",
		SessionID:  "session-a",
		Name:       "app-test",
		Root:       t.TempDir(),
		ListenAddr: listenAddr,
		Running:    true,
	}); err != nil {
		t.Fatalf("upsert app: %v", err)
	}
}

func dispatchDashboardAPICall(t *testing.T, server *dashboardServer, params map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	result, err := server.dispatchRPC(context.Background(), "api-call", raw)
	if err != nil {
		t.Fatalf("dispatchRPC api-call: %v", err)
	}
	body, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %T", result)
	}
	return body
}

func TestDashboardLogsListRPCWithoutVictoriaReturnsEmpty(t *testing.T) {
	t.Parallel()

	server := newTestDashboardServer(t)
	raw, err := json.Marshal(map[string]any{
		"app_id": "app-test",
		"limit":  10,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	result, err := server.dispatchRPC(context.Background(), "logs/list", raw)
	if err != nil {
		t.Fatalf("dispatchRPC: %v", err)
	}
	items, ok := result.([]dashboardLogEvent)
	if !ok {
		t.Fatalf("unexpected result type: %T", result)
	}
	if len(items) != 0 {
		t.Fatalf("expected no log events, got %+v", items)
	}
}
