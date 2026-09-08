package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

func runHarnessDevFollowProbeCheck(parent context.Context, repoRoot string) (summary map[string]any, diagnostics []checkDiagnostic, resultErr error) {
	if runtime.GOOS == "windows" {
		return map[string]any{"proof": "not_applicable_on_windows", "reason": "local agent control uses a Unix socket"}, nil, nil
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-dev-follow-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if resultErr == nil {
			_ = os.RemoveAll(root)
		}
	}()
	appRoot, home := filepath.Join(root, "app"), filepath.Join(root, "agent-home")
	var substrateOwners []localagent.Owner
	if err := copyHarnessBasicFixture(repoRoot, appRoot); err != nil {
		return nil, nil, err
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		_, err := runHarnessAppCLI(cleanupCtx, repoRoot, appRoot, home, "down", "-o", "json")
		resultErr = errors.Join(resultErr, err)
		// Owner SIGKILL deliberately bypasses normal parent shutdown. Retain
		// each observed component fingerprint until all owned children exit.
		for _, owner := range substrateOwners {
			if localagent.VerifyOwner(owner) != nil {
				continue
			}
			process, err := os.FindProcess(owner.PID)
			if err != nil {
				resultErr = errors.Join(resultErr, err)
				continue
			}
			if err := process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
				resultErr = errors.Join(resultErr, err)
			}
			if waitForPIDExit(cleanupCtx, owner.PID, 5*time.Second) {
				continue
			}
			if localagent.VerifyOwner(owner) == nil {
				resultErr = errors.Join(resultErr, process.Kill())
				if !waitForPIDExit(cleanupCtx, owner.PID, 5*time.Second) {
					resultErr = errors.Join(resultErr, fmt.Errorf("owned follower substrate PID %d did not exit", owner.PID))
				}
			}
		}
	}()
	env, err := harnessAppEnvWithVictoria(ctx, repoRoot, home)
	if err != nil {
		return nil, nil, err
	}
	if _, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "up", "--detach", "-o", "json"); err != nil {
		return nil, nil, err
	}
	session, err := harnessLiveSession(ctx, home, appRoot)
	if err != nil {
		return nil, nil, err
	}
	// Use the canonical root returned by the live owner, including macOS's
	// /var -> /private/var alias, for the public log-ownership boundary.
	appRoot = session.AppRoot
	paths, err := localagent.PathsForWorktree(home, appRoot)
	if err != nil {
		return nil, nil, err
	}
	client := localagent.NewClient(paths.Socket)
	defer client.CloseIdleConnections()
	if err := waitForHarnessCondition(ctx, func() bool {
		substrate, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
		if err != nil || substrate.Status != "ready" || len(substrate.PIDs) != 3 {
			return false
		}
		var owners []localagent.Owner
		for name, pid := range substrate.PIDs {
			owner := substrate.Owners[name]
			if owner.PID != pid || localagent.VerifyOwner(owner) != nil {
				return false
			}
			owners = append(owners, owner)
		}
		substrateOwners = owners
		return true
	}); err != nil {
		return nil, nil, err
	}
	outputPath := filepath.Join(root, "follower.log")
	output, err := os.Create(outputPath)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = output.Close() }()
	follower := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), "up", "--app-root", appRoot)
	follower.Dir, follower.Env = appRoot, env
	follower.Stdout, follower.Stderr = output, output
	if err := follower.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- follower.Wait() }()
	reaped := false
	defer func() {
		if !reaped {
			_ = killProcessTree(follower)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				resultErr = errors.Join(resultErr, fmt.Errorf("follower process did not stop"))
			}
		}
	}()
	if err := waitForHarnessCondition(ctx, func() bool {
		data, err := os.ReadFile(outputPath)
		return err == nil && strings.Contains(string(data), "Following the running runtime's logs") && strings.Contains(string(data), "worktree Victoria stack ready")
	}); err != nil {
		return nil, nil, fmt.Errorf("wait for public up follower attachment: %w", err)
	}
	// The preserved assertion is owner-process exit (the original release
	// fixture killed its owner), not observability shutdown ordering. Leave
	// child cleanup to the deferred public down using retained ownership.
	if session.Owner.PID != session.OwnerPID || localagent.VerifyOwner(session.Owner) != nil {
		return nil, nil, fmt.Errorf("follower owner lost its verified process identity")
	}
	owner, err := os.FindProcess(session.OwnerPID)
	if err != nil {
		return nil, nil, err
	}
	if err := owner.Kill(); err != nil {
		return nil, nil, err
	}
	select {
	case err := <-done:
		reaped = true
		if err != nil {
			data, _ := os.ReadFile(outputPath)
			return nil, nil, fmt.Errorf("public up follower: %w: %s", err, tailString(string(data), 8192))
		}
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, nil, err
	}
	if !strings.Contains(string(data), "The running dev runtime stopped") {
		return nil, nil, fmt.Errorf("follower output did not report owner exit: %s", tailString(string(data), 8192))
	}
	return map[string]any{"proof": "public_up_follows_verified_owner_process_exit", "owner_pid": session.OwnerPID, "app_root": appRoot, "agent_home": home, "attached": true, "owner_exit_reported": true}, nil, nil
}

const harnessDevFollowProbeName = "dev runtime follower process probe"

type harnessDevFollowCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessDevFollowProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessDevFollowProbeStepWithCheck(ctx, repoRoot, runHarnessDevFollowProbeCheck)
}

func runHarnessDevFollowProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessDevFollowCheck) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessDevFollowProbeName, Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix dev-runtime owner following, then rerun `go run ./scripts/verify --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}
