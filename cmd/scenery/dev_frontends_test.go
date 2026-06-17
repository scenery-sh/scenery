package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

func TestManagedFrontendCommandUsesViteLocalBin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontendPackage(t, root, `{"scripts":{"dev":"vite"}}`)
	bin := writeFrontendBin(t, root, "vite")
	cmd, args, err := managedFrontendCommand(root, "49231", "")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != bin {
		t.Fatalf("command = %q, want %q", cmd, bin)
	}
	wantArgs := []string{"--host", "127.0.0.1", "--port", "49231"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestManagedFrontendCommandUsesAstroLocalBin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontendPackage(t, root, `{"scripts":{"dev":"astro dev"}}`)
	bin := writeFrontendBin(t, root, "astro")
	cmd, args, err := managedFrontendCommand(root, "49232", "blog.main-test.local.dev")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != bin {
		t.Fatalf("command = %q, want %q", cmd, bin)
	}
	wantArgs := []string{"dev", "--host", "127.0.0.1", "--port", "49232", "--allowed-hosts", "blog.main-test.local.dev"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestManagedFrontendPackageManagerUsesWorkspaceParent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"packageManager":"bun@1.3.11"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	appRoot := filepath.Join(root, "apps", "web")
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFrontendPackage(t, appRoot, `{"scripts":{"dev":"custom-dev"}}`)
	if got := managedFrontendPackageManager(appRoot); got != "bun" {
		t.Fatalf("package manager = %q, want bun", got)
	}
}

func TestFrontendDevEnvIncludesSessionRoutes(t *testing.T) {
	t.Parallel()

	env := frontendDevEnv([]string{"EXISTING=1"}, "/repo/app", "127.0.0.1:49231", localagent.Session{
		SessionID: "main-abc123",
		Routes: map[string]string{
			localagent.RouteAPI: "http://api.main-abc123.local.dev:9440/",
			"electric":          "http://electric.main-abc123.local.dev:9440/",
			"web":               "http://web.main-abc123.local.dev:9440/",
		},
	}, "web")
	for _, want := range []string{
		"EXISTING=1",
		"HOST=127.0.0.1",
		"PORT=49231",
		"SCENERY_APP_ROOT=/repo/app",
		"SCENERY_SESSION_ID=main-abc123",
		"SCENERY_API_BASE_URL=http://api.main-abc123.local.dev:9440/",
		"VITE_API_BASE_URL=http://api.main-abc123.local.dev:9440/",
		"SCENERY_ELECTRIC_URL=http://electric.main-abc123.local.dev:9440/",
		"VITE_ELECTRIC_URL=http://electric.main-abc123.local.dev:9440/",
		"__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS=web.main-abc123.local.dev",
	} {
		if !containsString(env, want) {
			t.Fatalf("frontendDevEnv() missing %q in %s", want, strings.Join(env, "\n"))
		}
	}
}

func TestManagedFrontendAllowedHostFromRouteNamespace(t *testing.T) {
	t.Parallel()

	session := localagent.Session{
		SessionID: "main-abc123",
		RouteNamespace: localagent.RouteNamespace{
			BaseDomain: "onlv.dev",
			Hosts: map[string]string{
				"blog": "blog.onlv.dev",
			},
		},
	}
	if got, want := managedFrontendAllowedHost(session, "blog"), "blog.main-abc123.onlv.dev"; got != want {
		t.Fatalf("allowed host = %q, want %q", got, want)
	}
	if got, want := managedFrontendAllowedHost(session, "pulse"), "pulse.main-abc123.onlv.dev"; got != want {
		t.Fatalf("fallback allowed host = %q, want %q", got, want)
	}
}

func TestManagedFrontendBackendsRequiresExplicitSharedUpstream(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"web": {
					Root:     "apps/web",
					Upstream: "127.0.0.1:5173",
				},
			},
		},
	}
	_, _, err := managedFrontendBackendsForSession(context.Background(), root, cfg, nil, localagent.Session{
		SessionID: "main-test",
		StateRoot: filepath.Join(root, ".scenery", "sessions", "main-test"),
	})
	if err == nil {
		t.Fatal("expected managed frontend fallback error")
	}
	if !strings.Contains(err.Error(), "allow_shared_upstream") {
		t.Fatalf("error = %q, want allow_shared_upstream guidance", err)
	}
}

func TestManagedFrontendBackendsAllowsExplicitSharedUpstream(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"web": {
					Root:                "apps/web",
					Upstream:            "127.0.0.1:5173",
					AllowSharedUpstream: true,
				},
			},
		},
	}
	backends, processes, err := managedFrontendBackendsForSession(context.Background(), root, cfg, nil, localagent.Session{
		SessionID: "main-test",
		StateRoot: filepath.Join(root, ".scenery", "sessions", "main-test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 0 {
		t.Fatalf("processes = %d, want 0", len(processes))
	}
	if got := backends["web"]; got.Network != "tcp" || got.Addr != "127.0.0.1:5173" {
		t.Fatalf("web backend = %+v", got)
	}
}

func TestManagedFrontendFailsFastWhenChildExitsBeforeReady(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	frontendRoot := filepath.Join(appRoot, "apps", "web")
	writeFrontendPackage(t, frontendRoot, `{"scripts":{"dev":"vite"}}`)
	writeFrontendBinWithScript(t, frontendRoot, "vite", "echo frontend-boom\nexit 7\n")

	start := time.Now()
	_, _, err := managedFrontendBackendsForSession(context.Background(), appRoot, app.Config{
		Name: "demo",
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"web": {Root: "apps/web"},
			},
		},
	}, nil, localagent.Session{
		SessionID: "main-test",
		StateRoot: filepath.Join(root, "state"),
	})
	if err == nil {
		t.Fatal("managedFrontendBackendsForSession returned nil error, want early exit")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("managed frontend failure took %s, want early exit before timeout", elapsed)
	}
	got := err.Error()
	if !strings.Contains(got, "frontend web exited before becoming ready") || !strings.Contains(got, "frontend-boom") {
		t.Fatalf("error = %q, want early exit with output tail", got)
	}
}

func TestManagedFrontendBackendsStartsFrontendsConcurrently(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	markerDir := filepath.Join(root, "markers")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	serverPath := frontendTestServerBinary(t)
	for _, name := range []string{"alpha", "beta"} {
		frontendRoot := filepath.Join(appRoot, "apps", name)
		writeFrontendPackage(t, frontendRoot, `{"scripts":{"dev":"vite"}}`)
		writeFrontendBinWithScript(t, frontendRoot, "vite", `
set -eu
port=""
prev=""
for arg in "$@"; do
	if [ "$prev" = "--port" ]; then
		port="$arg"
		break
	fi
	prev="$arg"
done
name="${PWD##*/}"
touch "$SCENERY_FRONTEND_TEST_MARKER_DIR/$name.started"
while [ ! -f "$SCENERY_FRONTEND_TEST_MARKER_DIR/go" ]; do
	sleep 0.05
done
SCENERY_FRONTEND_TEST_SERVER_HELPER=1 exec "$SCENERY_FRONTEND_TEST_SERVER" -test.run '^TestManagedFrontendTestServerHelper$' -- "$port"
`)
	}
	cfg := app.Config{
		Name: "demo",
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"alpha": {Root: "apps/alpha"},
				"beta":  {Root: "apps/beta"},
			},
		},
	}
	type frontendResult struct {
		backends  map[string]localagent.Backend
		processes []*managedFrontendProcess
		err       error
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan frontendResult, 1)
	baseEnv := []string{
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
		"SCENERY_FRONTEND_TEST_MARKER_DIR=" + markerDir,
		"SCENERY_FRONTEND_TEST_SERVER=" + serverPath,
	}
	go func() {
		backends, processes, err := managedFrontendBackendsForSession(ctx, appRoot, cfg, baseEnv, localagent.Session{
			SessionID: "main-test",
			StateRoot: filepath.Join(root, "state"),
		})
		done <- frontendResult{backends: backends, processes: processes, err: err}
	}()

	waitForFile(t, filepath.Join(markerDir, "alpha.started"), 2*time.Second)
	waitForFile(t, filepath.Join(markerDir, "beta.started"), 2*time.Second)
	if err := os.WriteFile(filepath.Join(markerDir, "go"), []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		t.Cleanup(func() { stopManagedFrontendProcesses(result.processes) })
		if len(result.processes) != 2 {
			t.Fatalf("processes = %d, want 2", len(result.processes))
		}
		time.Sleep(300 * time.Millisecond)
		for _, name := range []string{"alpha", "beta"} {
			backend := result.backends[name]
			if backend.Network != "tcp" || backend.Addr == "" {
				t.Fatalf("%s backend = %+v, want tcp addr", name, backend)
			}
			if !tcpAddrAcceptsConnections(backend.Addr) {
				t.Fatalf("%s backend %s stopped accepting connections after startup returned", name, backend.Addr)
			}
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestManagedFrontendExitRestartsAndUpdatesAgentSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)
	defer func() {
		cancel()
		<-agentDone
	}()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	frontendRoot := filepath.Join(appRoot, "apps", "web")
	writeFrontendPackage(t, frontendRoot, `{"scripts":{"dev":"vite"}}`)
	serverPath := frontendTestServerBinary(t)
	markerPath := filepath.Join(root, "frontend-starts.log")
	t.Setenv("SCENERY_FRONTEND_TEST_SERVER", serverPath)
	t.Setenv("SCENERY_FRONTEND_RESTART_MARKER", markerPath)
	writeFrontendBinWithScript(t, frontendRoot, "vite", `
set -eu
port=""
prev=""
for arg in "$@"; do
	if [ "$prev" = "--port" ]; then
		port="$arg"
		break
	fi
	prev="$arg"
done
echo "$$ $port" >> "$SCENERY_FRONTEND_RESTART_MARKER"
SCENERY_FRONTEND_TEST_SERVER_HELPER=1 exec "$SCENERY_FRONTEND_TEST_SERVER" -test.run '^TestManagedFrontendTestServerHelper$' -- "$port"
`)
	cfg := app.Config{
		Name: "demo",
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"web": {Root: "apps/web"},
			},
		},
	}
	prepared, err := prepareDevAgentSessionDetailed(ctx, appRoot, cfg, devListenRequest{}, nil)
	if err != nil {
		t.Fatalf("prepareDevAgentSessionDetailed: %v", err)
	}
	defer prepared.Cleanup()
	if len(prepared.FrontendProcesses) != 1 {
		t.Fatalf("frontend processes = %d, want 1", len(prepared.FrontendProcesses))
	}
	supervisor, err := newDevSupervisor(ctx, appRoot, cfg, prepared.Backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer supervisor.Close()
	supervisor.agent = prepared.Client
	supervisor.agentSession = prepared.Session
	supervisor.adoptManagedFrontends(prepared.FrontendProcesses)

	first := prepared.FrontendProcesses[0]
	oldAddr := first.Addr
	oldPID := first.Process.PID
	if err := killProcessTree(first.Process.Cmd); err != nil {
		t.Fatal(err)
	}

	var latest localagent.Session
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		sessions, err := prepared.Client.List(ctx, appRoot)
		if err != nil {
			t.Fatal(err)
		}
		for _, session := range sessions {
			if session.SessionID == prepared.Session.SessionID {
				latest = session
				break
			}
		}
		backend := latest.Backends["web"]
		process := latest.Processes["frontend-web"]
		if backend.Addr != "" && backend.Addr != oldAddr && process.PID > 0 && process.PID != oldPID && tcpAddrAcceptsConnections(backend.Addr) {
			time.Sleep(300 * time.Millisecond)
			if !tcpAddrAcceptsConnections(backend.Addr) {
				t.Fatalf("restarted frontend backend %s stopped accepting connections shortly after session update", backend.Addr)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("frontend did not restart with updated session backend; old addr=%s pid=%d latest=%+v", oldAddr, oldPID, latest)
}

func writeFrontendPackage(t *testing.T, root, data string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFrontendBin(t *testing.T, root, name string) string {
	t.Helper()
	return writeFrontendBinWithScript(t, root, name, "exit 0\n")
}

func writeFrontendBinWithScript(t *testing.T, root, name, script string) string {
	t.Helper()
	dir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestManagedFrontendTestServerHelper(t *testing.T) {
	if os.Getenv("SCENERY_FRONTEND_TEST_SERVER_HELPER") != "1" {
		return
	}
	port := ""
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			port = os.Args[i+1]
			break
		}
	}
	if port == "" {
		t.Fatal("missing port")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}
}

func frontendTestServerBinary(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
