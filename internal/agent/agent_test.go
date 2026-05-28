package agent

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionIDUsesBranchAndRootHash(t *testing.T) {
	root := filepath.Join(t.TempDir(), "my-app")
	got := SessionID(root, "feature/Agent MVP")
	if !strings.HasPrefix(got, "feature-agent-mvp-") {
		t.Fatalf("SessionID prefix = %q", got)
	}
	if got2 := SessionID(root, "feature/Agent MVP"); got2 != got {
		t.Fatalf("SessionID not stable: %q then %q", got, got2)
	}
	if got2 := SessionID(filepath.Join(t.TempDir(), "my-app"), "feature/Agent MVP"); got2 == got {
		t.Fatalf("SessionID should include root hash, got duplicate %q", got)
	}
}

func TestDefaultPathsIgnoresDevCacheDir(t *testing.T) {
	t.Setenv(envAgentHome, "")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", filepath.Join(t.TempDir(), "dev-cache"))
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths.Home, filepath.Join(home, ".onlava"); got != want {
		t.Fatalf("agent home = %q, want %q", got, want)
	}
}

func TestRegistryUpsertWritesSessionManifest(t *testing.T) {
	root := t.TempDir()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	session, err := registry.Upsert(RegisterRequest{
		BaseAppID:   "demo",
		AppRoot:     root,
		Branch:      "feature/test",
		Status:      "running",
		ReportToken: "private-report-token",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.RuntimeAppID != "demo--"+session.SessionID {
		t.Fatalf("runtime app id = %q", session.RuntimeAppID)
	}
	manifestPath := filepath.Join(root, ".onlava", "sessions", session.SessionID, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest not written at %s: %v", manifestPath, err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-report-token") || strings.Contains(string(data), "report_token") {
		t.Fatalf("manifest leaked report token: %s", data)
	}
}

func TestRegistryUpsertPreservesReportTokenWhenOmitted(t *testing.T) {
	root := t.TempDir()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID:   "demo",
		AppRoot:     root,
		Status:      "starting",
		ReportToken: "private-report-token",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		Status:    "running",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4001"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID != first.SessionID {
		t.Fatalf("session id changed from %q to %q", first.SessionID, second.SessionID)
	}
	if second.ReportToken != "private-report-token" {
		t.Fatalf("report token = %q", second.ReportToken)
	}
}

func TestRegistryUpsertUsesExplicitSessionID(t *testing.T) {
	root := t.TempDir()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID:   "demo",
		AppRoot:     root,
		SessionID:   "review-a",
		Branch:      "feature/a",
		Status:      "starting",
		ReportToken: "private-report-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID != "review-a" || first.Branch != "feature/a" {
		t.Fatalf("session = %+v, want explicit id and branch", first)
	}
	second, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-a",
		Status:    "running",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID != "review-a" {
		t.Fatalf("session id = %q", second.SessionID)
	}
	if second.Branch != "feature/a" {
		t.Fatalf("branch = %q, want preserved branch", second.Branch)
	}
	if second.ReportToken != "private-report-token" {
		t.Fatalf("report token = %q", second.ReportToken)
	}
}

func TestRegistryTracksCurrentSessionByAppRoot(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(t.TempDir(), "sessions.json")
	registry, err := OpenRegistry(registryPath, "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	matches := registry.FindByAppRoot(root)
	if len(matches) != 2 || matches[0].SessionID != second.SessionID {
		t.Fatalf("matches = %+v, want current session %s first", matches, second.SessionID)
	}
	reloaded, err := OpenRegistry(registryPath, "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	matches = reloaded.FindByAppRoot(root)
	if len(matches) != 2 || matches[0].SessionID != second.SessionID {
		t.Fatalf("reloaded matches = %+v, want current session %s first", matches, second.SessionID)
	}
	if _, ok, err := reloaded.Delete(second.SessionID); err != nil || !ok {
		t.Fatalf("delete current ok=%v err=%v", ok, err)
	}
	matches = reloaded.FindByAppRoot(root)
	if len(matches) != 1 || matches[0].SessionID != first.SessionID {
		t.Fatalf("matches after delete = %+v, want fallback current %s", matches, first.SessionID)
	}
}

func TestRegistryRejectsInvalidExplicitSessionID(t *testing.T) {
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		SessionID: "___",
	}); err == nil {
		t.Fatal("expected invalid explicit session id to fail")
	}
}

func TestRegistryCapturesSessionOwnerFingerprint(t *testing.T) {
	root := t.TempDir()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	session, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		Status:    "running",
		OwnerPID:  os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Owner.PID != os.Getpid() || session.Owner.CmdlineHash == "" || session.Owner.CreatedBy != "onlava dev" {
		t.Fatalf("owner = %+v", session.Owner)
	}
	if err := VerifyOwner(session.Owner); err != nil {
		t.Fatalf("VerifyOwner returned error: %v", err)
	}
}

func TestVerifyOwnerRejectsMismatchedFingerprint(t *testing.T) {
	owner := CurrentOwner("test")
	if owner.PID <= 0 {
		t.Fatalf("owner = %+v", owner)
	}
	owner.CmdlineHash = "sha256:not-this-process"
	if err := VerifyOwner(owner); err == nil {
		t.Fatal("expected mismatched owner verification error")
	}
}

func TestVerifyOwnerRejectsUninspectableProcess(t *testing.T) {
	owner := Owner{
		PID:         99999999,
		StartedAt:   "not-a-live-process",
		CmdlineHash: "sha256:not-a-live-process",
		Exe:         "/not/a/live/process",
	}
	if err := VerifyOwner(owner); err == nil {
		t.Fatal("expected uninspectable owner verification error")
	}
}

func TestRegistryPersistsSharedSubstrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	registry, err := OpenRegistry(path, "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	substrate, err := registry.UpsertSubstrate(UpsertSubstrateRequest{
		Kind:     SubstrateVictoria,
		Status:   "ready",
		OwnerPID: 123,
		PIDs:     map[string]int{"metrics": 456},
		URLs:     map[string]string{"metrics": "http://127.0.0.1:8428"},
		Endpoints: map[string]string{
			"metrics": "http://127.0.0.1:8428/opentelemetry/v1/metrics",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if substrate.Kind != SubstrateVictoria || substrate.PIDs["metrics"] != 456 {
		t.Fatalf("substrate = %+v", substrate)
	}
	if substrate.Owner.PID != 123 {
		t.Fatalf("substrate owner = %+v", substrate.Owner)
	}

	reopened, err := OpenRegistry(path, "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.GetSubstrate(SubstrateVictoria)
	if !ok {
		t.Fatal("substrate not persisted")
	}
	if got.Endpoints["metrics"] != "http://127.0.0.1:8428/opentelemetry/v1/metrics" {
		t.Fatalf("substrate endpoints = %+v", got.Endpoints)
	}
}

func TestRegistryCapturesSubstrateComponentOwners(t *testing.T) {
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	substrate, err := registry.UpsertSubstrate(UpsertSubstrateRequest{
		Kind:     SubstrateVictoria,
		Status:   "ready",
		OwnerPID: os.Getpid(),
		PIDs: map[string]int{
			"metrics": os.Getpid(),
			"logs":    os.Getpid(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(substrate.Owners) != 2 {
		t.Fatalf("component owners = %+v, want 2", substrate.Owners)
	}
	for name, owner := range substrate.Owners {
		if owner.PID != os.Getpid() {
			t.Fatalf("owner %s pid = %d, want %d", name, owner.PID, os.Getpid())
		}
		if err := VerifyOwner(owner); err != nil {
			t.Fatalf("owner %s did not verify: %v", name, err)
		}
	}
	second, err := registry.UpsertSubstrate(UpsertSubstrateRequest{
		Kind:     SubstrateVictoria,
		Status:   "ready",
		OwnerPID: os.Getpid(),
		PIDs: map[string]int{
			"metrics": os.Getpid(),
			"logs":    os.Getpid(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Owners["metrics"].RecordedAt.Equal(substrate.Owners["metrics"].RecordedAt) {
		t.Fatalf("component owner was not preserved: first=%+v second=%+v", substrate.Owners["metrics"], second.Owners["metrics"])
	}
}

func TestServerRegistersAndRoutesSessionBackend(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	var publicHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/hello" {
			t.Fatalf("backend path = %q, want /hello", req.URL.Path)
		}
		if req.Host != publicHost {
			t.Fatalf("backend host = %q, want %q", req.Host, publicHost)
		}
		if got := req.Header.Get("X-Forwarded-Host"); got != publicHost {
			t.Fatalf("X-Forwarded-Host = %q, want %q", got, publicHost)
		}
		if got := req.Header.Get("X-Forwarded-Proto"); got != "http" {
			t.Fatalf("X-Forwarded-Proto = %q, want http", got)
		}
		if got := req.Header.Get("X-Forwarded-Port"); got != "80" {
			t.Fatalf("X-Forwarded-Port = %q, want 80", got)
		}
		if got := req.Header.Get("X-Forwarded-For"); got == "" {
			t.Fatal("X-Forwarded-For is empty")
		}
		_, _ = io.WriteString(w, "backend ok")
	}))
	defer backend.Close()
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

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
		Branch:    "feature/router",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: backendAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	publicHost = "api." + session.SessionID + ".onlava.localhost"

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = publicHost
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "backend ok" {
		t.Fatalf("router response status=%d body=%q", resp.StatusCode, body)
	}
}

func TestServerRouterTLSGeneratesHTTPSRoutes(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	t.Setenv("ONLAVA_DEV_CACHE_DIR", t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, "tls backend ok")
	}))
	defer backend.Close()
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(RunOptions{
		SocketPath: paths.SocketPath,
		RouterAddr: "127.0.0.1:0",
		RouterTLS:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
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
		Branch:    "feature/tls",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: backendAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	apiURL := session.Routes[RouteAPI]
	if !strings.HasPrefix(apiURL, "https://api."+session.SessionID+".onlava.localhost:") {
		t.Fatalf("api route = %q", apiURL)
	}

	routerAddr := server.RouterAddr()
	httpClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", routerAddr)
		},
	}}
	resp, err := httpClient.Get(apiURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "tls backend ok" {
		t.Fatalf("router response status=%d body=%q", resp.StatusCode, body)
	}
}

func TestServerRoutesFrontendBackend(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	var publicHost string
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != publicHost {
			t.Fatalf("frontend host = %q, want %q", req.Host, publicHost)
		}
		_, _ = io.WriteString(w, "frontend ok")
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
		Branch:    "feature/frontend",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
			"web":    {Network: "tcp", Addr: frontendAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Routes["web"] == "" || !strings.Contains(session.Routes["web"], "web."+session.SessionID+".onlava.localhost") {
		t.Fatalf("frontend route = %q", session.Routes["web"])
	}
	publicHost = "web." + session.SessionID + ".onlava.localhost"

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = publicHost
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "frontend ok" {
		t.Fatalf("router response status=%d body=%q", resp.StatusCode, body)
	}
}

func TestServerRoutesParallelSessionsWithoutRouteCollision(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	backend := func(label string) (*httptest.Server, *string) {
		var addr string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !strings.HasSuffix(req.Host, ".onlava.localhost") {
				t.Fatalf("%s backend host = %q, want onlava public host", label, req.Host)
			}
			_, _ = io.WriteString(w, label)
		}))
		addr = strings.TrimPrefix(server.URL, "http://")
		return server, &addr
	}
	apiA, apiAAddr := backend("api-a")
	defer apiA.Close()
	webA, webAAddr := backend("web-a")
	defer webA.Close()
	apiB, apiBAddr := backend("api-b")
	defer apiB.Close()
	webB, webBAddr := backend("web-b")
	defer webB.Close()

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
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer stopTestAgent(t, cancel, done)

	client := NewClient(server.paths.SocketPath)
	if err := waitForAgentPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	sessionA, err := client.Register(ctx, RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   filepath.Join(t.TempDir(), "worktree-a"),
		Branch:    "feature/parallel",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: *apiAAddr},
			"web":    {Network: "tcp", Addr: *webAAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionB, err := client.Register(ctx, RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   filepath.Join(t.TempDir(), "worktree-b"),
		Branch:    "feature/parallel",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: *apiBAddr},
			"web":    {Network: "tcp", Addr: *webBAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sessionA.SessionID == sessionB.SessionID {
		t.Fatalf("parallel sessions share session id %q", sessionA.SessionID)
	}

	assertRouteBody := func(host, want string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != want {
			t.Fatalf("%s response status=%d body=%q, want %q", host, resp.StatusCode, body, want)
		}
	}
	assertRouteBody("api."+sessionA.SessionID+".onlava.localhost", "api-a")
	assertRouteBody("web."+sessionA.SessionID+".onlava.localhost", "web-a")
	assertRouteBody("api."+sessionB.SessionID+".onlava.localhost", "api-b")
	assertRouteBody("web."+sessionB.SessionID+".onlava.localhost", "web-b")
}

func TestServerRoutesSharedSubstrateBackends(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	var publicHost string
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != publicHost {
			t.Fatalf("grafana host = %q, want %q", req.Host, publicHost)
		}
		_, _ = io.WriteString(w, "grafana ok")
	}))
	defer grafana.Close()
	grafanaAddr := strings.TrimPrefix(grafana.URL, "http://")

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
		Branch:    "feature/substrate-routes",
		Backends: map[string]Backend{
			RouteGrafana: {Network: "tcp", Addr: grafanaAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Routes[RouteGrafana] == "" || !strings.Contains(session.Routes[RouteGrafana], "grafana."+session.SessionID+".onlava.localhost") {
		t.Fatalf("grafana route = %q", session.Routes[RouteGrafana])
	}
	publicHost = "grafana." + session.SessionID + ".onlava.localhost"

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = publicHost
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "grafana ok" {
		t.Fatalf("router response status=%d body=%q", resp.StatusCode, body)
	}
}

func TestServerRoutesConsoleBackend(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, "dashboard ok")
	}))
	defer backend.Close()
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

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
		Branch:    "feature/console",
		Backends: map[string]Backend{
			RouteDashboard: {Network: "tcp", Addr: backendAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/s/"+session.SessionID+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "console.onlava.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "dashboard ok" {
		t.Fatalf("console response status=%d body=%q", resp.StatusCode, body)
	}
}

func TestServerRoutesConsoleToGlobalDashboardBackend(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		_, _ = io.WriteString(w, "global dashboard ok")
	}))
	defer backend.Close()
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(RunOptions{
		SocketPath: paths.SocketPath,
		RouterAddr: "127.0.0.1:0",
		DashboardBackend: Backend{
			Network: "tcp",
			Addr:    backendAddr,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer stopTestAgent(t, cancel, done)

	client := NewClient(server.paths.SocketPath)
	if err := waitForAgentPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	health, err := client.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if health.DashboardBackend.Addr != backendAddr {
		t.Fatalf("health dashboard backend = %+v, want addr %q", health.DashboardBackend, backendAddr)
	}
	session, err := client.Register(ctx, RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		Branch:    "feature/global-dashboard",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Routes[RouteDashboard] == "" {
		t.Fatalf("dashboard route missing: %+v", session.Routes)
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/s/"+session.SessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "console.onlava.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "global dashboard ok" {
		t.Fatalf("global console response status=%d body=%q", resp.StatusCode, body)
	}
	if gotPath != "/s/"+session.SessionID {
		t.Fatalf("global dashboard path = %q, want /s/%s", gotPath, session.SessionID)
	}
}

func TestServerConsoleFollowsGlobalDashboardRedirect(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	var sawSessionPath bool
	var sawAppPath bool
	var sessionPath string
	var appPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case sessionPath:
			sawSessionPath = true
			http.Redirect(w, req, appPath, http.StatusFound)
		case appPath:
			sawAppPath = true
			_, _ = io.WriteString(w, "redirect ok")
		default:
			t.Fatalf("backend path = %q", req.URL.Path)
		}
	}))
	defer backend.Close()
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(RunOptions{
		SocketPath: paths.SocketPath,
		RouterAddr: "127.0.0.1:0",
		DashboardBackend: Backend{
			Network: "tcp",
			Addr:    backendAddr,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
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
		Branch:    "feature/global-dashboard",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionPath = "/s/" + session.SessionID
	appPath = "/" + session.SessionID

	httpClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Host = "console.onlava.localhost"
			return nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/s/"+session.SessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "console.onlava.localhost"
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "redirect ok" {
		t.Fatalf("redirect response status=%d body=%q", resp.StatusCode, body)
	}
	if !sawSessionPath || !sawAppPath {
		t.Fatalf("redirect paths saw session=%v app=%v", sawSessionPath, sawAppPath)
	}
}

func TestServerRoutesMCPToGlobalDashboardBackend(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		_, _ = io.WriteString(w, "global mcp ok")
	}))
	defer backend.Close()
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(RunOptions{
		SocketPath: paths.SocketPath,
		RouterAddr: "127.0.0.1:0",
		DashboardBackend: Backend{
			Network: "tcp",
			Addr:    backendAddr,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
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
		Branch:    "feature/global-mcp",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Routes[RouteMCP] == "" {
		t.Fatalf("mcp route missing: %+v", session.Routes)
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "mcp." + session.SessionID + ".onlava.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "global mcp ok" {
		t.Fatalf("global MCP response status=%d body=%q", resp.StatusCode, body)
	}
	if gotPath != "/sse" {
		t.Fatalf("global MCP path = %q, want /sse", gotPath)
	}
}

func TestServerRoutesMCPBackend(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, "mcp ok")
	}))
	defer backend.Close()
	backendAddr := strings.TrimPrefix(backend.URL, "http://")

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
		Branch:    "feature/mcp",
		Backends: map[string]Backend{
			RouteMCP: {Network: "tcp", Addr: backendAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "mcp." + session.SessionID + ".onlava.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "mcp ok" {
		t.Fatalf("mcp response status=%d body=%q", resp.StatusCode, body)
	}
}

func TestServerSubstrateAPI(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
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
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer stopTestAgent(t, cancel, done)

	client := NewClient(server.paths.SocketPath)
	if err := waitForAgentPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	created, err := client.UpsertSubstrate(ctx, UpsertSubstrateRequest{
		Kind:   SubstrateVictoria,
		Status: "ready",
		URLs:   map[string]string{"metrics": "http://127.0.0.1:8428"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Kind != SubstrateVictoria {
		t.Fatalf("created substrate = %+v", created)
	}
	list, err := client.ListSubstrates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Kind != SubstrateVictoria {
		t.Fatalf("substrates = %+v", list)
	}
	got, err := client.GetSubstrate(ctx, SubstrateVictoria)
	if err != nil {
		t.Fatal(err)
	}
	if got.URLs["metrics"] != "http://127.0.0.1:8428" {
		t.Fatalf("substrate urls = %+v", got.URLs)
	}
	deleted, err := client.DeleteSubstrate(ctx, SubstrateVictoria)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Kind != SubstrateVictoria {
		t.Fatalf("deleted substrate = %+v", deleted)
	}
}

func TestUnixBackendRoute(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	socketPath := filepath.Join(t.TempDir(), "backend.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	backend := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, "unix ok")
	})}
	defer backend.Close()
	go func() { _ = backend.Serve(ln) }()

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
		Branch:    "feature/unix",
		Backends: map[string]Backend{
			RouteAPI: {Network: "unix", Addr: socketPath},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "api." + session.SessionID + ".onlava.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "unix ok" {
		t.Fatalf("router response status=%d body=%q", resp.StatusCode, body)
	}
}

func waitForAgentPing(ctx context.Context, client *Client) error {
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return lastErr
}

func stopTestAgent(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent server shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for agent server shutdown")
	}
}
