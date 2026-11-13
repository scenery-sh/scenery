package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

const harnessAssistantJourneyProbeName = "assistant journey probe"

// The assistant-journey probe runs testdata/assistant, its generated Eve
// helper and the fixture's mock model through `scenery up` in a disposable copy
// with its own agent home, so no existing checkout, session or database is
// read or changed. It proves on the real helper that a run's calls and events
// keep their run while a later run is accepted, that an approval resumes its
// own run, and that overlapping streams read one history. The fixture's
// assistant surface is authenticated; the probe signs in through the local
// development bootstrap.
func runHarnessAssistantJourneyProbeStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessAssistantJourneyProbeName, Command: []string{"go", "run", "./scripts/verify", "--probe", "assistant-journey", "--summary"}}
	summary, err := runHarnessAssistantJourneyProbe(ctx, repoRoot)
	step.Summary, step.DurationMS = summary, time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		step.Diagnostics = []checkDiagnostic{{Stage: step.Name, Severity: "error", Message: step.Error,
			SuggestedAction: "Fix the assistant run lifecycle through the real helper, then rerun `go run ./scripts/verify --probe assistant-journey --summary --write`."}}
		return step
	}
	step.OK = true
	return step
}

type harnessAssistantEvent struct {
	Type     string          `json:"type"`
	RunID    string          `json:"run_id"`
	Sequence uint64          `json:"sequence"`
	Data     json.RawMessage `json:"data"`
}

type harnessAssistantClient struct {
	ctx    context.Context
	api    string
	token  string
	client *http.Client
}

func (client *harnessAssistantClient) request(method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(client.ctx, method, client.api+path, reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	return response.StatusCode, data, err
}

func (client *harnessAssistantClient) create(message string) (conversation, run string, err error) {
	status, data, err := client.request(http.MethodPost, "/assistants/support/v1/conversations", map[string]any{"message": map[string]string{"role": "user", "content": message}})
	if err != nil || status != http.StatusOK {
		return "", "", fmt.Errorf("create %q = %d %s: %v", message, status, data, err)
	}
	var created struct {
		ConversationID string `json:"conversation_id"`
		RunID          string `json:"run_id"`
	}
	return created.ConversationID, created.RunID, json.Unmarshal(data, &created)
}

func (client *harnessAssistantClient) turn(conversation, message string) (string, error) {
	status, data, err := client.request(http.MethodPost, "/assistants/support/v1/conversations/"+conversation+"/turns", map[string]any{"message": map[string]string{"role": "user", "content": message}})
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("turn %q = %d %s: %v", message, status, data, err)
	}
	var accepted struct {
		RunID string `json:"run_id"`
	}
	return accepted.RunID, json.Unmarshal(data, &accepted)
}

func (client *harnessAssistantClient) approve(conversation, approval string) error {
	status, data, err := client.request(http.MethodPost, "/assistants/support/v1/conversations/"+conversation+"/approvals/"+approval, map[string]string{"decision": "approve"})
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("approve = %d %s: %v", status, data, err)
	}
	return nil
}

// events reads the conversation's complete public history.
func (client *harnessAssistantClient) events(conversation string) ([]harnessAssistantEvent, error) {
	status, data, err := client.request(http.MethodGet, "/assistants/support/v1/conversations/"+conversation+"/events?after=0", nil)
	if err != nil || status != http.StatusOK {
		return nil, fmt.Errorf("events = %d %s: %v", status, data, err)
	}
	var events []harnessAssistantEvent
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var event harnessAssistantEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, scanner.Err()
}

// waitEvents polls the history until done accepts it.
func (client *harnessAssistantClient) waitEvents(conversation, what string, done func([]harnessAssistantEvent) bool) ([]harnessAssistantEvent, error) {
	deadline := time.Now().Add(90 * time.Second)
	for {
		events, err := client.events(conversation)
		if err == nil && done(events) {
			return events, nil
		}
		if time.Now().After(deadline) {
			return events, fmt.Errorf("conversation never reached %s (last error %v): %s", what, err, harnessAssistantEventSummary(events))
		}
		if err := harnessWaitContext(client.ctx, 250*time.Millisecond); err != nil {
			return events, err
		}
	}
}

func harnessAssistantEventSummary(events []harnessAssistantEvent) string {
	var parts []string
	for _, event := range events {
		parts = append(parts, fmt.Sprintf("%d:%s:%s", event.Sequence, strings.TrimPrefix(event.Type, "assistant."), tailString(event.RunID, 6)))
	}
	return strings.Join(parts, " ")
}

func harnessAssistantApprovals(events []harnessAssistantEvent) []string {
	var approvals []string
	for _, event := range events {
		if event.Type != "assistant.approval.required" {
			continue
		}
		var data struct {
			ApprovalID string `json:"approval_id"`
		}
		if json.Unmarshal(event.Data, &data) == nil && data.ApprovalID != "" {
			approvals = append(approvals, data.ApprovalID)
		}
	}
	return approvals
}

func harnessAssistantHas(events []harnessAssistantEvent, eventType, run string) bool {
	return slices.ContainsFunc(events, func(event harnessAssistantEvent) bool {
		return event.Type == eventType && (run == "" || event.RunID == run)
	})
}

func harnessAssistantText(events []harnessAssistantEvent, run string) string {
	var text strings.Builder
	for _, event := range events {
		if event.Type == "assistant.message.completed" && event.RunID == run {
			text.Write(event.Data)
		}
	}
	return text.String()
}

func runHarnessAssistantJourneyProbe(parent context.Context, repoRoot string) (summary map[string]any, returnErr error) {
	ctx, cancel := context.WithTimeout(parent, 12*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-assistant-journey-")
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(root)) }()
	appRoot, home := filepath.Join(root, "app"), filepath.Join(root, "home")
	if err := copyHarnessAssistantFixture(repoRoot, appRoot); err != nil {
		return nil, err
	}
	env := harnessAppEnv(home)
	output, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "up", "--detach", "--wait", "ready", "-o", "json")
	if err != nil {
		return nil, err
	}
	var started detachedDevResult
	if err := decodeCLIJSON(output, &started); err != nil {
		return nil, err
	}
	var seen []int
	defer func() {
		if returnErr != nil {
			returnErr = fmt.Errorf("%w\nsession log:\n%s", returnErr, harnessAssistantLogTail(started.LogPath))
		}
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), time.Minute)
		defer cancelCleanup()
		if _, err := runHarnessAppCLIWithEnv(cleanup, repoRoot, appRoot, env, "down", "-o", "json"); err != nil {
			returnErr = errors.Join(returnErr, err)
			return
		}
		returnErr = errors.Join(returnErr, harnessProcessModelCleanup(home, appRoot, seen))
	}()
	api := strings.TrimRight(started.Session.RouteManifest.Routes[localagent.RouteAPI].URL, "/")
	if api == "" {
		return nil, fmt.Errorf("assistant fixture session published no API route: %s", output)
	}
	client := &harnessAssistantClient{ctx: ctx, api: api, client: &http.Client{Timeout: 60 * time.Second}}
	status, bootstrap, err := client.request(http.MethodPost, "/users/dev-bootstrap", map[string]any{})
	var signedIn struct {
		Token string `json:"token"`
	}
	if err != nil || status != http.StatusOK || json.Unmarshal(bootstrap, &signedIn) != nil || signedIn.Token == "" {
		return nil, fmt.Errorf("development sign-in = %d %s: %v", status, bootstrap, err)
	}
	client.token = signedIn.Token
	session := func() (localagent.Session, error) { return harnessLiveSession(ctx, home, appRoot) }
	current, err := session()
	if err != nil {
		return nil, err
	}
	for _, process := range current.Processes {
		seen = append(seen, process.PID)
	}

	// The helper starts after the host; a first conversation proves it serves.
	var readyErr error
	for begin := time.Now(); time.Since(begin) < 3*time.Minute; {
		if _, _, readyErr = client.create("hello"); readyErr == nil {
			break
		}
		if err := harnessWaitContext(ctx, time.Second); err != nil {
			return nil, err
		}
	}
	if readyErr != nil {
		return nil, fmt.Errorf("assistant helper never served: %w", readyErr)
	}

	// A: the first run parks on approval while a later run is accepted. The
	// later run executes nothing until the approval resumes the first run.
	conversation, first, err := client.create("local-mcp")
	if err != nil {
		return nil, err
	}
	parked, err := client.waitEvents(conversation, "the first run's approval", func(events []harnessAssistantEvent) bool {
		return len(harnessAssistantApprovals(events)) > 0
	})
	if err != nil {
		return nil, err
	}
	second, err := client.turn(conversation, "provider-local")
	if err != nil {
		return nil, err
	}
	if err := harnessWaitContext(ctx, 3*time.Second); err != nil {
		return nil, err
	}
	queued, err := client.events(conversation)
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(queued, func(event harnessAssistantEvent) bool { return event.RunID == second }) || harnessAssistantHas(queued, "assistant.run.completed", first) {
		return nil, fmt.Errorf("while the first run waited for approval: %s", harnessAssistantEventSummary(queued))
	}
	approvals := harnessAssistantApprovals(parked)
	if err := client.approve(conversation, approvals[len(approvals)-1]); err != nil {
		return nil, fmt.Errorf("approval of the parked run: %w", err)
	}
	both, err := client.waitEvents(conversation, "both runs' completion", func(events []harnessAssistantEvent) bool {
		return harnessAssistantHas(events, "assistant.run.completed", first) && harnessAssistantHas(events, "assistant.run.completed", second)
	})
	if err != nil {
		return nil, err
	}
	firstEnd := slices.IndexFunc(both, func(event harnessAssistantEvent) bool {
		return event.Type == "assistant.run.completed" && event.RunID == first
	})
	secondStart := slices.IndexFunc(both, func(event harnessAssistantEvent) bool { return event.RunID == second })
	if secondStart < firstEnd || !strings.Contains(harnessAssistantText(both, first), "processed:acceptance-scene") || !strings.Contains(harnessAssistantText(both, second), "fixture:provider-local") {
		return nil, fmt.Errorf("approval and queued run history: %s", harnessAssistantEventSummary(both))
	}
	for index, event := range both {
		if event.Sequence != uint64(index+1) || (event.RunID != first && event.RunID != second) {
			return nil, fmt.Errorf("history is not one contiguous sequence of the two runs: %s", harnessAssistantEventSummary(both))
		}
	}

	// B: overlapping streams read the same history.
	streams := make([][]harnessAssistantEvent, 2)
	streamErrs := make(chan error, 2)
	for index := range streams {
		go func() {
			var err error
			streams[index], err = client.events(conversation)
			streamErrs <- err
		}()
	}
	if err := errors.Join(<-streamErrs, <-streamErrs); err != nil {
		return nil, err
	}
	if left, right := harnessAssistantEventSummary(streams[0]), harnessAssistantEventSummary(streams[1]); left != right || left != harnessAssistantEventSummary(both) {
		return nil, fmt.Errorf("overlapping streams disagree:\n%s\n%s", left, right)
	}

	return map[string]any{
		"queued_run_after_approval": true,
		"history_events":            len(both),
		"host_pid":                  current.AppPID,
		"proof":                     "real_eve_helper_runs_keep_their_identity",
	}, nil
}

func copyHarnessAssistantFixture(repoRoot, appRoot string) error {
	source := filepath.Join(repoRoot, "testdata/assistant")
	skipped := map[string]bool{".scenery": true, ".env": true, "node_modules": true}
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if skipped[entry.Name()] {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(appRoot, relative), 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if relative == "go.mod" {
			content = bytes.ReplaceAll(content, []byte("=> ../.."), []byte("=> "+repoRoot))
		}
		return os.WriteFile(filepath.Join(appRoot, relative), content, 0o600)
	}); err != nil {
		return err
	}
	return nil
}

// harnessAssistantLogTail returns the session log's recent process output
// other than request traces, and its assistant and process events, for a
// failure report.
func harnessAssistantLogTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		var envelope struct {
			Data struct {
				Type string `json:"type"`
				Data struct {
					Output string `json:"output"`
					Source string `json:"source"`
				} `json:"data"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &envelope) != nil {
			continue
		}
		event := envelope.Data
		switch {
		case event.Type == "process.output" && !strings.Contains(event.Data.Output, " TRC "):
			lines = append(lines, event.Data.Source+": "+tailString(strings.TrimSpace(event.Data.Output), 400))
		case strings.HasPrefix(event.Type, "assistant") || strings.HasPrefix(event.Type, "process.") && event.Type != "process.output":
			lines = append(lines, event.Type+" "+tailString(line, 300))
		}
	}
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return strings.Join(lines, "\n")
}
