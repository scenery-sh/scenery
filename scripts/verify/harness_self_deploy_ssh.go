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
		Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary"},
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
				SuggestedAction: "Fix the deploy SSH child-process exit boundary, then rerun `go run ./scripts/verify --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessDeploySSHProcessProbeCheck(ctx context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
	root, err := os.MkdirTemp("", "scenery-deploy-ssh-process-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	appRoot := filepath.Join(root, "app with spaces")
	if err := copyHarnessNativeContractFixture(repoRoot, appRoot); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(appRoot, ".scenery.json"), []byte(`{"name":"nativeapp","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["probe.invalid"]}}}}`), 0o600); err != nil {
		return nil, nil, err
	}
	var generated map[string]any
	if err := readProductJSON(repoRoot, &generated, "generate", "--app-root", appRoot, "-o", "json"); err != nil {
		return nil, nil, err
	}
	ssh := filepath.Join(root, "ssh")
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\nprintf 'preflight output\\n'\nexit 7\n"), 0o755); err != nil {
		return nil, nil, err
	}
	// PATH contains an owned failing SSH double. No connection or rsync can run.
	cmd := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), "deploy", "--env", "production", "--app-root", appRoot)
	cmd.Env = envWithOverrides(envpolicy.Environ(), "PATH="+root+string(os.PathListSeparator)+envpolicy.Get("PATH"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		return nil, nil, fmt.Errorf("deploy SSH error = %v, want real child exit 7: %s %s", err, stderr.String(), stdout.String())
	}
	if got := stdout.String(); !strings.HasSuffix(got, "preflight output\n") || strings.Count(got, "preflight output\n") != 1 {
		return nil, nil, fmt.Errorf("deploy SSH stdout = %q", got)
	}
	return map[string]any{
		"proof":     "real_child_stdout_streamed_and_exit_code_preserved",
		"exit_code": exitErr.ExitCode(),
		"stdout":    strings.TrimSpace(stdout.String()),
	}, nil, nil
}
