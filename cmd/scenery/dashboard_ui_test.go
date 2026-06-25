package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"scenery.sh/internal/devdash"
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

func TestDashboardServesEmbeddedUIAssets(t *testing.T) {
	old := embeddedDashboardAssetFS
	embeddedDashboardAssetFS = func() fs.FS {
		return fstest.MapFS{
			"index.html":    {Data: []byte(`<!doctype html><html><body>embedded __APP_ID__</body></html>`)},
			"assets/app.js": {Data: []byte(`console.log("embedded-dashboard")`)},
		}
	}
	t.Cleanup(func() {
		embeddedDashboardAssetFS = old
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
