package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

func TestScanWatchedFilesIncludesWatchedSourceFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	writeWatchFile(t, root, ".scenery.json", `{"name":"watchapp"}`)
	writeWatchFile(t, root, ".config.json", `{"name":"watchapp"}`)
	writeWatchFile(t, root, "go.mod", "module example.com/watchapp\n\ngo 1.26.3\n")
	writeWatchFile(t, root, "go.sum", "example.com/mod v1.0.0 h1:abc\n")
	writeWatchFile(t, root, ".env", "DatabaseURL=postgres://localhost/db\n")
	writeWatchFile(t, root, ".env.local", "DatabaseURL=postgres://localhost/local\n")
	writeWatchFile(t, root, "svc/api.go", "package svc\n")
	writeWatchFile(t, root, "svc/native.cpp", "int main() { return 0; }\n")
	writeWatchFile(t, root, "svc/native.h", "#pragma once\n")
	writeWatchFile(t, root, "svc/native.s", "TEXT noop(SB),$0\n")
	writeWatchFile(t, root, "README.md", "# ignored\n")
	writeWatchFile(t, root, ".git/config", "[core]\n")
	writeWatchFile(t, root, "node_modules/pkg/index.js", "console.log('ignored')\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}

	for _, want := range []string{".scenery.json", ".config.json", "go.mod", "go.sum", ".env", ".env.local", "svc/api.go", "svc/native.cpp", "svc/native.h", "svc/native.s"} {
		if _, ok := snapshot[want]; !ok {
			t.Fatalf("snapshot missing %q: %+v", want, snapshot)
		}
	}
	for _, ignored := range []string{"README.md", ".git/config", "node_modules/pkg/index.js"} {
		if _, ok := snapshot[ignored]; ok {
			t.Fatalf("snapshot unexpectedly included %q: %+v", ignored, snapshot)
		}
	}
}

func TestScanWatchedFilesIncludesEmbeddedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	writeWatchFile(t, root, ".scenery.json", `{"name":"watchapp"}`)
	writeWatchFile(t, root, "go.mod", "module example.com/watchapp\n\ngo 1.26.3\n")
	writeWatchFile(t, root, "svc/embed.go", `package svc

import _ "embed"

//go:embed data/config.json "data/with space.txt" assets/*.txt static
var embedded []byte
`)
	writeWatchFile(t, root, "svc/data/config.json", `{"ok":true}`)
	writeWatchFile(t, root, "svc/data/with space.txt", "hello\n")
	writeWatchFile(t, root, "svc/assets/a.txt", "a\n")
	writeWatchFile(t, root, "svc/assets/ignored.md", "ignored\n")
	writeWatchFile(t, root, "svc/static/index.html", "<h1>hi</h1>\n")
	writeWatchFile(t, root, "svc/static/.hidden", "hidden\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	for _, want := range []string{"svc/data/config.json", "svc/data/with space.txt", "svc/assets/a.txt", "svc/static/index.html"} {
		if _, ok := snapshot[want]; !ok {
			t.Fatalf("snapshot missing embedded file %q: %+v", want, snapshot)
		}
	}
	for _, ignored := range []string{"svc/assets/ignored.md", "svc/static/.hidden"} {
		if _, ok := snapshot[ignored]; ok {
			t.Fatalf("snapshot unexpectedly included %q: %+v", ignored, snapshot)
		}
	}
}

func TestShouldIgnoreWatchPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{path: "svc/api.go", want: false},
		{path: "svc/native.cpp", want: false},
		{path: ".env", want: false},
		{path: ".env.local", want: false},
		{path: ".config.json", want: false},
		{path: ".git/config", want: true},
		{path: "node_modules/pkg/index.js", want: true},
		{path: "scenery_internal_main/main.go", want: true},
		{path: "svc/.cache/tmp.go", want: true},
	}
	for _, tt := range tests {
		if got := shouldIgnoreWatchPath(tt.path); got != tt.want {
			t.Fatalf("shouldIgnoreWatchPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestFileChangeWatcherHandlesResolvedRootEventPaths(t *testing.T) {
	target := t.TempDir()
	root := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, root); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	writeWatchFile(t, root, "svc/api.go", "package svc\n")

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks returned error: %v", err)
	}
	fw := &fileChangeWatcher{
		events:       make(chan struct{}, 1),
		root:         root,
		resolvedRoot: resolvedRoot,
		ignore:       newWatchIgnoreMatcher(root),
	}

	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks returned error: %v", err)
	}

	fw.handleEvent(fsnotify.Event{Name: filepath.Join(resolved, "svc", "api.go"), Op: fsnotify.Write})
	select {
	case <-fw.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("expected change signal for event path under the resolved root")
	}

	fw.handleEvent(fsnotify.Event{Name: filepath.Join(resolved, ".git", "index"), Op: fsnotify.Write})
	select {
	case <-fw.Events():
		t.Fatal("expected ignored path under the resolved root to not signal")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestScanWatchedFilesToleratesUnreadableDirs(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("directory permissions are not enforced for root")
	}
	root := t.TempDir()
	writeWatchFile(t, root, "svc/api.go", "package svc\n")
	writeWatchFile(t, root, "blocked/inner.go", "package blocked\n")
	blocked := filepath.Join(root, "blocked")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatalf("chmod returned error: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	if _, ok := snapshot["svc/api.go"]; !ok {
		t.Fatalf("snapshot missing svc/api.go: %+v", snapshot)
	}
}

func TestWaitForStableChangeEventsPollsWhenEventsAreMissed(t *testing.T) {
	oldPollInterval := watchPollInterval
	oldBackupPollInterval := watchBackupPollInterval
	oldSettleDelay := watchSettleDelay
	watchPollInterval = 10 * time.Millisecond
	watchBackupPollInterval = 10 * time.Millisecond
	watchSettleDelay = 10 * time.Millisecond
	t.Cleanup(func() {
		watchPollInterval = oldPollInterval
		watchBackupPollInterval = oldBackupPollInterval
		watchSettleDelay = oldSettleDelay
	})

	root := t.TempDir()
	writeWatchFile(t, root, ".scenery.json", `{"name":"watchapp"}`)
	writeWatchFile(t, root, "go.mod", "module example.com/watchapp\n\ngo 1.26.3\n")
	writeWatchFile(t, root, "svc/api.go", "package svc\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	writeWatchFile(t, root, "svc/api.go", "package svc\n\nconst changed = true\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan struct{})
	next, err := waitForStableChangeEvents(ctx, root, snapshot, events)
	if err != nil {
		t.Fatalf("waitForStableChangeEvents returned error: %v", err)
	}
	if snapshotsEqual(snapshot, next) {
		t.Fatal("snapshot did not change")
	}
}

func TestWatchDurationFromEnv(t *testing.T) {
	t.Setenv("SCENERY_TEST_WATCH_SETTLE_DELAY_MS", "25")
	if got := watchDurationFromEnv("SCENERY_TEST_WATCH_SETTLE_DELAY_MS", time.Second); got != 25*time.Millisecond {
		t.Fatalf("watchDurationFromEnv() = %s, want 25ms", got)
	}

	t.Setenv("SCENERY_TEST_WATCH_SETTLE_DELAY_MS", "0")
	if got := watchDurationFromEnv("SCENERY_TEST_WATCH_SETTLE_DELAY_MS", time.Second); got != time.Second {
		t.Fatalf("watchDurationFromEnv(invalid) = %s, want fallback", got)
	}
}

func TestApplyWatchTimingOverridesFromEnv(t *testing.T) {
	oldPollInterval := watchPollInterval
	oldBackupPollInterval := watchBackupPollInterval
	oldSettleDelay := watchSettleDelay
	t.Cleanup(func() {
		watchPollInterval = oldPollInterval
		watchBackupPollInterval = oldBackupPollInterval
		watchSettleDelay = oldSettleDelay
	})
	watchPollInterval = time.Second
	watchBackupPollInterval = 2 * time.Second
	watchSettleDelay = 3 * time.Second
	t.Setenv("SCENERY_TEST_WATCH_POLL_MS", "11")
	t.Setenv("SCENERY_TEST_WATCH_BACKUP_POLL_MS", "12")
	t.Setenv("SCENERY_TEST_WATCH_SETTLE_DELAY_MS", "13")

	applyWatchTimingOverridesFromEnv()

	if watchPollInterval != 11*time.Millisecond {
		t.Fatalf("watchPollInterval = %s, want 11ms", watchPollInterval)
	}
	if watchBackupPollInterval != 12*time.Millisecond {
		t.Fatalf("watchBackupPollInterval = %s, want 12ms", watchBackupPollInterval)
	}
	if watchSettleDelay != 13*time.Millisecond {
		t.Fatalf("watchSettleDelay = %s, want 13ms", watchSettleDelay)
	}
}

func TestSnapshotsEqual(t *testing.T) {
	t.Parallel()

	a := fileSnapshot{
		"a.go": {size: 1},
		"b.go": {size: 2},
	}
	b := fileSnapshot{
		"b.go": {size: 2},
		"a.go": {size: 1},
	}
	c := fileSnapshot{
		"a.go": {size: 3},
		"b.go": {size: 2},
	}

	if !snapshotsEqual(a, b) {
		t.Fatal("snapshotsEqual returned false for equal snapshots")
	}
	if snapshotsEqual(a, c) {
		t.Fatal("snapshotsEqual returned true for different snapshots")
	}
}

func TestChangedPaths(t *testing.T) {
	t.Parallel()

	before := fileSnapshot{
		"svc/added.go":   {size: 1},
		"svc/deleted.go": {size: 2},
		"svc/same.go":    {size: 3},
		"svc/updated.go": {size: 4},
	}
	after := fileSnapshot{
		"svc/added.go":   {size: 9},
		"svc/new.go":     {size: 5},
		"svc/same.go":    {size: 3},
		"svc/updated.go": {size: 7},
	}

	got := changedPaths(before, after)
	want := []string{
		"svc/added.go",
		"svc/deleted.go",
		"svc/new.go",
		"svc/updated.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changedPaths mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestPrepareDevAgentSessionDefaultsToUnixBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)

	root := t.TempDir()
	t.Setenv(devElectricUpstreamEnv, "http://127.0.0.1:3001")
	client, session, backend, restore, err := prepareDevAgentSession(ctx, root, app.Config{
		Name: "demo",
		Dev: app.DevConfig{
			Services: map[string]app.DevServiceConfig{
				"electric": {Kind: "electric", Route: "electric"},
			},
		},
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"web": {Host: "web.demo.localhost", Upstream: "127.0.0.1:5173", AllowSharedUpstream: true},
			},
		},
	}, devListenRequest{}, nil)
	defer restore()
	if err != nil {
		t.Fatalf("prepareDevAgentSession: %v", err)
	}
	if client == nil || session == nil {
		t.Fatalf("agent client/session = %v/%v, want both", client, session)
	}
	if got, want := session.RouteNamespace.Workspace, ""; got != want {
		t.Fatalf("route namespace workspace = %q, want %q", got, want)
	}
	if got, want := session.RouteNamespace.BaseDomain, localagent.DefaultRouteBaseDomain; got != want {
		t.Fatalf("route namespace base domain = %q, want %q", got, want)
	}
	if got, want := session.RouteNamespace.Hosts["web"], "web.demo.localhost"; got != want {
		t.Fatalf("route namespace web host = %q, want %q", got, want)
	}
	agentPaths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := os.Getenv("SCENERY_DEV_CACHE_DIR"), filepath.Join(agentPaths.AgentDir, "dashboard"); got != want {
		t.Fatalf("SCENERY_DEV_CACHE_DIR = %q, want %q", got, want)
	}
	if got, want := os.Getenv("SCENERY_AGENT_HOME"), agentPaths.Home; got != want {
		t.Fatalf("SCENERY_AGENT_HOME = %q, want %q", got, want)
	}
	if backend.Network != "unix" {
		t.Fatalf("backend network = %q, want unix", backend.Network)
	}
	wantPrefix := filepath.Join(root, ".scenery", "sessions", session.SessionID, "run")
	if len(filepath.Join(wantPrefix, "api.sock")) <= 100 && (!strings.HasPrefix(backend.Addr, wantPrefix) || filepath.Base(backend.Addr) != "api.sock") {
		t.Fatalf("backend addr = %q, want under %q", backend.Addr, wantPrefix)
	}
	if len(filepath.Join(wantPrefix, "api.sock")) > 100 && filepath.Dir(backend.Addr) != filepath.Clean(os.TempDir()) {
		t.Fatalf("backend addr = %q, want temp fallback for long socket path", backend.Addr)
	}
	api := session.Backends[localagent.RouteAPI]
	if api.Network != "unix" || api.Addr != backend.Addr {
		t.Fatalf("session API backend = %+v, want unix %q", api, backend.Addr)
	}
	if _, ok := session.Backends[localagent.RouteDashboard]; ok {
		t.Fatalf("session dashboard backend should not be visible when the agent dashboard is active: %+v", session.Backends)
	}
	if route := session.Routes[localagent.RouteDashboard]; !strings.Contains(route, "console."+session.SessionID+"."+localagent.DefaultRouteBaseDomain) || strings.Contains(route, "/s/"+session.SessionID) {
		t.Fatalf("session dashboard route = %q", route)
	}
	if _, err := os.Stat(filepath.Join(root, ".scenery", "sessions", session.SessionID, "manifest.json")); err != nil {
		t.Fatalf("session manifest missing: %v", err)
	}
	web := session.Backends["web"]
	if web.Network != "tcp" || web.Addr != "127.0.0.1:5173" {
		t.Fatalf("session frontend backend = %+v", web)
	}
	if route := session.Routes["web"]; !strings.Contains(route, "web."+session.SessionID+".demo.localhost") {
		t.Fatalf("session frontend route = %q", route)
	}
	electric := session.Backends["electric"]
	if electric.Network != "tcp" || electric.Addr != "127.0.0.1:3001" {
		t.Fatalf("session electric backend = %+v", electric)
	}
	if route := session.Routes["electric"]; !strings.Contains(route, "electric."+session.SessionID+"."+localagent.DefaultRouteBaseDomain) {
		t.Fatalf("session electric route = %q", route)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestDevAPIUnixSocketPathFallsBackWhenStateRootIsTooLong(t *testing.T) {
	t.Parallel()

	shortRoot := filepath.Join(string(filepath.Separator), "tmp", "scenery-short", ".scenery", "sessions", "dev")
	shortPath := devAPIUnixSocketPath(shortRoot)
	if want := filepath.Join(shortRoot, "run", "api.sock"); shortPath != want {
		t.Fatalf("short socket path = %q, want %q", shortPath, want)
	}

	longRoot := filepath.Join(os.TempDir(), strings.Repeat("nested-", 20), ".scenery", "sessions", "dev")
	longPath := devAPIUnixSocketPath(longRoot)
	if strings.HasPrefix(longPath, longRoot) {
		t.Fatalf("long socket path did not fall back: %q", longPath)
	}
	if filepath.Dir(longPath) != filepath.Clean(os.TempDir()) || !strings.HasPrefix(filepath.Base(longPath), "scenery-api-") || filepath.Ext(longPath) != ".sock" {
		t.Fatalf("long socket fallback path = %q", longPath)
	}
	if len(longPath) > 100 {
		t.Fatalf("long socket fallback path length = %d, want <= 100: %q", len(longPath), longPath)
	}
}

func TestRouteNamespaceForConfigUsesWorkspaceAndConfiguredHosts(t *testing.T) {
	t.Parallel()

	namespace := routeNamespaceForConfig(app.Config{
		ID: "pulse",
		Proxy: app.ProxyConfig{
			Workspace:       "ONLV",
			RouteBaseDomain: "local.onlv.dev",
			APIHost:         "https://api.onlv.localhost:443",
			ConsoleHost:     "console.onlv.localhost",
			TemporalHost:    "temporal.onlv.localhost",
			GrafanaHost:     "grafana.onlv.localhost",
			Frontends: map[string]app.FrontendConfig{
				"Pulse": {Host: "Pulse.Onlv.Localhost/path"},
				"blog":  {Host: "blog.onlv.localhost"},
			},
		},
	})
	if got, want := namespace.Workspace, "onlv"; got != want {
		t.Fatalf("workspace = %q, want %q", got, want)
	}
	if got, want := namespace.BaseDomain, "local.onlv.dev"; got != want {
		t.Fatalf("base domain = %q, want %q", got, want)
	}
	wantHosts := map[string]string{
		localagent.RouteAPI:      "api.onlv.localhost",
		"console":                "console.onlv.localhost",
		localagent.RouteTemporal: "temporal.onlv.localhost",
		localagent.RouteGrafana:  "grafana.onlv.localhost",
		"pulse":                  "pulse.onlv.localhost",
		"blog":                   "blog.onlv.localhost",
	}
	for route, want := range wantHosts {
		if got := namespace.Hosts[route]; got != want {
			t.Fatalf("host %q = %q, want %q in %+v", route, got, want, namespace.Hosts)
		}
	}
}

func TestRouteNamespaceForConfigFallbacks(t *testing.T) {
	t.Parallel()

	byExplicitHost := routeNamespaceForConfig(app.Config{
		ID: "pulse",
		Proxy: app.ProxyConfig{
			APIHost: "api.custom.localhost",
		},
	})
	if byExplicitHost.Workspace != "" {
		t.Fatalf("explicit-host workspace = %q, want empty", byExplicitHost.Workspace)
	}
	if got, want := byExplicitHost.BaseDomain, localagent.DefaultRouteBaseDomain; got != want {
		t.Fatalf("explicit-host base domain = %q, want %q", got, want)
	}

	byAppID := routeNamespaceForConfig(app.Config{ID: "Pulse App"})
	if got, want := byAppID.Workspace, "pulse-app"; got != want {
		t.Fatalf("app-id workspace = %q, want %q", got, want)
	}
	if got, want := byAppID.BaseDomain, localagent.DefaultRouteBaseDomain; got != want {
		t.Fatalf("app-id base domain = %q, want %q", got, want)
	}
	if byAppID.Hosts != nil {
		t.Fatalf("app-id hosts = %+v, want nil", byAppID.Hosts)
	}
}

func TestPrepareDevAgentSessionConfiguredRouteBaseDomainPublishesPortlessEdgeRoutes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServerWithPathSetup(t, ctx, func(paths localagent.Paths) {
		if err := localagent.WriteEdgeState(paths.EdgeStatePath, localagent.EdgeState{
			Kind:         localagent.EdgeKindCaddy,
			Status:       localagent.EdgeStatusRunning,
			PID:          os.Getpid(),
			PublicAddr:   "127.0.0.1:443",
			PublicScheme: "https",
			UpstreamAddr: "127.0.0.1:9440",
		}); err != nil {
			t.Fatal(err)
		}
	})
	restoreHooks := withConfiguredEdgeTestHooks(t,
		func(_ context.Context, _ *localagent.Client, domain string) (edgeStatusResult, error) {
			if domain != "onlv.dev" {
				t.Fatalf("edge readiness domain = %q, want onlv.dev", domain)
			}
			return healthyEdgeStatus(), nil
		},
		func(_ context.Context, route string) error {
			if strings.Contains(route, ":9440") {
				t.Fatalf("edge probe route used internal router port: %s", route)
			}
			if !strings.HasPrefix(route, "https://console.") || !strings.HasSuffix(route, ".onlv.dev/") {
				t.Fatalf("edge probe route = %q, want portless console onlv.dev URL", route)
			}
			return nil
		},
	)
	defer restoreHooks()

	_, session, _, restore, err := prepareDevAgentSession(ctx, t.TempDir(), app.Config{
		Name:  "demo",
		Proxy: app.ProxyConfig{RouteBaseDomain: "onlv.dev"},
	}, devListenRequest{}, nil)
	defer restore()
	if err != nil {
		t.Fatalf("prepareDevAgentSession: %v", err)
	}
	for route, raw := range session.Routes {
		if strings.Contains(raw, ":9440") {
			t.Fatalf("route %s = %q, should not expose internal router port", route, raw)
		}
	}
	if got := session.Routes[localagent.RouteDashboard]; !strings.HasPrefix(got, "https://console."+session.SessionID+".onlv.dev/") {
		t.Fatalf("dashboard route = %q, want portless onlv.dev", got)
	}
	if got := session.Routes[localagent.RouteAPI]; !strings.HasPrefix(got, "https://api."+session.SessionID+".onlv.dev/") {
		t.Fatalf("api route = %q, want portless onlv.dev", got)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestPrepareDevAgentSessionConfiguredRouteBaseDomainFailsLoudWhenEdgeStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)
	restoreHooks := withConfiguredEdgeTestHooks(t,
		func(_ context.Context, _ *localagent.Client, domain string) (edgeStatusResult, error) {
			status := healthyEdgeStatus()
			status.Ready = false
			status.Edge.State = localagent.EdgeStatusStopped
			return status, configuredEdgeNotReadyError(domain, status)
		},
		func(_ context.Context, route string) error {
			t.Fatalf("edge probe should not run after failed readiness check for %s", route)
			return nil
		},
	)
	defer restoreHooks()

	root := t.TempDir()
	_, session, _, restore, err := prepareDevAgentSession(ctx, root, app.Config{
		Name:  "demo",
		Proxy: app.ProxyConfig{RouteBaseDomain: "onlv.dev"},
	}, devListenRequest{}, nil)
	defer restore()
	if err == nil {
		t.Fatal("prepareDevAgentSession succeeded, want edge readiness failure")
	}
	if session != nil {
		t.Fatalf("session = %+v, want nil", session)
	}
	message := err.Error()
	for _, want := range []string{
		"Edge is not ready; refusing to publish portless onlv.dev URLs.",
		"DNS: ready",
		"Privileged listener: ready",
		"Caddy: stopped",
		"Router: ready at 127.0.0.1:9440 (internal/diagnostic)",
		"scenery system edge restart",
		"scenery system edge status",
		"scenery system edge install",
		"scenery system edge trust",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("edge error missing %q:\n%s", want, message)
		}
	}
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := client.List(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions after failed edge preflight = %+v, want none", sessions)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestPrepareDevAgentSessionWithoutConfiguredRouteBaseDomainAllowsDirectRouterRoutes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)
	restoreHooks := withConfiguredEdgeTestHooks(t,
		func(_ context.Context, _ *localagent.Client, domain string) (edgeStatusResult, error) {
			t.Fatalf("edge readiness should not run for unconfigured route_base_domain %q", domain)
			return edgeStatusResult{}, nil
		},
		func(_ context.Context, route string) error {
			t.Fatalf("edge probe should not run for unconfigured route_base_domain %s", route)
			return nil
		},
	)
	defer restoreHooks()

	_, session, _, restore, err := prepareDevAgentSession(ctx, t.TempDir(), app.Config{Name: "demo"}, devListenRequest{}, nil)
	defer restore()
	if err != nil {
		t.Fatalf("prepareDevAgentSession: %v", err)
	}
	route := session.Routes[localagent.RouteDashboard]
	if !strings.Contains(route, "."+localagent.DefaultRouteBaseDomain+":") {
		t.Fatalf("dashboard route = %q, want direct router URL with explicit port", route)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestPrepareDevAgentSessionPrefersTCPWhenRequested(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)

	root := t.TempDir()
	_, session, backend, restore, err := prepareDevAgentSession(ctx, root, app.Config{Name: "demo"}, devListenRequest{PreferTCP: true}, nil)
	defer restore()
	if err != nil {
		t.Fatalf("prepareDevAgentSession: %v", err)
	}
	if backend.Network != "tcp" || !strings.HasPrefix(backend.Addr, "127.0.0.1:") {
		t.Fatalf("backend = %+v, want hidden loopback TCP", backend)
	}
	api := session.Backends[localagent.RouteAPI]
	if api.Network != "tcp" || api.Addr != backend.Addr {
		t.Fatalf("session API backend = %+v, want tcp %q", api, backend.Addr)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestPrepareDevAgentSessionUsesStableAppRootSessionID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)

	root := t.TempDir()
	_, session, _, restore, err := prepareDevAgentSession(ctx, root, app.Config{Name: "demo"}, devListenRequest{}, nil)
	defer restore()
	if err != nil {
		t.Fatalf("prepareDevAgentSession: %v", err)
	}
	if want := localagent.SessionID(root, ""); session.SessionID != want {
		t.Fatalf("session id = %q, want %q", session.SessionID, want)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestPrepareDevAgentSessionRejectsLiveDuplicateOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)

	root := t.TempDir()
	owner := exec.Command("sleep", "30")
	if err := owner.Start(); err != nil {
		t.Fatalf("start owner fixture: %v", err)
	}
	defer func() {
		_ = owner.Process.Kill()
		_ = owner.Wait()
	}()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	sessionID := localagent.SessionID(root, "")
	if _, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   root,
		SessionID: sessionID,
		Status:    "running",
		OwnerPID:  owner.Process.Pid,
		Owner:     localagent.CaptureOwner(owner.Process.Pid, "test"),
	}); err != nil {
		t.Fatalf("register live owner session: %v", err)
	}

	_, _, _, restore, err := prepareDevAgentSession(ctx, root, app.Config{Name: "demo"}, devListenRequest{}, nil)
	defer restore()
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("prepareDevAgentSession duplicate error = %v, want already running", err)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestRejectLiveDuplicateDevSessionUsesEffectiveOwnerPID(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	owner := exec.Command("sleep", "30")
	if err := owner.Start(); err != nil {
		t.Fatalf("start owner fixture: %v", err)
	}
	defer func() {
		_ = owner.Process.Kill()
		_ = owner.Wait()
	}()
	sessionID := "review-a"
	err := rejectLiveDuplicateDevSession(root, []localagent.Session{
		{
			SessionID: sessionID,
			AppRoot:   root,
			Status:    "running",
			OwnerPID:  owner.Process.Pid,
			Owner: localagent.Owner{
				PID:         99999994,
				StartedAt:   "stale-owner-field",
				CmdlineHash: "sha256:stale-owner-field",
				Exe:         "/stale/owner",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("duplicate error = %v, want already running", err)
	}
}

func TestRejectLiveDuplicateDevSessionHandlesSpaceyAppRoots(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "app root with spaces")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := exec.Command("sleep", "30")
	if err := owner.Start(); err != nil {
		t.Fatalf("start owner fixture: %v", err)
	}
	defer func() {
		_ = owner.Process.Kill()
		_ = owner.Wait()
	}()
	sessionID := "review-a"
	err := rejectLiveDuplicateDevSession(root, []localagent.Session{
		{
			SessionID: sessionID,
			AppRoot:   root,
			Status:    "running",
			OwnerPID:  owner.Process.Pid,
			Owner:     localagent.CaptureOwner(owner.Process.Pid, "test"),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("duplicate error = %v, want already running", err)
	}
}

func TestRejectLiveDuplicateDevSessionIgnoresWrapperCommandText(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	binDir := t.TempDir()
	fakeScenery := filepath.Join(binDir, "scenery")
	if err := os.WriteFile(fakeScenery, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wrapperStyle := exec.Command("sh", fakeScenery, "up", "--app-root", root)
	if err := wrapperStyle.Start(); err != nil {
		t.Fatalf("start wrapper-style owner fixture: %v", err)
	}
	defer func() {
		_ = wrapperStyle.Process.Kill()
		_ = wrapperStyle.Wait()
	}()
	reorderedStyle := exec.Command("sh", fakeScenery, "--app-root", root, "up")
	if err := reorderedStyle.Start(); err != nil {
		t.Fatalf("start reordered-style owner fixture: %v", err)
	}
	defer func() {
		_ = reorderedStyle.Process.Kill()
		_ = reorderedStyle.Wait()
	}()
	if err := rejectLiveDuplicateDevSession(root, nil); err != nil {
		t.Fatalf("duplicate error = %v, want nil for unregistered command text", err)
	}
}

func TestRejectLiveDuplicateDevSessionBlocksCurrentOwner(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sessionID := "review-a"
	err := rejectLiveDuplicateDevSession(root, []localagent.Session{
		{
			SessionID: sessionID,
			AppRoot:   root,
			Status:    "running",
			OwnerPID:  os.Getpid(),
			Owner:     localagent.CaptureOwner(os.Getpid(), "test"),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("duplicate error = %v, want already running for current owner", err)
	}
}

func TestRejectLiveDuplicateDevSessionBlocksVerifiedAncestorOwner(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sessionID := "review-a"
	ancestorPID := os.Getppid()
	if ancestorPID <= 1 {
		t.Skip("no inspectable parent process")
	}
	ancestorOwner := localagent.CaptureOwner(ancestorPID, "test")
	if err := localagent.VerifyOwner(ancestorOwner); err != nil {
		t.Skipf("parent process is not inspectable: %v", err)
	}
	err := rejectLiveDuplicateDevSession(root, []localagent.Session{
		{
			SessionID: sessionID,
			AppRoot:   root,
			Status:    "running",
			OwnerPID:  ancestorPID,
			Owner:     ancestorOwner,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("duplicate error = %v, want already running for verified ancestor owner", err)
	}
}

func TestPrepareDevAgentSessionFallsBackWhenAgentDisabled(t *testing.T) {
	t.Setenv("SCENERY_AGENT_DISABLE", "1")
	_, session, backend, restore, err := prepareDevAgentSession(context.Background(), t.TempDir(), app.Config{Name: "demo"}, devListenRequest{}, nil)
	defer restore()
	if err != nil {
		t.Fatalf("prepareDevAgentSession: %v", err)
	}
	if session != nil {
		t.Fatalf("session = %+v, want nil", session)
	}
	if backend.Network != "tcp" || backend.Addr != "127.0.0.1:4000" {
		t.Fatalf("backend = %+v, want default TCP fallback", backend)
	}
}

func startTestAgentServer(t *testing.T, ctx context.Context) <-chan error {
	return startTestAgentServerWithPathSetup(t, ctx, nil)
}

func startTestAgentServerWithPathSetup(t *testing.T, ctx context.Context, setup func(localagent.Paths)) <-chan error {
	t.Helper()
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	paths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(paths)
	}
	server, err := localagent.NewServer(localagent.RunOptions{
		RouterAddr: "127.0.0.1:0",
		DashboardBackend: localagent.Backend{
			Network: "tcp",
			Addr:    "127.0.0.1:9",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	return done
}

func withConfiguredEdgeTestHooks(t *testing.T, readiness configuredEdgeReadinessChecker, probe configuredEdgeRouteProber) func() {
	t.Helper()
	oldReadiness := checkConfiguredEdgeReadiness
	oldProbe := probeConfiguredEdgeRoute
	checkConfiguredEdgeReadiness = readiness
	probeConfiguredEdgeRoute = probe
	return func() {
		checkConfiguredEdgeReadiness = oldReadiness
		probeConfiguredEdgeRoute = oldProbe
	}
}

func healthyEdgeStatus() edgeStatusResult {
	return edgeStatusResult{
		Ready: true,
		Edge: edgeStatusCaddy{
			State:       localagent.EdgeStatusRunning,
			Upstream:    "127.0.0.1:9440",
			AgentRouter: "127.0.0.1:9440",
		},
		DNS: edgeDNSStatusResult{
			Ready: true,
			DNSMasq: edgeDNSMasqStatus{
				State: "running",
			},
		},
		PrivilegedListener: edgeStatusPrivilegedListener{
			State:  "running",
			Target: defaultEdgeTargetAddr,
		},
	}
}

func waitForTestAgentServer(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for agent shutdown")
	}
}

func writeWatchFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
