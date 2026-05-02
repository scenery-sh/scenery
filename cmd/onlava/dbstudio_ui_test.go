package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDBStudioUIBuildStaleWhenDistMissing(t *testing.T) {
	uiRoot := t.TempDir()
	writeDashboardUITestFile(t, uiRoot, "package.json", "{}")
	writeDashboardUITestFile(t, uiRoot, "src/main.tsx", "export {}")

	stale, err := dbStudioUIBuildStale(uiRoot)
	if err != nil {
		t.Fatalf("dbStudioUIBuildStale returned error: %v", err)
	}
	if !stale {
		t.Fatal("expected DB Studio UI build to be stale when dist/index.html is missing")
	}
}

func TestDBStudioUIServesAssetsAndFallsBackToIndex(t *testing.T) {
	uiDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(uiDir, "index.html"), []byte(`<!doctype html><html><head><script defer="defer" data-site-id="local.drizzle.studio" src="https://assets.onedollarstats.com/stonks.js"></script></head><body>db studio</body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(uiDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "assets", "app.js"), []byte(`console.log("dbstudio")`), 0o644); err != nil {
		t.Fatal(err)
	}

	server := newDBStudioUIServer(uiDir)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	server.handle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != `console.log("dbstudio")` {
		t.Fatalf("asset body = %q", body)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/schemas/public/users", nil)
	server.handle(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "db studio") {
		t.Fatalf("index body = %q", body)
	}
	if strings.Contains(body, "stonks.js") {
		t.Fatalf("index body unexpectedly contains analytics script: %q", body)
	}
}

func TestDBStudioUIDepsStaleWhenPackageManifestIsNewer(t *testing.T) {
	uiRoot := t.TempDir()
	nodeModulesPath := filepath.Join(uiRoot, "node_modules")
	if err := os.MkdirAll(nodeModulesPath, 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	pkgPath := writeDashboardUITestFile(t, uiRoot, "package.json", "{}")

	now := time.Now()
	if err := os.Chtimes(nodeModulesPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("set node_modules times: %v", err)
	}
	if err := os.Chtimes(pkgPath, now, now); err != nil {
		t.Fatalf("set package times: %v", err)
	}

	stale, err := dbStudioUIDepsStale(uiRoot)
	if err != nil {
		t.Fatalf("dbStudioUIDepsStale returned error: %v", err)
	}
	if !stale {
		t.Fatal("expected DB Studio UI deps to be stale when package.json is newer than node_modules")
	}
}

func TestDBStudioUIURL(t *testing.T) {
	got := dbStudioUIURL(4002)
	want := "http://127.0.0.1:4003/?port=4002"
	if got != want {
		t.Fatalf("dbStudioUIURL(4002) = %q, want %q", got, want)
	}
}

func TestStripDBStudioAnalytics(t *testing.T) {
	input := []byte(`<!doctype html><script defer="defer" data-site-id="local.drizzle.studio" src="https://assets.onedollarstats.com/stonks.js"></script><body></body>`)
	got := string(stripDBStudioAnalytics(input))
	if strings.Contains(got, "stonks.js") {
		t.Fatalf("stripDBStudioAnalytics() left analytics script in %q", got)
	}
}

func TestPatchDBStudioHTMLHidesSupportUI(t *testing.T) {
	input := []byte(`<!doctype html><html><head></head><body>db studio</body></html>`)
	got := string(patchDBStudioHTML(input))
	if !strings.Contains(got, `[aria-label="Support Drizzle"]`) {
		t.Fatalf("patchDBStudioHTML() missing support-ui hide rule in %q", got)
	}
	if !strings.Contains(got, `MutationObserver(removeSupportDrizzle)`) {
		t.Fatalf("patchDBStudioHTML() missing support-ui cleanup script in %q", got)
	}
}

func TestPatchDBStudioAssetNoOp(t *testing.T) {
	input := []byte(`console.log("dbstudio")`)
	got := patchDBStudioAsset("index.js", input)
	if string(got) != string(input) {
		t.Fatalf("patchDBStudioAsset() changed asset unexpectedly: %q", string(got))
	}
}
