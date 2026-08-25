package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
)

const harnessCLIProcessProbeName = "CLI process exit and telemetry probe"

type harnessCLIProcessCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessCLIProcessProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessCLIProcessProbeStepWithCheck(ctx, repoRoot, runHarnessCLIProcessProbeCheck)
}

func runHarnessCLIProcessProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessCLIProcessCheck) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessCLIProcessProbeName, Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix CLI process exit codes or telemetry persistence, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessCLIProcessProbeCheck(ctx context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
	binary := harnessLocalSceneryBinaryPath(repoRoot)
	cases := []struct {
		name        string
		args        []string
		wantExit    int
		wantCommand string
	}{
		{name: "success", wantCommand: "help"},
		{name: "invalid_usage", args: []string{"not-a-command"}, wantExit: 2, wantCommand: "not-a-command"},
		{name: "missing_resource", args: []string{"get", "missing/operation/nope", "--app-root", filepath.Join(repoRoot, "internal", "compiler", "testdata", "native")}, wantExit: 2, wantCommand: "get"},
	}
	verified := make([]string, 0, len(cases))
	for _, test := range cases {
		home, err := os.MkdirTemp("", "scenery-cli-process-probe-*")
		if err != nil {
			return nil, nil, err
		}
		command := exec.CommandContext(ctx, binary, test.args...)
		command.Dir = repoRoot
		command.Env = envWithOverrides(envpolicy.Environ(), "HOME="+home)
		runErr := command.Run()
		exitCode := 0
		if runErr != nil {
			var exitErr *exec.ExitError
			if !errors.As(runErr, &exitErr) {
				_ = os.RemoveAll(home)
				return nil, nil, runErr
			}
			exitCode = exitErr.ExitCode()
		}
		if exitCode != test.wantExit {
			_ = os.RemoveAll(home)
			return nil, nil, fmt.Errorf("%s CLI exit = %d, want %d", test.name, exitCode, test.wantExit)
		}
		data, err := os.ReadFile(filepath.Join(home, ".scenery", "telemetry.jsonl"))
		_ = os.RemoveAll(home)
		if err != nil {
			return nil, nil, err
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) != 1 {
			return nil, nil, fmt.Errorf("%s telemetry records = %d, want 1", test.name, len(lines))
		}
		var record cliTelemetryRecord
		if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
			return nil, nil, err
		}
		if record.Command != test.wantCommand || record.ExitCode != test.wantExit {
			return nil, nil, fmt.Errorf("%s telemetry = %+v", test.name, record)
		}
		verified = append(verified, test.name)
	}
	return map[string]any{
		"proof":          "real_cli_process_exit_codes_and_per_process_telemetry_verified",
		"verified_cases": verified,
	}, nil, nil
}
