package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/devdash"
)

func TestAgentDashboardControllerUsesSessionRouteIDs(t *testing.T) {
	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	ctx := context.Background()
	if err := store.UpsertApp(ctx, devdash.AppRecord{
		ID:           "demo",
		BaseAppID:    "demo",
		RuntimeAppID: "demo--session-a",
		SessionID:    "session-a",
		Name:         "demo",
		Root:         "/tmp/session-a",
		ListenAddr:   "127.0.0.1:4100",
		Running:      true,
		UpdatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	controller := &agentDashboardController{store: store}
	apps, err := controller.dashboardListApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0]["id"] != "session-a" || apps[0]["base_app_id"] != "demo" {
		t.Fatalf("apps = %+v", apps)
	}

	status, err := controller.dashboardStatusFor(ctx, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if status.AppID != "session-a" || status.BaseAppID != "demo" || status.Addr != "127.0.0.1:4100" {
		t.Fatalf("status = %+v", status)
	}
}

func TestAgentDashboardControllerMarksMissingRegistrySessionOffline(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	agentServer, err := localagent.NewServer(localagent.RunOptions{RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agentServer.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("agent shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for agent shutdown")
		}
	})

	client := localagent.NewClient(agentServer.Paths().SocketPath)
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		Branch:    "feature/live",
		Backends: map[string]localagent.Backend{
			localagent.RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
			"victoria":          {Network: "tcp", Addr: "127.0.0.1:8428"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	now := time.Now().UTC()
	for _, app := range []devdash.AppRecord{
		{
			ID:           "demo",
			BaseAppID:    "demo",
			RuntimeAppID: "demo--" + session.SessionID,
			SessionID:    session.SessionID,
			Name:         "demo",
			Root:         session.AppRoot,
			ListenAddr:   "127.0.0.1:4100",
			Running:      true,
			UpdatedAt:    now,
		},
		{
			ID:           "demo",
			BaseAppID:    "demo",
			RuntimeAppID: "demo--stale-session",
			SessionID:    "stale-session",
			Name:         "demo",
			Root:         "/tmp/stale-session",
			ListenAddr:   "127.0.0.1:4200",
			Running:      true,
			UpdatedAt:    now.Add(time.Second),
		},
	} {
		if err := store.UpsertApp(ctx, app); err != nil {
			t.Fatal(err)
		}
	}

	controller := &agentDashboardController{store: store, agent: agentServer}
	apps, err := controller.dashboardListApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	offlineByID := map[string]bool{}
	for _, app := range apps {
		id, _ := app["id"].(string)
		offline, _ := app["offline"].(bool)
		offlineByID[id] = offline
	}
	if offlineByID[session.SessionID] {
		t.Fatalf("live session marked offline: apps=%+v", apps)
	}
	if !offlineByID["stale-session"] {
		t.Fatalf("stale session not marked offline: apps=%+v", apps)
	}

	status, err := controller.dashboardStatusFor(ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Routes[localagent.RouteAPI] == "" || status.Routes[localagent.RouteDashboard] == "" || status.Routes[localagent.RouteMCP] == "" {
		t.Fatalf("live status routes missing user-facing entries: %+v", status.Routes)
	}
	if _, ok := status.Routes["victoria"]; ok {
		t.Fatalf("live status exposed victoria route: %+v", status.Routes)
	}

	status, err = controller.dashboardStatusFor(ctx, "stale-session")
	if err != nil {
		t.Fatal(err)
	}
	if status.Running {
		t.Fatalf("stale status still running: %+v", status)
	}
}

func TestAgentDashboardReportUsesSessionReportToken(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	agentServer, err := localagent.NewServer(localagent.RunOptions{RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agentServer.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("agent shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for agent shutdown")
		}
	})

	client := localagent.NewClient(agentServer.Paths().SocketPath)
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   "demo",
		AppRoot:     t.TempDir(),
		Branch:      "feature/report-token",
		ReportToken: "report-secret",
		Backends: map[string]localagent.Backend{
			localagent.RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	store, err := devdash.OpenStore(filepath.Join(t.TempDir(), "dashboard"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	server := newDashboardServerWithController(&agentDashboardController{
		store: store,
		agent: agentServer,
	}, t.TempDir(), "127.0.0.1:0", "", nil)
	body, err := json.Marshal(devdash.ReportEnvelope{
		Type:      "log",
		AppID:     "demo",
		SessionID: session.SessionID,
		LogEvent: &devdash.LogEvent{
			AppID:     "demo",
			SessionID: session.SessionID,
			Level:     "INFO",
			Message:   "hello",
			Timestamp: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, devdash.ReportPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer report-secret")
	rec := httptest.NewRecorder()
	server.handleReport(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("report status = %d body=%q", rec.Code, rec.Body.String())
	}
	counts, err := store.CountLogsByLevelForSession(context.Background(), "demo", session.SessionID, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 || counts[0].Level != "INFO" || counts[0].Count != 1 {
		t.Fatalf("log counts = %+v", counts)
	}
}
