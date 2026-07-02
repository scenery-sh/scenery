package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/symphony"
)

func TestDashboardSymphonyRPCScopesByBaseAppID(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())

	server := newSymphonyDashboardTestServer(t)
	ctx := context.Background()
	created, err := dispatchSymphonyTestRPC[symphony.Task](ctx, server, "symphony/task/create", map[string]any{
		"app_id": "session-a",
		"input": map[string]any{
			"title":      "Shared task",
			"status_key": "todo",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Identifier != "SYM-1" {
		t.Fatalf("created = %+v", created)
	}
	shared, err := dispatchSymphonyTestRPC[symphony.State](ctx, server, "symphony/state", map[string]any{"app_id": "session-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(shared.Tasks) != 1 || shared.Tasks[0].Title != "Shared task" {
		t.Fatalf("shared state = %+v", shared)
	}
	other, err := dispatchSymphonyTestRPC[symphony.State](ctx, server, "symphony/state", map[string]any{"app_id": "other-session"})
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Tasks) != 0 {
		t.Fatalf("other app should be isolated: %+v", other.Tasks)
	}
	workflow, err := dispatchSymphonyTestRPC[symphony.Workflow](ctx, server, "symphony/workflow/update", map[string]any{
		"app_id": "session-a",
		"input": map[string]any{
			"workflow_markdown": "review locally",
			"mode":              "manual",
			"max_concurrency":   3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if workflow.WorkflowMarkdown != "review locally" || workflow.MaxConcurrency != 3 {
		t.Fatalf("workflow = %+v", workflow)
	}
}

func TestDashboardSymphonyRPCMovesAndRejectsRunnerMethods(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())

	server := newSymphonyDashboardTestServer(t)
	ctx := context.Background()
	created, err := dispatchSymphonyTestRPC[symphony.Task](ctx, server, "symphony/task/create", map[string]any{
		"app_id": "session-a",
		"input":  map[string]any{"title": "Move me"},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := dispatchSymphonyTestRPC[symphony.State](ctx, server, "symphony/task/move", map[string]any{
		"app_id":     "session-a",
		"id":         created.ID,
		"status_key": "in_progress",
		"index":      0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tasks) != 1 || state.Tasks[0].StatusKey != "in_progress" {
		t.Fatalf("state = %+v", state)
	}
	if _, err := dispatchSymphonyTestRPC[map[string]any](ctx, server, "symphony/run/start", map[string]any{"app_id": "missing-session"}); err == nil || !strings.Contains(err.Error(), "runner auth") {
		t.Fatalf("expected runner auth error, got %v", err)
	}
}

func TestDashboardSymphonyRPCFallsBackToDashboardAppIDWithoutBaseAppID(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())

	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertApp(context.Background(), devdash.AppRecord{
		ID:        "legacy",
		Name:      "legacy",
		Root:      t.TempDir(),
		Running:   true,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	server := newDashboardServerWithController(&symphonyNoBaseController{store: store}, t.TempDir(), "127.0.0.1:0", "", nil)
	task, err := dispatchSymphonyTestRPC[symphony.Task](context.Background(), server, "symphony/task/create", map[string]any{
		"app_id": "legacy",
		"input":  map[string]any{"title": "Fallback task"},
	})
	if err != nil {
		t.Fatalf("create task through fallback app id: %v", err)
	}
	if !strings.HasPrefix(task.Identifier, "SYM-") {
		t.Fatalf("task = %+v", task)
	}
	if task.AppID != "legacy" {
		t.Fatalf("expected fallback app id legacy, got task %+v", task)
	}
	state, err := dispatchSymphonyTestRPC[symphony.State](context.Background(), server, "symphony/state", map[string]any{"app_id": "legacy"})
	if err != nil {
		t.Fatalf("state through fallback app id: %v", err)
	}
	if len(state.Tasks) != 1 || state.Tasks[0].Title != "Fallback task" {
		t.Fatalf("state = %+v", state)
	}
}

func TestDashboardSymphonyRPCFailsClosedForSessionWithoutBaseAppID(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())

	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertApp(context.Background(), devdash.AppRecord{
		ID:        "legacy",
		SessionID: "legacy-session",
		Name:      "legacy",
		Root:      t.TempDir(),
		Running:   true,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	server := newDashboardServerWithController(&symphonyNoBaseController{store: store}, t.TempDir(), "127.0.0.1:0", "", nil)
	if _, err := dispatchSymphonyTestRPC[symphony.State](context.Background(), server, "symphony/state", map[string]any{"app_id": "legacy-session"}); err == nil {
		t.Fatal("expected missing base app id error")
	}
}

func newSymphonyDashboardTestServer(t *testing.T) *dashboardServer {
	t.Helper()
	store, err := devdash.OpenStore(filepath.Join(t.TempDir(), "devdash"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, app := range []devdash.AppRecord{
		{ID: "demo", BaseAppID: "demo", RuntimeAppID: "demo--a", SessionID: "session-a", Name: "demo", Root: t.TempDir(), Running: true, UpdatedAt: now},
		{ID: "demo", BaseAppID: "demo", RuntimeAppID: "demo--b", SessionID: "session-b", Name: "demo", Root: t.TempDir(), Running: true, UpdatedAt: now.Add(time.Second)},
		{ID: "other", BaseAppID: "other", RuntimeAppID: "other--a", SessionID: "other-session", Name: "other", Root: t.TempDir(), Running: true, UpdatedAt: now},
	} {
		if err := store.UpsertApp(context.Background(), app); err != nil {
			t.Fatal(err)
		}
	}
	return newDashboardServerWithController(&agentDashboardController{store: store}, t.TempDir(), "127.0.0.1:0", "", nil)
}

func dispatchSymphonyTestRPC[T any](ctx context.Context, server *dashboardServer, method string, params map[string]any) (T, error) {
	var zero T
	raw, err := json.Marshal(params)
	if err != nil {
		return zero, err
	}
	result, err := server.dispatchRPC(ctx, method, raw)
	if err != nil {
		return zero, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return zero, err
	}
	return out, nil
}

type symphonyNoBaseController struct {
	store *devdash.Store
}

func (c *symphonyNoBaseController) dashboardActiveAppID() string      { return "" }
func (c *symphonyNoBaseController) dashboardCurrentSessionID() string { return "" }
func (c *symphonyNoBaseController) dashboardListApps(context.Context) ([]map[string]any, error) {
	return nil, nil
}
func (c *symphonyNoBaseController) dashboardStore() *devdash.Store { return c.store }
func (c *symphonyNoBaseController) dashboardAuthorizeReport(*http.Request, devdash.ReportEnvelope) dashboardReportAuth {
	return dashboardReportAuth{}
}
func (c *symphonyNoBaseController) dashboardRootForApp(context.Context, string) (string, error) {
	return "", nil
}
func (c *symphonyNoBaseController) dashboardVictoria() dashboardVictoria { return nil }
func (c *symphonyNoBaseController) dashboardStatusFor(ctx context.Context, appID string) (devdash.AppStatus, error) {
	app, err := c.store.GetApp(ctx, appID)
	if err != nil {
		app, err = c.store.GetAppSession(ctx, appID)
		if err != nil {
			return devdash.AppStatus{}, err
		}
	}
	return devdash.AppStatus{AppID: firstNonEmpty(app.RouteID, app.ID), SessionID: app.SessionID, AppRoot: app.Root, Running: app.Running}, nil
}
