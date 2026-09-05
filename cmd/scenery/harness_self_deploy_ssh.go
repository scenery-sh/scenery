package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
)

const harnessDeploySSHProcessProbeName = "deploy SSH process probe"

type harnessDeploySSHProcessCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessDeploySSHProcessProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessDeploySSHProcessProbeStepWithCheck(ctx, repoRoot, runHarnessDeploySSHProcessProbeCheck)
}

func runHarnessDeploySSHProcessProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessDeploySSHProcessCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessDeploySSHProcessProbeName,
		Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the deploy SSH child-process exit boundary, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessDeploySSHProcessProbeCheck(_ context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	root, err := os.MkdirTemp("", "scenery-deploy-ssh-process-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	appRoot := filepath.Join(root, "app with spaces")
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		return nil, nil, err
	}
	ssh := filepath.Join(root, "ssh")
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\nprintf 'preflight output\\n'\nexit \"${DEPLOY_PREFLIGHT_EXIT:-0}\"\n"), 0o755); err != nil {
		return nil, nil, err
	}
	tools := deploySSHTools{SSH: ssh, Rsync: "/unused/rsync", Env: append(envpolicy.Environ(), "DEPLOY_PREFLIGHT_EXIT=7")}
	var stdout bytes.Buffer
	err = runDeploySSHCommands(&stdout, appRoot, "basicapp", "some-id", "production", false, tools)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 || cliExitCode(err) != 7 {
		return nil, nil, fmt.Errorf("deploy SSH error = %v, want real child exit 7", err)
	}
	if got := stdout.String(); got != "preflight output\n" {
		return nil, nil, fmt.Errorf("deploy SSH stdout = %q", got)
	}
	return map[string]any{
		"proof":     "real_child_stdout_streamed_and_exit_code_preserved",
		"exit_code": exitErr.ExitCode(),
		"stdout":    strings.TrimSpace(stdout.String()),
	}, nil, nil
}
