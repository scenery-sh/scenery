package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
)

type runtimeRPCTestController struct {
	status devdash.AppStatus
}

func (c runtimeRPCTestController) dashboardActiveAppID() string      { return "" }
func (c runtimeRPCTestController) dashboardCurrentSessionID() string { return "" }
func (c runtimeRPCTestController) dashboardStatusFor(context.Context, string) (devdash.AppStatus, error) {
	return c.status, nil
}
func (c runtimeRPCTestController) dashboardStore() *devdash.Store { return nil }
func (c runtimeRPCTestController) dashboardAuthorizeReport(*http.Request, devdash.ReportEnvelope) dashboardReportAuth {
	return dashboardReportAuth{}
}
func (c runtimeRPCTestController) dashboardVictoria() dashboardVictoria { return nil }

func TestRuntimeRPCStatusIsTheDocumentedShape(t *testing.T) {
	t.Parallel()

	server := newDashboardServerWithController(runtimeRPCTestController{status: devdash.AppStatus{
		Running:          true,
		AppID:            "session-a",
		BaseAppID:        "demo",
		SessionID:        "session-a",
		AppRoot:          "/tmp/demo",
		Meta:             json.RawMessage(`{"svcs":[]}`),
		APIEncoding:      json.RawMessage(`{}`),
		Addr:             "/tmp/demo/api.sock",
		SessionStatus:    "running",
		ServiceProcesses: []devdash.ServiceProcess{{Name: "books", PID: "42", State: "running"}},
		Observability: &devdash.ObservabilityState{
			Enabled: true,
			Logs:    devdash.ObservabilityBackendState{Enabled: true, Available: true, Status: "ready", URL: "http://127.0.0.1:9428"},
		},
	}}, t.TempDir(), "127.0.0.1:0", nil)

	result, err := server.dispatchRPC(context.Background(), "status", nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["kind"] != "scenery.dev-runtime.status" || payload["schema_revision"] == "" {
		t.Fatalf("status identity = %v / %v", payload["kind"], payload["schema_revision"])
	}
	if payload["app_id"] != "session-a" || payload["session_status"] != "running" || payload["running"] != true {
		t.Fatalf("status = %s", encoded)
	}
	for _, internal := range []string{"meta", "apiEncoding", "addr", "appID"} {
		if _, ok := payload[internal]; ok {
			t.Fatalf("status exposes internal field %q: %s", internal, encoded)
		}
	}
	if strings.Contains(string(encoded), "9428") {
		t.Fatalf("status exposes a substrate endpoint: %s", encoded)
	}
	processes, _ := payload["service_processes"].([]any)
	if len(processes) != 1 {
		t.Fatalf("service_processes = %v", payload["service_processes"])
	}
}

func TestRuntimeRPCRejectsUndocumentedMethodsAndParams(t *testing.T) {
	t.Parallel()

	server := newDashboardServerWithController(runtimeRPCTestController{}, t.TempDir(), "127.0.0.1:0", nil)
	for _, method := range []string{"list-apps", "logs/list", "process/output/list", "traces/list", "api-call", "stored-requests/list"} {
		if _, err := server.dispatchRPC(context.Background(), method, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "method not found") {
			t.Fatalf("%s error = %v, want method not found", method, err)
		}
	}
	for method, params := range map[string]string{
		"status":          `{"app":"x"}`,
		"postgres/tables": `{"app_id":"x","database":"main"}`,
		"db/query":        `{"app_id":"x","query":"select 1","array_mode":true}`,
	} {
		if _, err := server.dispatchRPC(context.Background(), method, json.RawMessage(params)); err == nil || !strings.Contains(err.Error(), "invalid params") {
			t.Fatalf("%s(%s) error = %v, want invalid params", method, params, err)
		}
	}
}

func TestDashboardClientWriteJSONUsesDeadline(t *testing.T) {
	t.Parallel()

	conn := &deadlineRecordingConn{}
	client := &dashboardClient{conn: conn}
	if err := client.writeJSON(map[string]any{"ok": true}); err != nil {
		t.Fatalf("writeJSON() error = %v", err)
	}
	deadlines := conn.deadlines()
	if len(deadlines) != 2 || deadlines[0].IsZero() || !deadlines[1].IsZero() {
		t.Fatalf("deadlines = %v, want a write deadline followed by a reset", deadlines)
	}
}

type deadlineRecordingConn struct {
	mu     sync.Mutex
	writes []time.Time
}

func (c *deadlineRecordingConn) WriteJSON(any) error { return nil }
func (c *deadlineRecordingConn) Close() error        { return nil }

func (c *deadlineRecordingConn) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, deadline)
	return nil
}

func (c *deadlineRecordingConn) deadlines() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.writes...)
}
