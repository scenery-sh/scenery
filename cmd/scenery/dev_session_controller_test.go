package main

import (
	"context"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

func TestPrepareDevAgentSessionRegistersOnceWithFrontendBackends(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	home := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", home)
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

	t.Setenv("SCENERY_FRONTEND_WEB_ADDR", "127.0.0.1:5173")
	var requests []localagent.RegisterRequest
	devSessionTestHooks.Lock()
	devSessionTestHooks.register = func(req localagent.RegisterRequest) {
		requests = append(requests, req)
	}
	devSessionTestHooks.Unlock()
	t.Cleanup(func() {
		devSessionTestHooks.Lock()
		devSessionTestHooks.register = nil
		devSessionTestHooks.Unlock()
	})

	prepared, err := prepareDevAgentSessionDetailed(ctx, t.TempDir(), app.Config{
		Name: "demo",
		Frontends: map[string]app.FrontendConfig{
			"web": {Root: "apps/web"},
		},
	}, devListenRequest{}, nil)
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
