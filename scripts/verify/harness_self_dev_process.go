package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
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
	step := harnessStep{Name: harnessDevManagedProcessProbeName, Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix managed-process startup diagnostics and cleanup, then rerun `go run ./scripts/verify --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessDevManagedProcessProbeCheck(parent context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
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
	defer func() { _ = process.Stop(250 * time.Millisecond) }()
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
	if err := process.Stop(250 * time.Millisecond); err != nil {
		return nil, nil, fmt.Errorf("second stop of unready child: %w", err)
	}
	basics, err := runHarnessManagedProcessBasics(parent)
	if err != nil {
		return nil, nil, err
	}
	sharedBuild, err := runHarnessSharedBuildProcessProof(parent, repoRoot)
	if err != nil {
		return nil, nil, err
	}
	detached, err := runHarnessDetachedStartupProbe(parent, repoRoot)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"basic_lifecycle":         basics,
		"shared_build_processes":  sharedBuild,
		"unready_stop_idempotent": true,
		"detached_startup":        detached,
		"proof":                   "real_managed_child_timeout_reported_last_probe_and_output_tail_then_reaped",
		"process_pid":             process.PID,
		"diagnostic":              diagnostic,
	}, nil, nil
}

func runHarnessSharedBuildProcessProof(parent context.Context, repoRoot string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-tags=scenery_build_cache_integration", "./internal/build", "-run=^TestSharedBinaryCrossProcess", "-count=1")
	command.Dir = repoRoot
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("shared build cross-process proof: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return map[string]any{
		"last_subscriber_cancels_producer": true,
		"crashed_lease_reclaimed":          true,
		"link_slots":                       2,
		"oldest_ticket_admission":          true,
		"command":                          "go test -tags=scenery_build_cache_integration ./internal/build -run=^TestSharedBinaryCrossProcess -count=1",
	}, nil
}

// These OS assertions moved from the ordinary process-runner tests. The fast
// roots retain readiness/once-guard coverage with in-process completion signals.
func runHarnessManagedProcessBasics(parent context.Context) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	exited, err := startDevManagedProcess(ctx, devProcessStartRequest{
		Name: "web", Kind: "frontend", Command: "/bin/sh",
		Args: []string{"-c", "echo ready-failed; exit 7"},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = exited.Stop(100 * time.Millisecond) }()
	started := time.Now()
	exitErr := exited.WaitReady(ctx, devProcessReadyRequest{
		Timeout: 5 * time.Second, Interval: 25 * time.Millisecond,
		Probe: func(context.Context) error { return net.ErrClosed },
	})
	if exitErr == nil || time.Since(started) > time.Second ||
		!strings.Contains(exitErr.Error(), "frontend web exited before becoming ready") ||
		!strings.Contains(exitErr.Error(), "ready-failed") {
		return nil, fmt.Errorf("early child exit did not preserve readiness failure and output: %v", exitErr)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer func() { _ = listener.Close() }()
	ready, err := startDevManagedProcess(ctx, devProcessStartRequest{
		Name: "web", Kind: "frontend", Command: "/bin/sh", Args: []string{"-c", "sleep 5"},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = ready.Stop(100 * time.Millisecond) }()
	if err := ready.WaitReady(ctx, devProcessReadyRequest{
		Timeout: time.Second, Interval: 25 * time.Millisecond,
		Probe: func(ctx context.Context) error {
			connection, err := (&net.Dialer{Timeout: 50 * time.Millisecond}).DialContext(ctx, "tcp", listener.Addr().String())
			if err != nil {
				return err
			}
			return connection.Close()
		},
	}); err != nil {
		return nil, fmt.Errorf("caller readiness probe success: %w", err)
	}
	if err := ready.Stop(100 * time.Millisecond); err != nil {
		return nil, err
	}
	if err := ready.Stop(100 * time.Millisecond); err != nil {
		return nil, fmt.Errorf("second stop of ready child: %w", err)
	}
	return map[string]any{
		"early_exit_and_output":       true,
		"caller_probe_success":        true,
		"ready_child_stop_idempotent": true,
		"early_exit_pid":              exited.PID,
		"ready_child_pid":             ready.PID,
	}, nil
}
