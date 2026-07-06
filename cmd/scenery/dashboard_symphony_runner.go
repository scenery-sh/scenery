package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/symphony"
)

var (
	symphonyRunnerInterval = 2 * time.Second
	errSymphonyRunStalled  = errors.New("codex app-server stalled")
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

type symphonyWorkflowRuntime struct {
	PromptTemplate string
	MaxConcurrency int
	MaxTurns       int
	MaxAttempts    int
	TurnTimeout    time.Duration
	StallTimeout   time.Duration
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
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if err := s.runSymphonyAutoOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
					slog.Warn("symphony runner tick failed", "err", err)
				}
				timer.Reset(symphonyRunnerInterval)
			}
		}
	}()
}

func (s *dashboardServer) runSymphonyAutoOnce(ctx context.Context) error {
	apps, err := s.dashboardListApps(ctx)
	if err != nil {
		return err
	}
	store, err := s.dashboardSymphonyStore(ctx)
	if err != nil {
		return err
	}
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
		if err := s.runSymphonyAutoForApp(ctx, store, status); err != nil {
			slog.Warn("symphony app runner failed", "app", requested, "err", err)
		}
	}
	return nil
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
			if err := cleanupSymphonyRunWorkspace(ctx, run); err != nil {
				slog.Warn("symphony workspace cleanup failed", "app", appID, "run", run.ID, "err", err)
			}
		}
	}
	return nil
}

func cleanupSymphonyRunWorkspace(ctx context.Context, run symphony.Run) error {
	repoWorkspace := strings.TrimSpace(run.RepoWorkspace)
	if repoWorkspace == "" {
		return nil
	}
	if !symphonyWorkspacePathAllowed(repoWorkspace) {
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

func (s *dashboardServer) runSymphonyAutoForApp(ctx context.Context, store *symphony.Store, status devdash.AppStatus) error {
	appID := dashboardStoreAppID(status)
	if appID == "" || status.AppRoot == "" {
		return nil
	}
	if _, err := store.MarkExpiredRunsStalled(ctx, appID); err != nil {
		return err
	}
	workflow, err := store.Workflow(ctx, appID)
	if err != nil {
		return err
	}
	if workflow.Mode != "auto" {
		return nil
	}
	runtimeWorkflow, err := loadSymphonyWorkflowRuntime(status.AppRoot, workflow)
	if err != nil {
		return err
	}
	maxConcurrency := firstPositive(runtimeWorkflow.MaxConcurrency, workflow.MaxConcurrency, 1)
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	active, err := store.ActiveRunCount(ctx, appID)
	if err != nil {
		return err
	}
	available := maxConcurrency - active
	if available <= 0 {
		return nil
	}
	tasks, err := store.RunnableTasksWithMaxAttempts(ctx, appID, []string{"todo"}, available, runtimeWorkflow.MaxAttempts)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		req, err := s.startSymphonyRunRecord(ctx, store, appID, status, runtimeWorkflow, task)
		if err != nil {
			slog.Warn("symphony run claim failed", "task", task.Identifier, "err", err)
			continue
		}
		s.runnerHooks().startAsync(func() { s.executeSymphonyRun(ctx, store, req) })
	}
	return nil
}

func (s *dashboardServer) startSymphonyRunRecord(ctx context.Context, store *symphony.Store, appID string, status devdash.AppStatus, workflow symphonyWorkflowRuntime, task symphony.Task) (symphonyRunRequest, error) {
	repoRoot, relAppRoot, err := gitRepoRootForApp(ctx, status.AppRoot)
	if err != nil {
		run, runErr := store.StartRun(ctx, appID, task.ID, "", status.SessionID)
		if runErr == nil {
			_, _ = store.CompleteRun(ctx, appID, run.ID, "failed", "", err.Error())
			_ = store.MoveTask(ctx, appID, task.ID, "rework", 0)
		}
		return symphonyRunRequest{}, err
	}
	repoWorkspace := filepath.Join(s.symphonyCacheRoot(), "workspaces", safePathSegment(appID), safePathSegment(task.Identifier), "repo")
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

	reset, err := prepareSymphonyWorkspace(runCtx, req.RepoRoot, req.RepoWorkspace, req.AppWorkspace)
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
		} else if errors.Is(err, errSymphonyRunStalled) {
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
	if _, err := store.CompleteRun(context.Background(), req.AppID, req.Run.ID, status, summary, runError); err != nil {
		slog.Warn("symphony run completion failed", "task", req.Task.Identifier, "run", req.Run.ID, "err", err)
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

func prepareSymphonyWorkspace(ctx context.Context, repoRoot, repoWorkspace, appWorkspace string) (bool, error) {
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
		if !symphonyWorkspacePathAllowed(repoWorkspace) {
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
	client, err := newCodexAppServerClient(ctx, handler)
	if err != nil {
		return symphonyRunResult{}, err
	}
	defer client.Close()
	if _, err := client.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "scenery-symphony", "version": sceneryVersion},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}); err != nil {
		return symphonyRunResult{}, err
	}
	threadResult, err := client.call(ctx, "thread/start", map[string]any{
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
		callbacks.ThreadStarted(client.pid(), threadID)
	}
	prompt := symphonyTaskPrompt(req)
	resultMu.Lock()
	result.ThreadID = threadID
	resultMu.Unlock()
	if _, err := client.call(ctx, "turn/start", map[string]any{
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
	if err := client.waitForTurnCompleted(ctx, req.StallTimeout); err != nil {
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

func renderSymphonyRunPrompt(workflow symphonyWorkflowRuntime, req symphonyRunRequest) (string, error) {
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
	rendered := regexp.MustCompile(`{{\s*([^{}]+?)\s*}}`).ReplaceAllStringFunc(template, func(match string) string {
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

func loadSymphonyWorkflowRuntime(appRoot string, workflow symphony.Workflow) (symphonyWorkflowRuntime, error) {
	out := symphonyWorkflowRuntime{
		MaxTurns:     20,
		MaxAttempts:  3,
		TurnTimeout:  time.Hour,
		StallTimeout: 5 * time.Minute,
	}
	if text := strings.TrimSpace(workflow.WorkflowMarkdown); text != "" {
		return parseSymphonyWorkflowRuntime(text, out)
	}
	data, err := os.ReadFile(filepath.Join(appRoot, "WORKFLOW.md"))
	if errors.Is(err, os.ErrNotExist) {
		return out, errors.New("missing WORKFLOW.md")
	}
	if err != nil {
		return out, err
	}
	return parseSymphonyWorkflowRuntime(string(data), out)
}

func parseSymphonyWorkflowRuntime(text string, out symphonyWorkflowRuntime) (symphonyWorkflowRuntime, error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "---") {
		out.PromptTemplate = text
		return out, nil
	}
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			if err := applySymphonyWorkflowConfig(lines[1:i], &out); err != nil {
				return out, err
			}
			out.PromptTemplate = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			return out, nil
		}
	}
	return out, errors.New("WORKFLOW.md front matter is not closed")
}

func applySymphonyWorkflowConfig(lines []string, out *symphonyWorkflowRuntime) error {
	section := ""
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(line, " ") && strings.HasSuffix(trimmed, ":") {
			section = strings.TrimSuffix(trimmed, ":")
			continue
		}
		if section != "agent" {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		switch key {
		case "max_concurrent_agents":
			n, err := parseSymphonyPositiveInt(value)
			if err != nil {
				return fmt.Errorf("invalid agent.%s: %w", key, err)
			}
			out.MaxConcurrency = n
		case "max_turns":
			n, err := parseSymphonyPositiveInt(value)
			if err != nil {
				return fmt.Errorf("invalid agent.%s: %w", key, err)
			}
			out.MaxTurns = n
		case "max_attempts":
			n, err := parseSymphonyPositiveInt(value)
			if err != nil {
				return fmt.Errorf("invalid agent.%s: %w", key, err)
			}
			out.MaxAttempts = n
		case "turn_timeout_ms":
			timeout, err := parseSymphonyPositiveDurationMillis(value)
			if err != nil {
				return fmt.Errorf("invalid agent.%s: %w", key, err)
			}
			out.TurnTimeout = timeout
		case "stall_timeout_ms":
			timeout, err := parseSymphonyPositiveDurationMillis(value)
			if err != nil {
				return fmt.Errorf("invalid agent.%s: %w", key, err)
			}
			out.StallTimeout = timeout
		}
	}
	return nil
}

func parseSymphonyPositiveInt(value string) (int, error) {
	value = strings.TrimSpace(strings.SplitN(value, "#", 2)[0])
	value = strings.Trim(value, `"'`)
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return n, nil
}

func parseSymphonyPositiveDurationMillis(value string) (time.Duration, error) {
	n, err := parseSymphonyPositiveInt(value)
	if err != nil {
		return 0, err
	}
	return time.Duration(n) * time.Millisecond, nil
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

type codexAppServerClient struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	pendingMu      sync.Mutex
	pending        map[int]chan codexRPCMessage
	nextID         int
	done           chan error
	turnDone       chan struct{}
	onNotification func(string, json.RawMessage)
	activityMu     sync.Mutex
	lastActivity   time.Time
}

type codexRPCMessage struct {
	ID     any             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newCodexAppServerClient(ctx context.Context, onNotification func(string, json.RawMessage)) (*codexAppServerClient, error) {
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	client := &codexAppServerClient{
		cmd:            cmd,
		stdin:          stdin,
		pending:        map[int]chan codexRPCMessage{},
		done:           make(chan error, 1),
		turnDone:       make(chan struct{}),
		onNotification: onNotification,
		lastActivity:   time.Now(),
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go io.Copy(io.Discard, stderr)
	go client.readLoop(stdout)
	go func() { client.done <- cmd.Wait() }()
	return client, nil
}

func (c *codexAppServerClient) pid() int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *codexAppServerClient) Close() error {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	_ = c.stdin.Close()
	if err := c.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = c.cmd.Process.Kill()
	}
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
	return nil
}

func (c *codexAppServerClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.pendingMu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan codexRPCMessage, 1)
	c.pending[id] = ch
	c.pendingMu.Unlock()

	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, err
	}
	select {
	case msg := <-ch:
		if msg.Error != nil {
			return nil, errors.New(msg.Error.Message)
		}
		return msg.Result, nil
	case err := <-c.done:
		return nil, fmt.Errorf("codex app-server exited: %w", err)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *codexAppServerClient) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var msg codexRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if id, ok := numericID(msg.ID); ok {
			c.pendingMu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.pendingMu.Unlock()
			if ch != nil {
				ch <- msg
				continue
			}
		}
		if msg.Method != "" {
			c.markActivity()
			if c.onNotification != nil {
				c.onNotification(msg.Method, msg.Params)
			}
			if msg.Method == "turn/completed" {
				select {
				case <-c.turnDone:
				default:
					close(c.turnDone)
				}
			}
		}
	}
}

func (c *codexAppServerClient) waitForTurnCompleted(ctx context.Context, stallTimeout time.Duration) error {
	if stallTimeout <= 0 {
		stallTimeout = 5 * time.Minute
	}
	tick := stallTimeout / 4
	if tick <= 0 || tick > time.Second {
		tick = time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	select {
	case <-c.turnDone:
		return nil
	default:
	}
	for {
		select {
		case <-c.turnDone:
			return nil
		case err := <-c.done:
			return fmt.Errorf("codex app-server exited before turn completed: %w", err)
		case <-ticker.C:
			if c.idleFor() >= stallTimeout {
				return errSymphonyRunStalled
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *codexAppServerClient) markActivity() {
	c.activityMu.Lock()
	c.lastActivity = time.Now()
	c.activityMu.Unlock()
}

func (c *codexAppServerClient) idleFor() time.Duration {
	c.activityMu.Lock()
	last := c.lastActivity
	c.activityMu.Unlock()
	if last.IsZero() {
		return 0
	}
	return time.Since(last)
}

func numericID(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
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

func safePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	re := regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	value = re.ReplaceAllString(value, "-")
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
