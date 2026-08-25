package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

const harnessDevManagedProcessProbeName = "dev managed process startup probe"

type harnessDevManagedProcessCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessDevManagedProcessProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessDevManagedProcessProbeStepWithCheck(ctx, repoRoot, runHarnessDevManagedProcessProbeCheck)
}

func runHarnessDevManagedProcessProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessDevManagedProcessCheck) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessDevManagedProcessProbeName, Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix managed-process startup diagnostics and cleanup, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessDevManagedProcessProbeCheck(parent context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	outputSeen := make(chan struct{}, 1)
	process, err := startDevManagedProcess(ctx, devProcessStartRequest{
		Name:    "web",
		Kind:    "frontend",
		Command: "/bin/sh",
		Args:    []string{"-c", "echo still-starting; sleep 30"},
		OnOutput: func(_ int, _ string, _ []byte) {
			select {
			case outputSeen <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		return nil, nil, err
	}
	defer process.Stop(250 * time.Millisecond)
	select {
	case <-outputSeen:
	case <-ctx.Done():
		return nil, nil, fmt.Errorf("wait for managed-process startup output: %w", ctx.Err())
	}
	err = process.WaitReady(ctx, devProcessReadyRequest{
		Timeout:  50 * time.Millisecond,
		Interval: 10 * time.Millisecond,
		Probe: func(context.Context) error {
			return os.ErrNotExist
		},
	})
	if err == nil {
		return nil, nil, fmt.Errorf("managed process unexpectedly became ready")
	}
	diagnostic := err.Error()
	if !strings.Contains(diagnostic, "file does not exist") || !strings.Contains(diagnostic, "still-starting") {
		return nil, nil, fmt.Errorf("startup timeout omitted last probe or output tail: %s", diagnostic)
	}
	if err := process.Stop(250 * time.Millisecond); err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"proof":       "real_managed_child_timeout_reported_last_probe_and_output_tail_then_reaped",
		"process_pid": process.PID,
		"diagnostic":  diagnostic,
	}, nil, nil
}
