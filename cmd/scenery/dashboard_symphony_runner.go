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
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/symphony"
)

var (
	symphonyRunnerInterval = 2 * time.Second
	symphonyRunCodexAgent  = runSymphonyCodexAppServer
	startSymphonyRunAsync  = func(fn func()) { go fn() }
)

type symphonyRunRequest struct {
	AppID         string
	AppRoot       string
	RepoRoot      string
	RepoWorkspace string
	AppWorkspace  string
	Task          symphony.Task
	Run           symphony.Run
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

func (s *dashboardServer) startSymphonyRunner(ctx context.Context) {
	go func() {
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

func (s *dashboardServer) runSymphonyAutoForApp(ctx context.Context, store *symphony.Store, status devdash.AppStatus) error {
	appID := dashboardStoreAppID(status)
	if appID == "" || status.AppRoot == "" {
		return nil
	}
	workflow, err := store.Workflow(ctx, appID)
	if err != nil {
		return err
	}
	if workflow.Mode != "auto" {
		return nil
	}
	maxConcurrency := workflow.MaxConcurrency
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
	tasks, err := store.RunnableTasks(ctx, appID, []string{"todo"}, available)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		req, err := s.startSymphonyRunRecord(ctx, store, appID, status, task)
		if err != nil {
			slog.Warn("symphony run claim failed", "task", task.Identifier, "err", err)
			continue
		}
		startSymphonyRunAsync(func() { s.executeSymphonyRun(ctx, store, req) })
	}
	return nil
}

func (s *dashboardServer) startSymphonyRunRecord(ctx context.Context, store *symphony.Store, appID string, status devdash.AppStatus, task symphony.Task) (symphonyRunRequest, error) {
	repoRoot, relAppRoot, err := gitRepoRootForApp(ctx, status.AppRoot)
	if err != nil {
		run, runErr := store.StartRun(ctx, appID, task.ID, "", status.SessionID)
		if runErr == nil {
			_, _ = store.CompleteRun(ctx, appID, run.ID, "failed", "", err.Error())
		}
		return symphonyRunRequest{}, err
	}
	stamp := fmt.Sprintf("%s-%d", task.Identifier, time.Now().UnixNano())
	repoWorkspace := filepath.Join(symphonyCacheRoot(), "workspaces", safePathSegment(appID), safePathSegment(stamp), "repo")
	appWorkspace := filepath.Join(repoWorkspace, relAppRoot)
	run, err := store.StartRun(ctx, appID, task.ID, appWorkspace, status.SessionID)
	if err != nil {
		return symphonyRunRequest{}, err
	}
	return symphonyRunRequest{
		AppID:         appID,
		AppRoot:       status.AppRoot,
		RepoRoot:      repoRoot,
		RepoWorkspace: repoWorkspace,
		AppWorkspace:  appWorkspace,
		Task:          task,
		Run:           run,
	}, nil
}

func (s *dashboardServer) executeSymphonyRun(ctx context.Context, store *symphony.Store, req symphonyRunRequest) {
	if err := prepareSymphonyWorkspace(ctx, req.RepoRoot, req.RepoWorkspace, req.AppWorkspace); err != nil {
		_, _ = store.CompleteRun(context.Background(), req.AppID, req.Run.ID, "failed", "", err.Error())
		return
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
	result, err := symphonyRunCodexAgent(ctx, req, callbacks)
	if err != nil {
		_, _ = store.CompleteRun(context.Background(), req.AppID, req.Run.ID, "failed", result.Summary, err.Error())
		return
	}
	_, _ = store.CompleteRun(context.Background(), req.AppID, req.Run.ID, "succeeded", result.Summary, "")
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

func prepareSymphonyWorkspace(ctx context.Context, repoRoot, repoWorkspace, appWorkspace string) error {
	if err := os.MkdirAll(filepath.Dir(repoWorkspace), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(repoWorkspace); errors.Is(err, os.ErrNotExist) {
		cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "add", "--detach", repoWorkspace, "HEAD")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("create worktree: %w: %s", err, strings.TrimSpace(string(output)))
		}
	} else if err != nil {
		return err
	}
	if info, err := os.Stat(appWorkspace); err != nil || !info.IsDir() {
		if err != nil {
			return fmt.Errorf("app workspace %s: %w", appWorkspace, err)
		}
		return fmt.Errorf("app workspace %s is not a directory", appWorkspace)
	}
	return nil
}

func runSymphonyCodexAppServer(ctx context.Context, req symphonyRunRequest, callbacks symphonyRunCallbacks) (symphonyRunResult, error) {
	client, err := newCodexAppServerClient(ctx)
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
	var result symphonyRunResult
	result.ThreadID = threadID
	client.onNotification = func(method string, params json.RawMessage) {
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
			if callbacks.Event != nil {
				callbacks.Event(method, map[string]any{"params": json.RawMessage(params)})
			}
		}
	}
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
		return result, err
	}
	if err := client.waitForTurnCompleted(ctx); err != nil {
		return result, err
	}
	result.Summary = "Codex app-server completed the Symphony task."
	return result, nil
}

func symphonyTaskPrompt(req symphonyRunRequest) string {
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

type codexAppServerClient struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	pendingMu      sync.Mutex
	pending        map[int]chan codexRPCMessage
	nextID         int
	done           chan error
	turnDone       chan struct{}
	onNotification func(string, json.RawMessage)
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

func newCodexAppServerClient(ctx context.Context) (*codexAppServerClient, error) {
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
		cmd:      cmd,
		stdin:    stdin,
		pending:  map[int]chan codexRPCMessage{},
		done:     make(chan error, 1),
		turnDone: make(chan struct{}),
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
			if msg.Method == "turn/completed" {
				select {
				case <-c.turnDone:
				default:
					close(c.turnDone)
				}
			}
			if c.onNotification != nil {
				c.onNotification(msg.Method, msg.Params)
			}
		}
	}
}

func (c *codexAppServerClient) waitForTurnCompleted(ctx context.Context) error {
	select {
	case <-c.turnDone:
		return nil
	case err := <-c.done:
		return fmt.Errorf("codex app-server exited before turn completed: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}
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

func boolFromMap(values map[string]any, key string) bool {
	if value, ok := values[key].(bool); ok {
		return value
	}
	return false
}
