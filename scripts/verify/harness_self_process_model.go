package main

import (
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
	"strconv"
	"strings"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

const harnessProcessModelProbeName = "process model replacement probe"

// The process-model probe runs testdata/apps/multiservice through the public
// `scenery up` lifecycle. Every response is attributed to its answering service
// process, and to the host and build that attest it, through its headers.
func runHarnessProcessModelProbeStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessProcessModelProbeName, Command: []string{"go", "run", "./scripts/verify", "--probe", "process-model", "--summary"}}
	summary, err := runHarnessProcessModelProbe(ctx, repoRoot)
	step.Summary, step.DurationMS = summary, time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		step.Diagnostics = []checkDiagnostic{{
			Stage: step.Name, Severity: "error", Message: step.Error,
			SuggestedAction: "Fix process-model generation, dispatch or supervisor replacement, then rerun `go run ./scripts/verify --probe process-model --summary --write`.",
		}}
		return step
	}
	step.OK = true
	return step
}

type harnessProcessModelResponse struct {
	Message        string
	PID            int
	Host           int
	Implementation string
	Build          string
	Generation     int
}

func runHarnessProcessModelProbe(parent context.Context, repoRoot string) (summary map[string]any, returnErr error) {
	ctx, cancel := context.WithTimeout(parent, 6*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-process-model-")
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(root)) }()
	appRoot, home := filepath.Join(root, "app"), filepath.Join(root, "agent")
	if err := copyHarnessMultiserviceFixture(repoRoot, appRoot); err != nil {
		return nil, err
	}
	env := harnessAppEnv(home)
	if err := verifyHarnessStartupFailureReport(ctx, repoRoot, appRoot, env); err != nil {
		return nil, err
	}
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
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if _, err := runHarnessAppCLIWithEnv(cleanup, repoRoot, appRoot, env, "down", "-o", "json"); err != nil {
			returnErr = errors.Join(returnErr, err)
			return
		}
		returnErr = errors.Join(returnErr, harnessProcessModelCleanup(home, appRoot, seen))
	}()
	host, err := strconv.Atoi(started.Session.AppPID)
	apiURL := strings.TrimRight(started.Session.RouteManifest.Routes[localagent.RouteAPI].URL, "/")
	if err != nil || host <= 0 || apiURL == "" || started.LogPath == "" {
		return nil, fmt.Errorf("process-model session did not publish its host process: %s", output)
	}
	seen = append(seen, host)
	call := func(ctx context.Context, path, body string) (harnessProcessModelResponse, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+path, strings.NewReader(body))
		if err != nil {
			return harnessProcessModelResponse{}, err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
		if err != nil {
			return harnessProcessModelResponse{}, err
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err != nil {
			return harnessProcessModelResponse{}, err
		}
		var decoded struct {
			Message string `json:"message"`
		}
		if response.StatusCode != http.StatusOK || json.Unmarshal(data, &decoded) != nil {
			return harnessProcessModelResponse{}, fmt.Errorf("%s answered %d %s", path, response.StatusCode, data)
		}
		// The host attests the generation's build and itself; the answering
		// worker process is named beside it.
		pid, _ := strconv.Atoi(response.Header.Get("X-App-Worker-Pid"))
		if pid > 0 && !slices.Contains(seen, pid) {
			seen = append(seen, pid)
		}
		host, _ := strconv.Atoi(response.Header.Get("X-App-Host-Pid"))
		if host > 0 && !slices.Contains(seen, host) {
			seen = append(seen, host)
		}
		generation, _ := strconv.Atoi(response.Header.Get("X-App-Generation"))
		return harnessProcessModelResponse{Message: decoded.Message, PID: pid, Host: host, Implementation: response.Header.Get("X-App-Implementation-Revision"), Build: response.Header.Get("X-App-Build-Digest"), Generation: generation}, nil
	}
	waitFor := func(path, body, want string) (harnessProcessModelResponse, time.Duration, error) {
		begin := time.Now()
		for {
			response, err := call(ctx, path, body)
			if err == nil && response.Message == want {
				return response, time.Since(begin), nil
			}
			if ctx.Err() != nil || time.Since(begin) > 90*time.Second {
				return response, 0, fmt.Errorf("%s never answered %q (last %q, %v)", path, want, response.Message, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	logOffset := func() int64 {
		info, err := os.Stat(started.LogPath)
		if err != nil {
			return 0
		}
		return info.Size()
	}
	echoOne, err := call(ctx, "/echo", `{"message":"hi"}`)
	if err != nil || echoOne.Message != "echo:hi" {
		return nil, fmt.Errorf("initial echo = %#v, %v", echoOne, err)
	}
	greeterOne, err := call(ctx, "/greet", `{"name":"probe"}`)
	if err != nil || greeterOne.Message != "greeter:echo:hello probe" {
		return nil, fmt.Errorf("initial greet = %#v, %v", greeterOne, err)
	}
	if echoOne.PID <= 0 || greeterOne.PID <= 0 || len(map[int]bool{host: true, echoOne.PID: true, greeterOne.PID: true}) != 3 {
		return nil, fmt.Errorf("host %d, echo %d and greeter %d are not three processes", host, echoOne.PID, greeterOne.PID)
	}
	if echoOne.Host != host || greeterOne.Host != host || echoOne.Build == "" || echoOne.Build != greeterOne.Build || echoOne.Generation != greeterOne.Generation {
		return nil, fmt.Errorf("answers of one generation attest hosts %d and %d with builds %q and %q (session host %d)", echoOne.Host, greeterOne.Host, echoOne.Build, greeterOne.Build, host)
	}

	// A worker that stops on its own restarts from its own executable, without
	// rebuilding and without disturbing other workers.
	if err := syscall.Kill(echoOne.PID, syscall.SIGKILL); err != nil {
		return nil, err
	}
	restarted, restartLatency, err := waitFor("/echo", `{"message":"hi"}`, "echo:hi")
	if err != nil {
		return nil, fmt.Errorf("a killed worker was not restarted: %w", err)
	}
	if restarted.PID == echoOne.PID || restarted.Build != echoOne.Build || restarted.Host != host {
		return nil, fmt.Errorf("restarted echo = %#v; want a new worker of the same build as %#v", restarted, echoOne)
	}
	if greet, err := call(ctx, "/greet", `{"name":"probe"}`); err != nil || greet.Message != "greeter:echo:hello probe" || greet.PID != greeterOne.PID {
		return nil, fmt.Errorf("after a restart greet = %#v, %v; want the unchanged greeter %d", greet, err, greeterOne.PID)
	}
	if !harnessWaitProcessExit(echoOne.PID, 15*time.Second) {
		return nil, fmt.Errorf("killed echo worker %d did not exit", echoOne.PID)
	}
	echoOne = restarted

	// An implementation edit replaces only the edited service's worker.
	echoSource := filepath.Join(appRoot, "echo", "api.go")
	original, err := os.ReadFile(echoSource)
	if err != nil {
		return nil, err
	}
	if err := harnessReplaceInFile(echoSource, `text.Label("echo", input.Message)`, `text.Label("echo-two", input.Message)`); err != nil {
		return nil, err
	}
	echoTwo, editLatency, err := waitFor("/echo", `{"message":"hi"}`, "echo-two:hi")
	if err != nil {
		return nil, err
	}
	greeterTwo, err := call(ctx, "/greet", `{"name":"probe"}`)
	if err != nil || greeterTwo.Message != "greeter:echo-two:hello probe" || greeterTwo.PID != greeterOne.PID || echoTwo.PID == echoOne.PID || echoTwo.Host != host || echoTwo.Generation <= echoOne.Generation {
		return nil, fmt.Errorf("after the echo edit greet = %#v (%v), echo = %#v; want greeter %d and a new echo worker of host %d", greeterTwo, err, echoTwo, greeterOne.PID, host)
	}

	// A failing build leaves the published generation serving.
	edited, err := os.ReadFile(echoSource)
	if err != nil {
		return nil, err
	}
	failedOffset := logOffset()
	if err := os.WriteFile(echoSource, append(append([]byte(nil), edited...), []byte("\nfunc brokenProbeEdit() { undefinedProbeSymbol() }\n")...), 0o600); err != nil {
		return nil, err
	}
	if err := harnessWaitBuildRequest(ctx, started.LogPath, failedOffset, false); err != nil {
		return nil, fmt.Errorf("a broken echo edit did not fail its build: %w", err)
	}
	if served, err := call(ctx, "/echo", `{"message":"hi"}`); err != nil || served.Message != "echo-two:hi" || served.PID != echoTwo.PID {
		return nil, fmt.Errorf("after a failed build echo = %#v, %v", served, err)
	}
	if err := os.WriteFile(echoSource, edited, 0o600); err != nil {
		return nil, err
	}

	// A package compiled into both services replaces both workers.
	if err := harnessReplaceInFile(filepath.Join(appRoot, "internal/text/text.go"), `service + ":" + message`, `service + "|" + message`); err != nil {
		return nil, err
	}
	greeterThree, sharedLatency, err := waitFor("/greet", `{"name":"probe"}`, "greeter|echo-two|hello probe")
	if err != nil {
		return nil, err
	}
	echoThree, err := call(ctx, "/echo", `{"message":"hi"}`)
	if err != nil || echoThree.Message != "echo-two|hi" || echoThree.PID == echoTwo.PID || greeterThree.PID == greeterTwo.PID || greeterThree.Host != host {
		return nil, fmt.Errorf("shared edit answered greet %#v and echo %#v (%v); want both workers replaced under host %d", greeterThree, echoThree, err, host)
	}

	// A contract change starts a new host serving every worker.
	if err := harnessReplaceEachInFile(filepath.Join(appRoot, "echo/package.scn"), [2]string{"record \"echo_result\" {\n", "record \"echo_result\" {\n  field \"note\" {\n    type = string\n  }\n\n"}); err != nil {
		return nil, err
	}
	var echoFour harnessProcessModelResponse
	if err := waitForHarnessCondition(ctx, func() bool {
		echoFour, err = call(ctx, "/echo", `{"message":"hi"}`)
		return err == nil && echoFour.Host > 0 && echoFour.Host != host
	}); err != nil {
		return nil, fmt.Errorf("a contract change kept host %d: echo = %#v: %w", host, echoFour, err)
	}
	greeterFour, err := call(ctx, "/greet", `{"name":"probe"}`)
	if err != nil || greeterFour.Host != echoFour.Host || greeterFour.Message != "greeter|echo-two|hello probe" {
		return nil, fmt.Errorf("after the contract change greet = %#v, %v", greeterFour, err)
	}
	if !harnessWaitProcessExit(host, 15*time.Second) {
		return nil, fmt.Errorf("host %d outlived its replacement", host)
	}
	if err := os.WriteFile(echoSource, original, 0o600); err != nil {
		return nil, err
	}
	return map[string]any{
		"host_pids":            []int{host, echoFour.Host},
		"worker_restart_ms":    restartLatency.Milliseconds(),
		"implementation_ms":    editLatency.Milliseconds(),
		"shared_edit_ms":       sharedLatency.Milliseconds(),
		"failed_build_kept":    true,
		"contract_change_host": echoFour.Host,
		"proof":                "public_scenery_up_host_replaced_only_changed_workers_restarted_crashed_workers_kept_failed_builds_and_restarted_for_contract_changes",
	}, nil
}

// harnessProcessModelWaitLog waits for a supervisor event after offset.
func harnessProcessModelWaitLog(ctx context.Context, log string, offset int64, event string) error {
	for begin := time.Now(); time.Since(begin) < 90*time.Second; {
		data, err := os.ReadFile(log)
		if err != nil {
			return err
		}
		if offset <= int64(len(data)) && bytes.Contains(data[offset:], []byte(`"`+event+`"`)) {
			return nil
		}
		if err := harnessWaitContext(ctx, 20*time.Millisecond); err != nil {
			return err
		}
	}
	return fmt.Errorf("the session never reported %s", event)
}

func copyHarnessMultiserviceFixture(repoRoot, appRoot string) error {
	source := filepath.Join(repoRoot, "testdata/apps/multiservice")
	generated := map[string]bool{".scenery": true, "echo/scenerycontract": true, "greeter/scenerycontract": true, "internal/scenerygen": true}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if generated[filepath.ToSlash(relative)] {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(appRoot, relative), 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if relative == "go.mod" {
			content = bytes.ReplaceAll(content, []byte("=> ../../.."), []byte("=> "+repoRoot))
		}
		return os.WriteFile(filepath.Join(appRoot, relative), content, 0o600)
	})
}

// harnessReplaceEachInFile applies every unique replacement in one save.
func harnessReplaceEachInFile(path string, replacements ...[2]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, replacement := range replacements {
		if bytes.Count(data, []byte(replacement[0])) != 1 {
			return fmt.Errorf("%s does not contain exactly one %q", path, replacement[0])
		}
		data = bytes.Replace(data, []byte(replacement[0]), []byte(replacement[1]), 1)
	}
	return harnessAtomicWatchSave(path, data)
}

func harnessReplaceInFile(path, old, replacement string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if bytes.Count(data, []byte(old)) != 1 {
		return fmt.Errorf("%s does not contain exactly one %q", path, old)
	}
	return harnessAtomicWatchSave(path, bytes.Replace(data, []byte(old), []byte(replacement), 1))
}

// harnessProcessModelCleanup requires every observed process to exit and the
// process-link wiring to disappear with the session.
func harnessProcessModelCleanup(home, appRoot string, pids []int) error {
	for _, pid := range pids {
		if !harnessWaitProcessExit(pid, 15*time.Second) {
			return fmt.Errorf("process %d outlived scenery down", pid)
		}
		if err := syscall.Kill(pid, 0); err == nil {
			return fmt.Errorf("process %d outlived scenery down", pid)
		}
	}
	paths, err := localagent.PathsForWorktree(home, appRoot)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Dir(paths.Socket))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		for _, pattern := range []string{"d.sock", "s[0-9]*.sock", "process-link.json", "host-state"} {
			if matched, _ := filepath.Match(pattern, name); matched {
				return fmt.Errorf("process-model wiring %s outlived scenery down", name)
			}
		}
	}
	return nil
}

// harnessWaitProcessExit reports whether the process exited within timeout.
func harnessWaitProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return syscall.Kill(pid, 0) != nil
}

// verifyHarnessStartupFailureReport starts the session on an address no host
// can bind (TEST-NET-3). The detached session fails internally, so its answer
// withholds the cause behind a report token; the token, minted inside that
// other process, must resolve to the cause from a later invocation, and the
// failed start must leave nothing that keeps the real start from succeeding.
func verifyHarnessStartupFailureReport(ctx context.Context, repoRoot, appRoot string, env []string) error {
	const unassignable = "203.0.113.7:59999"
	output, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "up", "--detach", "--wait", "ready", "--listen", unassignable, "-o", "json")
	if err == nil {
		return fmt.Errorf("a session started on the unassignable address %s", unassignable)
	}
	envelope, decodeErr := machine.Decode[graph.Diagnostic](output, currentMachineSpecRevision())
	if decodeErr != nil || len(envelope.Diagnostics) != 1 {
		return fmt.Errorf("startup failure is not one diagnostic: %v: %s", decodeErr, output)
	}
	diagnostic := envelope.Diagnostics[0]
	if !strings.HasPrefix(diagnostic.Code, "SCN9") || diagnostic.ReportToken == "" || strings.Contains(diagnostic.Message, "203.0.113.7") {
		return fmt.Errorf("startup failure diagnostic = %+v", diagnostic)
	}
	reported, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "inspect", "report", diagnostic.ReportToken, "-o", "json")
	if err != nil {
		return fmt.Errorf("report %s does not resolve: %w", diagnostic.ReportToken, err)
	}
	var report struct {
		ReportToken string   `json:"report_token"`
		Command     string   `json:"command"`
		Arguments   []string `json:"arguments"`
		Cause       string   `json:"cause"`
	}
	if err := decodeCLIJSON(reported, &report); err != nil {
		return err
	}
	if report.ReportToken != diagnostic.ReportToken || report.Command != "up" || !strings.Contains(report.Cause, "203.0.113.7") || !slices.Contains(report.Arguments, unassignable) {
		return fmt.Errorf("report %s does not carry the cause of the failed start: %+v", diagnostic.ReportToken, report)
	}
	return nil
}
