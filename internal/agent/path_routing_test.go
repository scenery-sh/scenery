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
	if got, want := session.RouteManifest.Routes[RouteDashboard].URL, "http://localhost:4001/console/"; got != want {
		t.Fatalf("dashboard route = %q, want %q", got, want)
	}
	if got, want := session.RouteManifest.Routes["ui"].StripPrefix, "/ui"; got != want {
		t.Fatalf("ui strip prefix = %q, want %q", got, want)
	}
	if got, want := session.RouteManifest.Routes["root"].Kind, "scenery-console"; got != want {
		t.Fatalf("root kind = %q, want %q", got, want)
	}
}

func TestPathRouteManifestRootFrontendIsCatchAllWithoutNamedMount(t *testing.T) {
	t.Parallel()

	session, err := NewSession(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		SessionID: "main",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
			"ui":     {Network: "tcp", Addr: "127.0.0.1:5173"},
		},
		RouteManifest: RouteManifest{
			Mode:    RouteModePath,
			BaseURL: "http://localhost:4001",
			Routes: map[string]RouteRecord{
				"root": {Name: "root", Kind: "frontend", URL: "http://localhost:4001/", Path: "/", Backend: "ui"},
			},
		},
	}, "127.0.0.1:9440", "http", nil)
	if err != nil {
		t.Fatal(err)
	}
	root := session.RouteManifest.Routes["root"]
	if root.Backend != "ui" || root.Kind != "frontend" || root.Path != "/" {
		t.Fatalf("root route = %+v", root)
	}
	if _, ok := session.RouteManifest.Routes["ui"]; ok {
		t.Fatalf("root frontend unexpectedly retained a named route: %+v", session.RouteManifest.Routes)
	}
	if got, ok := routeForPath(session.RouteManifest, "/projects/42"); !ok || got.Name != "root" {
		t.Fatalf("deep link route = %+v, ok=%v", got, ok)
	}
	if got, ok := routeForPath(session.RouteManifest, "/api/users"); !ok || got.Name != RouteAPI {
		t.Fatalf("api route = %+v, ok=%v", got, ok)
	}
	if got, ok := routeForPath(session.RouteManifest, PathModeDashboardPrefix+"/"); !ok || got.Name != RouteDashboard {
		t.Fatalf("dashboard route = %+v, ok=%v", got, ok)
	}
}

func TestProtectedRootFrontendLegacyPrefixUsesSegmentBoundary(t *testing.T) {
	t.Parallel()

	record := RouteRecord{Name: "root", Kind: "frontend", Path: "/", Backend: "web"}
	for path, want := range map[string]bool{
		"/web/api/users":        true,
		"/web/runtime":          true,
		"/web/__scenery/config": true,
		"/webapi/users":         false,
		"/webruntime":           false,
		"/web__scenery/config":  false,
		"/webapp/settings":      false,
		"/projects/42":          false,
	} {
		if got := isProtectedFrontendRoutePath(path, record); got != want {
			t.Errorf("%s protected = %v, want %v", path, got, want)
		}
	}
}

func TestPublicPathExposureUsesBestUnfilteredRoute(t *testing.T) {
	t.Parallel()

	manifest := RouteManifest{
		PublicRoutes: []string{"root"},
		Routes: map[string]RouteRecord{
			"root":    {Name: "root", Kind: "frontend", Path: "/", Backend: "web"},
			"admin":   {Name: "admin", Kind: "frontend", Path: "/admin/", Backend: "admin"},
			"console": {Name: RouteDashboard, Kind: "scenery-console", Path: "/console/", Backend: RouteDashboard},
		},
	}
	for path, want := range map[string]bool{
		"/":                 true,
		"/projects/42":      true,
		"/admin/":           false,
		"/admin/settings":   false,
		"/console/":         false,
		"/console/services": false,
	} {
		if got := publicPathExposed(manifest, path); got != want {
			t.Errorf("%s exposed = %v, want %v", path, got, want)
		}
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

	server, err := NewServer(RunOptions{
		Home:       t.TempDir(),
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
	if status != http.StatusOK || !strings.Contains(body, `"kind":"scenery.local.routes"`) || !strings.Contains(body, `"schema_revision":"sha256:`) || !strings.Contains(body, `"ui"`) {
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

func TestRegistryReopenPreservesSubstratesAndRoutesInProcess(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	registry, err := OpenRegistry(paths.RegistryPath, "127.0.0.1:4001")
	if err != nil {
		t.Fatal(err)
	}
	session, err := registry.Upsert(RegisterRequest{
		BaseAppID: "restart-test",
		AppRoot:   t.TempDir(),
		SessionID: "main",
		OwnerPID:  4100,
		Owner:     Owner{PID: 4100, StartedAt: "Tue Aug 25 10:30:00 2026", CmdlineHash: "session-owner"},
		Backends:  map[string]Backend{RouteAPI: {Network: "tcp", Addr: "127.0.0.1:9440"}},
		RouteManifest: RouteManifest{
			Mode:    RouteModePath,
			BaseURL: "http://localhost:4001",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]Owner{
		SubstratePostgres: {PID: 4101, StartedAt: "Tue Aug 25 10:30:01 2026", CmdlineHash: "postgres-owner"},
		SubstrateVictoria: {PID: 4102, StartedAt: "Tue Aug 25 10:30:02 2026", CmdlineHash: "victoria-owner"},
	}
	for kind, owner := range owners {
		if _, err := registry.UpsertSubstrate(UpsertSubstrateRequest{
			Kind: kind, Status: "ready", OwnerPID: owner.PID, Owner: owner,
			PIDs: map[string]int{"server": owner.PID}, Owners: map[string]Owner{"server": owner},
		}); err != nil {
			t.Fatal(err)
		}
	}

	restarted, err := OpenRegistry(paths.RegistryPath, "127.0.0.1:4001")
	if err != nil {
		t.Fatal(err)
	}
	for kind, owner := range owners {
		substrate, ok := restarted.GetSubstrate(kind)
		if !ok || substrate.OwnerPID != owner.PID || substrate.Owner.CmdlineHash != owner.CmdlineHash || substrate.PIDs["server"] != owner.PID {
			t.Fatalf("%s substrate after reopen = %+v, ok=%v", kind, substrate, ok)
		}
	}
	restored, ok := restarted.Get(session.SessionID)
	if !ok {
		t.Fatalf("session %q missing after registry reopen", session.SessionID)
	}
	route, ok := routeForPath(restored.RouteManifest, "/api/health")
	if !ok || route.Name != RouteAPI || restored.Backends[RouteAPI].Addr != "127.0.0.1:9440" {
		t.Fatalf("restored API route/session = %+v / %+v, ok=%v", route, restored, ok)
	}
}
