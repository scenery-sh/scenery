package agent

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testRouteHost(t *testing.T, route string) string {
	t.Helper()
	parsed, err := url.Parse(route)
	if err != nil || parsed.Host == "" {
		t.Fatalf("invalid route URL %q: %v", route, err)
	}
	host := parsed.Host
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	return host
}

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
	t.Setenv("SCENERY_DEV_CACHE_DIR", filepath.Join(t.TempDir(), "dev-cache"))
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths.Home, filepath.Join(home, ".scenery"); got != want {
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
	if session.RouteNamespace.Workspace != "demo" || session.RouteNamespace.BaseDomain != DefaultRouteBaseDomain {
		t.Fatalf("route namespace = %+v, want demo fallback namespace", session.RouteNamespace)
	}
	manifestPath := filepath.Join(root, ".scenery", "sessions", session.SessionID, "manifest.json")
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
	if !strings.Contains(string(data), `"route_namespace"`) || !strings.Contains(string(data), `"base_domain": "local.dev"`) {
		t.Fatalf("manifest missing route namespace: %s", data)
	}
}

func TestRegistryUpsertPersistsRouteNamespace(t *testing.T) {
	root := t.TempDir()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	session, err := registry.Upsert(RegisterRequest{
		BaseAppID: "pulse",
		AppRoot:   root,
		Branch:    "main",
		RouteNamespace: RouteNamespace{
			Workspace:  "ONLV",
			BaseDomain: "local.onlv.dev",
			Hosts: map[string]string{
				RouteAPI:       "https://api.onlv.localhost:443/path",
				RouteDashboard: "",
				"console":      "console.onlv.localhost",
				"Pulse App":    "Pulse.Onlv.Localhost",
				RouteTemporal:  "[temporal.onlv.localhost]",
			},
		},
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := session.RouteNamespace.Workspace, "onlv"; got != want {
		t.Fatalf("workspace = %q, want %q", got, want)
	}
	if got, want := session.RouteNamespace.BaseDomain, "local.onlv.dev"; got != want {
		t.Fatalf("base domain = %q, want %q", got, want)
	}
	wantHosts := map[string]string{
		RouteAPI:      "api.onlv.localhost",
		"console":     "console.onlv.localhost",
		"pulse-app":   "pulse.onlv.localhost",
		RouteTemporal: "temporal.onlv.localhost",
	}
	for route, want := range wantHosts {
		if got := session.RouteNamespace.Hosts[route]; got != want {
			t.Fatalf("host %q = %q, want %q in %+v", route, got, want, session.RouteNamespace.Hosts)
		}
	}

	updated, err := registry.Upsert(RegisterRequest{
		BaseAppID: "pulse",
		AppRoot:   root,
		SessionID: session.SessionID,
		Status:    "running",
		Backends:  session.Backends,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.RouteNamespace.BaseDomain != "local.onlv.dev" || updated.RouteNamespace.Hosts["console"] != "console.onlv.localhost" {
		t.Fatalf("route namespace was not preserved on update: %+v", updated.RouteNamespace)
	}
}

func TestRouteNamespaceDefaultsEvenWithExplicitHosts(t *testing.T) {
	root := t.TempDir()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	session, err := registry.Upsert(RegisterRequest{
		BaseAppID: "pulse",
		AppRoot:   root,
		RouteNamespace: RouteNamespace{
			Hosts: map[string]string{
				RouteAPI: "api.custom.localhost",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.RouteNamespace.Workspace != "" {
		t.Fatalf("workspace = %q, want empty for explicit-host namespace", session.RouteNamespace.Workspace)
	}
	if got, want := session.RouteNamespace.BaseDomain, DefaultRouteBaseDomain; got != want {
		t.Fatalf("base domain = %q, want %q", got, want)
	}
}

func TestRegistryClaimsConfiguredRouteAliases(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(t.TempDir(), "sessions.json")
	registry, err := OpenRegistry(registryPath, "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	session, err := registry.Upsert(RegisterRequest{
		BaseAppID: "pulse",
		AppRoot:   root,
		SessionID: "review-a",
		RouteNamespace: RouteNamespace{
			Hosts: map[string]string{
				RouteAPI:  "api.onlv.localhost",
				"console": "console.onlv.localhost",
				"web":     "pulse.onlv.localhost",
			},
		},
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
			"web":    {Network: "tcp", Addr: "127.0.0.1:5173"},
		},
		OwnerPID: os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := session.Aliases[RouteAPI], "http://api.onlv.localhost:9440/"; got != want {
		t.Fatalf("api alias = %q, want %q", got, want)
	}
	if got, want := session.Aliases[RouteDashboard], "http://console.onlv.localhost:9440/"; got != want {
		t.Fatalf("dashboard alias = %q, want %q", got, want)
	}
	if got, want := session.Aliases["web"], "http://pulse.onlv.localhost:9440/"; got != want {
		t.Fatalf("web alias = %q, want %q", got, want)
	}
	if !strings.Contains(session.Routes[RouteAPI], "api."+session.SessionID+".onlv.localhost") {
		t.Fatalf("canonical api route = %q, want session-scoped host", session.Routes[RouteAPI])
	}
	if session.Routes[RouteAPI] == session.Aliases[RouteAPI] {
		t.Fatalf("canonical route and alias should differ: route=%q alias=%q", session.Routes[RouteAPI], session.Aliases[RouteAPI])
	}

	manifestPath := filepath.Join(root, ".scenery", "sessions", session.SessionID, "manifest.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `"aliases"`) || !strings.Contains(string(manifest), `"api.onlv.localhost`) {
		t.Fatalf("manifest missing aliases: %s", manifest)
	}

	reopened, err := OpenRegistry(registryPath, "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	reloaded, ok := reopened.Get(session.SessionID)
	if !ok {
		t.Fatalf("reloaded registry missing session %q", session.SessionID)
	}
	if reloaded.Aliases[RouteAPI] != session.Aliases[RouteAPI] {
		t.Fatalf("reloaded aliases = %+v, want %+v", reloaded.Aliases, session.Aliases)
	}
}

func TestRegistryDoesNotStealLiveAliasLease(t *testing.T) {
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	namespace := RouteNamespace{
		Hosts: map[string]string{
			RouteAPI: "api.onlv.localhost",
			"web":    "pulse.onlv.localhost",
		},
	}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID:      "pulse",
		AppRoot:        filepath.Join(t.TempDir(), "worktree-a"),
		SessionID:      "review-a",
		RouteNamespace: namespace,
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
			"web":    {Network: "tcp", Addr: "127.0.0.1:5173"},
		},
		OwnerPID: os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	aliasURL := first.Aliases[RouteAPI]
	if aliasURL == "" {
		t.Fatalf("first session did not claim api alias: %+v", first.Aliases)
	}
	second, err := registry.Upsert(RegisterRequest{
		BaseAppID:      "pulse",
		AppRoot:        filepath.Join(t.TempDir(), "worktree-b"),
		SessionID:      "review-b",
		RouteNamespace: namespace,
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4001"},
			"web":    {Network: "tcp", Addr: "127.0.0.1:5174"},
		},
		OwnerPID: os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Aliases[RouteAPI] == "" || first.Aliases["web"] == "" {
		t.Fatalf("first session did not claim aliases: %+v", first.Aliases)
	}
	if len(second.Aliases) != 0 {
		t.Fatalf("second session stole live aliases: %+v", second.Aliases)
	}
	currentFirst, ok := registry.Get(first.SessionID)
	if !ok {
		t.Fatalf("first session missing")
	}
	if currentFirst.Aliases[RouteAPI] != first.Aliases[RouteAPI] {
		t.Fatalf("first aliases changed: %+v", currentFirst.Aliases)
	}
	if second.AliasConflicts[RouteAPI].SessionID != first.SessionID {
		t.Fatalf("second session did not report alias conflict: %+v", second.AliasConflicts)
	}
}

func TestRegistryExplicitlyTransfersAliasLease(t *testing.T) {
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	namespace := RouteNamespace{Hosts: map[string]string{RouteAPI: "api.onlv.localhost"}}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID:      "pulse",
		AppRoot:        filepath.Join(t.TempDir(), "worktree-a"),
		SessionID:      "review-a",
		RouteNamespace: namespace,
		Backends:       map[string]Backend{RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"}},
		OwnerPID:       os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	aliasURL := first.Aliases[RouteAPI]
	if aliasURL == "" {
		t.Fatalf("first session did not claim api alias: %+v", first.Aliases)
	}
	second, err := registry.Upsert(RegisterRequest{
		BaseAppID:      "pulse",
		AppRoot:        filepath.Join(t.TempDir(), "worktree-b"),
		SessionID:      "review-b",
		RouteNamespace: namespace,
		Backends:       map[string]Backend{RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4001"}},
		OwnerPID:       os.Getpid(),
		ClaimAliases:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Aliases[RouteAPI] != aliasURL {
		t.Fatalf("transferred alias = %+v, want %q", second.Aliases, aliasURL)
	}
	currentFirst, ok := registry.Get(first.SessionID)
	if !ok {
		t.Fatal("first session missing after alias transfer")
	}
	if len(currentFirst.Aliases) != 0 {
		t.Fatalf("first session kept transferred alias: %+v", currentFirst.Aliases)
	}
	if len(second.AliasConflicts) != 0 {
		t.Fatalf("transferring session reported conflicts: %+v", second.AliasConflicts)
	}
}

func TestRegistryReclaimsStaleAliasLease(t *testing.T) {
	stale := exec.Command("sleep", "30")
	if err := stale.Start(); err != nil {
		t.Fatalf("start stale owner fixture: %v", err)
	}
	staleOwner := CaptureOwner(stale.Process.Pid, "scenery up")
	if err := stale.Process.Kill(); err != nil {
		t.Fatalf("kill stale owner fixture: %v", err)
	}
	_ = stale.Wait()

	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	namespace := RouteNamespace{Hosts: map[string]string{RouteAPI: "api.onlv.localhost"}}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID:      "pulse",
		AppRoot:        filepath.Join(t.TempDir(), "worktree-a"),
		SessionID:      "review-a",
		RouteNamespace: namespace,
		Backends:       map[string]Backend{RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"}},
		OwnerPID:       staleOwner.PID,
		Owner:          staleOwner,
	})
	if err != nil {
		t.Fatal(err)
	}
	aliasURL := first.Aliases[RouteAPI]
	if aliasURL == "" {
		t.Fatalf("first session did not claim api alias: %+v", first.Aliases)
	}
	second, err := registry.Upsert(RegisterRequest{
		BaseAppID:      "pulse",
		AppRoot:        filepath.Join(t.TempDir(), "worktree-b"),
		SessionID:      "review-b",
		RouteNamespace: namespace,
		Backends:       map[string]Backend{RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4001"}},
		OwnerPID:       os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Aliases[RouteAPI] != aliasURL {
		t.Fatalf("stale alias was not reclaimed: second=%+v first=%+v", second.Aliases, first.Aliases)
	}
	currentFirst, ok := registry.Get(first.SessionID)
	if !ok {
		t.Fatal("first session missing after stale alias reclaim")
	}
	if len(currentFirst.Aliases) != 0 {
		t.Fatalf("stale session kept reclaimed alias: %+v", currentFirst.Aliases)
	}
}

func TestRegistryDeleteRemovesOnlyOwnedAliasLeases(t *testing.T) {
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID: "pulse",
		AppRoot:   filepath.Join(t.TempDir(), "worktree-a"),
		SessionID: "review-a",
		RouteNamespace: RouteNamespace{Hosts: map[string]string{
			RouteAPI: "api.onlv.localhost",
		}},
		Backends: map[string]Backend{RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"}},
		OwnerPID: os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Upsert(RegisterRequest{
		BaseAppID: "pulse",
		AppRoot:   filepath.Join(t.TempDir(), "worktree-b"),
		SessionID: "review-b",
		RouteNamespace: RouteNamespace{Hosts: map[string]string{
			"web": "pulse.onlv.localhost",
		}},
		Backends: map[string]Backend{"web": {Network: "tcp", Addr: "127.0.0.1:5173"}},
		OwnerPID: os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := registry.Delete(first.SessionID); err != nil || !ok {
		t.Fatalf("delete first ok=%v err=%v", ok, err)
	}
	reloadedSecond, ok := registry.Get(second.SessionID)
	if !ok {
		t.Fatal("second session missing after first delete")
	}
	if len(reloadedSecond.Aliases) != 1 || reloadedSecond.Aliases["web"] != second.Aliases["web"] {
		t.Fatalf("second aliases changed after first delete: %+v", reloadedSecond.Aliases)
	}
	third, err := registry.Upsert(RegisterRequest{
		BaseAppID: "pulse",
		AppRoot:   filepath.Join(t.TempDir(), "worktree-c"),
		SessionID: "review-c",
		RouteNamespace: RouteNamespace{Hosts: map[string]string{
			RouteAPI: "api.onlv.localhost",
		}},
		Backends: map[string]Backend{RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4002"}},
		OwnerPID: os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.Aliases[RouteAPI] != first.Aliases[RouteAPI] {
		t.Fatalf("deleted session alias was not freed: third=%+v first=%+v", third.Aliases, first.Aliases)
	}
}

func TestRegistryRouteTargetForHostUsesCanonicalAndAliasIndex(t *testing.T) {
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	session, err := registry.Upsert(RegisterRequest{
		BaseAppID: "pulse",
		AppRoot:   filepath.Join(t.TempDir(), "worktree-a"),
		SessionID: "review-a",
		RouteNamespace: RouteNamespace{Hosts: map[string]string{
			RouteAPI: "api.onlv.localhost",
			"web":    "pulse.onlv.localhost",
		}},
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
			"web":    {Network: "tcp", Addr: "127.0.0.1:5173"},
		},
		OwnerPID: os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}

	canonicalHost := testRouteHost(t, session.Routes["web"])
	found, route, ok := registry.RouteTargetForHost(canonicalHost)
	if !ok || found.SessionID != session.SessionID || route != "web" {
		t.Fatalf("canonical route target = %+v %q %v, want %s web true", found, route, ok, session.SessionID)
	}
	aliasHost := testRouteHost(t, session.Aliases[RouteAPI])
	found, route, ok = registry.RouteTargetForHost(aliasHost)
	if !ok || found.SessionID != session.SessionID || route != RouteAPI {
		t.Fatalf("alias route target = %+v %q %v, want %s api true", found, route, ok, session.SessionID)
	}
	if _, _, ok := registry.RouteTargetForHost("random.localhost"); ok {
		t.Fatal("random host unexpectedly resolved")
	}
	if _, ok, err := registry.Delete(session.SessionID); err != nil || !ok {
		t.Fatalf("delete session ok=%v err=%v", ok, err)
	}
	if _, _, ok := registry.RouteTargetForHost(canonicalHost); ok {
		t.Fatalf("deleted canonical host %q still resolved", canonicalHost)
	}
	if _, _, ok := registry.RouteTargetForHost(aliasHost); ok {
		t.Fatalf("deleted alias host %q still resolved", aliasHost)
	}
}

func TestSessionRoutesOmitHTTPSDefaultPort(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "main",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
			"web":    {Network: "tcp", Addr: "127.0.0.1:5173"},
		},
	}, "127.0.0.1:443", "https", nil)
	if err != nil {
		t.Fatal(err)
	}
	for route, url := range session.Routes {
		if strings.Contains(url, ":443") {
			t.Fatalf("route %q kept HTTPS default port: %q", route, url)
		}
	}
	if got, want := session.Routes[RouteAPI], "https://api.main.local.dev/"; got != want {
		t.Fatalf("api route = %q, want %q", got, want)
	}

	session, err = NewSession(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "main",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	}, "127.0.0.1:9440", "https", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Routes[RouteAPI]; !strings.Contains(got, ":9440") {
		t.Fatalf("api route omitted non-default port: %q", got)
	}
}

func TestListenRouterExplicitAddrDoesNotFallback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	defer ln.Close()

	router, _, err := listenRouter(addr)
	if err == nil {
		router.Close()
		t.Fatalf("listenRouter(%q) succeeded despite occupied explicit port", addr)
	}
	if !strings.Contains(err.Error(), "choose a different --router-listen") {
		t.Fatalf("listenRouter error = %q, want actionable router-listen message", err)
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
	if session.Owner.PID != os.Getpid() || session.Owner.CmdlineHash == "" || session.Owner.CreatedBy != "scenery up" {
		t.Fatalf("owner = %+v", session.Owner)
	}
	if err := VerifyOwner(session.Owner); err != nil {
		t.Fatalf("VerifyOwner returned error: %v", err)
	}
}

func TestRegistryRejectsDuplicateLiveSessionOwner(t *testing.T) {
	root := t.TempDir()
	duplicate := exec.Command("sleep", "30")
	if err := duplicate.Start(); err != nil {
		t.Fatalf("start duplicate owner fixture: %v", err)
	}
	defer func() {
		_ = duplicate.Process.Kill()
		_ = duplicate.Wait()
	}()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-a",
		Status:    "running",
		OwnerPID:  os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Upsert(RegisterRequest{
		BaseAppID:  "demo",
		AppRoot:    root,
		SessionID:  first.SessionID,
		Status:     "starting",
		OwnerPID:   duplicate.Process.Pid,
		ClaimOwner: true,
	}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("duplicate live owner error = %v, want already running", err)
	}
	updated, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: first.SessionID,
		Status:    "running",
		OwnerPID:  os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.OwnerPID != os.Getpid() {
		t.Fatalf("updated owner pid = %d, want %d", updated.OwnerPID, os.Getpid())
	}
}

func TestRegistryRejectsSecondLiveSessionForAppRoot(t *testing.T) {
	root := t.TempDir()
	secondOwner := exec.Command("sleep", "30")
	if err := secondOwner.Start(); err != nil {
		t.Fatalf("start second owner fixture: %v", err)
	}
	defer func() {
		_ = secondOwner.Process.Kill()
		_ = secondOwner.Wait()
	}()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-a",
		Status:    "running",
		OwnerPID:  os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-c",
		Status:    "starting",
		OwnerPID:  os.Getpid(),
	}); err == nil || !strings.Contains(err.Error(), "already running for app root") {
		t.Fatalf("second live app-root session from same owner error = %v, want already running", err)
	}
	if _, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-b",
		Status:    "starting",
		OwnerPID:  secondOwner.Process.Pid,
	}); err == nil || !strings.Contains(err.Error(), "already running for app root") {
		t.Fatalf("second live app-root session error = %v, want already running", err)
	}
}

func TestRegistryRejectsDuplicateWhenOwnerPIDMovedPastStaleOwnerField(t *testing.T) {
	root := t.TempDir()
	duplicate := exec.Command("sleep", "30")
	if err := duplicate.Start(); err != nil {
		t.Fatalf("start duplicate owner fixture: %v", err)
	}
	defer func() {
		_ = duplicate.Process.Kill()
		_ = duplicate.Wait()
	}()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-a",
		Status:    "running",
		OwnerPID:  os.Getpid(),
		Owner: Owner{
			PID:         99999996,
			StartedAt:   "stale-owner-field",
			CmdlineHash: "sha256:stale-owner-field",
			Exe:         "/stale/owner",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Owner.PID != os.Getpid() {
		t.Fatalf("stored owner pid = %d, want refreshed owner_pid %d", first.Owner.PID, os.Getpid())
	}
	if _, err := registry.Upsert(RegisterRequest{
		BaseAppID:  "demo",
		AppRoot:    root,
		SessionID:  first.SessionID,
		Status:     "starting",
		OwnerPID:   duplicate.Process.Pid,
		ClaimOwner: true,
	}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("duplicate live owner error = %v, want already running", err)
	}
}

func TestOwnerFromRequestRefreshesMismatchedOwnerPID(t *testing.T) {
	owner := OwnerFromRequest(os.Getpid(), Owner{
		PID:         99999995,
		StartedAt:   "stale-owner-field",
		CmdlineHash: "sha256:stale-owner-field",
		Exe:         "/stale/owner",
	}, "test")
	if owner.PID != os.Getpid() {
		t.Fatalf("owner pid = %d, want %d", owner.PID, os.Getpid())
	}
	if err := VerifyOwner(owner); err != nil {
		t.Fatalf("refreshed owner did not verify: %v", err)
	}
}

func TestRegistryClaimsDeadSessionOwner(t *testing.T) {
	root := t.TempDir()
	stale := exec.Command("sleep", "30")
	if err := stale.Start(); err != nil {
		t.Fatalf("start stale owner fixture: %v", err)
	}
	staleOwner := CaptureOwner(stale.Process.Pid, "scenery up")
	if err := stale.Process.Kill(); err != nil {
		t.Fatalf("kill stale owner fixture: %v", err)
	}
	_ = stale.Wait()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-a",
		Status:    "running",
		OwnerPID:  staleOwner.PID,
		Owner:     staleOwner,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := registry.Upsert(RegisterRequest{
		BaseAppID:  "demo",
		AppRoot:    root,
		SessionID:  first.SessionID,
		Status:     "starting",
		OwnerPID:   os.Getpid(),
		ClaimOwner: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.OwnerPID != os.Getpid() {
		t.Fatalf("claimed owner pid = %d, want %d", claimed.OwnerPID, os.Getpid())
	}
}

func TestRegistryOwnedDeleteDoesNotRemoveReplacedOwnerSession(t *testing.T) {
	root := t.TempDir()
	stale := exec.Command("sleep", "30")
	if err := stale.Start(); err != nil {
		t.Fatalf("start stale owner fixture: %v", err)
	}
	staleOwner := CaptureOwner(stale.Process.Pid, "scenery up")
	if err := stale.Process.Kill(); err != nil {
		t.Fatalf("kill stale owner fixture: %v", err)
	}
	_ = stale.Wait()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-a",
		Status:    "running",
		OwnerPID:  staleOwner.PID,
		Owner:     staleOwner,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := registry.Upsert(RegisterRequest{
		BaseAppID:  "demo",
		AppRoot:    root,
		SessionID:  first.SessionID,
		Status:     "running",
		OwnerPID:   os.Getpid(),
		ClaimOwner: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted, ok, err := registry.DeleteOwned(claimed.SessionID, staleOwner.PID)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("stale owner delete removed session: %+v", deleted)
	}
	current, ok := registry.Get(claimed.SessionID)
	if !ok {
		t.Fatal("session was removed by stale owner delete")
	}
	if current.OwnerPID != os.Getpid() || current.Owner.PID != os.Getpid() {
		t.Fatalf("current owner = owner_pid:%d owner:%+v, want %d", current.OwnerPID, current.Owner, os.Getpid())
	}
}

func TestRegistryStrictOwnedDeleteRequiresOwnerFingerprintMatch(t *testing.T) {
	root := t.TempDir()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	session, err := registry.Upsert(RegisterRequest{
		BaseAppID:  "demo",
		AppRoot:    root,
		SessionID:  "review-a",
		Status:     "running",
		OwnerPID:   os.Getpid(),
		ClaimOwner: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	staleOwner := session.Owner
	staleOwner.CmdlineHash = "sha256:older-owner"
	if deleted, ok, err := registry.DeleteOwnedIdentity(session.SessionID, os.Getpid(), staleOwner, true); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("strict stale fingerprint delete removed session: %+v", deleted)
	}
	current, ok := registry.Get(session.SessionID)
	if !ok {
		t.Fatal("session was removed by stale owner fingerprint")
	}
	if current.Owner.CmdlineHash != session.Owner.CmdlineHash {
		t.Fatalf("current owner fingerprint changed: %+v", current.Owner)
	}
	if _, ok, err := registry.DeleteOwnedIdentity(session.SessionID, os.Getpid(), session.Owner, true); err != nil || !ok {
		t.Fatalf("strict current owner delete ok=%v err=%v", ok, err)
	}
}

func TestRegistryRequiresClaimForDeadSessionOwner(t *testing.T) {
	root := t.TempDir()
	stale := exec.Command("sleep", "30")
	if err := stale.Start(); err != nil {
		t.Fatalf("start stale owner fixture: %v", err)
	}
	staleOwner := CaptureOwner(stale.Process.Pid, "scenery up")
	if err := stale.Process.Kill(); err != nil {
		t.Fatalf("kill stale owner fixture: %v", err)
	}
	_ = stale.Wait()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-a",
		Status:    "running",
		OwnerPID:  staleOwner.PID,
		Owner:     staleOwner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: first.SessionID,
		Status:    "running",
		OwnerPID:  os.Getpid(),
	}); err == nil {
		t.Fatal("expected non-claiming replacement of dead owner to fail")
	}
}

func TestRegistryTracksSessionProcesses(t *testing.T) {
	root := t.TempDir()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "sessions.json"), "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	session, err := registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: "review-a",
		Status:    "running",
		AppPID:    strconv.Itoa(os.Getpid()),
		Processes: map[string]Process{
			"frontend-web": {PID: os.Getpid()},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Processes[RouteAPI].PID != os.Getpid() || session.Processes["frontend-web"].PID != os.Getpid() {
		t.Fatalf("session processes = %+v", session.Processes)
	}
	if session.Processes[RouteAPI].Owner.PID != os.Getpid() || session.Processes["frontend-web"].Owner.PID != os.Getpid() {
		t.Fatalf("process owners = %+v", session.Processes)
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

func TestRegistryPersistsAndClearsSubstrateLeases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	registry, err := OpenRegistry(path, "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().Add(-time.Minute).UTC()
	updatedAt := time.Now().UTC()
	substrate, err := registry.UpsertSubstrate(UpsertSubstrateRequest{
		Kind:     SubstrateZeroFS + "-shared-cell",
		Status:   "running",
		OwnerPID: os.Getpid(),
		Leases: map[string]SubstrateLease{
			"session-a": {
				SessionID: "session-a",
				AppRoot:   "/tmp/app-a",
				Route:     "storage",
				URL:       "http://storage.session-a.local.dev",
				OwnerPID:  os.Getpid(),
				CreatedAt: createdAt,
				UpdatedAt: updatedAt,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if substrate.Leases["session-a"].Route != "storage" {
		t.Fatalf("substrate leases = %+v", substrate.Leases)
	}

	substrate, err = registry.UpsertSubstrate(UpsertSubstrateRequest{
		Kind:   SubstrateZeroFS + "-shared-cell",
		Status: "running",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(substrate.Leases) != 1 || !substrate.Leases["session-a"].CreatedAt.Equal(createdAt) {
		t.Fatalf("substrate leases should be preserved on nil lease update: %+v", substrate.Leases)
	}

	substrate, err = registry.UpsertSubstrate(UpsertSubstrateRequest{
		Kind:   SubstrateZeroFS + "-shared-cell",
		Status: "running",
		Leases: map[string]SubstrateLease{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(substrate.Leases) != 0 {
		t.Fatalf("substrate leases should be cleared by explicit empty lease map: %+v", substrate.Leases)
	}

	reopened, err := OpenRegistry(path, "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.GetSubstrate(SubstrateZeroFS + "-shared-cell")
	if !ok || len(got.Leases) != 0 {
		t.Fatalf("persisted substrate leases = %+v ok=%v", got.Leases, ok)
	}
}

func TestRegistryPersistsSubstrateExitState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	registry, err := OpenRegistry(path, "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	exit := SubstrateExit{
		Component:     "traces",
		PID:           456,
		StartedAt:     time.Now().Add(-time.Second).UTC(),
		ExitedAt:      time.Now().UTC(),
		ExitCode:      2,
		Error:         "exit status 2",
		LogPath:       "/tmp/traces.stderr.log",
		StdoutLogPath: "/tmp/traces.stdout.log",
		StderrLogPath: "/tmp/traces.stderr.log",
	}
	substrate, err := registry.UpsertSubstrate(UpsertSubstrateRequest{
		Kind:     SubstrateVictoria,
		Status:   "degraded",
		OwnerPID: 123,
		PIDs:     map[string]int{"traces": 456},
		LastExit: &exit,
		ComponentExits: map[string]SubstrateExit{
			"traces": exit,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if substrate.Status != "degraded" || substrate.LastExit == nil || substrate.LastExit.ExitCode != 2 {
		t.Fatalf("substrate exit state = %+v", substrate)
	}

	secondExit := exit
	secondExit.Component = "logs"
	secondExit.PID = 789
	secondExit.ExitCode = 9
	substrate, err = registry.UpsertSubstrate(UpsertSubstrateRequest{
		Kind:     SubstrateVictoria,
		Status:   "degraded",
		OwnerPID: 123,
		PIDs:     map[string]int{"logs": 789},
		LastExit: &secondExit,
		ComponentExits: map[string]SubstrateExit{
			"logs": secondExit,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(substrate.ComponentExits) != 2 || substrate.ComponentExits["traces"].ExitCode != 2 || substrate.ComponentExits["logs"].ExitCode != 9 {
		t.Fatalf("component exits were not merged: %+v", substrate.ComponentExits)
	}

	reopened, err := OpenRegistry(path, "127.0.0.1:9440")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.GetSubstrate(SubstrateVictoria)
	if !ok || got.LastExit == nil || got.ComponentExits["logs"].ExitCode != 9 {
		t.Fatalf("persisted substrate exit state = %+v ok=%v", got, ok)
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
	publicHost = testRouteHost(t, session.Routes[RouteAPI])

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

func TestServerUsesRunningEdgeStateForPublicRoutes(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EdgeTokenPath, []byte("edge-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteEdgeState(paths.EdgeStatePath, EdgeState{
		Kind:         EdgeKindCaddy,
		Status:       EdgeStatusRunning,
		PID:          os.Getpid(),
		PublicAddr:   "127.0.0.1:443",
		PublicScheme: "https",
		UpstreamAddr: "127.0.0.1:9440",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(RunOptions{
		SocketPath: paths.SocketPath,
		RouterAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	session, err := server.registry.Upsert(RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		Branch:    "feature/edge",
		OwnerPID:  os.Getpid(),
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(session.Routes[RouteAPI], "https://api."+session.SessionID+"."+DefaultRouteBaseDomain+"/") {
		t.Fatalf("edge route = %q", session.Routes[RouteAPI])
	}
	if strings.Contains(session.Routes[RouteAPI], ":443") {
		t.Fatalf("edge route should omit default HTTPS port: %q", session.Routes[RouteAPI])
	}
	if server.RouterAddr() == server.PublicRouterAddr() {
		t.Fatalf("internal and public router addr should differ with edge: %s", server.RouterAddr())
	}
	if server.RouterScheme() != "https" {
		t.Fatalf("public router scheme = %q, want https", server.RouterScheme())
	}
}

func TestTLSAllowEndpointRequiresLiveRegisteredHost(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	server, err := NewServer(RunOptions{RouterAddr: "127.0.0.1:0"})
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
	live, err := client.Register(ctx, RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		Branch:    "feature/live",
		OwnerPID:  os.Getpid(),
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := client.Register(ctx, RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		Branch:    "feature/stale",
		OwnerPID:  99999991,
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4001"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTLSAllowStatus := func(host string, want int) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/v1/tls/allow?domain="+url.QueryEscape(host), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("tls allow %s status = %d, want %d", host, resp.StatusCode, want)
		}
	}
	assertTLSAllowStatus(testRouteHost(t, live.Routes[RouteAPI]), http.StatusNoContent)
	assertTLSAllowStatus(testRouteHost(t, stale.Routes[RouteAPI]), http.StatusNotFound)
	assertTLSAllowStatus("random.localhost", http.StatusNotFound)
}

func TestRouterTrustsForwardedHeadersOnlyFromEdgeToken(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EdgeTokenPath, []byte("edge-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var seen []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seen = append(seen, req.Header.Get("X-Forwarded-Proto")+":"+req.Header.Get("X-Forwarded-Port"))
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()
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
		Branch:    "feature/forwarded",
		OwnerPID:  os.Getpid(),
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: strings.TrimPrefix(backend.URL, "http://")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host := testRouteHost(t, session.Routes[RouteAPI])
	request := func(token string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Port", "443")
		if token != "" {
			req.Header.Set("X-Scenery-Edge-Token", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	request("edge-token")
	request("wrong-token")
	if strings.Join(seen, ",") != "https:443,http:80" {
		t.Fatalf("forwarded proto/port seen = %v", seen)
	}
}

func TestServerDeleteOwnedSkipsMismatchedOwnerPID(t *testing.T) {
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
	session, err := client.Register(ctx, RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		SessionID: "review-a",
		Status:    "running",
		OwnerPID:  os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	deletedSession, deleted, err := client.DeleteOwned(ctx, session.SessionID, 99999993, false)
	if err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Fatalf("mismatched owner delete removed session: %+v", deletedSession)
	}
	sessions, err := client.List(ctx, session.AppRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions after skipped delete = %+v, want one", sessions)
	}
	current := sessions[0]
	if current.OwnerPID != os.Getpid() {
		t.Fatalf("current owner pid = %d, want %d", current.OwnerPID, os.Getpid())
	}
}

func TestServerRouterTLSGeneratesHTTPSRoutes(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
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
	if !strings.HasPrefix(apiURL, "https://api."+session.SessionID+"."+DefaultRouteBaseDomain+":") {
		t.Fatalf("api route = %q", apiURL)
	}

	routerAddr := server.RouterAddr()
	httpClient := &http.Client{Transport: &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
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
	if resp.ProtoMajor != 2 {
		t.Fatalf("router response protocol = %s, want HTTP/2", resp.Proto)
	}
}

func TestServerRoutesOwnedAliasThroughTLS(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	var publicRequestHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != publicRequestHost {
			t.Fatalf("backend host = %q, want %q", req.Host, publicRequestHost)
		}
		_, _ = io.WriteString(w, "alias tls ok")
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
		BaseAppID: "pulse",
		AppRoot:   t.TempDir(),
		SessionID: "review-a",
		RouteNamespace: RouteNamespace{
			Hosts: map[string]string{
				RouteAPI: "api.onlv.localhost",
			},
		},
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: backendAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	aliasURL := session.Aliases[RouteAPI]
	if aliasURL == "" {
		t.Fatalf("session missing api alias: %+v", session)
	}
	if !server.agentTLSHostAllowed("api.onlv.localhost") {
		t.Fatal("TLS host allow-list rejected owned alias")
	}
	if server.agentTLSHostAllowed("unclaimed.onlv.localhost") {
		t.Fatal("TLS host allow-list accepted unclaimed alias")
	}
	parsedAliasURL, err := url.Parse(aliasURL)
	if err != nil {
		t.Fatal(err)
	}
	publicRequestHost = parsedAliasURL.Host

	routerAddr := server.RouterAddr()
	httpClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", routerAddr)
		},
	}}
	resp, err := httpClient.Get(aliasURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "alias tls ok" {
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
	if session.Routes["web"] == "" || !strings.Contains(session.Routes["web"], "web."+session.SessionID+"."+DefaultRouteBaseDomain) {
		t.Fatalf("frontend route = %q", session.Routes["web"])
	}
	publicHost = testRouteHost(t, session.Routes["web"])

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

func TestServerRoutesFrontendSPAFallback(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())

	var frontendHits []string
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		frontendHits = append(frontendHits, req.URL.Path)
		switch req.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "frontend shell")
		case "/assets/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(w, "asset ok")
		default:
			http.NotFound(w, req)
		}
	}))
	defer frontend.Close()
	frontendAddr := strings.TrimPrefix(frontend.URL, "http://")

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/__scenery/config" {
			t.Fatalf("api path = %q, want /__scenery/config", req.URL.Path)
		}
		_, _ = io.WriteString(w, "config ok")
	}))
	defer api.Close()
	apiAddr := strings.TrimPrefix(api.URL, "http://")

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
		Branch:    "feature/frontend-fallback",
		Backends: map[string]Backend{
			RouteAPI: {Network: "tcp", Addr: apiAddr},
			"web":    {Network: "tcp", Addr: frontendAddr},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	publicHost := testRouteHost(t, session.Routes["web"])

	request := func(targetPath, accept string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+targetPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = publicHost
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(body)
	}

	status, body := request("/settings/profile", "text/html")
	if status != http.StatusOK || body != "frontend shell" {
		t.Fatalf("deep link response status=%d body=%q", status, body)
	}
	if strings.Join(frontendHits, ",") != "/settings/profile,/" {
		t.Fatalf("frontend hits after fallback = %q", strings.Join(frontendHits, ","))
	}

	status, body = request("/assets/app.js", "text/html")
	if status != http.StatusOK || body != "asset ok" {
		t.Fatalf("asset response status=%d body=%q", status, body)
	}
	status, body = request("/assets/missing.js", "text/html")
	if status != http.StatusNotFound || strings.Contains(body, "frontend shell") {
		t.Fatalf("missing asset response status=%d body=%q", status, body)
	}
	status, body = request("/api/users", "text/html")
	if status != http.StatusNotFound || strings.Contains(body, "frontend shell") {
		t.Fatalf("api path response status=%d body=%q", status, body)
	}
	status, body = request("/sync/shapes", "text/html")
	if status != http.StatusNotFound || strings.Contains(body, "frontend shell") {
		t.Fatalf("sync path response status=%d body=%q", status, body)
	}
	status, body = request("/__scenery/config", "text/html")
	if status != http.StatusOK || body != "config ok" {
		t.Fatalf("config response status=%d body=%q", status, body)
	}
	status, body = request("/__scenery/unknown", "text/html")
	if status != http.StatusNotFound || strings.Contains(body, "frontend shell") {
		t.Fatalf("control path response status=%d body=%q", status, body)
	}
	status, body = request("/settings/profile", "application/json")
	if status != http.StatusNotFound || strings.Contains(body, "frontend shell") {
		t.Fatalf("json request response status=%d body=%q", status, body)
	}
}

func TestServerRoutesParallelSessionsWithoutRouteCollision(t *testing.T) {
	t.Setenv(envAgentHome, t.TempDir())
	backend := func(label string) (*httptest.Server, *string) {
		var addr string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !strings.HasSuffix(req.Host, "."+DefaultRouteBaseDomain) {
				t.Fatalf("%s backend host = %q, want public dev-domain host", label, req.Host)
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
	assertRouteBody(testRouteHost(t, sessionA.Routes[RouteAPI]), "api-a")
	assertRouteBody(testRouteHost(t, sessionA.Routes["web"]), "web-a")
	assertRouteBody(testRouteHost(t, sessionB.Routes[RouteAPI]), "api-b")
	assertRouteBody(testRouteHost(t, sessionB.Routes["web"]), "web-b")
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
	if session.Routes[RouteGrafana] == "" || !strings.Contains(session.Routes[RouteGrafana], "grafana."+session.SessionID+"."+DefaultRouteBaseDomain) {
		t.Fatalf("grafana route = %q", session.Routes[RouteGrafana])
	}
	publicHost = testRouteHost(t, session.Routes[RouteGrafana])

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

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = testRouteHost(t, session.Routes[RouteDashboard])
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

	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = testRouteHost(t, session.Routes[RouteDashboard])
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "global dashboard ok" {
		t.Fatalf("global console response status=%d body=%q", resp.StatusCode, body)
	}
	if gotPath != "/" {
		t.Fatalf("global dashboard path = %q, want /", gotPath)
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
	sessionPath = "/"
	appPath = "/" + session.SessionID

	consoleHost := testRouteHost(t, session.Routes[RouteDashboard])
	httpClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Host = consoleHost
			return nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+server.routerAddr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = consoleHost
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
	req.Host = testRouteHost(t, session.Routes[RouteAPI])
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
