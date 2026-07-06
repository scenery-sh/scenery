package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
)

func TestAgentDashboardControllerUsesSessionRouteIDs(t *testing.T) {
	t.Parallel()

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

func TestAppRecordStatusUsesStoredSessionHealth(t *testing.T) {
	t.Parallel()

	status := appRecordStatus(devdash.AppRecord{
		ID:                  "demo",
		SessionID:           "session-a",
		Root:                "/tmp/demo",
		Running:             true,
		SessionStatus:       "degraded",
		SessionStatusReason: "app process 42 is not running",
	})
	if status.Running || status.SessionStatus != "degraded" || status.SessionStatusReason == "" {
		t.Fatalf("status = %+v, want degraded and not running", status)
	}
}

func TestAppRecordStatusIncludesDashboardBundleJSON(t *testing.T) {
	t.Parallel()

	status := appRecordStatus(devdash.AppRecord{
		ID:        "demo",
		SessionID: "session-a",
		Root:      "/tmp/demo",
	})
	if status.DashboardBundle == nil || status.DashboardBundle.RunningHash == "" {
		t.Fatalf("dashboard bundle = %+v, want running hash", status.DashboardBundle)
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	bundle, ok := payload["dashboardBundle"].(map[string]any)
	if !ok || bundle["runningHash"] == "" {
		t.Fatalf("dashboardBundle JSON = %#v", payload["dashboardBundle"])
	}
}

func TestDashboardControlPlaneWritesThroughAgentDashboardStore(t *testing.T) {
	t.Parallel()

	agentServer, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
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
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   "demo",
		AppRoot:     t.TempDir(),
		SessionID:   "session-a",
		ReportToken: "report-secret",
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
	controller := &agentDashboardController{store: store, agent: agentServer}
	dashboard := newDashboardServerWithController(controller, t.TempDir(), "127.0.0.1:0", "", nil)
	server := httptest.NewServer(dashboard.http.Handler)
	t.Cleanup(server.Close)

	app := devdash.AppRecord{
		ID:        "demo",
		SessionID: session.SessionID,
		Name:      "Demo",
		Root:      session.AppRoot,
		Running:   true,
		UpdatedAt: time.Now().UTC(),
	}
	postControlPlane := func(payload dashboardControlPlaneRequest) int {
		t.Helper()
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+dashboardControlPlanePath, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer report-secret")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if got := postControlPlane(dashboardControlPlaneRequest{SessionID: session.SessionID, UpsertApp: &app}); got != http.StatusNoContent {
		t.Fatalf("upsert status = %d", got)
	}
	stored, err := store.GetAppSession(ctx, session.SessionID)
	if err != nil {
		t.Fatalf("stored app session: %v", err)
	}
	if stored.ID != "demo" || !stored.Running {
		t.Fatalf("stored app = %+v", stored)
	}

	if got := postControlPlane(dashboardControlPlaneRequest{
		SessionID: session.SessionID,
		ProcessEvent: &dashboardProcessEventPost{
			AppID:       "demo",
			SessionID:   session.SessionID,
			Kind:        "process/reload",
			PayloadJSON: json.RawMessage(`{"pid":"42"}`),
		},
	}); got != http.StatusNoContent {
		t.Fatalf("process event status = %d", got)
	}
	events, err := store.ListProcessEvents(ctx, "demo", 10)
	if err != nil {
		t.Fatalf("process events: %v", err)
	}
	if len(events) != 1 || events[0].Kind != "process/reload" || string(events[0].PayloadJSON) != `{"pid":"42"}` {
		t.Fatalf("events = %+v", events)
	}
}

func TestAgentDashboardControllerMarksMissingRegistrySessionOffline(t *testing.T) {
	t.Parallel()

	agentServer, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
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
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	owner := localagent.CaptureOwner(os.Getpid(), "scenery up")
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		Branch:    "feature/live",
		Status:    "running",
		OwnerPID:  owner.PID,
		Owner:     owner,
		RouteNamespace: localagent.RouteNamespace{
			Hosts: map[string]string{
				localagent.RouteAPI: "api.demo.localhost",
				"victoria":          "victoria.demo.localhost",
			},
		},
		Backends: map[string]localagent.Backend{
			localagent.RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
			"victoria":          {Network: "tcp", Addr: "127.0.0.1:8428"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	degradedSession, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		SessionID: "degraded-session",
		Status:    "running",
		OwnerPID:  owner.PID,
		Owner:     owner,
		Processes: map[string]localagent.Process{
			"frontend-web": {PID: 2147483647},
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
			RuntimeAppID: "demo--" + degradedSession.SessionID,
			SessionID:    degradedSession.SessionID,
			Name:         "demo",
			Root:         degradedSession.AppRoot,
			ListenAddr:   "127.0.0.1:4300",
			Running:      true,
			UpdatedAt:    now.Add(2 * time.Second),
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
	statusByID := map[string]string{}
	reasonByID := map[string]string{}
	for _, app := range apps {
		id, _ := app["id"].(string)
		offline, _ := app["offline"].(bool)
		offlineByID[id] = offline
		statusByID[id], _ = app["sessionStatus"].(string)
		reasonByID[id], _ = app["sessionStatusReason"].(string)
	}
	if offlineByID[session.SessionID] {
		t.Fatalf("live session marked offline: apps=%+v", apps)
	}
	if statusByID[session.SessionID] != "running" {
		t.Fatalf("live session status = %q, want running: apps=%+v", statusByID[session.SessionID], apps)
	}
	if !offlineByID[degradedSession.SessionID] {
		t.Fatalf("degraded session not marked offline: apps=%+v", apps)
	}
	if statusByID[degradedSession.SessionID] != "degraded" || reasonByID[degradedSession.SessionID] == "" {
		t.Fatalf("degraded session status = %q reason %q, apps=%+v", statusByID[degradedSession.SessionID], reasonByID[degradedSession.SessionID], apps)
	}
	if !offlineByID["stale-session"] {
		t.Fatalf("stale session not marked offline: apps=%+v", apps)
	}
	if statusByID["stale-session"] != "stale" {
		t.Fatalf("stale session status = %q, want stale: apps=%+v", statusByID["stale-session"], apps)
	}

	status, err := controller.dashboardStatusFor(ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.SessionStatus != "running" {
		t.Fatalf("live status health = %+v, want running", status)
	}
	if status.Routes[localagent.RouteAPI] == "" || status.Routes[localagent.RouteDashboard] == "" {
		t.Fatalf("live status routes missing user-facing entries: %+v", status.Routes)
	}
	if _, ok := status.Routes["victoria"]; ok {
		t.Fatalf("live status exposed victoria route: %+v", status.Routes)
	}
	if status.Aliases[localagent.RouteAPI] == "" {
		t.Fatalf("live status aliases missing api entry: %+v", status.Aliases)
	}
	if _, ok := status.Aliases["victoria"]; ok {
		t.Fatalf("live status exposed victoria alias: %+v", status.Aliases)
	}

	status, err = controller.dashboardStatusFor(ctx, degradedSession.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Running || status.SessionStatus != "degraded" || status.SessionStatusReason == "" {
		t.Fatalf("degraded status health = %+v, want degraded and not running", status)
	}

	status, err = controller.dashboardStatusFor(ctx, "stale-session")
	if err != nil {
		t.Fatal(err)
	}
	if status.Running || status.SessionStatus != "stale" {
		t.Fatalf("stale status health = %+v, want stale and not running", status)
	}
}

func TestAgentDashboardControllerUsesVictoriaSubstrate(t *testing.T) {
	t.Parallel()

	agentServer, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
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
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind: localagent.SubstrateVictoria,
		URLs: map[string]string{
			"metrics": "http://127.0.0.1:8428",
			"logs":    "http://127.0.0.1:9428",
			"traces":  "http://127.0.0.1:10428",
		},
		Endpoints: map[string]string{
			"metrics": "http://127.0.0.1:8428/opentelemetry/v1/metrics",
			"logs":    "http://127.0.0.1:9428/insert/opentelemetry/v1/logs",
			"traces":  "http://127.0.0.1:10428/insert/opentelemetry/v1/traces",
		},
	}); err != nil {
		t.Fatal(err)
	}
	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	controller := &agentDashboardController{store: store, agent: agentServer}
	victoria := controller.dashboardVictoria()
	if victoria == nil {
		t.Fatal("dashboardVictoria returned nil")
	}
	if got := victoria.Endpoint("traces"); got != "http://127.0.0.1:10428/insert/opentelemetry/v1/traces" {
		t.Fatalf("trace endpoint = %q", got)
	}
}

func TestAgentDashboardReportUsesSessionReportToken(t *testing.T) {
	t.Parallel()

	agentServer, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
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
	defer client.CloseIdleConnections()
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
	if len(counts) != 0 {
		t.Fatalf("log counts = %+v", counts)
	}
}

func TestAgentDashboardRejectsStaleReportWithStructuredLog(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	agentHome := t.TempDir()
	runDir, err := os.MkdirTemp("/tmp", "scenery-agent-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(runDir)
	})
	agentServer, err := localagent.NewServer(localagent.RunOptions{
		Home:       agentHome,
		SocketPath: filepath.Join(runDir, "agent.sock"),
		RouterAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := agentServer.Paths().RegistryPath; filepath.Dir(got) != filepath.Join(agentHome, "agent") {
		t.Fatalf("agent registry path = %q, want under isolated agent home %q", got, agentHome)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(ctx)
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
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   "demo",
		AppRoot:     t.TempDir(),
		ReportToken: "report-secret",
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
		Type:        "trace-event",
		AppID:       "demo",
		SessionID:   "missing-session",
		ReporterPID: 12345,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, devdash.ReportPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer report-secret")
	rec := httptest.NewRecorder()
	server.handleReport(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("report status = %d body=%q", rec.Code, rec.Body.String())
	}

	counts, err := store.CountLogsByLevelForSession(context.Background(), "demo", "missing-session", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Fatalf("stale log counts = %+v", counts)
	}
	currentCounts, err := store.CountLogsByLevelForSession(context.Background(), "demo", session.SessionID, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(currentCounts) != 0 {
		t.Fatalf("current session should not receive stale report log: %+v", currentCounts)
	}
}
