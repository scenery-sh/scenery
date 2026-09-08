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

	localagent "scenery.sh/internal/agent"

	"scenery.sh/internal/desktop"
	"scenery.sh/internal/envpolicy"
)

const harnessDesktopProcessProbeName = "desktop shell process probe"

type harnessDesktopProcessCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessDesktopProcessProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessDesktopProcessProbeStepWithCheck(ctx, repoRoot, runHarnessDesktopProcessProbeCheck)
}

func runHarnessDesktopProcessProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessDesktopProcessCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessDesktopProcessProbeName,
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
				SuggestedAction: "Fix the desktop shell process/agent boundary, then rerun `go run ./scripts/verify --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessDesktopRunnerBoundary(ctx context.Context, root string) (map[string]any, error) {
	commandPath := filepath.Join(root, "desktop-command")
	if err := writeHarnessDesktopFile(commandPath, `#!/bin/sh
printf 'stdout:%s:%s:%s\n' "$PWD" "$1" "$DESKTOP_TEST_VALUE"
printf 'stderr:desktop failed\n' >&2
exit 7
`, 0o755); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	runErr := desktop.Run(ctx, desktop.Command{Path: commandPath, Args: []string{"build"}, Dir: root}, envWithOverrides(envpolicy.Environ(), "DESKTOP_TEST_VALUE=runner-env"), &output)
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 7 {
		return nil, fmt.Errorf("desktop runner error = %v, want child exit 7", runErr)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	for _, want := range []string{
		"stdout:" + resolvedRoot + ":build:runner-env",
		"stderr:desktop failed",
	} {
		if !strings.Contains(output.String(), want) || !strings.Contains(runErr.Error(), want) {
			return nil, fmt.Errorf("desktop runner output or error missing %q: output=%q error=%v", want, output.String(), runErr)
		}
	}
	return map[string]any{"exit_code": exitErr.ExitCode(), "combined_streams": true, "error_tail": true}, nil
}

func waitForHarnessAgentHealth(ctx context.Context, client *localagent.Client) error {
	return waitForHarnessCondition(ctx, func() bool {
		_, err := client.Health(ctx)
		return err == nil
	})
}

func waitForHarnessCondition(ctx context.Context, condition func() bool) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func writeHarnessDesktopFile(path, body string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), mode)
}
