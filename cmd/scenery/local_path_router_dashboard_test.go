package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func startDashboardTestBackend(t *testing.T, body string) (*httptest.Server, localagent.Backend) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprintf(w, "%s:%s?%s", body, req.URL.Path, req.URL.RawQuery)
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
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

// Native agent health, Unix transport and router lifecycle run in dev-process.
// Keep backend replacement and bounded failure policy directly covered in process.
func TestLocalPathRouterDashboardFollowsAgentRestart(t *testing.T) {
	source := newDashboardBackendSource(nil, localagent.Backend{Network: "tcp", Addr: "dash-a"})
	clock := time.Unix(0, 0)
	waits := 0
	dead := false
	handler := newLocalDialRetryHandler(func(backend localagent.Backend) *httputil.ReverseProxy {
		proxy := reverseProxyForLocalBackend(backend)
		configureQuietLocalProxy(proxy, "dashboard backend "+backend.Addr)
		proxy.Transport = routerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if dead {
				return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
			}
			response := httptest.NewRecorder()
			_, _ = fmt.Fprintf(response, "%s:%s?%s", backend.Addr, req.URL.Path, req.URL.RawQuery)
			return response.Result(), nil
		})
		return proxy
	}, source, localDialRetryPolicy{Budget: 400 * time.Millisecond, Interval: 50 * time.Millisecond, now: func() time.Time { return clock }, wait: func(context.Context, time.Duration) bool {
		waits++
		clock = clock.Add(400 * time.Millisecond)
		return true
	}})
	for _, backend := range []string{"dash-a", "dash-b"} {
		source.mu.Lock()
		source.backend.Addr = backend
		source.mu.Unlock()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://app.test/storage?key=a.txt", nil))
		if response.Code != http.StatusOK || response.Body.String() != backend+":/storage?key=a.txt" {
			t.Fatalf("backend %s response=%d %q", backend, response.Code, response.Body.String())
		}
	}
	dead = true
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://app.test/storage?key=a.txt", nil))
	if waits != 1 || response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "scenery: dashboard backend") || strings.Contains(response.Body.String(), "proxy error") {
		t.Fatalf("dead backend waits=%d response=%d %q", waits, response.Code, response.Body.String())
	}
}

func TestLocalPathRouterRootFrontendOwnsRootAssets(t *testing.T) {
	t.Parallel()

	paths := localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	fake := startFakeAgentHealthServer(t, paths.SocketPath, 111)

	dashboardServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<script src="/assets/dashboard.js"></script>`)
		case "/assets/dashboard.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = fmt.Fprint(w, "dashboard-js")
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
			_, _ = fmt.Fprint(w, `<script src="/assets/app.js"></script>`)
		case "/assets/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = fmt.Fprint(w, "frontend-js")
		case "/favicon.ico":
			w.Header().Set("Content-Type", "image/x-icon")
			_, _ = fmt.Fprint(w, "frontend-icon")
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
		Agent:            localagent.NewClient(paths.SocketPath),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	for path, want := range map[string]string{
		"/":              "/assets/app.js",
		"/assets/app.js": "frontend-js",
		"/favicon.ico":   "frontend-icon",
	} {
		status, body := localPathRouterTestGet(t, baseURL+path)
		if status != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("%s = %d %q, want 200 containing %q", path, status, body, want)
		}
	}
	// Scenery serves no dashboard UI: /console belongs to the application.
	if status, body := localPathRouterTestGet(t, baseURL+"/console/assets/dashboard.js"); strings.Contains(body, "dashboard-js") {
		t.Fatalf("/console/assets/dashboard.js = %d %q, want the application's response", status, body)
	}
}

// TestLocalPathRouterUpstreamDialRetryBridgesRestart proves a briefly-down
// agent router upstream is bridged by the bounded dial retry instead of
// answering 502 immediately.
func TestLocalPathRouterUpstreamDialRetryBridgesRestart(t *testing.T) {
	t.Parallel()

	paths := localagent.PathsForHome(t.TempDir())
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	fake := startFakeAgentHealthServer(t, paths.SocketPath, 111)
	_, dashboard := startDashboardTestBackend(t, "dash")
	fake.setDashboard(dashboard)

	upstreamPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	upstreamAddr := fmt.Sprintf("127.0.0.1:%d", upstreamPort)
	type upstreamStartResult struct {
		server *http.Server
		err    error
	}
	started := make(chan upstreamStartResult, 1)
	var startOnce sync.Once
	var startErr error
	dialRetry := localDialRetryPolicy{
		Budget:   2 * time.Second,
		Interval: 50 * time.Millisecond,
		wait: func(context.Context, time.Duration) bool {
			startOnce.Do(func() {
				ln, err := net.Listen("tcp", upstreamAddr)
				if err != nil {
					startErr = err
					started <- upstreamStartResult{err: err}
					return
				}
				mux := http.NewServeMux()
				mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
					_, _ = fmt.Fprint(w, "upstream-ok")
				})
				server := &http.Server{Handler: mux}
				go func() { _ = server.Serve(ln) }()
				started <- upstreamStartResult{server: server}
			})
			return startErr == nil
		},
	}
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
		DialRetry:        dialRetry,
		Agent:            localagent.NewClient(paths.SocketPath),
	})
	if err != nil {
		t.Fatalf("startLocalPathRouter: %v", err)
	}
	defer cleanup()

	statusCode, body := localPathRouterTestGet(t, fmt.Sprintf("http://127.0.0.1:%d/api/anything", port))
	var upstream *http.Server
	select {
	case result := <-started:
		if result.err != nil {
			t.Fatalf("start retry upstream: %v", result.err)
		}
		upstream = result.server
	case <-time.After(time.Second):
		t.Fatal("upstream was not started by a failed-dial retry")
	}
	t.Cleanup(func() { _ = upstream.Close() })
	if statusCode != http.StatusOK || body != "upstream-ok" {
		t.Fatalf("upstream retry result = %d %q", statusCode, body)
	}
}
