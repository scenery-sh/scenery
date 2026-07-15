package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDevDomainHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		domain, branch, want string
	}{
		{"local.clean.tech", "main", "local.clean.tech"},
		{"local.clean.tech", "pricing", "pricing-local.clean.tech"},
		{"local.clean.tech", "feature/new-pricing", "feature-new-pricing-local.clean.tech"},
		{"LOCAL.Clean.Tech", "Main", "local.clean.tech"},
		{"https://local.clean.tech/path", "pricing", "pricing-local.clean.tech"},
		{"local.clean.tech:443", "pricing", "pricing-local.clean.tech"},
		{"", "pricing", ""},
		{"local.clean.tech", "", ""},
		{"local.clean.tech", "---", ""},
	}
	for _, tc := range cases {
		if got := DevDomainHost(tc.domain, tc.branch); got != tc.want {
			t.Errorf("DevDomainHost(%q, %q) = %q, want %q", tc.domain, tc.branch, got, tc.want)
		}
	}
}

func TestDevDomainManifestNormalization(t *testing.T) {
	t.Parallel()

	session, err := NewSession(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		SessionID: "pricing",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
		RouteManifest: RouteManifest{
			Mode:       RouteModePath,
			BaseURL:    "http://localhost:4001",
			DomainHost: "Pricing-Local.Clean.Tech",
		},
	}, "127.0.0.1:9440", "http", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := session.RouteManifest.DomainHost, "pricing-local.clean.tech"; got != want {
		t.Fatalf("domain host = %q, want %q", got, want)
	}
	if got, want := session.RouteManifest.DomainURL, "https://pricing-local.clean.tech"; got != want {
		t.Fatalf("domain url = %q, want %q", got, want)
	}

	hostMode, err := NewSession(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		SessionID: "hostmode",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
		RouteManifest: RouteManifest{
			Mode:       RouteModeHost,
			DomainHost: "pricing-local.clean.tech",
			Routes: map[string]RouteRecord{
				RouteAPI: {Name: RouteAPI, Kind: RouteAPI, URL: "https://api.hostmode.local.dev/"},
			},
		},
	}, "127.0.0.1:9440", "http", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hostMode.RouteManifest.DomainHost != "" || hostMode.RouteManifest.DomainURL != "" {
		t.Fatalf("host mode manifest kept domain host %q url %q", hostMode.RouteManifest.DomainHost, hostMode.RouteManifest.DomainURL)
	}
}

func TestRegistryDevDomainHostOwnership(t *testing.T) {
	registryPath := t.TempDir() + "/registry.json"
	registry, err := OpenRegistry(registryPath, "127.0.0.1:9440", "http")
	if err != nil {
		t.Fatal(err)
	}

	pathManifest := func(host string) RouteManifest {
		return RouteManifest{
			Mode:       RouteModePath,
			BaseURL:    "http://localhost:4001",
			DomainHost: host,
		}
	}
	backends := map[string]Backend{RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"}}

	first, err := registry.Upsert(RegisterRequest{
		BaseAppID:     "demo",
		AppRoot:       t.TempDir(),
		SessionID:     "pricing-a",
		Status:        "running",
		OwnerPID:      os.Getpid(),
		Backends:      backends,
		RouteManifest: pathManifest("pricing-local.clean.tech"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.RouteManifest.DomainHost != "pricing-local.clean.tech" {
		t.Fatalf("first domain host = %q", first.RouteManifest.DomainHost)
	}
	if session, route, ok := registry.RouteTargetForHost("pricing-local.clean.tech"); !ok || route != RoutePathMode || session.SessionID != first.SessionID {
		t.Fatalf("host target = (%q, %q, %v)", session.SessionID, route, ok)
	}

	// A live verified owner keeps the host; the newcomer records the conflict.
	second, err := registry.Upsert(RegisterRequest{
		BaseAppID:     "demo",
		AppRoot:       t.TempDir(),
		SessionID:     "pricing-b",
		Status:        "running",
		OwnerPID:      os.Getpid(),
		Backends:      backends,
		RouteManifest: pathManifest("pricing-local.clean.tech"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.RouteManifest.DomainHost != "" || second.RouteManifest.DomainURL != "" {
		t.Fatalf("second session kept contested host %q", second.RouteManifest.DomainHost)
	}
	if second.DomainHostConflict == nil || second.DomainHostConflict.SessionID != first.SessionID {
		t.Fatalf("second conflict = %+v", second.DomainHostConflict)
	}
	if session, _, ok := registry.RouteTargetForHost("pricing-local.clean.tech"); !ok || session.SessionID != first.SessionID {
		t.Fatalf("contested host owner = %q ok=%v", session.SessionID, ok)
	}

	// A provably stale owner loses the host to the newcomer.
	staleRoot := t.TempDir()
	if _, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   staleRoot,
		SessionID: "stale-owner",
		Status:    "running",
		OwnerPID:  os.Getpid(),
		Owner: Owner{
			PID:         os.Getpid(),
			StartedAt:   "1999-01-01T00:00:00Z",
			CmdlineHash: "sha256:stale",
			Exe:         "/nonexistent/scenery",
		},
		Backends:      backends,
		RouteManifest: pathManifest("stale-local.clean.tech"),
	}); err != nil {
		t.Fatal(err)
	}
	taker, err := registry.Upsert(RegisterRequest{
		BaseAppID:     "demo",
		AppRoot:       t.TempDir(),
		SessionID:     "stale-taker",
		Status:        "running",
		OwnerPID:      os.Getpid(),
		Backends:      backends,
		RouteManifest: pathManifest("stale-local.clean.tech"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if taker.RouteManifest.DomainHost != "stale-local.clean.tech" || taker.DomainHostConflict != nil {
		t.Fatalf("taker host = %q conflict = %+v", taker.RouteManifest.DomainHost, taker.DomainHostConflict)
	}
	if session, _, ok := registry.RouteTargetForHost("stale-local.clean.tech"); !ok || session.SessionID != taker.SessionID {
		t.Fatalf("reclaimed host owner = %q ok=%v", session.SessionID, ok)
	}
	if stale, ok := registry.Get("stale-owner"); !ok || stale.RouteManifest.DomainHost != "" {
		t.Fatalf("stale session kept host %q ok=%v", stale.RouteManifest.DomainHost, ok)
	}

	// ClaimAliases transfers a live host the way alias leases transfer.
	forced, err := registry.Upsert(RegisterRequest{
		BaseAppID:     "demo",
		AppRoot:       second.AppRoot,
		SessionID:     second.SessionID,
		Status:        "running",
		OwnerPID:      os.Getpid(),
		Backends:      backends,
		RouteManifest: pathManifest("pricing-local.clean.tech"),
		ClaimAliases:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if forced.RouteManifest.DomainHost != "pricing-local.clean.tech" || forced.DomainHostConflict != nil {
		t.Fatalf("forced host = %q conflict = %+v", forced.RouteManifest.DomainHost, forced.DomainHostConflict)
	}
	if remaining, ok := registry.Get(first.SessionID); !ok || remaining.RouteManifest.DomainHost != "" {
		t.Fatalf("first session kept host after forced transfer: %q", remaining.RouteManifest.DomainHost)
	}

	// Deleting the owner frees the host.
	if _, _, err := registry.Delete(forced.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := registry.RouteTargetForHost("pricing-local.clean.tech"); ok {
		t.Fatal("deleted session still routes its domain host")
	}
}

func TestServerDevDomainHostServesPathMode(t *testing.T) {
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
		Status:    "running",
		OwnerPID:  os.Getpid(),
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: apiAddr},
			"ui":     {Network: "tcp", Addr: frontendAddr},
		},
		RouteManifest: RouteManifest{
			Mode:       RouteModePath,
			BaseURL:    "http://localhost:4001",
			DomainHost: "pricing-local.clean.tech",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.RouteManifest.DomainURL != "https://pricing-local.clean.tech" {
		t.Fatalf("registered domain url = %q", session.RouteManifest.DomainURL)
	}

	request := func(host, targetPath string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+targetPath, nil)
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
		return resp.StatusCode, string(body)
	}

	status, body := request("pricing-local.clean.tech", "/api/v1/users")
	if status != http.StatusOK || body != "api:/v1/users" {
		t.Fatalf("api status=%d body=%q", status, body)
	}
	status, body = request("pricing-local.clean.tech", "/ui/settings")
	if status != http.StatusOK || body != "frontend:/ui/settings" {
		t.Fatalf("frontend status=%d body=%q", status, body)
	}
	status, body = request("pricing-local.clean.tech", "/")
	if status != http.StatusOK || !strings.Contains(body, "Services") {
		t.Fatalf("route index status=%d body=%q", status, body)
	}
	status, body = request("pricing-local.clean.tech", PathModeRuntimePrefix+"/routes")
	if status != http.StatusOK {
		t.Fatalf("runtime routes status=%d", status)
	}
	var routesPayload struct {
		BaseURL string `json:"base_url"`
		Routes  map[string]struct {
			URL string `json:"url"`
		} `json:"routes"`
	}
	if err := json.Unmarshal([]byte(body), &routesPayload); err != nil {
		t.Fatalf("runtime routes payload: %v", err)
	}
	if routesPayload.BaseURL != "https://pricing-local.clean.tech" {
		t.Fatalf("runtime base_url = %q", routesPayload.BaseURL)
	}
	if got := routesPayload.Routes[RouteAPI].URL; got != "https://pricing-local.clean.tech/api/" {
		t.Fatalf("runtime api url = %q", got)
	}
	status, _ = request("unclaimed-local.clean.tech", "/")
	if status != http.StatusNotFound {
		t.Fatalf("unknown host status=%d, want 404", status)
	}

	status, _ = request(server.routerAddr, "/v1/tls/allow?domain=pricing-local.clean.tech")
	if status != http.StatusNoContent {
		t.Fatalf("tls allow status=%d, want 204", status)
	}
	status, _ = request(server.routerAddr, "/v1/tls/allow?domain=unclaimed-local.clean.tech")
	if status != http.StatusNotFound {
		t.Fatalf("tls allow unknown status=%d, want 404", status)
	}

	// Narrow the exposed surface to api only: frontends, index, and the
	// runtime surface disappear from the domain origin.
	if _, err := client.Register(ctx, RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   session.AppRoot,
		SessionID: session.SessionID,
		Status:    "running",
		OwnerPID:  os.Getpid(),
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: apiAddr},
			"ui":     {Network: "tcp", Addr: frontendAddr},
		},
		RouteManifest: RouteManifest{
			Mode:         RouteModePath,
			BaseURL:      "http://localhost:4001",
			DomainHost:   "pricing-local.clean.tech",
			PublicRoutes: []string{"api"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	status, body = request("pricing-local.clean.tech", "/api/v1/users")
	if status != http.StatusOK || body != "api:/v1/users" {
		t.Fatalf("exposed api status=%d body=%q", status, body)
	}
	status, _ = request("pricing-local.clean.tech", "/ui/settings")
	if status != http.StatusNotFound {
		t.Fatalf("unexposed frontend status=%d, want 404", status)
	}
	status, _ = request("pricing-local.clean.tech", "/")
	if status != http.StatusNotFound {
		t.Fatalf("unexposed root status=%d, want 404", status)
	}
	status, _ = request("pricing-local.clean.tech", PathModeRuntimePrefix+"/routes")
	if status != http.StatusNotFound {
		t.Fatalf("unexposed runtime status=%d, want 404", status)
	}
	status, _ = request("pricing-local.clean.tech", "/__scenery/config")
	if status != http.StatusNotFound {
		t.Fatalf("unexposed scenery control status=%d, want 404", status)
	}
}

func TestNormalizePublicRoutes(t *testing.T) {
	t.Parallel()

	got := normalizePublicRoutes([]string{"console", "API", "runtime", "api", "", "next"})
	want := []string{"api", "dashboard", "next", "runtime"}
	if len(got) != len(want) {
		t.Fatalf("normalizePublicRoutes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalizePublicRoutes = %v, want %v", got, want)
		}
	}
	if normalizePublicRoutes(nil) != nil {
		t.Fatal("nil input must stay nil")
	}
}
