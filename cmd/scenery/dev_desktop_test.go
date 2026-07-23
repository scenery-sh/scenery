package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

func TestParseDevArgsAcceptsDesktop(t *testing.T) {
	opts, err := parseDevArgs([]string{"--desktop", "--detach"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Desktop || !opts.Detach {
		t.Fatalf("options = %+v", opts)
	}
}

func TestConfiguredDesktopShellsRequiresConfiguredTauriProject(t *testing.T) {
	root := t.TempDir()
	if _, err := configuredDesktopShells(root, app.Config{}); err == nil || !strings.Contains(err.Error(), "frontends.<name>.tauri") {
		t.Fatalf("no desktop config error = %v", err)
	}
	cfg := app.Config{
		Frontends: map[string]app.FrontendConfig{
			"web": {Root: "apps/web", Tauri: &app.FrontendTauriConfig{}},
		},
	}
	if _, err := configuredDesktopShells(root, cfg); err == nil || !strings.Contains(err.Error(), "src-tauri") {
		t.Fatalf("missing Tauri config error = %v", err)
	}
	writeDesktopTestFile(t, filepath.Join(root, "apps", "web", "src-tauri", "tauri.conf.json"), `{}`)
	if _, err := configuredDesktopShells(root, cfg); err == nil || !strings.Contains(err.Error(), "@tauri-apps/cli") {
		t.Fatalf("missing Tauri CLI error = %v", err)
	}
}

func TestDesktopShellUsesFrontendBackendAndRegistersProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)
	defer func() {
		cancel()
		waitForTestAgentServer(t, agentDone)
	}()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	frontendRoot := filepath.Join(root, "apps", "web")
	writeDesktopTestFile(t, filepath.Join(frontendRoot, "src-tauri", "tauri.conf.json"), `{}`)
	marker := filepath.Join(root, "tauri-invocation.json")
	writeDesktopTestExecutable(t, filepath.Join(frontendRoot, "node_modules", ".bin", "tauri"), `#!/bin/sh
set -eu
printf '%s\n%s\n%s\n%s\n%s\n%s\n' "$PWD" "$1" "$2" "$3" "$SCENERY_ENV" "$VITE_API_BASE_URL" > "$TAURI_TEST_MARKER"
trap 'exit 0' INT TERM
while :; do sleep 1; done
`)
	cfg := app.Config{
		Name: "desktop-demo",
		Frontends: map[string]app.FrontendConfig{
			"web": {Root: "apps/web", Tauri: &app.FrontendTauriConfig{}},
		},
		Envs: map[string]app.EnvConfig{"local": {Default: true}},
	}
	env, err := cfg.ResolveEnv("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Frontends = env.Frontends
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   cfg.AppID(),
		Environment: env.Name,
		AppRoot:     root,
		Status:      "starting",
		OwnerPID:    os.Getpid(),
		Backends: map[string]localagent.Backend{
			"web": {Network: "tcp", Addr: "127.0.0.1:5173"},
			"api": {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
		RouteManifest: localagent.RouteManifest{
			Routes: map[string]localagent.RouteRecord{
				"api": {URL: "https://desktop-demo.local.dev/api/"},
			},
		},
		ClaimOwner: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAURI_TEST_MARKER", marker)
	supervisor, err := newDevSupervisor(ctx, root, cfg, env, devBackend{Network: "tcp", Addr: "127.0.0.1:4000"}, nil, client, &session)
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.startDesktopShells(ctx); err != nil {
		t.Fatal(err)
	}

	var invocation []string
	waitForDesktopTest(t, 3*time.Second, func() bool {
		data, err := os.ReadFile(marker)
		if err != nil {
			return false
		}
		invocation = strings.Split(strings.TrimSpace(string(data)), "\n")
		sessions, err := client.List(ctx, root)
		return len(invocation) == 6 && err == nil && len(sessions) == 1 && sessions[0].Processes["desktop-web"].PID > 0
	})
	gotCwd, err := filepath.EvalSymlinks(invocation[0])
	if err != nil {
		t.Fatal(err)
	}
	wantCwd, err := filepath.EvalSymlinks(frontendRoot)
	if err != nil {
		t.Fatal(err)
	}
	if gotCwd != wantCwd {
		t.Fatalf("cwd = %q, want %q", invocation[0], frontendRoot)
	}
	if invocation[1] != "dev" || invocation[2] != "--config" {
		t.Fatalf("invocation = %#v", invocation)
	}
	var overlay struct {
		Build struct {
			DevURL           string `json:"devUrl"`
			BeforeDevCommand string `json:"beforeDevCommand"`
		} `json:"build"`
	}
	if err := json.Unmarshal([]byte(invocation[3]), &overlay); err != nil {
		t.Fatal(err)
	}
	if overlay.Build.DevURL != "http://127.0.0.1:5173" || overlay.Build.BeforeDevCommand != "" {
		t.Fatalf("overlay = %+v", overlay)
	}
	if invocation[4] != "local" || invocation[5] != "https://desktop-demo.local.dev/api/" {
		t.Fatalf("invocation env = %#v", invocation)
	}
	supervisor.mu.RLock()
	desktopProcess := supervisor.desktops["web"]
	supervisor.mu.RUnlock()
	if desktopProcess == nil || desktopProcess.Process == nil {
		t.Fatal("desktop process is not managed by the supervisor")
	}
	if err := supervisor.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-desktopProcess.Process.done:
	case <-time.After(3 * time.Second):
		t.Fatal("desktop process remained alive after supervisor shutdown")
	}
}

func TestDesktopShellExitDoesNotRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)
	defer func() {
		cancel()
		waitForTestAgentServer(t, agentDone)
	}()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	frontendRoot := filepath.Join(root, "apps", "web")
	writeDesktopTestFile(t, filepath.Join(frontendRoot, "src-tauri", "tauri.conf.json"), `{}`)
	marker := filepath.Join(root, "desktop-starts.log")
	writeDesktopTestExecutable(t, filepath.Join(frontendRoot, "node_modules", ".bin", "tauri"), `#!/bin/sh
set -eu
echo start >> "$TAURI_TEST_MARKER"
`)
	t.Setenv("TAURI_TEST_MARKER", marker)
	cfg := app.Config{
		Name: "desktop-exit",
		Frontends: map[string]app.FrontendConfig{
			"web": {Root: "apps/web", Tauri: &app.FrontendTauriConfig{}},
		},
		Envs: map[string]app.EnvConfig{"local": {Default: true}},
	}
	env, err := cfg.ResolveEnv("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Frontends = env.Frontends
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: cfg.AppID(), Environment: env.Name, AppRoot: root,
		Status: "running", OwnerPID: os.Getpid(), ClaimOwner: true,
		Backends: map[string]localagent.Backend{"web": {Network: "tcp", Addr: "127.0.0.1:5173"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisor, err := newDevSupervisor(ctx, root, cfg, env, devBackend{Network: "tcp", Addr: "127.0.0.1:4000"}, nil, client, &session)
	if err != nil {
		t.Fatal(err)
	}
	defer supervisor.Close()
	if err := supervisor.startDesktopShells(ctx); err != nil {
		t.Fatal(err)
	}
	waitForDesktopTest(t, 3*time.Second, func() bool {
		sessions, err := client.List(ctx, root)
		if err != nil || len(sessions) != 1 {
			return false
		}
		_, registered := sessions[0].Processes["desktop-web"]
		return !registered
	})
	sessions, err := client.List(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Status != "running" || sessions[0].Backends["web"].Addr != "127.0.0.1:5173" {
		t.Fatalf("session changed after desktop exit: %+v", sessions)
	}
	time.Sleep(150 * time.Millisecond)
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if starts := strings.Count(string(data), "start"); starts != 1 {
		t.Fatalf("desktop starts = %d, want exactly one", starts)
	}
}

func writeDesktopTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeDesktopTestExecutable(t *testing.T, path, content string) {
	t.Helper()
	writeDesktopTestFile(t, path, content)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func waitForDesktopTest(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for desktop test condition")
}
