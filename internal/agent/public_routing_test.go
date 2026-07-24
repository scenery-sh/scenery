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

func TestServerPublicDeployRoutesByHostWithContainment(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, "api:"+req.URL.Path)
	}))
	defer api.Close()
	apiAddr := strings.TrimPrefix(api.URL, "http://")

	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, "frontend:"+req.URL.Path)
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
	appRoot := t.TempDir()
	registry := EmptyDeployRegistry()
	registry.Targets = []DeployTarget{
		{Domain: "onlv.dev", AppRoot: appRoot, RootService: "ui", Enabled: true},
		{Domain: "down.dev", AppRoot: t.TempDir(), RootService: "ui", Enabled: true},
	}
	if err := WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   appRoot,
		Status:    "running",
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
	manifest := publicRouteManifest(session, registry.Targets[0])
	if _, ok := manifest.Routes["ui"]; ok {
		t.Fatalf("root frontend unexpectedly retained /ui route: %+v", manifest.Routes)
	}
	if got := manifest.Routes["root"]; got.Backend != "ui" || got.Kind != "frontend" {
		t.Fatalf("root route = %+v", got)
	}

	request := func(host, targetPath string, publicEdge, spoofLocalSession bool) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+targetPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		if publicEdge {
			req.Header.Set("X-Scenery-Edge-Token", "test-token")
			req.Header.Set("X-Scenery-Public-Edge", "1")
			req.Header.Set("X-Forwarded-Proto", "https")
			req.Header.Set("X-Forwarded-Port", "443")
		}
		if spoofLocalSession {
			req.Header.Set("X-Scenery-Session", session.SessionID)
			req.Header.Set("X-Scenery-Local-Route-Mode", string(RouteModePath))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(body)
	}

	status, body := request("onlv.dev", "/", true, false)
	if status != http.StatusOK || body != "frontend:/" {
		t.Fatalf("root status=%d body=%q", status, body)
	}
	status, body = request("onlv.dev", "/api/v1/users", true, false)
	if status != http.StatusOK || body != "api:/v1/users" {
		t.Fatalf("api status=%d body=%q", status, body)
	}
	status, body = request("onlv.dev", "/ui/settings", true, false)
	if status != http.StatusOK || body != "frontend:/ui/settings" {
		t.Fatalf("root catch-all status=%d body=%q", status, body)
	}
	status, _ = request("onlv.dev", PathModeRuntimePrefix+"/health", true, true)
	if status != http.StatusNotFound {
		t.Fatalf("public runtime status=%d, want 404", status)
	}
	status, _ = request("onlv.dev", PathModeDashboardPrefix+"/", true, false)
	if status != http.StatusNotFound {
		t.Fatalf("public dashboard status=%d, want 404", status)
	}
	status, _ = request("onlv.dev", "/__scenery/config", true, false)
	if status != http.StatusNotFound {
		t.Fatalf("public control status=%d, want 404", status)
	}
	status, _ = request("onlv.dev", "/ui/__scenery/config", true, false)
	if status != http.StatusNotFound {
		t.Fatalf("public frontend control status=%d, want 404", status)
	}
	status, _ = request("onlv.dev", "/api/v1/users", false, false)
	if status != http.StatusNotFound {
		t.Fatalf("missing public header status=%d, want 404", status)
	}
	status, _ = request("unknown.dev", "/", true, false)
	if status != http.StatusNotFound {
		t.Fatalf("unknown public host status=%d, want 404", status)
	}
	status, body = request("down.dev", "/", true, false)
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "app is not running") {
		t.Fatalf("down status=%d body=%q", status, body)
	}
}
