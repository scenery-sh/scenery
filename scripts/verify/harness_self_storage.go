package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
)

func runHarnessStorageProbeStep(ctx context.Context, repoRoot, sceneryPath string) (step harnessStep) {
	started := time.Now()
	step = harnessStep{Name: "storage fixture probe", Command: []string{sceneryPath, "storage"}}
	defer func() { step.DurationMS = time.Since(started).Milliseconds() }()
	summary, err := runHarnessStorageIsolationProbe(ctx, repoRoot, sceneryPath)
	step.Summary = summary
	step.OK = err == nil
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

func runHarnessStorageProbeCommand(ctx context.Context, repoRoot, agentHome string, command []string) (string, string, error) {
	cmd := commandTreeContext(ctx, command[0], command[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = harnessStorageProbeEnv(agentHome)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// harnessStorageProbeEnv isolates probe commands in their own agent home AND
// on an ephemeral router port: the probe agent must never contend for the
// machine agent's router address — before stale-agent reaping learned to skip
// live foreign agents, that contention killed the machine's real agent, and
// now it would correctly fail the probe agent closed instead.
func harnessStorageProbeEnv(agentHome string) []string {
	return envWithOverrides(envpolicy.Environ(),
		"SCENERY_AGENT_HOME="+agentHome,
		"SCENERY_AGENT_ROUTER_ADDR=127.0.0.1:0",
	)
}

// runHarnessLocalStorageRestartProbe proves durability of the local storage
// backend across a full dev-runtime restart: it writes an object through the
// live app route, stops the runtime with `scenery down`, restarts it, and reads
// the same object back. Because storage is a plain fsync'd directory tree there
// is no separate storage process. This proves a clean restart, not a process
// crash or power-loss durability.
func runHarnessLocalStorageRestartProbe(ctx context.Context, repoRoot, sceneryPath, fixtureRoot, agentHome string) (summary map[string]any, resultErr error) {
	summary = map[string]any{
		"local_storage_restart_probe": "skipped",
	}
	env := harnessStorageProbeEnv(agentHome)
	upCommand := []string{sceneryPath, "up", "--app-root", fixtureRoot, "-o", "json", "--detach"}
	// Cleanup must not depend on the probe agent staying reachable: record
	// every detached `scenery up` child PID directly so the runtime is torn
	// down even when `scenery down` and the agent-based cleanup both fail.
	// A leaked detached child once outlived its deleted agent home and drove
	// an agent restart storm on the host machine.
	owners := map[int]localagent.Owner{}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		downCommand := []string{sceneryPath, "down", "--app-root", fixtureRoot, "-o", "json"}
		_, _, downErr := runHarnessStorageProbeCommandWithEnv(cleanupCtx, repoRoot, env, downCommand)
		cleanupErr := cleanupHarnessStorageRestartAgent(cleanupCtx, repoRoot, sceneryPath, agentHome, env, owners)
		resultErr = errors.Join(resultErr, downErr, cleanupErr)
		summary["process_cleanup"] = "passed"
		if downErr != nil || cleanupErr != nil {
			summary["process_cleanup"] = "failed"
		}
	}()
	upOut, upErr, err := runHarnessStorageProbeCommandWithEnv(ctx, repoRoot, env, upCommand)
	if err != nil {
		return summary, fmt.Errorf("local storage scenery up failed: %s\n%s", strings.TrimSpace(err.Error()), tailString(firstNonEmpty(upErr, upOut), 8192))
	}
	apiSocket, upPID, err := harnessDetachInfo(upOut)
	if upPID > 0 {
		owners[upPID] = localagent.CaptureOwner(upPID, "storage probe")
	}
	if err != nil {
		return summary, err
	}
	probeBody, err := waitForHarnessStorageHTTPProbe(ctx, apiSocket, http.MethodPost, 2*time.Minute)
	if err != nil {
		return summary, err
	}
	var probe struct {
		Key       string `json:"key"`
		SizeBytes string `json:"size_bytes"`
		Body      string `json:"body"`
	}
	if err := json.Unmarshal([]byte(probeBody), &probe); err != nil {
		return summary, fmt.Errorf("parse local storage probe response: %w: %s", err, probeBody)
	}
	if probe.Key != "probe/public.txt" || probe.Body != "hello public" || probe.SizeBytes != strconv.Itoa(len("hello public")) {
		return summary, fmt.Errorf("unexpected local storage probe response: %s", probeBody)
	}
	inspectCommand := []string{sceneryPath, "inspect", "storage", "--app-root", fixtureRoot, "-o", "json"}
	inspectOut, inspectErr, err := runHarnessStorageProbeCommandWithEnv(ctx, repoRoot, env, inspectCommand)
	if err != nil {
		return summary, fmt.Errorf("local storage inspect failed: %s\n%s", strings.TrimSpace(err.Error()), tailString(firstNonEmpty(inspectErr, inspectOut), 8192))
	}
	var inspect struct {
		Storage struct {
			Readiness string `json:"readiness"`
			Scope     struct {
				WorktreeKey string `json:"worktree_key"`
				Incarnation string `json:"incarnation"`
			} `json:"scope"`
		} `json:"storage"`
	}
	if err := decodeCLIJSON([]byte(inspectOut), &inspect); err != nil {
		return summary, fmt.Errorf("parse local storage inspect JSON: %w", err)
	}
	if inspect.Storage.Readiness != "ready" || inspect.Storage.Scope.WorktreeKey == "" || inspect.Storage.Scope.Incarnation == "" {
		return summary, fmt.Errorf("local storage inspect not ready: %s", strings.TrimSpace(inspectOut))
	}
	summary["local_storage_probe"] = "passed"
	summary["local_storage_agent_home"] = filepath.ToSlash(agentHome)
	summary["local_storage_response"] = probeBody
	summary["local_storage_readiness"] = inspect.Storage.Readiness
	summary["local_storage_worktree_key"] = inspect.Storage.Scope.WorktreeKey

	// Restart the whole runtime and confirm the fsync'd object survived.
	downCommand := []string{sceneryPath, "down", "--app-root", fixtureRoot, "-o", "json"}
	if downOut, downErr, err := runHarnessStorageProbeCommandWithEnv(ctx, repoRoot, env, downCommand); err != nil {
		return summary, fmt.Errorf("local storage restart proof down failed: %s\n%s", strings.TrimSpace(err.Error()), tailString(firstNonEmpty(downErr, downOut), 8192))
	}
	restartOut, restartErr, err := runHarnessStorageProbeCommandWithEnv(ctx, repoRoot, env, upCommand)
	if err != nil {
		return summary, fmt.Errorf("local storage restart scenery up failed: %s\n%s", strings.TrimSpace(err.Error()), tailString(firstNonEmpty(restartErr, restartOut), 8192))
	}
	restartAPISocket, restartPID, err := harnessDetachInfo(restartOut)
	if restartPID > 0 {
		owners[restartPID] = localagent.CaptureOwner(restartPID, "storage probe")
	}
	if err != nil {
		return summary, err
	}
	restartProbeBody, err := waitForHarnessStorageHTTPProbe(ctx, restartAPISocket, http.MethodGet, 2*time.Minute)
	if err != nil {
		return summary, err
	}
	var restartProbe struct {
		Key       string `json:"key"`
		SizeBytes string `json:"size_bytes"`
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

// harnessDetachInfo extracts the advertised API socket and the detached
// `scenery up` child PID from a scenery.dev.detach payload. The PID is
// returned even when the backend is missing so callers can always record
// the child for direct cleanup.
func harnessDetachInfo(detachJSON string) (string, int, error) {
	var detach struct {
		PID     int `json:"pid"`
		Session struct {
			Backends map[string]localagent.Backend `json:"backends"`
		} `json:"session"`
	}
	if err := decodeCLIJSON([]byte(detachJSON), &detach); err != nil {
		return "", 0, fmt.Errorf("parse storage detach JSON: %w", err)
	}
	backend := detach.Session.Backends[localagent.RouteAPI]
	if backend.Network != "unix" || strings.TrimSpace(backend.Addr) == "" {
		return "", detach.PID, fmt.Errorf("storage detach JSON did not include an advertised Unix API backend")
	}
	return backend.Addr, detach.PID, nil
}

func cleanupHarnessStorageRestartAgent(ctx context.Context, repoRoot, sceneryPath, agentHome string, env []string, owners map[int]localagent.Owner) error {
	psCommand := []string{sceneryPath, "ps", "-o", "json"}
	psOut, _, err := runHarnessStorageProbeCommandWithEnv(ctx, repoRoot, env, psCommand)
	if err == nil {
		var status struct {
			Agent struct {
				PID int `json:"pid"`
			} `json:"agent"`
			Sessions   []localagent.Session   `json:"sessions"`
			Substrates []localagent.Substrate `json:"substrates"`
		}
		if err := decodeCLIJSON([]byte(psOut), &status); err != nil {
			return err
		}
		if status.Agent.PID > 0 {
			owners[status.Agent.PID] = localagent.CaptureOwner(status.Agent.PID, "storage probe")
		}
		harnessCleanupOwnersFromSessions(owners, status.Sessions, localagent.VerifyOwner)
		for _, substrate := range status.Substrates {
			if substrate.OwnerPID > 0 && substrate.Owner.PID == substrate.OwnerPID {
				owners[substrate.OwnerPID] = substrate.Owner
			}
			for name, pid := range substrate.PIDs {
				if pid > 0 && substrate.Owners[name].PID == pid {
					owners[pid] = substrate.Owners[name]
				}
			}
		}
	}
	// Only fingerprint-bearing records can grant cleanup authority after an
	// agent crash. A bare stale PID from agent.json is not sufficient.
	if registry, regErr := localagent.OpenRegistry(filepath.Join(agentHome, "agent", "sessions.json"), localagent.RouterAddrFromEnv()); regErr == nil {
		harnessCleanupOwnersFromSessions(owners, registry.List(), localagent.VerifyOwner)
	}
	return stopHarnessStorageOwners(ctx, owners)
}

func harnessCleanupOwnersFromSessions(owners map[int]localagent.Owner, sessions []localagent.Session, verifyOwner func(localagent.Owner) error) {
	for _, session := range sessions {
		ownerPID := firstPositiveInt(session.OwnerPID, session.Owner.PID)
		if ownerPID > 0 && session.Owner.PID == ownerPID && verifyOwner(session.Owner) == nil {
			owners[ownerPID] = session.Owner
		}
		for _, process := range session.Processes {
			if process.PID > 0 && process.Owner.PID == process.PID && verifyOwner(process.Owner) == nil {
				owners[process.PID] = process.Owner
			}
		}
	}
}

func stopHarnessStorageOwners(ctx context.Context, owners map[int]localagent.Owner) error {
	var result error
	for pid, owner := range owners {
		if pid == os.Getpid() || !processAliveForEdge(pid) {
			continue
		}
		if err := localagent.VerifyOwner(owner); err != nil {
			result = errors.Join(result, fmt.Errorf("refuse unverified storage probe cleanup PID %d: %w", pid, err))
			continue
		}
		proc, err := os.FindProcess(pid)
		if err == nil {
			err = proc.Signal(os.Interrupt)
		}
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			result = errors.Join(result, err)
		}
	}
	for pid, owner := range owners {
		if pid == os.Getpid() || waitForPIDExit(ctx, pid, 500*time.Millisecond) {
			continue
		}
		if err := localagent.VerifyOwner(owner); err != nil {
			result = errors.Join(result, fmt.Errorf("storage probe cleanup lost PID %d ownership: %w", pid, err))
			continue
		}
		proc, err := os.FindProcess(pid)
		if err == nil {
			err = proc.Kill()
		}
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			result = errors.Join(result, err)
		}
		if !waitForPIDExit(ctx, pid, 3*time.Second) {
			result = errors.Join(result, fmt.Errorf("storage probe PID %d survived cleanup", pid))
		}
	}
	return result
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
	defer func() { _ = resp.Body.Close() }()
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
