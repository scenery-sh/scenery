package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPathRouteManifestForSession(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "main",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
			"ui":     {Network: "tcp", Addr: "127.0.0.1:5173"},
		},
		RouteManifest: RouteManifest{
			Mode:    RouteModePath,
			BaseURL: "http://localhost:4001/",
		},
	}, "127.0.0.1:9440", "http", nil)
	if err != nil {
		t.Fatal(err)
	}
	if session.RouteManifest.Mode != RouteModePath {
		t.Fatalf("mode = %q", session.RouteManifest.Mode)
	}
	if got, want := session.RouteManifest.BaseURL, "http://localhost:4001"; got != want {
		t.Fatalf("base url = %q, want %q", got, want)
	}
	if got, want := session.RouteManifest.Routes[RouteAPI].URL, "http://localhost:4001/api/"; got != want {
		t.Fatalf("api route = %q, want %q", got, want)
	}
	if got, want := session.RouteManifest.Routes[RouteDashboard].URL, "http://localhost:4001/consolenext/"; got != want {
		t.Fatalf("dashboard route = %q, want %q", got, want)
	}
	if got, want := session.RouteManifest.Routes["ui"].StripPrefix, "/ui"; got != want {
		t.Fatalf("ui strip prefix = %q, want %q", got, want)
	}
	if got, want := session.RouteManifest.Routes["root"].Kind, "scenery-console"; got != want {
		t.Fatalf("root kind = %q, want %q", got, want)
	}
}

func TestPathProxyOptionsPreserveFrontendPrefix(t *testing.T) {
	t.Parallel()

	session := Session{RouteManifest: RouteManifest{BaseURL: "http://localhost:4747"}}
	frontend := pathProxyOptions(session, RouteRecord{
		Name:        "storage",
		Kind:        "frontend",
		Path:        "/storage/",
		StripPrefix: "/storage",
	})
	if frontend.stripPrefix != "" {
		t.Fatalf("frontend stripPrefix = %q, want empty", frontend.stripPrefix)
	}
	api := pathProxyOptions(session, RouteRecord{
		Name:        "api",
		Kind:        "api",
		Path:        "/api/",
		StripPrefix: "/api",
	})
	if api.stripPrefix != "/api" {
		t.Fatalf("api stripPrefix = %q, want /api", api.stripPrefix)
	}
}

func TestShouldRedirectPathPrefixPreservesTrailingSlash(t *testing.T) {
	t.Parallel()

	record := RouteRecord{Path: "/storage/"}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/storage/", nil)
	if shouldRedirectPathPrefix(req, record) {
		t.Fatal("already-slashed route path should not redirect")
	}
	req = httptest.NewRequest(http.MethodGet, "http://localhost/storage", nil)
	if !shouldRedirectPathPrefix(req, record) {
		t.Fatal("unslashed route path should redirect")
	}
}

func TestRewriteHTMLRootRefs(t *testing.T) {
	t.Parallel()

	body := []byte(`<script src="/assets/app.js"></script><a href="/storage/">Storage</a><img src="/favicon.svg">`)
	got := string(rewriteHTMLRootRefs(body, "/storage"))
	want := `<script src="/storage/assets/app.js"></script><a href="/storage/">Storage</a><img src="/storage/favicon.svg">`
	if got != want {
		t.Fatalf("rewrite = %q, want %q", got, want)
	}
}

func TestServerPathModeRoutesByTrustedSessionHeader(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/", "/v1/users":
			_, _ = io.WriteString(w, "api:"+req.URL.Path)
		case "/__scenery/config":
			_, _ = io.WriteString(w, "config ok")
		default:
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	apiAddr := strings.TrimPrefix(api.URL, "http://")

	var frontendHits []string
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		frontendHits = append(frontendHits, req.URL.Path)
		switch req.URL.Path {
		case "/":
			_, _ = io.WriteString(w, "frontend shell")
		case "/assets/app.js":
			_, _ = io.WriteString(w, "asset ok")
		default:
			http.NotFound(w, req)
		}
	}))
	defer frontend.Close()
	frontendAddr := strings.TrimPrefix(frontend.URL, "http://")

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(RunOptions{
		SocketPath: paths.SocketPath,
		RouterAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	server.edgeToken = "test-token"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer stopTestAgent(t, cancel, done)

	client := NewClient(server.paths.SocketPath)
	if err := waitForAgentPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		Branch:    "feature/path-routing",
		OwnerPID:  os.Getpid(),
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: apiAddr},
			"ui":     {Network: "tcp", Addr: frontendAddr},
		},
		RouteManifest: RouteManifest{
			Mode:    RouteModePath,
			BaseURL: "http://localhost:4001",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	request := func(method, targetPath, accept string, trusted bool) (int, string, http.Header) {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+server.routerAddr+targetPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "localhost:4001"
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		req.Header.Set("X-Scenery-Session", session.SessionID)
		req.Header.Set("X-Scenery-Local-Route-Mode", string(RouteModePath))
		if trusted {
			req.Header.Set("X-Scenery-Edge-Token", "test-token")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(body), resp.Header
	}

	status, body, _ := request(http.MethodGet, "/", "text/html", true)
	if status != http.StatusOK || !strings.Contains(body, "demo") || !strings.Contains(body, "/api/") || !strings.Contains(body, "/ui/") {
		t.Fatalf("root status=%d body=%q", status, body)
	}
	status, body, _ = request(http.MethodGet, PathModeRuntimePrefix+"/health", "", true)
	if status != http.StatusOK || !strings.Contains(body, `"base_url":"http://localhost:4001"`) {
		t.Fatalf("health status=%d body=%q", status, body)
	}
	status, body, _ = request(http.MethodGet, PathModeRuntimePrefix+"/routes", "", true)
	if status != http.StatusOK || !strings.Contains(body, `"schema_version":"scenery.local.routes.v1"`) || !strings.Contains(body, `"ui"`) {
		t.Fatalf("routes status=%d body=%q", status, body)
	}
	status, body, _ = request(http.MethodGet, "/api/v1/users", "", true)
	if status != http.StatusOK || body != "api:/v1/users" {
		t.Fatalf("api status=%d body=%q", status, body)
	}
	status, body, _ = request(http.MethodPost, "/api", "", true)
	if status != http.StatusOK || body != "api:/" {
		t.Fatalf("api root status=%d body=%q", status, body)
	}
	status, body, _ = request(http.MethodGet, "/ui/settings", "text/html", true)
	if status != http.StatusOK || body != "frontend shell" {
		t.Fatalf("ui deep link status=%d body=%q", status, body)
	}
	if strings.Join(frontendHits, ",") != "/ui/settings,/" {
		t.Fatalf("frontend hits = %q", strings.Join(frontendHits, ","))
	}
	status, body, _ = request(http.MethodGet, PathModeRuntimePrefix+"/config", "", true)
	if status != http.StatusOK || body != "config ok" {
		t.Fatalf("config status=%d body=%q", status, body)
	}
	status, _, _ = request(http.MethodGet, "/__scenery/health", "", true)
	if status != http.StatusNotFound {
		t.Fatalf("legacy control path status=%d, want 404", status)
	}
	status, body, _ = request(http.MethodGet, "/unknown", "", true)
	if status != http.StatusNotFound || !strings.Contains(body, "Available routes") {
		t.Fatalf("unknown status=%d body=%q", status, body)
	}
	status, _, _ = request(http.MethodGet, "/api/v1/users", "", false)
	if status != http.StatusNotFound {
		t.Fatalf("spoofed request status=%d, want 404", status)
	}
}
