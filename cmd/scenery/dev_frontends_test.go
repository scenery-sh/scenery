package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/localproxy"
)

func TestManagedFrontendCommandUsesViteLocalBin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontendPackage(t, root, `{"scripts":{"dev":"vite"}}`)
	bin := writeFrontendBin(t, root, "vite")
	cmd, args, err := managedFrontendCommand(root, "49231", "", "/web")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != bin {
		t.Fatalf("command = %q, want %q", cmd, bin)
	}
	wantArgs := []string{"--host", "127.0.0.1", "--port", "49231", "--base", "/web/"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestManagedFrontendCommandUsesHoistedViteLocalBin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "apps", "web")
	writeFrontendPackage(t, appRoot, `{"scripts":{"dev":"vite"}}`)
	bin := writeFrontendBin(t, root, "vite")
	cmd, args, err := managedFrontendCommand(appRoot, "49231", "", "/web")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != bin {
		t.Fatalf("command = %q, want %q", cmd, bin)
	}
	wantArgs := []string{"--host", "127.0.0.1", "--port", "49231", "--base", "/web/"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestManagedFrontendCommandUsesAstroLocalBin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFrontendPackage(t, root, `{"scripts":{"dev":"astro dev"}}`)
	bin := writeFrontendBin(t, root, "astro")
	cmd, args, err := managedFrontendCommand(root, "49232", "blog.main-test.local.dev", "/blog")
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

	start := time.Now()
	_, _, err := managedFrontendBackendsForSessionWithStarter(context.Background(), appRoot, app.Config{
		Name: "demo",
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"web": {Root: "apps/web"},
			},
		},
	}, nil, localagent.Session{
		SessionID: "main-test",
		StateRoot: filepath.Join(root, "state"),
	}, func(_ context.Context, _ string, _ string, index int, frontend localproxy.FrontendConfig, _ []string, _ localagent.Session) managedFrontendStartResult {
		return managedFrontendStartResult{
			index: index,
			name:  frontend.Name,
			err:   errors.New("frontend web exited before becoming ready: exit status 7\nfrontend-boom"),
		}
	})
	if err == nil {
		t.Fatal("managedFrontendBackendsForSession returned nil error, want early exit")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
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
	cfg := app.Config{
		Name: "demo",
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"alpha": {Root: "apps/alpha"},
				"beta":  {Root: "apps/beta"},
			},
		},
	}
	addrs := map[string]string{
		"alpha": "127.0.0.1:4101",
		"beta":  "127.0.0.1:4102",
	}
	pids := map[string]int{
		"alpha": 4101,
		"beta":  4102,
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	starter := func(ctx context.Context, _ string, _ string, index int, frontend localproxy.FrontendConfig, _ []string, _ localagent.Session) managedFrontendStartResult {
		started <- frontend.Name
		select {
		case <-ctx.Done():
			return managedFrontendStartResult{index: index, name: frontend.Name, err: ctx.Err()}
		case <-release:
		}
		addr := addrs[frontend.Name]
		return managedFrontendStartResult{
			index:   index,
			name:    frontend.Name,
			backend: localagent.Backend{Network: "tcp", Addr: addr},
			process: fakeManagedFrontendProcess(frontend.Name, addr, pids[frontend.Name]),
		}
	}
	type frontendResult struct {
		backends  map[string]localagent.Backend
		processes []*managedFrontendProcess
		err       error
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan frontendResult, 1)
	go func() {
		backends, processes, err := managedFrontendBackendsForSessionWithStarter(ctx, appRoot, cfg, nil, localagent.Session{
			SessionID: "main-test",
			StateRoot: filepath.Join(root, "state"),
		}, starter)
		done <- frontendResult{backends: backends, processes: processes, err: err}
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case name := <-started:
			seen[name] = true
		case result := <-done:
			t.Fatalf("managedFrontendBackendsForSession returned before all starters ran: %+v", result)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(release)

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if len(result.processes) != 2 {
			t.Fatalf("processes = %d, want 2", len(result.processes))
		}
		for _, name := range []string{"alpha", "beta"} {
			backend := result.backends[name]
			if backend.Network != "tcp" || backend.Addr != addrs[name] {
				t.Fatalf("%s backend = %+v, want tcp %s", name, backend, addrs[name])
			}
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestBeginManagedFrontendBackendsReturnsBeforeReady(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	frontendRoot := filepath.Join(appRoot, "apps", "web")
	readyFile := filepath.Join(root, "frontend.ready")
	writeFrontendPackage(t, frontendRoot, `{"scripts":{"dev":"vite"}}`)
	serverPath := frontendTestServerBinary(t)
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
SCENERY_FRONTEND_TEST_SERVER_HELPER=1 SCENERY_FRONTEND_TEST_READY_FILE="$SCENERY_FRONTEND_TEST_READY_FILE" exec "$SCENERY_FRONTEND_TEST_SERVER" -test.run '^TestManagedFrontendTestServerHelper$' -- "$port"
`)
	cfg := app.Config{
		Name: "demo",
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"web": {Root: "apps/web"},
			},
		},
	}
	baseEnv := []string{
		"SCENERY_FRONTEND_TEST_SERVER=" + serverPath,
		"SCENERY_FRONTEND_TEST_READY_FILE=" + readyFile,
	}
	backends, processes, wait, err := beginManagedFrontendBackendsForSession(ctx, appRoot, cfg, baseEnv, localagent.Session{
		SessionID: "main-test",
		StateRoot: filepath.Join(root, "state"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stopManagedFrontendProcesses(processes)
	if wait == nil {
		t.Fatal("wait = nil, want readiness join")
	}
	if got := backends["web"]; got.Network != "tcp" || got.Addr == "" {
		t.Fatalf("web backend = %+v", got)
	}
	if len(processes) != 1 || processes[0].Process == nil || processes[0].Process.PID <= 0 {
		t.Fatalf("processes = %+v", processes)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- wait(ctx) }()
	select {
	case err := <-waitDone:
		t.Fatalf("frontend readiness finished before release: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := os.WriteFile(readyFile, []byte("ready"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestManagedFrontendExitRestartsAndUpdatesAgentSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	frontendRoot := filepath.Join(appRoot, "apps", "web")
	agentClient, agentDone := startManagedFrontendTestAgentServer(t, ctx, filepath.Join(root, "agent-home"))
	defer func() {
		cancel()
		<-agentDone
	}()

	writeFrontendPackage(t, frontendRoot, `{"scripts":{"dev":"vite"}}`)
	serverPath := frontendTestServerBinary(t)
	markerPath := filepath.Join(root, "frontend-starts.log")
	if err := os.WriteFile(filepath.Join(appRoot, ".env"), []byte(
		"SCENERY_FRONTEND_TEST_SERVER="+serverPath+"\n"+
			"SCENERY_FRONTEND_RESTART_MARKER="+markerPath+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
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
	session, err := agentClient.Register(ctx, localagent.RegisterRequest{
		BaseAppID:  cfg.AppID(),
		AppRoot:    appRoot,
		Status:     "starting",
		OwnerPID:   os.Getpid(),
		ClaimOwner: true,
	})
	if err != nil {
		t.Fatalf("register agent session: %v", err)
	}
	baseEnv, err := appEnvWithDotEnv(os.Environ(), appRoot, ".env", ".env.local")
	if err != nil {
		t.Fatalf("load frontend test env: %v", err)
	}
	frontendBackends, frontendProcesses, err := managedFrontendBackendsForSession(ctx, appRoot, cfg, baseEnv, session)
	if err != nil {
		t.Fatalf("start managed frontend: %v", err)
	}
	if len(frontendBackends) > 0 {
		session, err = agentClient.Register(ctx, localagent.RegisterRequest{
			BaseAppID:  cfg.AppID(),
			AppRoot:    appRoot,
			SessionID:  session.SessionID,
			Branch:     session.Branch,
			Status:     "starting",
			OwnerPID:   os.Getpid(),
			Backends:   frontendBackends,
			Processes:  frontendSessionProcesses(frontendProcesses),
			ClaimOwner: true,
		})
		if err != nil {
			stopManagedFrontendProcesses(frontendProcesses)
			t.Fatalf("register frontend backend: %v", err)
		}
	}
	prepared := &PreparedDevSession{
		Client:            agentClient,
		Session:           &session,
		FrontendProcesses: frontendProcesses,
		Cleanup: func() {
			stopManagedFrontendProcesses(frontendProcesses)
		},
	}
	defer prepared.Cleanup()
	if len(prepared.FrontendProcesses) != 1 {
		t.Fatalf("frontend processes = %d, want 1", len(prepared.FrontendProcesses))
	}
	supervisor, err := newDevSupervisor(ctx, appRoot, cfg, prepared.Backend, nil, prepared.Client, prepared.Session)
	if err != nil {
		t.Fatal(err)
	}
	defer supervisor.Close()
	oldDelay := managedFrontendRestartDelay
	managedFrontendRestartDelay = time.Millisecond
	defer func() { managedFrontendRestartDelay = oldDelay }()
	type frontendUpdate struct {
		backend localagent.Backend
		pid     int
	}
	updates := make(chan frontendUpdate, 2)
	managedFrontendTestHooks.Lock()
	managedFrontendTestHooks.sessionUpdated = func(name string, backend localagent.Backend, process *managedFrontendProcess) {
		if name != "web" || process == nil || process.Process == nil {
			return
		}
		select {
		case updates <- frontendUpdate{backend: backend, pid: process.Process.PID}:
		default:
		}
	}
	managedFrontendTestHooks.Unlock()
	defer func() {
		managedFrontendTestHooks.Lock()
		managedFrontendTestHooks.sessionUpdated = nil
		managedFrontendTestHooks.Unlock()
	}()
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
	deadline := time.After(8 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.backend.Addr == "" || update.backend.Addr == oldAddr || update.pid <= 0 || update.pid == oldPID {
				continue
			}
			if !tcpAddrAcceptsConnections(update.backend.Addr) {
				t.Fatalf("restarted frontend backend %s is not accepting connections", update.backend.Addr)
			}
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
			if backend.Addr == update.backend.Addr && process.PID == update.pid {
				return
			}
			t.Fatalf("session update = backend=%+v pid=%d, latest=%+v", update.backend, update.pid, latest)
		case <-deadline:
			t.Fatalf("frontend did not restart with updated session backend; old addr=%s pid=%d latest=%+v", oldAddr, oldPID, latest)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func startManagedFrontendTestAgentServer(t *testing.T, ctx context.Context, home string) (*localagent.Client, <-chan error) {
	t.Helper()
	paths := localagent.PathsForHome(home)
	if err := localagent.EnsureDirs(paths); err != nil {
		t.Fatal(err)
	}
	server, err := localagent.NewServer(localagent.RunOptions{
		Home:       home,
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
	client := localagent.NewClient(paths.SocketPath)
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	return client, done
}

func fakeManagedFrontendProcess(name, addr string, pid int) *managedFrontendProcess {
	done := make(chan struct{})
	close(done)
	outputDone := make(chan struct{})
	close(outputDone)
	return &managedFrontendProcess{
		Name: name,
		Addr: addr,
		Process: &devManagedProcess{
			Name:       name,
			Kind:       "frontend",
			Role:       "web-frontend",
			PID:        pid,
			StartedAt:  time.Now().UTC(),
			done:       done,
			outputDone: outputDone,
		},
	}
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
	if readyFile := os.Getenv("SCENERY_FRONTEND_TEST_READY_FILE"); readyFile != "" {
		for {
			if _, err := os.Stat(readyFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
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
