package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/symphony"
)

var (
	symphonyRunnerInterval = 2 * time.Second
	// symphonyRunnerIdleInterval paces the runner when no workflow is in
	// auto mode, so an unused Symphony board does not hit Postgres every
	// two seconds. `scenery symphony auto --on` is picked up within one
	// idle interval.
	symphonyRunnerIdleInterval = 15 * time.Second
	// symphonyRunnerBackoffMax caps the exponential backoff applied to
	// failing ticks (typically an unreachable symphony Postgres store), so
	// a persistent outage logs a bounded warning stream instead of one
	// line every tick.
	symphonyRunnerBackoffMax = 5 * time.Minute
)

const maxSymphonyArtifactBytes = 120 * 1024

type symphonyRunRequest struct {
	AppID         string
	AppRoot       string
	RepoRoot      string
	RepoWorkspace string
	AppWorkspace  string
	Task          symphony.Task
	Run           symphony.Run
	Prompt        string
	MaxTurns      int
	TurnTimeout   time.Duration
	StallTimeout  time.Duration
}

type symphonyRunResult struct {
	ThreadID string
	TurnID   string
	Summary  string
}

type symphonyRunCallbacks struct {
	ThreadStarted func(processID int, threadID string)
	TurnStarted   func(turnID string)
	Event         func(eventType string, payload any)
}

type symphonyRunnerHooks struct {
	runCodexAgent func(context.Context, symphonyRunRequest, symphonyRunCallbacks) (symphonyRunResult, error)
	startAsync    func(func())
}

func (h symphonyRunnerHooks) withDefaults() symphonyRunnerHooks {
	if h.runCodexAgent == nil {
		h.runCodexAgent = runSymphonyCodexAppServer
	}
	if h.startAsync == nil {
		h.startAsync = func(fn func()) { go fn() }
	}
	return h
}

func (s *dashboardServer) runnerHooks() symphonyRunnerHooks {
	if s == nil {
		return (symphonyRunnerHooks{}).withDefaults()
	}
	return s.symphonyHooks.withDefaults()
}

func (s *dashboardServer) startSymphonyRunner(ctx context.Context) {
	go func() {
		if err := s.cleanupSymphonyTerminalWorkspaces(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("symphony workspace cleanup failed", "err", err)
		}
		failureBackoff := symphonyRunnerInterval
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			auto, err := s.runSymphonyAutoOnce(ctx)
			var interval time.Duration
			interval, failureBackoff = nextSymphonyRunnerInterval(auto, err, failureBackoff)
			if err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("symphony runner tick failed", "err", err, "retry_in", interval)
			}
			timer.Reset(interval)
		}
	}()
}

// nextSymphonyRunnerInterval paces the runner loop: failing ticks back off
// exponentially up to symphonyRunnerBackoffMax, boards with no auto workflow
// idle at symphonyRunnerIdleInterval, and active auto boards keep the fast
// tick. A canceled context is shutdown, not a failure.
func nextSymphonyRunnerInterval(auto bool, err error, failureBackoff time.Duration) (time.Duration, time.Duration) {
	if err != nil && !errors.Is(err, context.Canceled) {
		failureBackoff = min(failureBackoff*2, symphonyRunnerBackoffMax)
		return failureBackoff, failureBackoff
	}
	if !auto {
		return symphonyRunnerIdleInterval, symphonyRunnerInterval
	}
	return symphonyRunnerInterval, symphonyRunnerInterval
}

// runSymphonyAutoOnce runs one runner tick. It reports whether any running
// app's workflow is in auto mode, so the caller can idle the tick loop when
// Symphony is not in use; per-app failures are joined into one tick error so
// the caller's backoff bounds the log volume.
func (s *dashboardServer) runSymphonyAutoOnce(ctx context.Context) (bool, error) {
	apps, err := s.dashboardListApps(ctx)
	if err != nil {
		return false, err
	}
	store, err := s.dashboardSymphonyStore(ctx)
	if err != nil {
		return false, err
	}
	anyAuto := false
	var errs []error
	for _, app := range apps {
		if boolFromMap(app, "offline") {
			continue
		}
		requested := stringFromMap(app, "id")
		if requested == "" {
			continue
		}
		status, err := s.dashboardStatusFor(ctx, requested)
		if err != nil || !status.Running {
			continue
		}
		auto, err := s.runSymphonyAutoForApp(ctx, store, status)
		if auto {
			anyAuto = true
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("app %s: %w", requested, err))
		}
	}
	return anyAuto, errors.Join(errs...)
}

func (s *dashboardServer) cleanupSymphonyTerminalWorkspaces(ctx context.Context) error {
	apps, err := s.dashboardListApps(ctx)
	if err != nil {
		return err
	}
	store, err := s.dashboardSymphonyStore(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, app := range apps {
		requested := stringFromMap(app, "id")
		if requested == "" {
			continue
		}
		status, err := s.dashboardStatusFor(ctx, requested)
		if err != nil {
			continue
		}
		appID := dashboardStoreAppID(status)
		if appID == "" || seen[appID] {
			continue
		}
		seen[appID] = true
		runs, err := store.TerminalWorkspaces(ctx, appID)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if err := cleanupSymphonyRunWorkspace(ctx, s.symphonyCacheRoot(), run); err != nil {
				slog.Warn("symphony workspace cleanup failed", "app", appID, "run", run.ID, "err", err)
			}
		}
	}
	return nil
}

func cleanupSymphonyRunWorkspace(ctx context.Context, cacheRoot string, run symphony.Run) error {
	repoWorkspace := strings.TrimSpace(run.RepoWorkspace)
	if repoWorkspace == "" {
		return nil
	}
	if !symphonyWorkspacePathAllowedInRoot(cacheRoot, repoWorkspace) {
		return fmt.Errorf("refusing to remove workspace outside Symphony cache: %s", repoWorkspace)
	}
	if _, err := os.Stat(repoWorkspace); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	repoRoot := strings.TrimSpace(run.RepoRoot)
	if repoRoot != "" {
		cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "remove", "--force", repoWorkspace)
		if output, err := cmd.CombinedOutput(); err != nil {
			if _, statErr := os.Stat(repoWorkspace); errors.Is(statErr, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("remove worktree: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	return os.RemoveAll(repoWorkspace)
}

// runSymphonyAutoForApp reports whether this app's workflow is in auto mode
// alongside any tick error, so the runner can idle when nothing is auto.
func (s *dashboardServer) runSymphonyAutoForApp(ctx context.Context, store *symphony.Store, status devdash.AppStatus) (bool, error) {
	appID := dashboardStoreAppID(status)
	if appID == "" || status.AppRoot == "" {
		return false, nil
	}
	if _, err := store.MarkExpiredRunsStalled(ctx, appID); err != nil {
		return false, err
	}
	workflow, err := store.Workflow(ctx, appID)
	if err != nil {
		return false, err
	}
	if workflow.Mode != "auto" {
		return false, nil
	}
	runtimeWorkflow, err := symphony.LoadWorkflowRuntime(status.AppRoot, workflow)
	if err != nil {
		return true, err
	}
	maxConcurrency := firstPositive(runtimeWorkflow.MaxConcurrency, workflow.MaxConcurrency, 1)
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	active, err := store.ActiveRunCount(ctx, appID)
	if err != nil {
		return true, err
	}
	available := maxConcurrency - active
	if available <= 0 {
		return true, nil
	}
	tasks, err := store.RunnableTasksWithMaxAttempts(ctx, appID, []string{"todo"}, available, runtimeWorkflow.MaxAttempts)
	if err != nil {
		return true, err
	}
	var errs []error
	for _, task := range tasks {
		req, err := s.startSymphonyRunRecord(ctx, store, appID, status, runtimeWorkflow, task)
		if err != nil {
			errs = append(errs, fmt.Errorf("claim task %s: %w", task.Identifier, err))
			continue
		}
		s.runnerHooks().startAsync(func() { s.executeSymphonyRun(ctx, store, req) })
	}
	return true, errors.Join(errs...)
}

func (s *dashboardServer) startSymphonyRunRecord(ctx context.Context, store *symphony.Store, appID string, status devdash.AppStatus, workflow symphony.WorkflowRuntime, task symphony.Task) (symphonyRunRequest, error) {
	repoRoot, relAppRoot, err := gitRepoRootForApp(ctx, status.AppRoot)
	if err != nil {
		run, runErr := store.StartRun(ctx, appID, task.ID, "", status.SessionID)
		if runErr == nil {
			_, _ = store.CompleteRun(ctx, appID, run.ID, "failed", "", err.Error())
			_ = store.MoveTask(ctx, appID, task.ID, "rework", 0)
		}
		return symphonyRunRequest{}, err
	}
	// Each run gets its own worktree. Sharing one worktree per task lets a
	// lease-expired-but-alive previous runner race the retry inside the same
	// checkout, so the retry must never reuse a prior run's directory.
	repoWorkspace := filepath.Join(s.symphonyCacheRoot(), "workspaces", safePathSegment(appID), safePathSegment(task.Identifier), symphonyRunWorkspaceSegment(), "repo")
	appWorkspace := filepath.Join(repoWorkspace, relAppRoot)
	run, err := store.StartRunWithRepo(ctx, appID, task.ID, appWorkspace, status.SessionID, repoRoot, repoWorkspace)
	if err != nil {
		return symphonyRunRequest{}, err
	}
	req := symphonyRunRequest{
		AppID:         appID,
		AppRoot:       status.AppRoot,
		RepoRoot:      repoRoot,
		RepoWorkspace: repoWorkspace,
		AppWorkspace:  appWorkspace,
		Task:          task,
		Run:           run,
		MaxTurns:      workflow.MaxTurns,
		TurnTimeout:   workflow.TurnTimeout,
		StallTimeout:  workflow.StallTimeout,
	}
	prompt, err := renderSymphonyRunPrompt(workflow, req)
	if err != nil {
		completeSymphonyRun(store, req, "failed", "", err.Error(), "rework")
		return symphonyRunRequest{}, err
	}
	req.Prompt = prompt
	if err := store.MoveTask(ctx, appID, task.ID, "in_progress", 0); err != nil {
		_, _ = store.CompleteRun(ctx, appID, run.ID, "failed", "", err.Error())
		return symphonyRunRequest{}, err
	}
	return req, nil
}

func (s *dashboardServer) executeSymphonyRun(ctx context.Context, store *symphony.Store, req symphonyRunRequest) {
	runCtx := ctx
	var cancel context.CancelFunc
	if req.TurnTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.TurnTimeout)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()
	stopHeartbeat := startSymphonyRunHeartbeat(runCtx, store, req)
	defer stopHeartbeat()

	reset, err := prepareSymphonyWorkspace(runCtx, s.symphonyCacheRoot(), req.RepoRoot, req.RepoWorkspace, req.AppWorkspace)
	if err != nil {
		completeSymphonyRun(store, req, "failed", "", err.Error(), "rework")
		return
	}
	if reset {
		_ = store.RecordRunEvent(context.Background(), req.AppID, req.Run.ID, "workspace.reset", map[string]any{"repo_workspace_path": req.RepoWorkspace})
	}
	callbacks := symphonyRunCallbacks{
		ThreadStarted: func(processID int, threadID string) {
			_, _ = store.MarkRunRunning(context.Background(), req.AppID, req.Run.ID, processID, threadID)
		},
		TurnStarted: func(turnID string) {
			_, _ = store.MarkRunTurn(context.Background(), req.AppID, req.Run.ID, turnID)
		},
		Event: func(eventType string, payload any) {
			// Detailed event storage can be widened later; the lifecycle events already cover recovery.
		},
	}
	result, err := s.runnerHooks().runCodexAgent(runCtx, req, callbacks)
	if err != nil {
		status := "failed"
		if errors.Is(err, context.DeadlineExceeded) {
			status = "timed_out"
		} else if errors.Is(err, symphony.ErrRunStalled) {
			status = "stalled"
		}
		completeSymphonyRun(store, req, status, result.Summary, err.Error(), "rework")
		return
	}
	completeSymphonyRun(store, req, "succeeded", result.Summary, "", "human_review")
}

func startSymphonyRunHeartbeat(ctx context.Context, store *symphony.Store, req symphonyRunRequest) func() {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(symphonyRunnerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if _, err := store.RenewRunLease(context.Background(), req.AppID, req.Run.ID, symphony.DefaultRunLeaseDuration); err != nil {
					slog.Warn("symphony run heartbeat failed", "task", req.Task.Identifier, "run", req.Run.ID, "err", err)
				}
			}
		}
	}()
	return cancel
}

func completeSymphonyRun(store *symphony.Store, req symphonyRunRequest, status, summary, runError, nextStatus string) {
	if diffStat, diff, err := collectSymphonyRunArtifacts(context.Background(), req.AppWorkspace); err == nil {
		if diffStat != "" || diff != "" {
			if _, err := store.RecordRunArtifacts(context.Background(), req.AppID, req.Run.ID, diffStat, diff); err != nil {
				slog.Warn("symphony run artifact recording failed", "task", req.Task.Identifier, "run", req.Run.ID, "err", err)
			}
		}
	} else {
		slog.Warn("symphony run artifact collection failed", "task", req.Task.Identifier, "run", req.Run.ID, "err", err)
	}
	run, err := store.CompleteRun(context.Background(), req.AppID, req.Run.ID, status, summary, runError)
	if err != nil {
		slog.Warn("symphony run completion failed", "task", req.Task.Identifier, "run", req.Run.ID, "err", err)
		return
	}
	if run.Status != status {
		// Another owner finalized this run first (for example lease recovery
		// marked it stalled); the task now belongs to that outcome.
		slog.Warn("symphony run completion superseded", "task", req.Task.Identifier, "run", req.Run.ID, "recorded_status", run.Status, "attempted_status", status)
		return
	}
	current, err := store.Task(context.Background(), req.AppID, req.Task.ID)
	if err != nil {
		slog.Warn("symphony task lookup after run failed", "task", req.Task.Identifier, "status", nextStatus, "err", err)
		return
	}
	if current.StatusKey != "todo" && current.StatusKey != "in_progress" {
		return
	}
	if err := store.MoveTask(context.Background(), req.AppID, req.Task.ID, nextStatus, 0); err != nil {
		slog.Warn("symphony task move after run failed", "task", req.Task.Identifier, "status", nextStatus, "err", err)
	}
}

func gitRepoRootForApp(ctx context.Context, appRoot string) (string, string, error) {
	appRoot = strings.TrimSpace(appRoot)
	if appRoot == "" {
		return "", "", errors.New("app root is required")
	}
	canonicalAppRoot, err := filepath.EvalSymlinks(appRoot)
	if err == nil {
		appRoot = canonicalAppRoot
	}
	cmd := exec.CommandContext(ctx, "git", "-C", appRoot, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("resolve git root for %s: %w", appRoot, err)
	}
	repoRoot := strings.TrimSpace(string(out))
	canonicalRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err == nil {
		repoRoot = canonicalRepoRoot
	}
	rel, err := filepath.Rel(repoRoot, appRoot)
	if err != nil {
		return "", "", err
	}
	if rel == "." {
		rel = ""
	}
	return repoRoot, rel, nil
}

func prepareSymphonyWorkspace(ctx context.Context, cacheRoot, repoRoot, repoWorkspace, appWorkspace string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(repoWorkspace), 0o755); err != nil {
		return false, err
	}
	if _, err := os.Stat(repoWorkspace); errors.Is(err, os.ErrNotExist) {
		cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "add", "--detach", repoWorkspace, "HEAD")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return false, fmt.Errorf("create worktree: %w: %s", err, strings.TrimSpace(string(output)))
		}
	} else if err != nil {
		return false, err
	} else {
		if !symphonyWorkspacePathAllowedInRoot(cacheRoot, repoWorkspace) {
			return false, fmt.Errorf("workspace path %s is outside the Symphony cache", repoWorkspace)
		}
		if _, err := gitOutput(ctx, repoWorkspace, "reset", "--hard", "HEAD"); err != nil {
			return false, err
		}
		if _, err := gitOutput(ctx, repoWorkspace, "clean", "-fd"); err != nil {
			return false, err
		}
		if info, err := os.Stat(appWorkspace); err != nil || !info.IsDir() {
			if err != nil {
				return true, fmt.Errorf("app workspace %s: %w", appWorkspace, err)
			}
			return true, fmt.Errorf("app workspace %s is not a directory", appWorkspace)
		}
		return true, nil
	}
	if info, err := os.Stat(appWorkspace); err != nil || !info.IsDir() {
		if err != nil {
			return false, fmt.Errorf("app workspace %s: %w", appWorkspace, err)
		}
		return false, fmt.Errorf("app workspace %s is not a directory", appWorkspace)
	}
	return false, nil
}

func runSymphonyCodexAppServer(ctx context.Context, req symphonyRunRequest, callbacks symphonyRunCallbacks) (symphonyRunResult, error) {
	var resultMu sync.Mutex
	var result symphonyRunResult
	var agentSummary strings.Builder
	handler := func(method string, params json.RawMessage) {
		resultMu.Lock()
		defer resultMu.Unlock()
		switch method {
		case "turn/started":
			turnID := jsonString(params, "turn", "id")
			if turnID != "" {
				result.TurnID = turnID
				if callbacks.TurnStarted != nil {
					callbacks.TurnStarted(turnID)
				}
			}
		case "turn/completed":
			if turnID := jsonString(params, "turn", "id"); turnID != "" {
				result.TurnID = turnID
			}
		case "item/agentMessage/delta":
			if text := firstNonEmpty(jsonString(params, "delta"), jsonString(params, "text")); text != "" && agentSummary.Len() < maxSymphonyArtifactBytes {
				agentSummary.WriteString(text)
			}
			if callbacks.Event != nil {
				callbacks.Event(method, map[string]any{"params": json.RawMessage(params)})
			}
		}
	}
	snapshotResult := func(defaultSummary bool) symphonyRunResult {
		resultMu.Lock()
		defer resultMu.Unlock()
		out := result
		out.Summary = strings.TrimSpace(agentSummary.String())
		if defaultSummary && out.Summary == "" {
			out.Summary = "Codex app-server completed the Symphony task."
		}
		return out
	}
	client, err := symphony.NewCodexAppServerClient(ctx, handler)
	if err != nil {
		return symphonyRunResult{}, err
	}
	defer client.Close()
	if _, err := client.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "scenery-symphony", "version": sceneryVersion},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}); err != nil {
		return symphonyRunResult{}, err
	}
	threadResult, err := client.Call(ctx, "thread/start", map[string]any{
		"cwd":                   req.AppWorkspace,
		"runtimeWorkspaceRoots": []string{req.AppWorkspace},
		"sandbox":               "workspace-write",
		"approvalPolicy":        "never",
		"ephemeral":             true,
		"developerInstructions": "You are running inside a Scenery Symphony task workspace. Do not commit. Keep changes scoped to the requested app workspace and run focused validation when practical.",
	})
	if err != nil {
		return symphonyRunResult{}, err
	}
	threadID := jsonString(threadResult, "thread", "id")
	if threadID == "" {
		return symphonyRunResult{}, fmt.Errorf("codex app-server thread/start did not return a thread id")
	}
	if callbacks.ThreadStarted != nil {
		callbacks.ThreadStarted(client.PID(), threadID)
	}
	prompt := symphonyTaskPrompt(req)
	resultMu.Lock()
	result.ThreadID = threadID
	resultMu.Unlock()
	if _, err := client.Call(ctx, "turn/start", map[string]any{
		"threadId": threadID,
		"cwd":      req.AppWorkspace,
		"input": []map[string]string{{
			"type": "text",
			"text": prompt,
		}},
		"approvalPolicy":        "never",
		"runtimeWorkspaceRoots": []string{req.AppWorkspace},
	}); err != nil {
		return snapshotResult(false), err
	}
	if err := client.WaitForTurnCompleted(ctx, req.StallTimeout); err != nil {
		return snapshotResult(false), err
	}
	return snapshotResult(true), nil
}

func symphonyTaskPrompt(req symphonyRunRequest) string {
	if prompt := strings.TrimSpace(req.Prompt); prompt != "" {
		return prompt
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Implement this Scenery Symphony task in the current app workspace.\n\n")
	fmt.Fprintf(&b, "Task: %s %s\n", req.Task.Identifier, req.Task.Title)
	if req.Task.Description != "" {
		fmt.Fprintf(&b, "\nDescription:\n%s\n", req.Task.Description)
	}
	fmt.Fprintf(&b, "\nApp workspace: %s\n", req.AppWorkspace)
	fmt.Fprintf(&b, "\nRules:\n- Do not commit.\n- Keep edits inside this workspace.\n- Prefer the smallest correct change.\n- Run focused validation for the app if practical.\n- Finish by summarizing changed files and validation.\n")
	return b.String()
}

func renderSymphonyRunPrompt(workflow symphony.WorkflowRuntime, req symphonyRunRequest) (string, error) {
	template := strings.TrimSpace(workflow.PromptTemplate)
	if template == "" {
		return symphonyTaskPrompt(req), nil
	}
	values := map[string]string{
		"issue.id":          req.Task.ID,
		"issue.identifier":  req.Task.Identifier,
		"issue.title":       req.Task.Title,
		"issue.description": req.Task.Description,
		"issue.priority":    req.Task.Priority,
		"issue.state":       req.Task.StatusKey,
		"issue.branch_name": req.Task.BranchName,
		"issue.url":         req.Task.URL,
		"issue.labels":      strings.Join(req.Task.Labels, ", "),
		"workspace.path":    req.AppWorkspace,
		"attempt":           fmt.Sprintf("%d", req.Run.Attempt),
		"run.attempt":       fmt.Sprintf("%d", req.Run.Attempt),
	}
	var unknown []string
	rendered := symphonyTemplateVariablePattern.ReplaceAllStringFunc(template, func(match string) string {
		key := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}"))
		if value, ok := values[key]; ok {
			return value
		}
		unknown = append(unknown, key)
		return match
	})
	if len(unknown) > 0 {
		return "", fmt.Errorf("unsupported WORKFLOW.md template variables: %s", strings.Join(unknown, ", "))
	}
	return strings.TrimSpace(rendered), nil
}

func collectSymphonyRunArtifacts(ctx context.Context, appWorkspace string) (string, string, error) {
	appWorkspace = strings.TrimSpace(appWorkspace)
	if appWorkspace == "" {
		return "", "", nil
	}
	if info, err := os.Stat(appWorkspace); err != nil || !info.IsDir() {
		if err != nil {
			return "", "", err
		}
		return "", "", fmt.Errorf("%s is not a directory", appWorkspace)
	}
	status, err := gitOutput(ctx, appWorkspace, "status", "--short", "--", ".")
	if err != nil {
		return "", "", err
	}
	diff, err := gitOutput(ctx, appWorkspace, "diff", "--no-ext-diff", "--no-color", "--", ".")
	if err != nil {
		return "", "", err
	}
	cached, err := gitOutput(ctx, appWorkspace, "diff", "--cached", "--no-ext-diff", "--no-color", "--", ".")
	if err != nil {
		return "", "", err
	}
	if cached != "" {
		diff = strings.TrimSpace("Staged diff:\n" + cached + "\n\nWorktree diff:\n" + diff)
	}
	return trimArtifact(status), trimArtifact(diff), nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return trimArtifact(string(out)), nil
}

func trimArtifact(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxSymphonyArtifactBytes {
		return value
	}
	return value[:maxSymphonyArtifactBytes] + "\n\n[truncated]"
}

func jsonString(raw json.RawMessage, path ...string) string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	for _, key := range path {
		obj, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = obj[key]
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func symphonyRunWorkspaceSegment() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(buf[:])
}

var (
	symphonyTemplateVariablePattern = regexp.MustCompile(`{{\s*([^{}]+?)\s*}}`)
	unsafePathSegmentPattern        = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

func safePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	value = unsafePathSegmentPattern.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-.")
	if value == "" {
		return "unknown"
	}
	if len(value) > 80 {
		return value[:80]
	}
	return value
}

func stringFromMap(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func boolFromMap(values map[string]any, key string) bool {
	if value, ok := values[key].(bool); ok {
		return value
	}
	return false
}
