package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func startDashboardTestBackend(t *testing.T, body string) (*httptest.Server, localagent.Backend) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, "%s:%s", body, req.URL.Path)
	}))
	t.Cleanup(server.Close)
	return server, localagent.Backend{Network: "tcp", Addr: strings.TrimPrefix(server.URL, "http://")}
}

func localPathRouterTestGet(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

// TestLocalPathRouterDashboardFollowsAgentRestart proves the availability
// contract from the 2026-07-14 incident: after an agent restart moves the
// dashboard to a new loopback address, existing local path routers must stop
// proxying to the dead old backend and follow the current one, and a dead
// backend answers with a terse 502 instead of the default proxy error.
func TestLocalPathRouterDashboardFollowsAgentRestart(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	fake := startFakeAgentHealthServer(t, paths.SocketPath, 111)
	_, backendA := startDashboardTestBackend(t, "dash-a")
	fake.setDashboard(backendA)

	oldBudget, oldInterval := localProxyDialRetryBudget, localProxyDialRetryInterval
	t.Cleanup(func() {
		localProxyDialRetryBudget, localProxyDialRetryInterval = oldBudget, oldInterval
	})
	localProxyDialRetryBudget = 400 * time.Millisecond
	localProxyDialRetryInterval = 50 * time.Millisecond

	port, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	session := localagent.Session{
		SessionID: "sess-1",
		AppRoot:   t.TempDir(),
		StateRoot: stateRoot,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanup, err := startLocalPathRouter(ctx, localPathRouterOptions{
		Session:          session,
		PortLease:        localagent.PortLease{Port: port, URL: fmt.Sprintf("http://localhost:%d", port)},
		EdgeToken:        "test-token",
		UpstreamAddr:     "127.0.0.1:1",
		DashboardBackend: backendA,
	})
	if err != nil {
		t.Fatalf("startLocalPathRouter: %v", err)
	}
	defer cleanup()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	statusCode, body := localPathRouterTestGet(t, baseURL+localagent.PathModeDashboardPrefix+"/overview")
	if statusCode != http.StatusOK || !strings.Contains(body, "dash-a:/overview") {
		t.Fatalf("dashboard via backend A = %d %q", statusCode, body)
	}

	// Simulate a supervised agent restart: the dashboard moves to a new
	// loopback address and only agent health knows the new one.
	serverB, backendB := startDashboardTestBackend(t, "dash-b")
	fake.setDashboard(backendB)
	statusCode, body = localPathRouterTestGet(t, baseURL+localagent.PathModeDashboardPrefix+"/overview")
	if statusCode != http.StatusOK || !strings.Contains(body, "dash-b:/overview") {
		t.Fatalf("dashboard after backend change = %d %q", statusCode, body)
	}

	// A dead current backend must answer with the terse scenery 502, not the
	// default httputil proxy error, once the bounded dial retry is exhausted.
	serverB.Close()
	statusCode, body = localPathRouterTestGet(t, baseURL+localagent.PathModeDashboardPrefix+"/overview")
	if statusCode != http.StatusBadGateway {
		t.Fatalf("dead dashboard backend status = %d %q", statusCode, body)
	}
	if !strings.Contains(body, "scenery: dashboard backend") || strings.Contains(body, "proxy error") {
		t.Fatalf("dead dashboard backend body = %q", body)
	}
}

func TestLocalPathRouterRootFrontendOwnsRootAssets(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	fake := startFakeAgentHealthServer(t, paths.SocketPath, 111)

	dashboardServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<script src="/assets/dashboard.js"></script>`)
		case "/assets/dashboard.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, "dashboard-js")
		default:
			http.NotFound(w, req)
		}
	}))
	defer dashboardServer.Close()
	dashboard := localagent.Backend{Network: "tcp", Addr: strings.TrimPrefix(dashboardServer.URL, "http://")}
	fake.setDashboard(dashboard)

	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<script src="/assets/app.js"></script>`)
		case "/assets/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, "frontend-js")
		case "/favicon.ico":
			w.Header().Set("Content-Type", "image/x-icon")
			fmt.Fprint(w, "frontend-icon")
		default:
			http.NotFound(w, req)
		}
	}))
	defer frontend.Close()

	port, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	session := localagent.Session{
		SessionID: "root-assets",
		AppRoot:   t.TempDir(),
		StateRoot: t.TempDir(),
		RouteManifest: localagent.RouteManifest{
			Mode:    localagent.RouteModePath,
			BaseURL: fmt.Sprintf("http://localhost:%d", port),
			Routes: map[string]localagent.RouteRecord{
				"root": {
					Name: "root", Kind: "frontend", Path: "/", Backend: "web",
				},
			},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanup, err := startLocalPathRouter(ctx, localPathRouterOptions{
		Session:          session,
		PortLease:        localagent.PortLease{Port: port, URL: session.RouteManifest.BaseURL},
		EdgeToken:        "test-token",
		UpstreamAddr:     strings.TrimPrefix(frontend.URL, "http://"),
		DashboardBackend: dashboard,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	for path, want := range map[string]string{
		"/":                            "/assets/app.js",
		"/assets/app.js":               "frontend-js",
		"/favicon.ico":                 "frontend-icon",
		"/console/":                    "/console/assets/dashboard.js",
		"/console/assets/dashboard.js": "dashboard-js",
	} {
		status, body := localPathRouterTestGet(t, baseURL+path)
		if status != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("%s = %d %q, want 200 containing %q", path, status, body, want)
		}
	}
}

// TestLocalPathRouterUpstreamDialRetryBridgesRestart proves a briefly-down
// agent router upstream is bridged by the bounded dial retry instead of
// answering 502 immediately.
func TestLocalPathRouterUpstreamDialRetryBridgesRestart(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	fake := startFakeAgentHealthServer(t, paths.SocketPath, 111)
	_, dashboard := startDashboardTestBackend(t, "dash")
	fake.setDashboard(dashboard)

	oldBudget, oldInterval := localProxyDialRetryBudget, localProxyDialRetryInterval
	t.Cleanup(func() {
		localProxyDialRetryBudget, localProxyDialRetryInterval = oldBudget, oldInterval
	})
	localProxyDialRetryBudget = 2 * time.Second
	localProxyDialRetryInterval = 50 * time.Millisecond

	upstreamPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	upstreamAddr := fmt.Sprintf("127.0.0.1:%d", upstreamPort)
	port, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	session := localagent.Session{SessionID: "sess-2", AppRoot: t.TempDir(), StateRoot: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanup, err := startLocalPathRouter(ctx, localPathRouterOptions{
		Session:          session,
		PortLease:        localagent.PortLease{Port: port, URL: fmt.Sprintf("http://localhost:%d", port)},
		EdgeToken:        "test-token",
		UpstreamAddr:     upstreamAddr,
		DashboardBackend: dashboard,
	})
	if err != nil {
		t.Fatalf("startLocalPathRouter: %v", err)
	}
	defer cleanup()

	// The upstream comes up only after the first dial attempts have failed,
	// like an agent router during a supervised restart.
	go func() {
		time.Sleep(300 * time.Millisecond)
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
			fmt.Fprint(w, "upstream-ok")
		})
		server := &http.Server{Addr: upstreamAddr, Handler: mux}
		go func() { _ = server.ListenAndServe() }()
		<-ctx.Done()
		_ = server.Close()
	}()

	statusCode, body := localPathRouterTestGet(t, fmt.Sprintf("http://127.0.0.1:%d/api/anything", port))
	if statusCode != http.StatusOK || body != "upstream-ok" {
		t.Fatalf("upstream retry result = %d %q", statusCode, body)
	}
}
