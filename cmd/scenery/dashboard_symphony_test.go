package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/symphony"
)

func TestDashboardSymphonyRPCScopesByBaseAppID(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

func TestDashboardSymphonyRPCBlocksAutoEscalation(t *testing.T) {
	t.Parallel()

	server := newSymphonyDashboardTestServer(t)
	_, err := dispatchSymphonyTestRPC[symphony.Workflow](context.Background(), server, "symphony/workflow/update", map[string]any{
		"app_id": "session-a",
		"input": map[string]any{
			"mode":            "auto",
			"max_concurrency": 1,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "mode=auto") {
		t.Fatalf("expected auto mode gate, got %v", err)
	}
}

func TestDashboardSymphonyRPCRequiresLoopbackPeerForMutations(t *testing.T) {
	t.Parallel()

	server := newSymphonyDashboardTestServer(t)
	remote := withDashboardLoopbackPeer(context.Background(), false)
	_, err := dispatchSymphonyTestRPC[symphony.Task](remote, server, "symphony/task/create", map[string]any{
		"app_id": "session-a",
		"input":  map[string]any{"title": "Remote task"},
	})
	if err == nil || !strings.Contains(err.Error(), "local dashboard clients") {
		t.Fatalf("expected loopback gate for task create, got %v", err)
	}
	if _, err := dispatchSymphonyTestRPC[symphony.State](remote, server, "symphony/state", map[string]any{"app_id": "session-a"}); err != nil {
		t.Fatalf("read should stay available to remote peers: %v", err)
	}
	local := withDashboardLoopbackPeer(context.Background(), true)
	created, err := dispatchSymphonyTestRPC[symphony.Task](local, server, "symphony/task/create", map[string]any{
		"app_id": "session-a",
		"input":  map[string]any{"title": "Local task"},
	})
	if err != nil || created.Identifier != "SYM-1" {
		t.Fatalf("local create = %+v, %v", created, err)
	}
}

func TestIsLoopbackRemoteAddr(t *testing.T) {
	t.Parallel()

	for addr, want := range map[string]bool{
		"127.0.0.1:52011": true,
		"[::1]:52011":     true,
		"10.0.0.5:52011":  false,
		"192.168.1.2:80":  false,
		"":                false,
		"not-an-address":  false,
	} {
		if got := isLoopbackRemoteAddr(addr); got != want {
			t.Errorf("isLoopbackRemoteAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestDashboardWebSocketOriginCheck(t *testing.T) {
	t.Parallel()

	req := &http.Request{Host: "localhost:4747", Header: http.Header{"Origin": []string{"http://localhost:4747"}}}
	if !dashboardCheckOrigin(req) {
		t.Fatal("expected same-origin dashboard websocket to pass")
	}
	req.Header.Set("Origin", "https://example.com")
	if dashboardCheckOrigin(req) {
		t.Fatal("expected cross-origin dashboard websocket to be rejected")
	}
}

func TestDashboardSymphonyRPCRejectsAppWithoutBaseAppID(t *testing.T) {
	t.Parallel()

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
	server.symphonyStore = newSymphonyStore(t)
	_, err = dispatchSymphonyTestRPC[symphony.Task](context.Background(), server, "symphony/task/create", map[string]any{
		"app_id": "legacy",
		"input":  map[string]any{"title": "Rejected task"},
	})
	if err == nil || !strings.Contains(err.Error(), "stable app id") {
		t.Fatalf("create task error = %v", err)
	}
}

func TestDashboardSymphonyRPCFailsClosedForSessionWithoutBaseAppID(t *testing.T) {
	t.Parallel()

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
	server.symphonyStore = newSymphonyStore(t)
	if _, err := dispatchSymphonyTestRPC[symphony.State](context.Background(), server, "symphony/state", map[string]any{"app_id": "legacy-session"}); err == nil {
		t.Fatal("expected missing base app id error")
	}
}

func TestDashboardSymphonyAutoRunnerClaimsTodoTask(t *testing.T) {
	t.Parallel()

	var runnerStore *symphony.Store
	hooks := symphonyRunnerHooks{}
	hooks.startAsync = func(fn func()) { fn() }
	hooks.runCodexAgent = func(ctx context.Context, req symphonyRunRequest, callbacks symphonyRunCallbacks) (symphonyRunResult, error) {
		if !strings.Contains(req.Prompt, "Ticket "+req.Task.Identifier) || !strings.Contains(req.Prompt, req.AppWorkspace) {
			return symphonyRunResult{}, fmt.Errorf("prompt = %q", req.Prompt)
		}
		if callbacks.ThreadStarted != nil {
			callbacks.ThreadStarted(1234, "thread-test")
		}
		if callbacks.TurnStarted != nil {
			callbacks.TurnStarted("turn-test")
		}
		state, err := runnerStore.State(ctx, req.AppID)
		if err != nil {
			return symphonyRunResult{}, err
		}
		var found bool
		for _, task := range state.Tasks {
			if task.ID == req.Task.ID && task.StatusKey == "in_progress" {
				found = true
			}
		}
		if !found {
			return symphonyRunResult{}, fmt.Errorf("task status while running = %+v, want in_progress", state.Tasks)
		}
		if err := os.WriteFile(filepath.Join(req.AppWorkspace, "prepared.txt"), []byte(req.Task.Identifier), 0o644); err != nil {
			return symphonyRunResult{}, err
		}
		return symphonyRunResult{ThreadID: "thread-test", TurnID: "turn-test", Summary: "prepared"}, nil
	}

	repoRoot := t.TempDir()
	appRoot := filepath.Join(repoRoot, "testdata", "apps", "basic")
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "go.mod"), []byte("module example.com/basic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, ".scenery.json"), []byte(`{"name":"demo","id":"demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "WORKFLOW.md"), []byte("---\nagent:\n  max_concurrent_agents: 1\n  max_attempts: 3\n  max_turns: 20\n  unknown_future_key: ignored\n---\nTicket {{ issue.identifier }} in {{ workspace.path }}"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "scenery@example.test")
	runGit(t, repoRoot, "config", "user.name", "Scenery Test")
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "commit", "-m", "initial")

	store := newSymphonyStore(t)
	runnerStore = store
	task, err := store.CreateTask(context.Background(), "demo", symphony.TaskInput{Title: "Prepare me", StatusKey: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.CreateTask(context.Background(), "demo", symphony.TaskInput{Title: "Wait for slot", StatusKey: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateWorkflow(context.Background(), "demo", symphony.WorkflowInput{Mode: "auto", MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	server := &dashboardServer{symphonyRoot: t.TempDir(), symphonyHooks: hooks}
	_, err = server.runSymphonyAutoForApp(context.Background(), store, devdash.AppStatus{
		AppID:     "demo--session",
		BaseAppID: "demo",
		SessionID: "session-1",
		AppRoot:   appRoot,
		Running:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.State(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tasks) != 2 {
		t.Fatalf("state = %+v", state)
	}
	var completed symphony.Task
	var waiting symphony.Task
	for _, item := range state.Tasks {
		if item.ID == task.ID {
			completed = item
		}
		if item.ID == queued.ID {
			waiting = item
		}
	}
	if completed.LatestRun == nil || completed.StatusKey != "human_review" {
		t.Fatalf("completed task = %+v", completed)
	}
	if waiting.StatusKey != "todo" || waiting.LatestRun != nil {
		t.Fatalf("queued task = %+v, want untouched todo due workflow concurrency", waiting)
	}
	run := completed.LatestRun
	if run.Status != "succeeded" || run.ThreadID != "thread-test" || run.TurnID != "turn-test" || run.WorkspacePath == "" {
		t.Fatalf("run = %+v", run)
	}
	if !strings.Contains(run.WorkspacePath, filepath.Join("workspaces", "demo", task.Identifier)+string(filepath.Separator)+"run-") {
		t.Fatalf("workspace path = %q, want per-run task workspace", run.WorkspacePath)
	}
	if !strings.Contains(run.DiffStat, "prepared.txt") {
		t.Fatalf("run diff stat = %q, want prepared.txt", run.DiffStat)
	}
	detailServer := newSymphonyDashboardTestServer(t, store)
	detailServer.symphonyRoot = server.symphonyRoot
	detail, err := dispatchSymphonyTestRPC[symphonyRunDetail](context.Background(), detailServer, "symphony/run/detail", map[string]any{
		"app_id": "session-a",
		"run_id": run.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.ID != run.ID || !strings.Contains(detail.Run.DiffStat, "prepared.txt") || len(detail.Events) == 0 {
		t.Fatalf("detail = %+v", detail)
	}
	if data, err := os.ReadFile(filepath.Join(run.WorkspacePath, "prepared.txt")); err != nil || string(data) != task.Identifier {
		t.Fatalf("prepared workspace file: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "prepared.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("live app root was modified or stat failed: %v", err)
	}
}

func TestDashboardSymphonyAutoRunnerRecoversExpiredRun(t *testing.T) {
	t.Parallel()

	server := &dashboardServer{
		symphonyRoot: t.TempDir(),
		symphonyHooks: symphonyRunnerHooks{
			startAsync: func(fn func()) { fn() },
			runCodexAgent: func(ctx context.Context, req symphonyRunRequest, callbacks symphonyRunCallbacks) (symphonyRunResult, error) {
				return symphonyRunResult{Summary: "recovered"}, nil
			},
		},
	}

	repoRoot, appRoot := newSymphonyGitFixture(t, "---\nagent:\n  max_concurrent_agents: 1\n  max_attempts: 3\n---\nRecover {{ issue.identifier }}")
	store := newSymphonyStore(t)
	task, err := store.CreateTask(context.Background(), "demo", symphony.TaskInput{Title: "Recover", StatusKey: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	oldRun, err := store.StartRunWithRepo(context.Background(), "demo", task.ID, filepath.Join(repoRoot, "app"), "old-session", repoRoot, filepath.Join(server.symphonyCacheRoot(), "workspaces", "demo", task.Identifier, "repo"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MoveTask(context.Background(), "demo", task.ID, "in_progress", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RenewRunLease(context.Background(), "demo", oldRun.ID, -time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateWorkflow(context.Background(), "demo", symphony.WorkflowInput{Mode: "auto", MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.runSymphonyAutoForApp(context.Background(), store, devdash.AppStatus{
		AppID:     "demo--session",
		BaseAppID: "demo",
		SessionID: "session-1",
		AppRoot:   appRoot,
		Running:   true,
	}); err != nil {
		t.Fatal(err)
	}
	stalled, err := store.Run(context.Background(), "demo", oldRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.State(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if stalled.Status != "stalled" || len(state.Tasks) != 1 || state.Tasks[0].StatusKey != "human_review" || state.Tasks[0].LatestRun == nil || state.Tasks[0].LatestRun.Attempt != 2 {
		t.Fatalf("stalled=%+v state=%+v", stalled, state)
	}
}

func TestDashboardSymphonyAutoRunnerTimesOutRun(t *testing.T) {
	t.Parallel()

	server := &dashboardServer{
		symphonyRoot: t.TempDir(),
		symphonyHooks: symphonyRunnerHooks{
			startAsync: func(fn func()) { fn() },
			runCodexAgent: func(ctx context.Context, req symphonyRunRequest, callbacks symphonyRunCallbacks) (symphonyRunResult, error) {
				return symphonyRunResult{Summary: "timed out"}, context.DeadlineExceeded
			},
		},
	}

	_, appRoot := newSymphonyGitFixture(t, "---\nagent:\n  turn_timeout_ms: 1000\n  stall_timeout_ms: 1000\n---\nTimeout")
	store := newSymphonyStore(t)
	if _, err := store.CreateTask(context.Background(), "demo", symphony.TaskInput{Title: "Timeout", StatusKey: "todo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateWorkflow(context.Background(), "demo", symphony.WorkflowInput{Mode: "auto", MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.runSymphonyAutoForApp(context.Background(), store, devdash.AppStatus{AppID: "demo", BaseAppID: "demo", SessionID: "session-1", AppRoot: appRoot, Running: true}); err != nil {
		t.Fatal(err)
	}
	state, err := store.State(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if state.Tasks[0].StatusKey != "rework" || state.Tasks[0].LatestRun == nil || state.Tasks[0].LatestRun.Status != "timed_out" {
		t.Fatalf("state = %+v", state)
	}
}

func TestDashboardSymphonyAutoRunnerHonorsMaxAttempts(t *testing.T) {
	t.Parallel()

	server := &dashboardServer{
		symphonyRoot: t.TempDir(),
		symphonyHooks: symphonyRunnerHooks{
			runCodexAgent: func(context.Context, symphonyRunRequest, symphonyRunCallbacks) (symphonyRunResult, error) {
				t.Fatal("runner should not reclaim an exhausted task")
				return symphonyRunResult{}, nil
			},
		},
	}

	appRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(appRoot, "WORKFLOW.md"), []byte("---\nagent:\n  max_attempts: 1\n  max_turns: 20\n---\nNo retry"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newSymphonyStore(t)
	task, err := store.CreateTask(context.Background(), "demo", symphony.TaskInput{Title: "No retry", StatusKey: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(context.Background(), "demo", task.ID, "", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteRun(context.Background(), "demo", run.ID, "failed", "", "failed once"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateWorkflow(context.Background(), "demo", symphony.WorkflowInput{Mode: "auto", MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.runSymphonyAutoForApp(context.Background(), store, devdash.AppStatus{AppID: "demo", BaseAppID: "demo", AppRoot: appRoot, Running: true}); err != nil {
		t.Fatal(err)
	}
	state, err := store.State(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if state.Tasks[0].LatestRun == nil || state.Tasks[0].LatestRun.Attempt != 1 || state.Tasks[0].LatestRun.Status != "failed" {
		t.Fatalf("state = %+v", state)
	}
}

func TestCompleteSymphonyRunDoesNotOverrideMovedTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newSymphonyStore(t)
	task, err := store.CreateTask(ctx, "demo", symphony.TaskInput{Title: "Do not override", StatusKey: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(ctx, "demo", task.ID, "", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MoveTask(ctx, "demo", task.ID, "in_progress", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveTask(ctx, "demo", task.ID, "done", 0); err != nil {
		t.Fatal(err)
	}
	completeSymphonyRun(store, symphonyRunRequest{AppID: "demo", Task: task, Run: run}, "succeeded", "done", "", "human_review")
	state, err := store.State(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tasks) != 1 {
		t.Fatalf("state = %+v", state)
	}
	if state.Tasks[0].StatusKey != "done" {
		t.Fatalf("task status = %q, want done", state.Tasks[0].StatusKey)
	}
	if state.Tasks[0].LatestRun == nil || state.Tasks[0].LatestRun.Status != "succeeded" {
		t.Fatalf("latest run = %+v", state.Tasks[0].LatestRun)
	}
}

func TestCompleteSymphonyRunSkipsSupersededCompletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newSymphonyStore(t)
	task, err := store.CreateTask(ctx, "demo", symphony.TaskInput{Title: "Superseded", StatusKey: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(ctx, "demo", task.ID, "", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MoveTask(ctx, "demo", task.ID, "in_progress", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RenewRunLease(ctx, "demo", run.ID, -time.Second); err != nil {
		t.Fatal(err)
	}
	if marked, err := store.MarkExpiredRunsStalled(ctx, "demo"); err != nil || marked != 1 {
		t.Fatalf("marked = %d, err = %v", marked, err)
	}
	completeSymphonyRun(store, symphonyRunRequest{AppID: "demo", Task: task, Run: run}, "succeeded", "late finish", "", "human_review")
	got, err := store.Run(ctx, "demo", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "stalled" {
		t.Fatalf("run status = %q, want stalled to win over late completion", got.Status)
	}
	state, err := store.State(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if state.Tasks[0].StatusKey != "todo" {
		t.Fatalf("task status = %q, want recovered todo untouched by late completion", state.Tasks[0].StatusKey)
	}
}

func TestDashboardSymphonyAutoRunnerUsesFreshWorkspacePerRun(t *testing.T) {
	t.Parallel()

	var workspaceMu sync.Mutex
	var workspaces []string
	server := &dashboardServer{
		symphonyRoot: t.TempDir(),
		symphonyHooks: symphonyRunnerHooks{
			startAsync: func(fn func()) { fn() },
			runCodexAgent: func(ctx context.Context, req symphonyRunRequest, callbacks symphonyRunCallbacks) (symphonyRunResult, error) {
				workspaceMu.Lock()
				workspaces = append(workspaces, req.RepoWorkspace)
				workspaceMu.Unlock()
				return symphonyRunResult{}, fmt.Errorf("fail to trigger retry")
			},
		},
	}

	_, appRoot := newSymphonyGitFixture(t, "---\nagent:\n  max_attempts: 3\n---\nRetry {{ issue.identifier }}")
	store := newSymphonyStore(t)
	task, err := store.CreateTask(context.Background(), "demo", symphony.TaskInput{Title: "Retry", StatusKey: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateWorkflow(context.Background(), "demo", symphony.WorkflowInput{Mode: "auto", MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	status := devdash.AppStatus{AppID: "demo", BaseAppID: "demo", SessionID: "session-1", AppRoot: appRoot, Running: true}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := server.runSymphonyAutoForApp(context.Background(), store, status); err != nil {
			t.Fatal(err)
		}
		if err := store.MoveTask(context.Background(), "demo", task.ID, "todo", 0); err != nil {
			t.Fatal(err)
		}
	}
	if len(workspaces) != 2 || workspaces[0] == workspaces[1] {
		t.Fatalf("workspaces = %v, want two distinct per-run worktrees", workspaces)
	}
}

func TestDashboardSymphonyAutoRunnerRequiresWorkflow(t *testing.T) {
	t.Parallel()

	store := newSymphonyStore(t)
	task, err := store.CreateTask(context.Background(), "demo", symphony.TaskInput{Title: "Needs workflow", StatusKey: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateWorkflow(context.Background(), "demo", symphony.WorkflowInput{Mode: "auto", MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	_, err = (&dashboardServer{symphonyRoot: t.TempDir()}).runSymphonyAutoForApp(context.Background(), store, devdash.AppStatus{
		AppID:     "demo--session",
		BaseAppID: "demo",
		SessionID: "session-1",
		AppRoot:   t.TempDir(),
		Running:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "WORKFLOW.md") {
		t.Fatalf("expected missing workflow error, got %v", err)
	}
	state, err := store.State(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tasks) != 1 || state.Tasks[0].ID != task.ID || state.Tasks[0].StatusKey != "todo" || state.Tasks[0].LatestRun != nil {
		t.Fatalf("task should stay unclaimed: %+v", state.Tasks)
	}
}

// TestSymphonyRunnerPacing proves the tick loop's pacing contract: failing
// ticks back off exponentially (bounded log volume during a store outage),
// boards with no auto workflow idle instead of hammering Postgres every two
// seconds, and healthy auto boards keep the fast tick. Shutdown cancellation
// is not a failure.
func TestSymphonyRunnerPacing(t *testing.T) {
	t.Parallel()

	backoff := symphonyRunnerInterval
	var interval time.Duration
	tickErr := errors.New("store unavailable")
	seen := []time.Duration{}
	for range 12 {
		interval, backoff = nextSymphonyRunnerInterval(false, tickErr, backoff)
		seen = append(seen, interval)
	}
	if seen[0] != 2*symphonyRunnerInterval || seen[1] != 4*symphonyRunnerInterval {
		t.Fatalf("failure backoff = %v, want doubling from %v", seen[:2], symphonyRunnerInterval)
	}
	if seen[len(seen)-1] != symphonyRunnerBackoffMax {
		t.Fatalf("failure backoff cap = %v, want %v", seen[len(seen)-1], symphonyRunnerBackoffMax)
	}

	interval, backoff = nextSymphonyRunnerInterval(false, nil, backoff)
	if interval != symphonyRunnerIdleInterval || backoff != symphonyRunnerInterval {
		t.Fatalf("idle interval = %v backoff = %v, want %v and reset", interval, backoff, symphonyRunnerIdleInterval)
	}
	interval, backoff = nextSymphonyRunnerInterval(true, nil, backoff)
	if interval != symphonyRunnerInterval || backoff != symphonyRunnerInterval {
		t.Fatalf("active interval = %v, want %v", interval, symphonyRunnerInterval)
	}
	interval, _ = nextSymphonyRunnerInterval(true, context.Canceled, symphonyRunnerInterval)
	if interval != symphonyRunnerInterval {
		t.Fatalf("canceled tick interval = %v, want %v (shutdown is not a failure)", interval, symphonyRunnerInterval)
	}
}

// TestRunSymphonyAutoForAppReportsAutoMode proves the idle gate's input: the
// per-app runner reports whether the workflow is in auto mode, including on
// error paths, so a non-auto board can idle the tick loop.
func TestRunSymphonyAutoForAppReportsAutoMode(t *testing.T) {
	t.Parallel()

	store := newSymphonyStore(t)
	server := &dashboardServer{symphonyRoot: t.TempDir()}
	status := devdash.AppStatus{
		AppID:     "demo--session",
		BaseAppID: "demo",
		SessionID: "session-1",
		AppRoot:   t.TempDir(),
		Running:   true,
	}
	auto, err := server.runSymphonyAutoForApp(context.Background(), store, status)
	if err != nil || auto {
		t.Fatalf("manual workflow auto = %v err = %v, want false, nil", auto, err)
	}
	if _, err := store.UpdateWorkflow(context.Background(), "demo", symphony.WorkflowInput{Mode: "auto", MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	// Auto mode with a missing WORKFLOW.md errors but still reports auto,
	// so the failure is retried at the failure backoff, not the idle pace.
	auto, err = server.runSymphonyAutoForApp(context.Background(), store, status)
	if err == nil || !auto {
		t.Fatalf("auto workflow auto = %v err = %v, want true with error", auto, err)
	}
}

func TestPrepareSymphonyWorkspaceResetsExistingWorktree(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())

	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "tracked.txt"), []byte("clean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "scenery@example.test")
	runGit(t, repoRoot, "config", "user.name", "Scenery Test")
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "commit", "-m", "initial")
	repoWorkspace := filepath.Join(symphonyCacheRoot(), "workspaces", "demo", "SYM-1", "repo")
	reset, err := prepareSymphonyWorkspace(context.Background(), symphonyCacheRoot(), repoRoot, repoWorkspace, repoWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if reset {
		t.Fatal("new worktree should not report reset")
	}
	if err := os.WriteFile(filepath.Join(repoWorkspace, "tracked.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoWorkspace, "untracked.txt"), []byte("remove me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reset, err = prepareSymphonyWorkspace(context.Background(), symphonyCacheRoot(), repoRoot, repoWorkspace, repoWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if !reset {
		t.Fatal("existing worktree should report reset")
	}
	data, err := os.ReadFile(filepath.Join(repoWorkspace, "tracked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "clean\n" {
		t.Fatalf("tracked file = %q", data)
	}
	if _, err := os.Stat(filepath.Join(repoWorkspace, "untracked.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("untracked file survived reset: %v", err)
	}
}

func TestCleanupSymphonyRunWorkspaceRemovesWorktree(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())

	repoRoot, appRoot := newSymphonyGitFixture(t, "manual")
	repoWorkspace := filepath.Join(symphonyCacheRoot(), "workspaces", "demo", "SYM-1", "repo")
	if _, err := prepareSymphonyWorkspace(context.Background(), symphonyCacheRoot(), repoRoot, repoWorkspace, repoWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := cleanupSymphonyRunWorkspace(context.Background(), symphonyCacheRoot(), symphony.Run{RepoRoot: repoRoot, RepoWorkspace: repoWorkspace, WorkspacePath: appRoot}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repoWorkspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace still exists: %v", err)
	}
	out := string(runGitOutput(t, repoRoot, "worktree", "list", "--porcelain"))
	if strings.Contains(out, repoWorkspace) {
		t.Fatalf("worktree registration survived:\n%s", out)
	}
}

func newSymphonyDashboardTestServer(t *testing.T, symphonyStores ...*symphony.Store) *dashboardServer {
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
	server := newDashboardServerWithController(&agentDashboardController{store: store}, t.TempDir(), "127.0.0.1:0", "", nil)
	if len(symphonyStores) > 0 {
		server.symphonyStore = symphonyStores[0]
	} else {
		server.symphonyStore = newSymphonyStore(t)
	}
	server.symphonyRoot = t.TempDir()
	return server
}

func newSymphonyStore(t *testing.T) *symphony.Store {
	t.Helper()
	testURL, cleanup := createSymphonyDashboardTestDatabase(t)
	t.Cleanup(cleanup)
	store, err := symphony.Open(context.Background(), testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createSymphonyDashboardTestDatabase(t *testing.T) (string, func()) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("SCENERY_TEST_DATABASE_URL"))
	if raw == "" {
		t.Skip("SCENERY_TEST_DATABASE_URL is not set; skipping live Postgres symphony dashboard test")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse SCENERY_TEST_DATABASE_URL: %v", err)
	}
	adminURL := *u
	adminURL.Path = "/postgres"
	admin, err := postgresdb.Open(context.Background(), adminURL.String())
	if err != nil {
		t.Skipf("SCENERY_TEST_DATABASE_URL is not reachable for live Postgres tests: %v", err)
	}
	name := "scenery_symphony_dashboard_test_" + harnessRandomLabel()
	if _, err := admin.ExecContext(context.Background(), `CREATE DATABASE `+name); err != nil {
		_ = admin.Close()
		t.Skipf("SCENERY_TEST_DATABASE_URL cannot create per-test database: %v", err)
	}
	u.Path = "/" + name
	return u.String(), func() {
		db, _ := sql.Open(postgresdb.DriverName, adminURL.String())
		if db != nil {
			_, _ = db.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
			_, _ = db.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name)
			_ = db.Close()
		}
		_ = admin.Close()
	}
}

func newSymphonyGitFixture(t *testing.T, workflow string) (string, string) {
	t.Helper()
	repoRoot := t.TempDir()
	appRoot := filepath.Join(repoRoot, "testdata", "apps", "basic")
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, ".scenery.json"), []byte(`{"name":"demo","id":"demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "go.mod"), []byte("module example.com/basic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "WORKFLOW.md"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "scenery@example.test")
	runGit(t, repoRoot, "config", "user.name", "Scenery Test")
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "commit", "-m", "initial")
	return repoRoot, appRoot
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return output
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
