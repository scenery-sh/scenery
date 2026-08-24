package main

import (
	"context"
	"path/filepath"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
)

func TestPrepareDevAgentSessionRegistersOnceWithFrontendBackends(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	home := t.TempDir()
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
		Identity: cliBuildIdentity(),
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
	t.Cleanup(func() {
		cancel()
		<-done
	})

	var requests []localagent.RegisterRequest

	cfg := app.Config{
		Name: "demo",
		Frontends: map[string]app.FrontendConfig{
			"web": {Root: "apps/web"},
		},
		Envs: map[string]app.EnvConfig{"local": {Default: true}},
	}
	env, err := cfg.ResolveEnv("")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := (&DevSessionController{
		root:  t.TempDir(),
		cfg:   cfg,
		env:   env,
		paths: &paths,
		environment: overlayEnv(envpolicy.Environ(), map[string]string{
			"SCENERY_AGENT_HOME":         paths.Home,
			"SCENERY_DEV_CACHE_DIR":      filepath.Join(paths.AgentDir, "dashboard"),
			"SCENERY_DEV_DASHBOARD_ADDR": "127.0.0.1:9",
		}),
		frontendOverride: func(name string) string {
			if name == "web" {
				return "127.0.0.1:5173"
			}
			return ""
		},
		onRegister: func(req localagent.RegisterRequest) {
			requests = append(requests, req)
		},
	}).Prepare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()

	if len(requests) != 1 {
		t.Fatalf("register calls = %d, want 1", len(requests))
	}
	req := requests[0]
	if got := req.Backends[localagent.RouteAPI]; got.Network != "unix" || got.Addr == "" {
		t.Fatalf("api backend = %+v, want unix socket", got)
	}
	if got := req.Backends["web"]; got.Network != "tcp" || got.Addr != "127.0.0.1:5173" {
		t.Fatalf("web backend = %+v", got)
	}
	if prepared.Session == nil || prepared.Session.Backends["web"].Addr != "127.0.0.1:5173" {
		t.Fatalf("prepared session = %+v", prepared.Session)
	}
	if prepared.Backend.Network != "unix" || prepared.Backend.Addr == "" {
		t.Fatalf("prepared backend = %+v", prepared.Backend)
	}
}
