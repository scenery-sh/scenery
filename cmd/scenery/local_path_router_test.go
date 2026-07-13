package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestLocalPathRouterShouldNotStripFrontendPrefix(t *testing.T) {
	t.Parallel()

	if localPathRouterShouldStripPrefix(localagent.RouteRecord{Kind: "frontend"}) {
		t.Fatal("frontend routes must preserve their base path for Vite and Astro dev servers")
	}
	if !localPathRouterShouldStripPrefix(localagent.RouteRecord{Name: "storage", Kind: "frontend", Backend: "storage"}) {
		t.Fatal("storage web UI must strip its route prefix before proxying assets")
	}
	if !localPathRouterShouldStripPrefix(localagent.RouteRecord{Kind: "api"}) {
		t.Fatal("non-frontend routes should keep the existing strip-prefix behavior")
	}
}

func TestLocalPathRouterRedirect(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := localPathRouterRedirect(next, "https://local.clean.tech")

	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/", want: "https://local.clean.tech/"},
		{path: "/api/healthy?probe=1", want: "https://local.clean.tech/api/healthy?probe=1"},
		{path: "/next/mails?mail=abc%2Fdef", want: "https://local.clean.tech/next/mails?mail=abc%2Fdef"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost:4748"+test.path, nil))
		if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != test.want {
			t.Errorf("%s response = %d %q, want %d %q", test.path, response.Code, response.Header().Get("Location"), http.StatusTemporaryRedirect, test.want)
		}
	}
}

func TestLocalPathRouterRedirectKeepsControlRoutesLocal(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := localPathRouterRedirect(next, "https://local.clean.tech")

	for _, path := range []string{
		"/console/",
		"/console/__scenery",
		"/runtime/health",
		"/__scenery",
		"/assets/index.js",
		"/site.webmanifest",
		"/favicon.ico",
		"/apple-touch-icon.png",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost:4748"+path, nil))
		if response.Code != http.StatusNoContent {
			t.Errorf("%s response = %d, want %d", path, response.Code, http.StatusNoContent)
		}
	}
}

func TestLocalPathRouterRewriteHTMLRootRefs(t *testing.T) {
	t.Parallel()

	body := []byte(`<script type="module" src="/@vite/client"></script><astro-island component-url="/@id/astro:scripts/before-hydration.js"></astro-island><link href="/blog"><img src="/profile.jpg">`)
	got := string(localPathRouterRewriteHTMLRootRefs(body, "/blog"))
	want := `<script type="module" src="/blog/@vite/client"></script><astro-island component-url="/blog/@id/astro:scripts/before-hydration.js"></astro-island><link href="/blog"><img src="/blog/profile.jpg">`
	if got != want {
		t.Fatalf("rewrite = %q, want %q", got, want)
	}
}

func TestLocalPathRouterRewriteJSRootRefs(t *testing.T) {
	t.Parallel()

	body := []byte(`import "/@vite/client";import { injectIntoGlobalHook } from "/@react-refresh";import("/src/main.tsx");const logo="/logo.png";const home="/";const api="/api/healthy";const path="/Users/me/app"`)
	got := string(localPathRouterRewriteJSRootRefs(body, "/blog"))
	want := `import "/blog/@vite/client";import { injectIntoGlobalHook } from "/blog/@react-refresh";import("/blog/src/main.tsx");const logo="/blog/logo.png";const home="/";const api="/api/healthy";const path="/Users/me/app"`
	if got != want {
		t.Fatalf("rewrite = %q, want %q", got, want)
	}
}

func TestLocalPathRouterRewriteJSRootRefsPreservesRouterInternals(t *testing.T) {
	t.Parallel()

	body := []byte(`return path === "/" ? path : path.replace(/^\/{1,}/, "");const result = cleanPath(baseSegments.join("/")) || "/"`)
	got := string(localPathRouterRewriteJSRootRefs(body, "/blog"))
	want := string(body)
	if got != want {
		t.Fatalf("rewrite = %q, want %q", got, want)
	}
}

func TestLocalPathRouterRewriteStorageRootRefs(t *testing.T) {
	t.Parallel()

	body := []byte("to:`/files`;to:`/dashboard`;to:`/terminal`;src:`/favicon.svg`;host}/ws/9p;replace(/^\\/files/,``)")
	got := string(localPathRouterRewriteStorageRootRefs(body, "/storage"))
	want := "to:`/storage/files`;to:`/storage/dashboard`;to:`/storage/terminal`;src:`/storage/favicon.svg`;host}/storage/ws/9p;replace(/^\\/storage\\/files/,``)"
	if got != want {
		t.Fatalf("rewrite = %q, want %q", got, want)
	}
}

func TestLocalPathRouterRewriteStorageAssetRefs(t *testing.T) {
	t.Parallel()

	body := []byte(`<script src="/storage/assets/index.js"></script><link href="/storage/assets/index.css"><img src="/storage/favicon.svg">`)
	got := string(localPathRouterRewriteStorageAssetRefs(body))
	want := `<script src="/storage/assets/index.js?scenery_path=storage_v2"></script><link href="/storage/assets/index.css?scenery_path=storage_v2"><img src="/storage/favicon.svg">`
	if got != want {
		t.Fatalf("rewrite = %q, want %q", got, want)
	}
}

func TestLocalPathRouterStorageAssetsStripPrefix(t *testing.T) {
	t.Parallel()

	var upstreamPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upstreamPath = req.URL.Path
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte("console.log('ok')"))
	}))
	defer upstream.Close()

	addr := upstream.Listener.Addr().String()
	session := localagent.Session{
		RouteManifest: localagent.RouteManifest{Routes: map[string]localagent.RouteRecord{
			"storage": {
				Name:        "storage",
				Kind:        "frontend",
				Path:        "/storage/",
				StripPrefix: "/storage",
				Backend:     "storage",
			},
		}},
		Backends: map[string]localagent.Backend{
			"storage": {Network: "tcp", Addr: addr},
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost:4747/storage/assets/app.js", nil)

	if !localPathRouterProxySessionBackend(rec, req, session, "/storage/assets/app.js", 4747, "http://localhost:4747") {
		t.Fatal("storage route was not proxied")
	}
	if upstreamPath != "/assets/app.js" {
		t.Fatalf("upstream path = %q, want /assets/app.js", upstreamPath)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/javascript" {
		t.Fatalf("content-type = %q", got)
	}
}

func TestLocalPathRouterStorageRootRedirectsToFiles(t *testing.T) {
	t.Parallel()

	session := localagent.Session{
		RouteManifest: localagent.RouteManifest{Routes: map[string]localagent.RouteRecord{
			"storage": {
				Name:        "storage",
				Kind:        "frontend",
				Path:        "/storage/",
				StripPrefix: "/storage",
				Backend:     "storage",
			},
		}},
		Backends: map[string]localagent.Backend{
			"storage": {Network: "tcp", Addr: "127.0.0.1:1"},
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost:4747/storage/", nil)

	if !localPathRouterProxySessionBackend(rec, req, session, "/storage", 4747, "http://localhost:4747") {
		t.Fatal("storage route was not handled")
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if got := rec.Header().Get("Location"); got != "/storage/files" {
		t.Fatalf("Location = %q, want /storage/files", got)
	}
}

func TestLocalPathRouterStorageWebSocketRoute(t *testing.T) {
	t.Parallel()

	var upstreamPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upstreamPath = req.URL.Path
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer upstream.Close()

	addr := upstream.Listener.Addr().String()
	session := localagent.Session{
		RouteManifest: localagent.RouteManifest{Routes: map[string]localagent.RouteRecord{
			"storage": {
				Name:        "storage",
				Kind:        "frontend",
				Path:        "/storage/",
				StripPrefix: "/storage",
				Backend:     "storage",
			},
		}},
		Backends: map[string]localagent.Backend{
			"storage": {Network: "tcp", Addr: addr},
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost:4747/ws/9p", nil)

	if !localPathRouterProxySessionBackend(rec, req, session, "/ws/9p", 4747, "http://localhost:4747") {
		t.Fatal("storage websocket route was not proxied")
	}
	if upstreamPath != "/ws/9p" {
		t.Fatalf("upstream path = %q, want /ws/9p", upstreamPath)
	}
}
