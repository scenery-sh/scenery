package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"scenery.sh/internal/devprocess"
	"scenery.sh/internal/envpolicy"
)

const harnessDevNamedLockProbeName = "dev named-lock process probe"

type harnessDevNamedLockCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessDevNamedLockProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessDevNamedLockProbeStepWithCheck(ctx, repoRoot, runHarnessDevNamedLockProbeCheck)
}

func runHarnessDevNamedLockProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessDevNamedLockCheck) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessDevNamedLockProbeName, Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix cross-process dev named-lock contention, then rerun `go run ./scripts/verify --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessDevNamedLockProbeCheck(ctx context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	if runtime.GOOS == "windows" {
		return map[string]any{"proof": "not_applicable_on_windows", "reason": "process helper uses Unix advisory locking"}, nil, nil
	}
	root, err := os.MkdirTemp("", "scenery-dev-lock-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	sourcePath := filepath.Join(root, "holder.go")
	binaryPath := filepath.Join(root, "holder")
	readyPath := filepath.Join(root, "ready")
	const source = `package main

import (
	"os"
	"syscall"
	"time"
)

func main() {
	file, err := os.OpenFile(os.Args[1], os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil { panic(err) }
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil { panic(err) }
	if err := os.WriteFile(os.Args[2], []byte("ready"), 0o600); err != nil { panic(err) }
	for { time.Sleep(time.Hour) }
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		return nil, nil, err
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, sourcePath)
	build.Env = envWithOverrides(envpolicy.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("build named-lock holder: %w: %s", err, strings.TrimSpace(string(output)))
	}
	lockPath := filepath.Join(root, "substrate-postgres.lock")
	holder := exec.CommandContext(ctx, binaryPath, lockPath, readyPath)
	holder.Stdout = io.Discard
	holder.Stderr = io.Discard
	if err := holder.Start(); err != nil {
		return nil, nil, err
	}
	holderDone := false
	defer func() {
		if !holderDone {
			_ = holder.Process.Kill()
			_ = holder.Wait()
		}
	}()
	if err := waitForHarnessCondition(ctx, func() bool {
		_, err := os.Stat(readyPath)
		return err == nil
	}); err != nil {
		return nil, nil, err
	}

	var warnings bytes.Buffer
	options := devprocess.LockOptions{RetryInterval: 10 * time.Millisecond, WarnAfter: 20 * time.Millisecond, WarnRepeat: 40 * time.Millisecond, Timeout: 120 * time.Millisecond, Warnings: &warnings}
	unlock, lockErr := devprocess.LockSubstrate(root, "postgres", options)
	if unlock != nil {
		unlock()
		return nil, nil, fmt.Errorf("contended cross-process lock unexpectedly succeeded")
	}
	if lockErr == nil || !strings.Contains(lockErr.Error(), "timed out waiting for shared substrate postgres lock") || !strings.Contains(warnings.String(), "waiting for shared substrate postgres lock at") {
		return nil, nil, fmt.Errorf("contended lock warning/error = %q / %v", warnings.String(), lockErr)
	}
	if err := holder.Process.Kill(); err != nil {
		return nil, nil, err
	}
	_ = holder.Wait()
	holderDone = true
	unlocked, err := devprocess.LockSubstrate(root, "postgres", options)
	if err != nil {
		return nil, nil, fmt.Errorf("lock after holder exit: %w", err)
	}
	// The same-process real OS boundary lives here, alongside cross-process
	// contention. The ordinary root uses an in-process lock double.
	type acquisition struct {
		unlock func()
		err    error
	}
	acquired := make(chan acquisition, 1)
	go func() {
		release, err := devprocess.LockSubstrate(root, "postgres", options)
		acquired <- acquisition{release, err}
	}()
	select {
	case result := <-acquired:
		unlocked()
		if result.unlock != nil {
			result.unlock()
		}
		return nil, nil, fmt.Errorf("same-process lock acquired before release: %v", result.err)
	case <-time.After(30 * time.Millisecond):
	}
	unlocked()
	select {
	case result := <-acquired:
		if result.err != nil {
			return nil, nil, result.err
		}
		result.unlock()
	case <-time.After(time.Second):
		return nil, nil, fmt.Errorf("same-process lock did not acquire after release")
	}
	return map[string]any{
		"proof":                   "real_cross_process_advisory_lock_timeout_warning_and_recovery_verified",
		"warning_emitted":         true,
		"named_timeout":           true,
		"acquired_after_release":  true,
		"same_process_serialized": true,
	}, nil, nil
}
