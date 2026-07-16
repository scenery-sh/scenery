package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"syscall"
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

func TestLocalPathRouterWithoutValidatedDomainDoesNotRedirect(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	response := httptest.NewRecorder()
	localPathRouterRedirect(next, "").ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost:4748/platform/", nil))
	if response.Code != http.StatusNoContent || response.Header().Get("Location") != "" {
		t.Fatalf("response = %d location %q", response.Code, response.Header().Get("Location"))
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

func TestReverseProxyForLocalBackendReusesUnixTransport(t *testing.T) {
	backend := localagent.Backend{Network: "unix", Addr: "/tmp/scenery-lpr-test.sock"}

	first := reverseProxyForLocalBackend(backend)
	second := reverseProxyForLocalBackend(backend)
	if first.Transport == nil || second.Transport == nil {
		t.Fatal("unix backend proxy has no transport")
	}
	if first.Transport != second.Transport {
		t.Fatal("unix backend produced distinct transports; per-request allocation leaks goroutines and FDs")
	}
	tr, ok := first.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unix transport type = %T, want *http.Transport", first.Transport)
	}
	if tr.IdleConnTimeout == 0 {
		t.Fatal("cached unix transport has no IdleConnTimeout; idle connections never reap")
	}

	// A TCP backend leaves the default transport in place (proxy.Transport nil).
	if tcp := reverseProxyForLocalBackend(localagent.Backend{Network: "tcp", Addr: "127.0.0.1:4000"}); tcp.Transport != nil {
		t.Fatal("tcp backend should not install a custom transport")
	}
}

func TestLocalRequestRetryablePolicy(t *testing.T) {
	t.Parallel()

	dialErr := &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	resetErr := &net.OpError{Op: "read", Err: syscall.ECONNRESET}
	body := func(req *http.Request) *http.Request {
		req.ContentLength = 5
		return req
	}
	cases := []struct {
		name string
		req  *http.Request
		err  error
		want bool
	}{
		{"GET after reset", httptest.NewRequest(http.MethodGet, "/", nil), resetErr, true},
		{"HEAD after eof", httptest.NewRequest(http.MethodHead, "/", nil), io.EOF, true},
		{"POST after reset", httptest.NewRequest(http.MethodPost, "/", nil), resetErr, false},
		{"DELETE after eof", httptest.NewRequest(http.MethodDelete, "/", nil), io.EOF, false},
		{"PUT after reset", httptest.NewRequest(http.MethodPut, "/", nil), resetErr, false},
		{"POST after dial failure", httptest.NewRequest(http.MethodPost, "/", nil), dialErr, true},
		{"DELETE after dial failure", httptest.NewRequest(http.MethodDelete, "/", nil), dialErr, true},
		{"GET with body after dial failure", body(httptest.NewRequest(http.MethodGet, "/", nil)), dialErr, false},
		{"POST with body after dial failure", body(httptest.NewRequest(http.MethodPost, "/", nil)), dialErr, false},
	}
	for _, tc := range cases {
		if got := localRequestRetryable(tc.req, tc.err); got != tc.want {
			t.Errorf("%s: localRequestRetryable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// countingCloseOnceBackend serves a real listener whose first request is
// consumed and then dropped without a response — the exact failure a
// supervised backend restart produces on an established connection.
func countingCloseOnceBackend(t *testing.T, count *int32) *httptest.Server {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if atomic.AddInt32(count, 1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)
	return backend
}

func newRetryHandlerForBackend(t *testing.T, backend *httptest.Server) http.Handler {
	t.Helper()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	return newLocalDialRetryHandler(func(localagent.Backend) *httputil.ReverseProxy {
		// A fresh transport per attempt so keep-alive pooling never masks the
		// mid-request failure with net/http's own idempotent retry.
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.Transport = &http.Transport{}
		return proxy
	}, nil)
}

func TestLocalDialRetryHandlerNeverReplaysMutationAfterMidRequestFailure(t *testing.T) {
	t.Parallel()

	var count int32
	backend := countingCloseOnceBackend(t, &count)
	handler := newRetryHandlerForBackend(t, backend)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mutate", nil))

	if got := atomic.LoadInt32(&count); got != 1 {
		t.Fatalf("backend received %d requests, want exactly 1: a mid-request failure must never replay a mutation", got)
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

func TestLocalDialRetryHandlerRetriesSafeMethodAfterMidRequestFailure(t *testing.T) {
	t.Parallel()

	var count int32
	backend := countingCloseOnceBackend(t, &count)
	handler := newRetryHandlerForBackend(t, backend)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/page", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := atomic.LoadInt32(&count); got != 2 {
		t.Fatalf("backend received %d requests, want 2 (one failed, one retried)", got)
	}
}

func TestIsLocalBackendUnavailableMatchesReusedDeadConn(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"dial", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{"reset reused conn", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, true},
		{"broken pipe", &net.OpError{Op: "write", Err: syscall.EPIPE}, true},
		{"eof", io.EOF, true},
		{"unrelated read", &net.OpError{Op: "read", Err: errors.New("boom")}, false},
	}
	for _, tc := range cases {
		if got := isLocalBackendUnavailable(tc.err); got != tc.want {
			t.Errorf("%s: isLocalBackendUnavailable = %v, want %v", tc.name, got, tc.want)
		}
	}
}
