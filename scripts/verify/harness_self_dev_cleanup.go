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
	step := harnessStep{Name: harnessDevSessionCleanupProbeName, Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix dev-session process discovery and cleanup, then rerun `go run ./scripts/verify --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessDevSessionCleanupProbeCheck(parent context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	if runtime.GOOS == "windows" {
		return map[string]any{"proof": "not_applicable_on_windows", "reason": "recorded ownership uses Unix process identities and groups"}, nil, nil
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-dev-session-cleanup-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()

	// A process that looks exactly like a session child (its executable lives
	// under the session state root and is named scenery-app-*) but that no
	// session recorded belongs to someone else and must survive every cleanup.
	stateRoot := filepath.Join(root, "app", ".scenery", "sessions", "review-a")
	lookalike, err := startHarnessStateRootAppProcess(ctx, stateRoot)
	if err != nil {
		return nil, nil, err
	}
	defer reapHarnessCommand(lookalike)
	unrecorded := localagent.Session{SessionID: "review-a", AppRoot: filepath.Join(root, "app"), StateRoot: stateRoot}
	if err := stopDeletedSessionProcesses(ctx, unrecorded); err != nil {
		return nil, nil, err
	}
	current := localagent.Session{
		SessionID: "review-a",
		AppRoot:   filepath.Join(root, "app"),
		StateRoot: stateRoot,
		OwnerPID:  os.Getpid(),
		Owner:     localagent.CurrentOwner("harness cleanup"),
	}
	if err := cleanupStaleDevSessionProcesses(ctx, current, []localagent.Session{unrecorded}); err != nil {
		return nil, nil, err
	}
	if _, alive := inspectProcess(lookalike.Process.Pid); !alive {
		return nil, nil, fmt.Errorf("unrecorded look-alike session process %d was stopped", lookalike.Process.Pid)
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
	changed, changedOwner, err := startHarnessOwnedSleepProcess(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer reapHarnessCommand(changed)
	// A record whose identity no longer matches the live process names a
	// process that ended; the PID now belongs to another process.
	changedOwner.StartedAt = "Thu Jan  1 00:00:00 1970"
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
			"worker":  {PID: registeredStale.Process.Pid, Owner: registeredStaleOwner},
			"changed": {PID: changed.Process.Pid, Owner: changedOwner},
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
	if _, alive := inspectProcess(changed.Process.Pid); !alive {
		return nil, nil, fmt.Errorf("process %d whose recorded identity no longer verifies was stopped", changed.Process.Pid)
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
		"proof":                      "only_verified_recorded_processes_signaled",
		"lookalike_process_pid":      lookalike.Process.Pid,
		"lookalike_process_alive":    true,
		"registered_child_pid":       registeredStale.Process.Pid,
		"unrelated_registered_alive": true,
		"changed_identity_pid":       changed.Process.Pid,
		"changed_identity_alive":     true,
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
		if info, ok := inspectProcess(cmd.Process.Pid); ok && strings.Contains(filepath.ToSlash(info.Command), wantRoot+"/") && strings.Contains(info.Command, "scenery-app") {
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
