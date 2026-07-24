package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"scenery.sh/internal/app"
)

func TestManagedFrontendBuildCommandUsesViteLocalBin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontendPackage(t, root, `{"scripts":{"dev":"vite","build":"vite build"}}`)
	bin := writeFrontendBin(t, root, "vite")
	cmd, args, err := managedFrontendBuildCommand(root, "/web")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != bin {
		t.Fatalf("command = %q, want %q", cmd, bin)
	}
	wantArgs := []string{"build", "--base", "/web/"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestManagedRootFrontendBuildOmitsViteBaseFlag(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontendPackage(t, root, `{"scripts":{"dev":"vite","build":"vite build"}}`)
	bin := writeFrontendBin(t, root, "vite")
	cmd, args, err := managedFrontendBuildCommand(root, "/")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != bin {
		t.Fatalf("command = %q, want %q", cmd, bin)
	}
	if !reflect.DeepEqual(args, []string{"build"}) {
		t.Fatalf("args = %#v, want build without --base", args)
	}
}

func TestManagedFrontendBuildCommandFallsBackToPackageManager(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontendPackage(t, root, `{"scripts":{"build":"vite build"}}`)
	if err := os.WriteFile(filepath.Join(root, "bun.lock"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, args, err := managedFrontendBuildCommand(root, "/web")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "bun" {
		t.Fatalf("command = %q, want bun", cmd)
	}
	wantArgs := []string{"run", "build", "--base", "/web/"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestManagedFrontendBuildCommandRequiresBuildScript(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontendPackage(t, root, `{"scripts":{"dev":"vite"}}`)
	if _, _, err := managedFrontendBuildCommand(root, "/web"); err == nil || !strings.Contains(err.Error(), "no build script") {
		t.Fatalf("err = %v", err)
	}
}

func TestStaticFrontendServerHandler(t *testing.T) {
	t.Parallel()

	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>app</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "app.js"), []byte("js"), 0o644); err != nil {
		t.Fatal(err)
	}
	static := &staticFrontendServer{Name: "web", Dir: dist, BasePath: "/web"}
	handler := static.handler()

	request := func(method, target string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, target, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Result().Body)
		return rec.Code, string(body)
	}

	if status, _ := request(http.MethodGet, "/web/"); status != http.StatusServiceUnavailable {
		t.Fatalf("before build status = %d, want 503", status)
	}
	static.ready.Store(true)

	if status, body := request(http.MethodGet, "/web/"); status != http.StatusOK || !strings.Contains(body, "app") {
		t.Fatalf("index status=%d body=%q", status, body)
	}
	if status, body := request(http.MethodGet, "/web/assets/app.js"); status != http.StatusOK || body != "js" {
		t.Fatalf("asset status=%d body=%q", status, body)
	}
	if status, body := request(http.MethodGet, "/web/settings/profile"); status != http.StatusOK || !strings.Contains(body, "app") {
		t.Fatalf("spa fallback status=%d body=%q", status, body)
	}
	if status, _ := request(http.MethodGet, "/web/assets/missing.js"); status != http.StatusNotFound {
		t.Fatalf("missing asset status=%d, want 404", status)
	}
	if status, _ := request(http.MethodPost, "/web/"); status != http.StatusMethodNotAllowed {
		t.Fatalf("post status=%d, want 405", status)
	}
}

func TestValidateFrontendServeModes(t *testing.T) {
	t.Parallel()

	cfg := app.Config{Frontends: map[string]app.FrontendConfig{
		"next": {Serve: "Production"},
		"ui":   {},
	}}
	if err := validateFrontendServeModes(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Frontends["blog"] = app.FrontendConfig{Serve: "prod"}
	if err := validateFrontendServeModes(cfg); err == nil || !strings.Contains(err.Error(), "frontends.blog.serve") {
		t.Fatalf("err = %v", err)
	}
}

func TestSplitProductionFrontendPaths(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{Frontends: map[string]app.FrontendConfig{
		"blog": {Root: "apps/blog", Serve: "production"},
		"next": {Root: "apps/next"},
	}}
	setProductionFrontendWatch(root, cfg)
	defer setProductionFrontendWatch(root, app.Config{})

	names, appPaths := splitProductionFrontendPaths(root, []string{
		"apps/blog/src/page.astro",
		"apps/blog/" + testPackageFilename,
		"apps/next/src/App.tsx",
		"internal/service/service.go",
	})
	if len(names) != 1 || names[0] != "blog" {
		t.Fatalf("names = %v", names)
	}
	wantApp := []string{"apps/blog/" + testPackageFilename, "apps/next/src/App.tsx", "internal/service/service.go"}
	if !reflect.DeepEqual(appPaths, wantApp) {
		t.Fatalf("appPaths = %v, want %v", appPaths, wantApp)
	}

	if name, ok := productionFrontendForWatchPath(root, "apps/blog/dist/index.html"); ok {
		t.Fatalf("dist output classified as source for %q", name)
	}
}
