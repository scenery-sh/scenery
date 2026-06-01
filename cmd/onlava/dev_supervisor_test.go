package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/devdash"
	"github.com/pbrazdil/onlava/internal/workers"
	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

func TestAppChildEnvForcesColorWhenRequested(t *testing.T) {
	t.Parallel()

	env := appChildEnv([]string{"A=1"}, true, "B=2")
	if !containsString(env, "CLICOLOR_FORCE=1") {
		t.Fatalf("appChildEnv(%v) missing CLICOLOR_FORCE=1", env)
	}
}

func TestAppChildEnvLeavesColorUnsetWhenDisabled(t *testing.T) {
	t.Parallel()

	env := appChildEnv([]string{"A=1"}, false, "B=2")
	if containsString(env, "CLICOLOR_FORCE=1") {
		t.Fatalf("appChildEnv(%v) unexpectedly added CLICOLOR_FORCE=1", env)
	}
}

func TestSessionIdentityEnvUsesAgentSession(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		cfg: app.Config{Name: "demo"},
		agentSession: &localagent.Session{
			SessionID:    "feature-a-123abc",
			BaseAppID:    "demo",
			RuntimeAppID: "demo--feature-a-123abc",
			AppRoot:      "/tmp/onlv-a",
			Branch:       "feature/a",
		},
	}
	env := s.sessionIdentityEnv()
	for _, want := range []string{
		"ONLAVA_SESSION_ID=feature-a-123abc",
		"ONLAVA_BASE_APP_ID=demo",
		"ONLAVA_RUNTIME_APP_ID=demo--feature-a-123abc",
		"ONLAVA_APP_ROOT_HASH=" + appRootHash("/tmp/onlv-a"),
		"ONLAVA_BRANCH=feature/a",
		"ONLAVA_WORKTREE=onlv-a",
	} {
		if !containsString(env, want) {
			t.Fatalf("sessionIdentityEnv() = %v, missing %q", env, want)
		}
	}
}

func TestSessionTemporalEnvUsesAgentSession(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		cfg: app.Config{
			Name: "demo",
			Temporal: app.TemporalConfig{
				Enabled: true,
			},
		},
		status: devdash.AppRecord{ID: "demo"},
		agentSession: &localagent.Session{
			SessionID: "feature-a-123abc",
			BaseAppID: "demo",
		},
	}
	env := s.sessionTemporalEnv()
	for _, want := range []string{
		"ONLAVA_TEMPORAL_TASK_QUEUE_PREFIX=onlava.demo.feature-a-123abc",
		"ONLAVA_TEMPORAL_DEPLOYMENT_NAME=onlava-demo-feature-a-123abc",
		"ONLAVA_BUILD_ID=feature-a-123abc",
	} {
		if !containsString(env, want) {
			t.Fatalf("sessionTemporalEnv() = %v, missing %q", env, want)
		}
	}
}

func TestSessionAuthEnvUsesRoutedSessionURLs(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		cfg: app.Config{
			Name: "demo",
			Auth: app.AuthConfig{
				Enabled:             true,
				PublicAppURLEnv:     "APP_URL",
				APIBaseURLEnv:       "API_URL",
				AuthCookieDomainEnv: "COOKIE_DOMAIN",
			},
		},
		addr: "127.0.0.1:4000",
		agentSession: &localagent.Session{
			SessionID: "feature-a-123abc",
			Routes: map[string]string{
				localagent.RouteAPI: "http://api.feature-a-123abc.onlava.localhost",
			},
		},
	}
	env := s.sessionAuthEnv()
	for _, want := range []string{
		"API_URL=http://api.feature-a-123abc.onlava.localhost",
		"API_BASE_URL=http://api.feature-a-123abc.onlava.localhost",
		"ONLAVA_API_BASE_URL=http://api.feature-a-123abc.onlava.localhost",
		"APP_URL=http://api.feature-a-123abc.onlava.localhost",
		"PUBLIC_APP_URL=http://api.feature-a-123abc.onlava.localhost",
		"ONLAVA_PUBLIC_APP_URL=http://api.feature-a-123abc.onlava.localhost",
		"COOKIE_DOMAIN=",
		"AUTH_COOKIE_DOMAIN=",
		"ONLAVA_AUTH_COOKIE_DOMAIN=",
	} {
		if !containsString(env, want) {
			t.Fatalf("sessionAuthEnv() = %v, missing %q", env, want)
		}
	}
}

func TestAppStatusIncludesVisibleSessionRoutes(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{
		status: devdash.AppRecord{
			ID:        "demo",
			SessionID: "feature-a-123abc",
			Running:   true,
		},
		agentSession: &localagent.Session{
			SessionID: "feature-a-123abc",
			Routes: map[string]string{
				localagent.RouteAPI:       "https://api.feature-a-123abc.onlava.localhost:9440/",
				localagent.RouteDashboard: "https://console.onlava.localhost:9440/s/feature-a-123abc",
				localagent.RouteGrafana:   "https://grafana.feature-a-123abc.onlava.localhost:9440/",
				"web":                     "https://web.feature-a-123abc.onlava.localhost:9440/",
				"victoria":                "https://victoria.feature-a-123abc.onlava.localhost:9440/",
			},
		},
	}
	status := s.appStatus()
	for _, name := range []string{localagent.RouteAPI, localagent.RouteDashboard, localagent.RouteGrafana, "web"} {
		if status.Routes[name] == "" {
			t.Fatalf("appStatus routes missing %q: %+v", name, status.Routes)
		}
	}
	if _, ok := status.Routes["victoria"]; ok {
		t.Fatalf("appStatus exposed victoria route: %+v", status.Routes)
	}
}

func TestAppEnvWithDotEnvAddsMissingValuesWithoutOverridingProcessEnv(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("A=from-file\nB=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := appEnvWithDotEnv([]string{"A=from-process"}, root)
	if err != nil {
		t.Fatalf("appEnvWithDotEnv: %v", err)
	}
	if !containsString(env, "A=from-process") {
		t.Fatalf("env missing process value: %v", env)
	}
	if containsString(env, "A=from-file") {
		t.Fatalf("env should not override process value: %v", env)
	}
	if !containsString(env, "B=2") {
		t.Fatalf("env missing .env value: %v", env)
	}
}

func TestAppEnvWithDotEnvCanLoadLocalOverride(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("A=from-env\nB=from-env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("B=from-local\nC=from-local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := appEnvWithDotEnv([]string{"A=from-process"}, root, ".env", ".env.local")
	if err != nil {
		t.Fatalf("appEnvWithDotEnv: %v", err)
	}
	if !containsString(env, "A=from-process") {
		t.Fatalf("env missing process value: %v", env)
	}
	if !containsString(env, "B=from-local") {
		t.Fatalf("env missing .env.local override: %v", env)
	}
	if !containsString(env, "C=from-local") {
		t.Fatalf("env missing .env.local value: %v", env)
	}
}

func TestAppEnvWithRequiredDotEnvFailsWhenMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, err := appEnvWithRequiredDotEnv(nil, root)
	if err == nil {
		t.Fatal("appEnvWithRequiredDotEnv returned nil error")
	}
	if !strings.Contains(err.Error(), "missing required local env file") || !strings.Contains(err.Error(), filepath.Join(root, ".env")) {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateLocalSecretsFilesRequiresDotEnv(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	err := validateLocalSecretsFiles(root)
	if err == nil {
		t.Fatal("validateLocalSecretsFiles returned nil error")
	}
	if !strings.Contains(err.Error(), "missing required local env file") {
		t.Fatalf("error = %v", err)
	}
}

func TestAppProcessEnvAllowsMissingDotEnvForProduction(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	env, err := appProcessEnv(root, app.Config{Name: "demo"}, "json", "production")
	if err != nil {
		t.Fatalf("appProcessEnv production returned error: %v", err)
	}
	if !containsString(env, "ONLAVA_ENV=production") || !containsString(env, "ONLAVA_RUNTIME_ENV=production") {
		t.Fatalf("production env missing markers: %v", env)
	}
}

func TestAppStartupExitErrorIncludesOutput(t *testing.T) {
	t.Parallel()

	output := &safeLineTail{limit: 10}
	output.Add("warning: something happened")
	output.Add("fatal: database missing")
	err := appStartupExitError(&runningApp{output: output}, os.ErrInvalid)
	for _, want := range []string{"onlava app exited during startup", os.ErrInvalid.Error(), "fatal: database missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestTCPAddrAcceptsConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if !tcpAddrAcceptsConnections(addr) {
		t.Fatalf("tcpAddrAcceptsConnections(%q) = false, want true", addr)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if tcpAddrAcceptsConnections(addr) {
		t.Fatalf("tcpAddrAcceptsConnections(%q) after close = true, want false", addr)
	}
}

func TestTemporalDevHelpers(t *testing.T) {
	t.Setenv(onlavaruntime.DefaultTemporalAddressEnv, "")

	host, port, err := splitTemporalAddress("127.0.0.1:7233")
	if err != nil {
		t.Fatalf("splitTemporalAddress: %v", err)
	}
	if host != "127.0.0.1" || port != 7233 {
		t.Fatalf("host/port = %s/%d", host, port)
	}
	if _, _, err := splitTemporalAddress("not-a-host-port"); err == nil {
		t.Fatal("expected invalid address error")
	}

	root := t.TempDir()
	if got, want := temporalLocalDBPath(root, ".onlava/temporal/dev.db"), filepath.Join(root, ".onlava/temporal/dev.db"); got != want {
		t.Fatalf("temporalLocalDBPath = %q, want %q", got, want)
	}

	cfg := app.TemporalConfig{
		Enabled:    true,
		AddressEnv: "CUSTOM_TEMPORAL_ADDRESS",
		Namespace:  "orders",
		Local: app.TemporalLocalConfig{
			AutoStart:  true,
			DBFilename: ".onlava/temporal/dev.db",
		},
	}
	rtCfg := temporalRuntimeConfigFromApp(cfg)
	if !rtCfg.Enabled || rtCfg.AddressEnv != "CUSTOM_TEMPORAL_ADDRESS" || rtCfg.Namespace != "orders" || !rtCfg.Local.AutoStart {
		t.Fatalf("runtime temporal config = %+v", rtCfg)
	}

	server := &temporalDevServer{info: onlavaRuntimeInfoForTest()}
	env := server.Env()
	if !containsString(env, "CUSTOM_TEMPORAL_ADDRESS=127.0.0.1:7233") || !containsString(env, "TEMPORAL_NAMESPACE=orders") {
		t.Fatalf("temporal env = %+v", env)
	}
	if got := temporalUIUpstreamForConfig(app.Config{Name: "test"}); got != "127.0.0.1:8233" {
		t.Fatalf("temporal UI upstream = %q, want %q", got, "127.0.0.1:8233")
	}
}

func TestTypeScriptWorkerAutoStartEnablesTemporalDevServer(t *testing.T) {
	cfg := app.Config{
		Name: "demo",
		Temporal: app.TemporalConfig{
			TypeScript: app.TemporalTypeScript{
				Enabled:   true,
				AutoStart: true,
			},
		},
	}
	ts := workers.TypeScriptWorkerModel{Activities: []workers.TypeScriptActivity{{
		Name:      "house.RenderRoofPreview/v1",
		TaskQueue: "onlv.house.preview.ts",
	}}}

	got := effectiveDevConfigForTypeScriptWorker(cfg, ts)
	if !got.Temporal.Enabled || got.Temporal.Mode != "local" || !got.Temporal.Local.AutoStart {
		t.Fatalf("effective TypeScript Temporal dev config = %+v", got.Temporal)
	}
}

func TestTypeScriptWorkerAutoStartRequiresActivity(t *testing.T) {
	cfg := app.Config{
		Name: "demo",
		Temporal: app.TemporalConfig{
			TypeScript: app.TemporalTypeScript{
				Enabled:   true,
				AutoStart: true,
			},
		},
	}
	if typeScriptWorkerAutoStartEnabled(cfg, workers.TypeScriptWorkerModel{}) {
		t.Fatal("typeScriptWorkerAutoStartEnabled returned true without activities")
	}
}

func TestTypeScriptWorkerEnvUsesTemporalAndSessionOverrides(t *testing.T) {
	s := &devSupervisor{
		root: "/tmp/onlv-demo",
		cfg: app.Config{
			Name: "demo",
			Temporal: app.TemporalConfig{
				Enabled: true,
			},
		},
		status:   devdash.AppRecord{ID: "demo"},
		temporal: &temporalDevServer{info: onlavaRuntimeInfoForTest()},
		agentSession: &localagent.Session{
			SessionID:    "feature-a-123abc",
			BaseAppID:    "demo",
			RuntimeAppID: "demo--feature-a-123abc",
			AppRoot:      "/tmp/onlv-demo",
			Branch:       "feature/a",
		},
	}

	env := s.typeScriptWorkerEnv([]string{
		"TEMPORAL_ADDRESS=old:7233",
		"ONLAVA_BUILD_ID=old-build",
	})
	for _, want := range []string{
		"TEMPORAL_ADDRESS=127.0.0.1:7233",
		"TEMPORAL_NAMESPACE=orders",
		"ONLAVA_APP_ID=demo",
		"ONLAVA_APP_ROOT=/tmp/onlv-demo",
		"ONLAVA_ROLE=typescript-worker",
		fmt.Sprintf("ONLAVA_DEV_SUPERVISOR_PID=%d", os.Getpid()),
		"ONLAVA_TEMPORAL_TASK_QUEUE_PREFIX=onlava.demo.feature-a-123abc",
		"ONLAVA_TEMPORAL_DEPLOYMENT_NAME=onlava-demo-feature-a-123abc",
		"ONLAVA_BUILD_ID=feature-a-123abc",
		"ONLAVA_SESSION_ID=feature-a-123abc",
	} {
		if !containsString(env, want) {
			t.Fatalf("typeScriptWorkerEnv() = %v, missing %q", env, want)
		}
	}
	if countEnvKey(env, "ONLAVA_BUILD_ID") != 1 || countEnvKey(env, "TEMPORAL_ADDRESS") != 1 {
		t.Fatalf("typeScriptWorkerEnv() has duplicate overrides: %v", env)
	}
}

func TestCompactEnvOverridesKeepsLastValue(t *testing.T) {
	got := compactEnvOverrides([]string{"A=1", "B=2", "A=3"})
	want := []string{"B=2", "A=3"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("compactEnvOverrides() = %v, want %v", got, want)
	}
}

func TestTemporalSubstrateRoundTrip(t *testing.T) {
	server := &temporalDevServer{
		info:   onlavaRuntimeInfoForTest(),
		uiURL:  "http://127.0.0.1:8233",
		dbPath: filepath.Join(t.TempDir(), "temporal.db"),
	}
	req := server.SubstrateRequest(123)
	if req.Kind != localagent.SubstrateTemporal || req.OwnerPID != 123 {
		t.Fatalf("substrate request = %+v", req)
	}
	if req.Endpoints["address"] != "127.0.0.1:7233" || req.Endpoints["namespace"] != "orders" {
		t.Fatalf("substrate endpoints = %+v", req.Endpoints)
	}
	if req.URLs["ui"] != "http://127.0.0.1:8233" {
		t.Fatalf("substrate urls = %+v", req.URLs)
	}

	restored := temporalDevServerFromSubstrate(localagent.Substrate{
		Kind:      localagent.SubstrateTemporal,
		URLs:      req.URLs,
		Endpoints: req.Endpoints,
	}, "orders", app.TemporalConfig{
		Enabled:    true,
		AddressEnv: "CUSTOM_TEMPORAL_ADDRESS",
		Namespace:  "default",
		Local: app.TemporalLocalConfig{
			AutoStart: true,
		},
	})
	if restored == nil {
		t.Fatal("restored temporal server is nil")
	}
	if !restored.external || restored.info.Address != "127.0.0.1:7233" || restored.info.Namespace != "orders" {
		t.Fatalf("restored server = %+v", restored)
	}
	if restored.URL() != "http://127.0.0.1:8233" {
		t.Fatalf("restored UI URL = %q", restored.URL())
	}
}

func countEnvKey(env []string, key string) int {
	prefix := key + "="
	count := 0
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			count++
		}
	}
	return count
}

func TestFrontendURLsFromAgentRoutes(t *testing.T) {
	urls := frontendURLsFromAgentRoutes(map[string]string{
		localagent.RouteAPI:       "http://api.session.onlava.localhost",
		localagent.RouteDashboard: "http://console.onlava.localhost/s/session",
		localagent.RouteGrafana:   "http://grafana.session.onlava.localhost",
		"web":                     "http://web.session.onlava.localhost",
		"blog":                    "http://blog.session.onlava.localhost",
		"electric":                "http://electric.session.onlava.localhost",
		localagent.RouteTemporal:  "http://temporal.session.onlava.localhost",
	}, map[string]app.FrontendConfig{"web": {}, "blog": {}})
	if len(urls) != 2 {
		t.Fatalf("frontend urls = %+v", urls)
	}
	if urls["web"] != "http://web.session.onlava.localhost" || urls["blog"] != "http://blog.session.onlava.localhost" {
		t.Fatalf("frontend urls = %+v", urls)
	}
}

func TestTemporalURLUsesAgentRoute(t *testing.T) {
	s := &devSupervisor{
		agentSession: &localagent.Session{Routes: map[string]string{
			localagent.RouteTemporal: "http://temporal.session.onlava.localhost",
		}},
		temporal: &temporalDevServer{info: onlavaRuntimeInfoForTest()},
	}
	if got, want := s.temporalURL(), "http://temporal.session.onlava.localhost"; got != want {
		t.Fatalf("temporalURL() = %q, want %q", got, want)
	}
}

func TestBackendFromHTTPURL(t *testing.T) {
	got := backendFromHTTPURL("http://127.0.0.1:10429")
	if got.Network != "tcp" || got.Addr != "127.0.0.1:10429" {
		t.Fatalf("backendFromHTTPURL() = %+v", got)
	}
}

func TestDevReportURLUsesLocalDashboardReportEndpoint(t *testing.T) {
	s := &devSupervisor{
		agentSession: &localagent.Session{
			Routes: map[string]string{
				localagent.RouteDashboard: "http://console.session.onlava.localhost:4100/s/session",
			},
		},
	}
	if got, want := s.devReportURL(), "http://127.0.0.1:9401/__onlava/report"; got != want {
		t.Fatalf("devReportURL() = %q, want %q", got, want)
	}
}

func TestDevReportURLUsesAgentDashboardBackend(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	server, err := localagent.NewServer(localagent.RunOptions{
		RouterAddr: "127.0.0.1:0",
		DashboardBackend: localagent.Backend{
			Network: "tcp",
			Addr:    "127.0.0.1:45678",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("agent shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for agent shutdown")
		}
	}()

	client := localagent.NewClient(server.Paths().SocketPath)
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	s := &devSupervisor{agent: client}
	if got, want := s.devReportURL(), "http://127.0.0.1:45678/__onlava/report"; got != want {
		t.Fatalf("devReportURL() = %q, want %q", got, want)
	}
}

func onlavaRuntimeInfoForTest() onlavaruntime.TemporalRuntimeInfo {
	return onlavaruntime.TemporalRuntimeInfo{
		Enabled:         true,
		Address:         "127.0.0.1:7233",
		AddressEnv:      "CUSTOM_TEMPORAL_ADDRESS",
		Namespace:       "orders",
		TaskQueuePrefix: "onlava.orders",
	}
}

func TestStripANSI(t *testing.T) {
	input := []byte("\x1b[34mTRC\x1b[0m request completed code=ok\n")
	got := stripANSI(input)
	want := []byte("TRC request completed code=ok\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("stripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestIsExpectedOutputReadError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "eof", err: io.EOF, want: true},
		{name: "os err closed", err: os.ErrClosed, want: true},
		{name: "net err closed", err: net.ErrClosed, want: true},
		{name: "wrapped path error", err: &fs.PathError{Op: "read", Path: "|0", Err: os.ErrClosed}, want: true},
		{name: "other", err: io.ErrUnexpectedEOF, want: false},
	}
	for _, tt := range tests {
		if got := isExpectedOutputReadError(tt.err); got != tt.want {
			t.Fatalf("%s: isExpectedOutputReadError(%v) = %v, want %v", tt.name, tt.err, got, tt.want)
		}
	}
}

func TestListAppsReturnsOnlyActiveSupervisorApp(t *testing.T) {
	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	for _, rec := range []devdash.AppRecord{
		{ID: "basicapp", Name: "basicapp", Root: "/tmp/basicapp", UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "cronapp", Name: "cronapp", Root: "/tmp/cronapp", UpdatedAt: now.Add(-1 * time.Minute)},
		{ID: "demoapp-dev", Name: "demoapp", Root: "/tmp/demoapp", Running: true, UpdatedAt: now},
	} {
		if err := store.UpsertApp(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	s := &devSupervisor{
		cfg:   app.Config{Name: "demoapp", ID: "demoapp-dev"},
		store: store,
		status: devdash.AppRecord{
			ID:      "demoapp-dev",
			Name:    "demoapp",
			Root:    "/tmp/demoapp",
			Running: true,
		},
	}

	items, err := s.listApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("listApps() returned %d items, want 1: %#v", len(items), items)
	}
	if got := items[0]["id"]; got != "demoapp-dev" {
		t.Fatalf("listApps()[0].id = %v, want demoapp-dev", got)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestLooksLikeOnlavaDashboardProcess(t *testing.T) {
	tests := []struct {
		name string
		info procInfo
		want bool
	}{
		{
			name: "onlava dev process",
			info: procInfo{pid: 100, ppid: 1, cmd: "/usr/local/bin/onlava dev"},
			want: true,
		},
		{
			name: "non orphaned onlava dev process",
			info: procInfo{pid: 100, ppid: 42, cmd: "/usr/local/bin/onlava dev"},
			want: true,
		},
		{
			name: "onlava serve is headless",
			info: procInfo{pid: 100, ppid: 42, cmd: "/usr/local/bin/onlava serve"},
			want: false,
		},
		{
			name: "onlava app binary is not dashboard",
			info: procInfo{pid: 100, ppid: 42, cmd: "/tmp/onlava-app"},
			want: false,
		},
		{
			name: "non onlava process",
			info: procInfo{pid: 100, ppid: 1, cmd: "/usr/bin/python3 -m http.server"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeOnlavaDashboardProcess(tt.info); got != tt.want {
				t.Fatalf("looksLikeOnlavaDashboardProcess(%+v) = %v, want %v", tt.info, got, tt.want)
			}
		})
	}
}
