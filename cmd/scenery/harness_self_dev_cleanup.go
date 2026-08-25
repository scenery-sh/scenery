package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

const harnessDevSessionCleanupProbeName = "dev session process cleanup probe"

type harnessDevSessionCleanupCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessDevSessionCleanupProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessDevSessionCleanupProbeStepWithCheck(ctx, repoRoot, runHarnessDevSessionCleanupProbeCheck)
}

func runHarnessDevSessionCleanupProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessDevSessionCleanupCheck) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessDevSessionCleanupProbeName, Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix dev-session process discovery and cleanup, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessDevSessionCleanupProbeCheck(parent context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	if runtime.GOOS == "windows" {
		return map[string]any{"proof": "not_applicable_on_windows", "reason": "process discovery uses ps and Unix process groups"}, nil, nil
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-dev-session-cleanup-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(root)
	stateRoot := filepath.Join(root, "app", ".scenery", "sessions", "review-a")
	otherStateRoot := filepath.Join(root, "app", ".scenery", "sessions", "review-b")
	stale, err := startHarnessStateRootAppProcess(ctx, stateRoot)
	if err != nil {
		return nil, nil, err
	}
	defer reapHarnessCommand(stale)
	other, err := startHarnessStateRootAppProcess(ctx, otherStateRoot)
	if err != nil {
		return nil, nil, err
	}
	defer reapHarnessCommand(other)

	session := localagent.Session{SessionID: "review-a", AppRoot: filepath.Join(root, "app"), StateRoot: stateRoot}
	if err := stopDeletedSessionProcesses(ctx, session); err != nil {
		return nil, nil, err
	}
	if !waitForPIDExit(ctx, stale.Process.Pid, 2*time.Second) {
		return nil, nil, fmt.Errorf("matched state-root process %d survived cleanup", stale.Process.Pid)
	}
	if _, alive := inspectProcess(other.Process.Pid); !alive {
		return nil, nil, fmt.Errorf("unrelated state-root process %d was stopped", other.Process.Pid)
	}
	cleanupStateRoot := filepath.Join(root, "app", ".scenery", "sessions", "cleanup-current")
	cleanupStale, err := startHarnessStateRootAppProcess(ctx, cleanupStateRoot)
	if err != nil {
		return nil, nil, err
	}
	defer reapHarnessCommand(cleanupStale)
	current := localagent.Session{
		SessionID: "cleanup-current",
		AppRoot:   filepath.Join(root, "app"),
		StateRoot: cleanupStateRoot,
		OwnerPID:  os.Getpid(),
		Owner:     localagent.CurrentOwner("harness cleanup"),
	}
	if err := cleanupStaleDevSessionProcesses(ctx, current, nil); err != nil {
		return nil, nil, err
	}
	if !waitForPIDExit(ctx, cleanupStale.Process.Pid, 2*time.Second) {
		return nil, nil, fmt.Errorf("stale-session state-root process %d survived cleanup", cleanupStale.Process.Pid)
	}
	if _, alive := inspectProcess(other.Process.Pid); !alive {
		return nil, nil, fmt.Errorf("stale-session cleanup stopped unrelated state-root process %d", other.Process.Pid)
	}
	registeredStale, registeredStaleOwner, err := startHarnessOwnedSleepProcess(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer reapHarnessCommand(registeredStale)
	registeredOther, registeredOtherOwner, err := startHarnessOwnedSleepProcess(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer reapHarnessCommand(registeredOther)
	registeredRoot := filepath.Join(root, "registered-app")
	registeredCurrent := localagent.Session{
		SessionID: "registered-a",
		AppRoot:   registeredRoot,
		OwnerPID:  os.Getpid(),
		Owner:     localagent.CurrentOwner("harness registered cleanup"),
	}
	registeredPrevious := localagent.Session{
		SessionID: "registered-a",
		AppRoot:   registeredRoot,
		OwnerPID:  os.Getpid(),
		Processes: map[string]localagent.Process{
			"worker": {PID: registeredStale.Process.Pid, Owner: registeredStaleOwner},
		},
	}
	registeredUnrelated := localagent.Session{
		SessionID: "registered-b",
		AppRoot:   registeredRoot,
		OwnerPID:  os.Getpid(),
		Owner:     localagent.CurrentOwner("harness unrelated cleanup"),
		Processes: map[string]localagent.Process{
			"worker": {PID: registeredOther.Process.Pid, Owner: registeredOtherOwner},
		},
	}
	if err := cleanupStaleDevSessionProcesses(ctx, registeredCurrent, []localagent.Session{registeredPrevious, registeredUnrelated}); err != nil {
		return nil, nil, err
	}
	if !waitForPIDExit(ctx, registeredStale.Process.Pid, 2*time.Second) {
		return nil, nil, fmt.Errorf("same-session registered child %d survived cleanup", registeredStale.Process.Pid)
	}
	if _, alive := inspectProcess(registeredOther.Process.Pid); !alive {
		return nil, nil, fmt.Errorf("different-session registered child %d was stopped", registeredOther.Process.Pid)
	}
	owner, ownerIdentity, err := startHarnessOwnedSleepProcess(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer reapHarnessCommand(owner)
	ownerSession := localagent.Session{
		SessionID: "owner-session",
		AppRoot:   filepath.Join(root, "owner-app"),
		OwnerPID:  owner.Process.Pid,
		Owner:     ownerIdentity,
	}
	if err := stopDeletedSessionProcesses(ctx, ownerSession); err != nil {
		return nil, nil, err
	}
	if !waitForPIDExit(ctx, owner.Process.Pid, 2*time.Second) {
		return nil, nil, fmt.Errorf("verified session owner %d survived cleanup", owner.Process.Pid)
	}
	return map[string]any{
		"proof":                      "real_owner_and_ps_discovered_orphan_processes_signaled_and_reaped",
		"matched_process_pid":        stale.Process.Pid,
		"stale_cleanup_process_pid":  cleanupStale.Process.Pid,
		"unrelated_process_pid":      other.Process.Pid,
		"unrelated_process_alive":    true,
		"registered_child_pid":       registeredStale.Process.Pid,
		"unrelated_registered_alive": true,
		"owner_process_pid":          owner.Process.Pid,
	}, nil, nil
}

func startHarnessOwnedSleepProcess(ctx context.Context) (*exec.Cmd, localagent.Owner, error) {
	cmd := exec.CommandContext(ctx, "/bin/sleep", "30")
	configureChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, localagent.Owner{}, err
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		owner := localagent.CaptureOwner(cmd.Process.Pid, "harness dev-session owner")
		if filepath.Base(owner.Exe) == "sleep" && localagent.VerifyOwner(owner) == nil {
			return cmd, owner, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	reapHarnessCommand(cmd)
	return nil, localagent.Owner{}, fmt.Errorf("sleep process did not expose a verifiable owner identity")
}

func startHarnessStateRootAppProcess(ctx context.Context, stateRoot string) (*exec.Cmd, error) {
	appPath := filepath.Join(stateRoot, "run", "app", "scenery-app-harness")
	if err := os.MkdirAll(filepath.Dir(appPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(appPath, []byte("#!/bin/sh\nsleep \"$@\"\n"), 0o755); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, appPath, "30")
	configureChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	wantRoot := filepath.ToSlash(cleanAbsPath(stateRoot))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if info, ok := inspectProcess(cmd.Process.Pid); ok && commandMatchesSessionStateRoot(info.cmd, wantRoot) {
			return cmd, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	reapHarnessCommand(cmd)
	return nil, fmt.Errorf("state-root process did not expose matching command path")
}

func reapHarnessCommand(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = killProcessTree(cmd)
	_ = cmd.Wait()
}
