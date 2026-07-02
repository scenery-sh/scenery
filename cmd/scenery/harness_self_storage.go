package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
)

func runHarnessStorageProbeStep(ctx context.Context, repoRoot, sceneryPath string) harnessStep {
	started := time.Now()
	fixtureRoot := filepath.Join(repoRoot, "testdata", "apps", "storage-basic")
	agentHome := filepath.Join(repoRoot, ".scenery", "harness", "storage-probe-agent-home")
	worktreeRoot := filepath.Join(repoRoot, ".scenery", "harness", "storage-probe-worktrees")
	command := []string{sceneryPath, "task", "run", "--app-root", fixtureRoot, "service:storage-probe"}
	step := harnessStep{Name: "storage fixture probe", Command: command}
	if err := os.RemoveAll(agentHome); err != nil {
		step.OK = false
		step.Error = err.Error()
		step.DurationMS = time.Since(started).Milliseconds()
		return step
	}
	if err := os.RemoveAll(worktreeRoot); err != nil {
		step.OK = false
		step.Error = err.Error()
		step.DurationMS = time.Since(started).Milliseconds()
		return step
	}
	cmd := commandTreeContext(ctx, sceneryPath, command[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = envWithOverrides(envpolicy.Environ(), "SCENERY_AGENT_HOME="+agentHome)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	step.DurationMS = time.Since(started).Milliseconds()
	step.Summary = map[string]any{
		"fixture":    "testdata/apps/storage-basic",
		"output":     strings.TrimSpace(stdout.String()),
		"agent_home": filepath.ToSlash(agentHome),
	}
	objectPath := filepath.Join(agentHome, "agent", "storage", "storage-basic", "objects", "app", "__scenery", "tenants", base64.RawURLEncoding.EncodeToString([]byte("storage-probe")), "task", "probe.txt")
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		step.OutputTail = tailString(firstNonEmpty(stderr.String(), stdout.String()), 8192)
		return step
	}
	if _, statErr := os.Stat(objectPath); statErr != nil {
		step.OK = false
		step.Error = "storage probe object was not written: " + statErr.Error()
		step.Diagnostics = []checkDiagnostic{{
			Stage:           "storage fixture probe",
			Severity:        "error",
			File:            filepath.ToSlash(objectPath),
			Message:         step.Error,
			SuggestedAction: "Run `scenery task run --app-root testdata/apps/storage-basic service:storage-probe` and inspect SCENERY_STORAGE_CONFIG handling.",
		}}
		return step
	}
	rootA := filepath.Join(worktreeRoot, "a")
	rootB := filepath.Join(worktreeRoot, "b")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		step.OK = false
		step.Error = err.Error()
		return step
	}
	if err := os.CopyFS(rootA, os.DirFS(fixtureRoot)); err != nil {
		step.OK = false
		step.Error = "copy storage fixture A: " + err.Error()
		return step
	}
	if err := os.CopyFS(rootB, os.DirFS(fixtureRoot)); err != nil {
		step.OK = false
		step.Error = "copy storage fixture B: " + err.Error()
		return step
	}
	sourcePath := filepath.Join(worktreeRoot, "shared-input.txt")
	outputPath := filepath.Join(worktreeRoot, "shared-output.txt")
	const sharedBody = "shared storage across worktrees\n"
	if err := os.WriteFile(sourcePath, []byte(sharedBody), 0o644); err != nil {
		step.OK = false
		step.Error = err.Error()
		return step
	}
	putCommand := []string{sceneryPath, "storage", "put", "app", "worktree/shared.txt", sourcePath, "--json", "--app-root", rootA}
	putOut, putErrOut, err := runHarnessStorageProbeCommand(ctx, repoRoot, agentHome, putCommand)
	if err != nil {
		step.OK = false
		step.Error = "storage put from fixture worktree A failed: " + strings.TrimSpace(err.Error())
		step.OutputTail = tailString(firstNonEmpty(putErrOut, putOut), 8192)
		return step
	}
	getCommand := []string{sceneryPath, "storage", "get", "app", "worktree/shared.txt", "--output", outputPath, "--json", "--app-root", rootB}
	getOut, getErrOut, err := runHarnessStorageProbeCommand(ctx, repoRoot, agentHome, getCommand)
	if err != nil {
		step.OK = false
		step.Error = "storage get from fixture worktree B failed: " + strings.TrimSpace(err.Error())
		step.OutputTail = tailString(firstNonEmpty(getErrOut, getOut), 8192)
		return step
	}
	got, err := os.ReadFile(outputPath)
	if err != nil {
		step.OK = false
		step.Error = err.Error()
		return step
	}
	if string(got) != sharedBody {
		step.OK = false
		step.Error = fmt.Sprintf("shared storage read mismatch: got %q", string(got))
		return step
	}
	sharedObjectPath := filepath.Join(agentHome, "agent", "storage", "storage-basic", "objects", "app", "worktree", "shared.txt")
	if _, statErr := os.Stat(sharedObjectPath); statErr != nil {
		step.OK = false
		step.Error = "shared storage object was not written: " + statErr.Error()
		step.Diagnostics = []checkDiagnostic{{
			Stage:           "storage fixture probe",
			Severity:        "error",
			File:            filepath.ToSlash(sharedObjectPath),
			Message:         step.Error,
			SuggestedAction: "Run the storage put/get commands from the two harness fixture roots and inspect shared storage cell resolution.",
		}}
		return step
	}
	step.OK = true
	step.Summary["object_path"] = filepath.ToSlash(objectPath)
	step.Summary["shared_object_path"] = filepath.ToSlash(sharedObjectPath)
	step.Summary["worktree_a"] = filepath.ToSlash(rootA)
	step.Summary["worktree_b"] = filepath.ToSlash(rootB)
	step.Summary["shared_get_output"] = strings.TrimSpace(getOut)
	restartSummary, err := runHarnessLocalStorageRestartProbe(ctx, repoRoot, sceneryPath, fixtureRoot)
	if err != nil {
		step.OK = false
		step.Error = err.Error()
		return step
	}
	for key, value := range restartSummary {
		step.Summary[key] = value
	}
	return step
}

func runHarnessStorageProbeCommand(ctx context.Context, repoRoot, agentHome string, command []string) (string, string, error) {
	cmd := commandTreeContext(ctx, command[0], command[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = envWithOverrides(envpolicy.Environ(), "SCENERY_AGENT_HOME="+agentHome)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// runHarnessLocalStorageRestartProbe proves durability of the local storage
// backend across a full dev-runtime restart: it writes an object through the
// live app route, stops the runtime with `scenery down`, restarts it, and reads
// the same object back. Because storage is a plain fsync'd directory tree there
// is no separate storage process to interrupt; stopping the whole runtime is the
// strongest crash surface the fixture exercises.
func runHarnessLocalStorageRestartProbe(ctx context.Context, repoRoot, sceneryPath, fixtureRoot string) (map[string]any, error) {
	summary := map[string]any{
		"local_storage_restart_probe": "skipped",
	}
	baseEnv := envpolicy.Environ()
	agentHome := filepath.Join(repoRoot, ".scenery", "harness", "storage-restart-agent-home")
	sessionRoot := filepath.Join(fixtureRoot, ".scenery", "sessions", "main-f49603")
	env := envWithOverrides(baseEnv, "SCENERY_AGENT_HOME="+agentHome)
	cleanupHarnessStorageRestartAgent(ctx, repoRoot, sceneryPath, agentHome, env)
	if err := os.RemoveAll(agentHome); err != nil {
		return summary, err
	}
	if err := os.RemoveAll(sessionRoot); err != nil {
		return summary, err
	}
	upCommand := []string{sceneryPath, "up", "--app-root", fixtureRoot, "--json", "--detach"}
	upOut, upErr, err := runHarnessStorageProbeCommandWithEnv(ctx, repoRoot, env, upCommand)
	if err != nil {
		return summary, fmt.Errorf("local storage scenery up failed: %s\n%s", strings.TrimSpace(err.Error()), tailString(firstNonEmpty(upErr, upOut), 8192))
	}
	defer func() {
		downCommand := []string{sceneryPath, "down", "--app-root", fixtureRoot, "--json"}
		_, _, _ = runHarnessStorageProbeCommandWithEnv(context.Background(), repoRoot, env, downCommand)
		cleanupHarnessStorageRestartAgent(context.Background(), repoRoot, sceneryPath, agentHome, env)
	}()
	stateRoot, err := harnessDetachStateRoot(upOut)
	if err != nil {
		return summary, err
	}
	probeBody, err := waitForHarnessStorageHTTPProbe(ctx, devAPIUnixSocketPath(stateRoot), http.MethodPost, 2*time.Minute)
	if err != nil {
		return summary, err
	}
	var probe struct {
		Key       string `json:"key"`
		SizeBytes int64  `json:"size_bytes"`
		Body      string `json:"body"`
	}
	if err := json.Unmarshal([]byte(probeBody), &probe); err != nil {
		return summary, fmt.Errorf("parse local storage probe response: %w: %s", err, probeBody)
	}
	if probe.Key != "probe/public.txt" || probe.Body != "hello public" || probe.SizeBytes != int64(len("hello public")) {
		return summary, fmt.Errorf("unexpected local storage probe response: %s", probeBody)
	}
	inspectCommand := []string{sceneryPath, "inspect", "storage", "--app-root", fixtureRoot, "--json"}
	inspectOut, inspectErr, err := runHarnessStorageProbeCommandWithEnv(ctx, repoRoot, env, inspectCommand)
	if err != nil {
		return summary, fmt.Errorf("local storage inspect failed: %s\n%s", strings.TrimSpace(err.Error()), tailString(firstNonEmpty(inspectErr, inspectOut), 8192))
	}
	var inspect struct {
		Storage struct {
			Readiness string `json:"readiness"`
			Runtime   struct {
				CellRoot string `json:"cell_root"`
				Exists   bool   `json:"exists"`
			} `json:"runtime"`
		} `json:"storage"`
	}
	if err := json.Unmarshal([]byte(inspectOut), &inspect); err != nil {
		return summary, fmt.Errorf("parse local storage inspect JSON: %w", err)
	}
	if inspect.Storage.Readiness != "ready" || !inspect.Storage.Runtime.Exists || inspect.Storage.Runtime.CellRoot == "" {
		return summary, fmt.Errorf("local storage inspect not ready: %s", strings.TrimSpace(inspectOut))
	}
	summary["local_storage_probe"] = "passed"
	summary["local_storage_agent_home"] = filepath.ToSlash(agentHome)
	summary["local_storage_response"] = probeBody
	summary["local_storage_readiness"] = inspect.Storage.Readiness
	summary["local_storage_cell_root"] = inspect.Storage.Runtime.CellRoot

	// Restart the whole runtime and confirm the fsync'd object survived.
	downCommand := []string{sceneryPath, "down", "--app-root", fixtureRoot, "--json"}
	if downOut, downErr, err := runHarnessStorageProbeCommandWithEnv(ctx, repoRoot, env, downCommand); err != nil {
		return summary, fmt.Errorf("local storage restart proof down failed: %s\n%s", strings.TrimSpace(err.Error()), tailString(firstNonEmpty(downErr, downOut), 8192))
	}
	restartOut, restartErr, err := runHarnessStorageProbeCommandWithEnv(ctx, repoRoot, env, upCommand)
	if err != nil {
		return summary, fmt.Errorf("local storage restart scenery up failed: %s\n%s", strings.TrimSpace(err.Error()), tailString(firstNonEmpty(restartErr, restartOut), 8192))
	}
	restartStateRoot, err := harnessDetachStateRoot(restartOut)
	if err != nil {
		return summary, err
	}
	restartProbeBody, err := waitForHarnessStorageHTTPProbe(ctx, devAPIUnixSocketPath(restartStateRoot), http.MethodGet, 2*time.Minute)
	if err != nil {
		return summary, err
	}
	var restartProbe struct {
		Key       string `json:"key"`
		SizeBytes int64  `json:"size_bytes"`
		Body      string `json:"body"`
	}
	if err := json.Unmarshal([]byte(restartProbeBody), &restartProbe); err != nil {
		return summary, fmt.Errorf("parse local storage restart probe response: %w: %s", err, restartProbeBody)
	}
	if restartProbe.Key != probe.Key || restartProbe.Body != probe.Body || restartProbe.SizeBytes != probe.SizeBytes {
		return summary, fmt.Errorf("unexpected local storage restart probe response: %s", restartProbeBody)
	}
	summary["local_storage_restart_probe"] = "passed"
	summary["local_storage_restart_response"] = restartProbeBody
	return summary, nil
}

func harnessDetachStateRoot(detachJSON string) (string, error) {
	var detach struct {
		Session struct {
			StateRoot string `json:"state_root"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(detachJSON), &detach); err != nil {
		return "", fmt.Errorf("parse storage detach JSON: %w", err)
	}
	stateRoot := strings.TrimSpace(detach.Session.StateRoot)
	if stateRoot == "" {
		return "", fmt.Errorf("storage detach JSON did not include session.state_root")
	}
	return stateRoot, nil
}

func cleanupHarnessStorageRestartAgent(ctx context.Context, repoRoot, sceneryPath, agentHome string, env []string) {
	if strings.TrimSpace(agentHome) == "" {
		return
	}
	psCommand := []string{sceneryPath, "ps", "--json"}
	psOut, _, err := runHarnessStorageProbeCommandWithEnv(ctx, repoRoot, env, psCommand)
	pids := map[int]bool{}
	if err == nil && strings.TrimSpace(psOut) != "" {
		var status struct {
			Agent struct {
				PID int `json:"pid"`
			} `json:"agent"`
			Sessions []struct {
				OwnerPID  int    `json:"owner_pid"`
				AppPID    string `json:"app_pid"`
				Processes map[string]struct {
					PID int `json:"pid"`
				} `json:"processes"`
			} `json:"sessions"`
			Substrates []struct {
				OwnerPID int            `json:"owner_pid"`
				PIDs     map[string]int `json:"pids"`
			} `json:"substrates"`
		}
		if json.Unmarshal([]byte(psOut), &status) == nil {
			addHarnessCleanupPID(pids, status.Agent.PID)
			for _, session := range status.Sessions {
				addHarnessCleanupPID(pids, session.OwnerPID)
				if pid, convErr := strconv.Atoi(strings.TrimSpace(session.AppPID)); convErr == nil {
					addHarnessCleanupPID(pids, pid)
				}
				for _, process := range session.Processes {
					addHarnessCleanupPID(pids, process.PID)
				}
			}
			for _, substrate := range status.Substrates {
				addHarnessCleanupPID(pids, substrate.OwnerPID)
				for _, pid := range substrate.PIDs {
					addHarnessCleanupPID(pids, pid)
				}
			}
		}
	}
	if data, readErr := os.ReadFile(filepath.Join(agentHome, "run", "agent.json")); readErr == nil {
		var agent struct {
			PID int `json:"pid"`
		}
		if json.Unmarshal(data, &agent) == nil {
			addHarnessCleanupPID(pids, agent.PID)
		}
	}
	for pid := range pids {
		proc, findErr := os.FindProcess(pid)
		if findErr == nil {
			_ = proc.Signal(os.Interrupt)
		}
	}
	time.Sleep(500 * time.Millisecond)
	for pid := range pids {
		proc, findErr := os.FindProcess(pid)
		if findErr == nil {
			_ = proc.Kill()
		}
	}
}

func addHarnessCleanupPID(pids map[int]bool, pid int) {
	if pid > 0 && pid != os.Getpid() {
		pids[pid] = true
	}
}

func waitForHarnessStorageHTTPProbe(ctx context.Context, socketPath, method string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		body, err := harnessStorageHTTPProbe(ctx, socketPath, method)
		if err == nil {
			return body, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("local storage HTTP probe did not succeed within %s: %v", timeout, lastErr)
}

func harnessStorageHTTPProbe(ctx context.Context, socketPath, method string) (string, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix/storage/probe-public", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return strings.TrimSpace(string(data)), nil
}

func runHarnessStorageProbeCommandWithEnv(ctx context.Context, repoRoot string, env []string, command []string) (string, string, error) {
	cmd := commandTreeContext(ctx, command[0], command[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}
