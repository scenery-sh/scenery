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

type stubAgentDashboardRegistry struct {
	sessions   map[string]localagent.Session
	substrates map[string]localagent.Substrate
}

func TestAgentDashboardControllerUsesVictoriaSubstrate(t *testing.T) {
	t.Parallel()
	registry := &stubAgentDashboardRegistry{substrates: map[string]localagent.Substrate{
		localagent.SubstrateVictoria: {
			Kind:      localagent.SubstrateVictoria,
			URLs:      map[string]string{"metrics": "http://127.0.0.1:8428", "logs": "http://127.0.0.1:9428", "traces": "http://127.0.0.1:10428"},
			Endpoints: map[string]string{"metrics": "http://127.0.0.1:8428/opentelemetry/v1/metrics", "logs": "http://127.0.0.1:9428/insert/opentelemetry/v1/logs", "traces": "http://127.0.0.1:10428/insert/opentelemetry/v1/traces"},
		},
	}}
	controller := &agentDashboardController{agent: registry}
	victoria := controller.dashboardVictoria()
	if victoria == nil {
		t.Fatal("dashboardVictoria returned nil")
	}
	if got := victoria.Endpoint("traces"); got != "http://127.0.0.1:10428/insert/opentelemetry/v1/traces" {
		t.Fatalf("trace endpoint = %q", got)
	}
}

func (r *stubAgentDashboardRegistry) GetSession(id string) (localagent.Session, bool) {
	session, ok := r.sessions[id]
	return session, ok
}

func (r *stubAgentDashboardRegistry) GetSubstrate(kind string) (localagent.Substrate, bool) {
	substrate, ok := r.substrates[kind]
	return substrate, ok
}

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

func TestDashboardControlPlaneWritesThroughAgentDashboardStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	session, err := localagent.NewSession(localagent.RegisterRequest{
		BaseAppID:   "demo",
		AppRoot:     t.TempDir(),
		SessionID:   "session-a",
		ReportToken: "report-secret",
	}, "127.0.0.1:4040", "http", nil)
	if err != nil {
		t.Fatal(err)
	}
	agentRegistry := &stubAgentDashboardRegistry{sessions: map[string]localagent.Session{
		session.SessionID: session,
	}}

	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	controller := &agentDashboardController{store: store, agent: agentRegistry}
	dashboard := newDashboardServerWithController(controller, t.TempDir(), "127.0.0.1:0", nil)

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
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, dashboardControlPlanePath, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer report-secret")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		dashboard.http.Handler.ServeHTTP(rec, req)
		return rec.Code
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

	ctx := context.Background()

	owner := localagent.CaptureOwner(os.Getpid(), "scenery up")
	session, err := localagent.NewSession(localagent.RegisterRequest{
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
	}, "127.0.0.1:4040", "http", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.Aliases = map[string]string{
		localagent.RouteAPI: "http://api.demo.localhost:4040",
		"victoria":          "http://victoria.demo.localhost:4040",
	}
	degradedSession, err := localagent.NewSession(localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		SessionID: "degraded-session",
		Status:    "running",
		OwnerPID:  owner.PID,
		Owner:     owner,
		Processes: map[string]localagent.Process{
			"frontend-web": {PID: 2147483647},
		},
	}, "127.0.0.1:4040", "http", nil)
	if err != nil {
		t.Fatal(err)
	}
	agentRegistry := &stubAgentDashboardRegistry{sessions: map[string]localagent.Session{
		session.SessionID:         session,
		degradedSession.SessionID: degradedSession,
	}}

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

	controller := &agentDashboardController{store: store, agent: agentRegistry}
	status, err := controller.dashboardStatusFor(ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.SessionStatus != "running" {
		t.Fatalf("live status health = %+v, want running", status)
	}
	if status.Routes[localagent.RouteAPI] == "" {
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

func TestAgentDashboardReportUsesSessionReportToken(t *testing.T) {
	t.Parallel()

	session, err := localagent.NewSession(localagent.RegisterRequest{
		BaseAppID:   "demo",
		AppRoot:     t.TempDir(),
		Branch:      "feature/report-token",
		ReportToken: "report-secret",
		Backends: map[string]localagent.Backend{
			localagent.RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	}, "127.0.0.1:4040", "http", nil)
	if err != nil {
		t.Fatal(err)
	}
	agentRegistry := &stubAgentDashboardRegistry{sessions: map[string]localagent.Session{
		session.SessionID: session,
	}}

	store, err := devdash.OpenStore(filepath.Join(t.TempDir(), "dashboard"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	server := newDashboardServerWithController(&agentDashboardController{
		store: store,
		agent: agentRegistry,
	}, t.TempDir(), "127.0.0.1:0", nil)
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
}

func TestAgentDashboardRejectsStaleReportWithStructuredLog(t *testing.T) {
	t.Parallel()

	agentHome := t.TempDir()
	agentPaths := localagent.PathsForHome(agentHome)
	if got := agentPaths.RegistryPath; filepath.Dir(got) != filepath.Join(agentHome, "agent") {
		t.Fatalf("agent registry path = %q, want under isolated agent home %q", got, agentHome)
	}

	session, err := localagent.NewSession(localagent.RegisterRequest{
		BaseAppID:   "demo",
		AppRoot:     t.TempDir(),
		Branch:      "feature/report-token",
		ReportToken: "report-secret",
	}, "127.0.0.1:4040", "http", nil)
	if err != nil {
		t.Fatal(err)
	}
	agentRegistry := &stubAgentDashboardRegistry{sessions: map[string]localagent.Session{
		session.SessionID: session,
	}}
	exported := make(chan *devdash.LogEvent, 1)
	controller := &agentDashboardController{agent: agentRegistry}
	server := newDashboardServerWithControllerHooks(controller, t.TempDir(), "127.0.0.1:0", nil, dashboardServerHooks{
		exportLogEvent: func(event *devdash.LogEvent) {
			exported <- event
		},
	})

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

	var event *devdash.LogEvent
	select {
	case event = <-exported:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rejected report log export")
	}
	if event.Level != "warn" {
		t.Fatalf("rejected report level = %q, want warn", event.Level)
	}
	if event.Message != "stale or unauthorized dev report rejected" {
		t.Fatalf("rejected report message = %q", event.Message)
	}
	for key, want := range map[string]any{
		"kind":         "dev-report-rejected",
		"reason":       "stale-session",
		"report_type":  "trace-event",
		"reporter_pid": 12345,
	} {
		if got := event.Attrs[key]; got != want {
			t.Fatalf("rejected report attr %s = %#v, want %#v; attrs=%+v", key, got, want, event.Attrs)
		}
	}
}
