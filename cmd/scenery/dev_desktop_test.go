package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

func TestParseDevArgsAcceptsDesktop(t *testing.T) {
	t.Parallel()

	opts, err := parseDevArgs([]string{"--desktop", "--detach"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Desktop || !opts.Detach {
		t.Fatalf("options = %+v", opts)
	}
}

func TestConfiguredDesktopShellsRequiresConfiguredTauriProject(t *testing.T) {
	t.Parallel()

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

func TestDesktopShellUsesFrontendBackendAndPlansRegistrationInProcess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	frontendRoot := filepath.Join(root, "apps", "web")
	writeDesktopTestFile(t, filepath.Join(frontendRoot, "src-tauri", "tauri.conf.json"), `{}`)
	writeDesktopTestExecutable(t, filepath.Join(frontendRoot, "node_modules", ".bin", "tauri"), "#!/bin/sh\n")
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
	session := localagent.Session{
		BaseAppID:   cfg.AppID(),
		Environment: env.Name,
		AppRoot:     root,
		SessionID:   "desktop-session",
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
	}
	shells, err := configuredDesktopShells(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(shells) != 1 {
		t.Fatalf("desktop shells = %d, want 1", len(shells))
	}
	request, err := desktopProcessStartRequest(root, shells[0], "http://127.0.0.1:5173", []string{"BASE=present"}, session)
	if err != nil {
		t.Fatal(err)
	}
	if request.Dir != frontendRoot || request.Command != filepath.Join(frontendRoot, "node_modules", ".bin", "tauri") {
		t.Fatalf("desktop process command = %+v", request)
	}
	if got := strings.Join(request.Args[:2], " "); got != "dev --config" {
		t.Fatalf("desktop process args = %#v", request.Args)
	}
	var overlay struct {
		Build struct {
			DevURL           string `json:"devUrl"`
			BeforeDevCommand string `json:"beforeDevCommand"`
		} `json:"build"`
	}
	if err := json.Unmarshal([]byte(request.Args[2]), &overlay); err != nil {
		t.Fatal(err)
	}
	if overlay.Build.DevURL != "http://127.0.0.1:5173" || overlay.Build.BeforeDevCommand != "" {
		t.Fatalf("overlay = %+v", overlay)
	}
	for _, want := range []string{"SCENERY_ENV=local", "VITE_API_BASE_URL=https://desktop-demo.local.dev/api/"} {
		if !containsString(request.Env, want) {
			t.Fatalf("desktop process env missing %q: %#v", want, request.Env)
		}
	}
	processes := desktopSessionProcesses(map[string]localagent.Process{"app": {PID: 11}}, "web", 42)
	if processes["app"].PID != 11 || processes["desktop-web"].PID != 42 {
		t.Fatalf("registered desktop processes = %+v", processes)
	}
}

func TestDesktopShellExitClearsRegistrationWithoutRestartInProcess(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	close(done)
	outputDone := make(chan struct{})
	close(outputDone)
	desktop := &managedDesktopProcess{
		Name: "web",
		Process: &devManagedProcess{
			Name:       "web",
			Kind:       "desktop",
			Role:       "desktop-shell",
			PID:        42,
			done:       done,
			outputDone: outputDone,
		},
	}
	var updates []int
	supervisor := &devSupervisor{
		ctx:      context.Background(),
		desktops: map[string]*managedDesktopProcess{"web": desktop},
		desktopSessionProcessUpdater: func(_ context.Context, name string, pid int) error {
			if name != "web" {
				t.Fatalf("updated desktop = %q", name)
			}
			updates = append(updates, pid)
			return nil
		},
	}
	supervisor.monitorManagedDesktop("web", desktop)
	if len(updates) != 1 || updates[0] != 0 {
		t.Fatalf("desktop registration updates = %v, want one removal", updates)
	}
	if supervisor.isCurrentManagedDesktop("web", desktop) {
		t.Fatal("exited desktop remained supervised")
	}
	remaining := desktopSessionProcesses(map[string]localagent.Process{
		"app":         {PID: 11},
		"desktop-web": {PID: 42},
	}, "web", 0)
	if len(remaining) != 1 || remaining["app"].PID != 11 {
		t.Fatalf("remaining session processes = %+v", remaining)
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
