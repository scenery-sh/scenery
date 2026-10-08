package main

import (
	"bytes"
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
	step := harnessStep{Name: harnessCLIProcessProbeName, Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix CLI process exit codes or telemetry persistence, then rerun `go run ./scripts/verify --release --summary --write`.",
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
		missingApp  bool
	}{
		{name: "success", wantCommand: "help"},
		{name: "invalid_usage", args: []string{"not-a-command"}, wantExit: 2, wantCommand: "unknown"},
		{name: "private_operand", args: []string{"task", "private-task-token"}, wantExit: 2, wantCommand: "task"},
		{name: "missing_resource", args: []string{"get", "missing/operation/nope", "--app-root", filepath.Join(repoRoot, "internal", "compiler", "testdata", "native")}, wantExit: 2, wantCommand: "get"},
		{name: "missing_app", missingApp: true, wantExit: 2, wantCommand: "compile"},
		{name: "semantic_status", args: []string{"status", "-o", "json"}, wantExit: 2, wantCommand: "unknown"},
	}
	verified := make([]string, 0, len(cases))
	for _, test := range cases {
		home, err := os.MkdirTemp("", "scenery-cli-process-probe-*")
		if err != nil {
			return nil, nil, err
		}
		args := test.args
		if test.missingApp {
			args = []string{"compile", "--app-root", home, "-o", "json"}
		}
		command := exec.CommandContext(ctx, binary, args...)
		command.Dir = repoRoot
		command.Env = envWithOverrides(envpolicy.Environ(), "HOME="+home, "SCENERY_AGENT_HOME="+filepath.Join(home, "private-agent"))
		output, runErr := command.Output()
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
		data, err := os.ReadFile(filepath.Join(home, "private-agent", "telemetry.jsonl"))
		_, leaked := os.Stat(filepath.Join(home, ".scenery", "telemetry.jsonl"))
		_ = os.RemoveAll(home)
		if !os.IsNotExist(leaked) {
			return nil, nil, fmt.Errorf("%s telemetry escaped the injected agent home", test.name)
		}
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
		if record.Command != test.wantCommand || record.ExitCode != test.wantExit || record.InvocationID == "" || record.Producer == nil || (test.wantExit != 0 && record.DiagnosticCode == "") {
			return nil, nil, fmt.Errorf("%s telemetry = %+v", test.name, record)
		}
		purpose := envpolicy.Get("SCENERY_EXECUTION_PURPOSE")
		if purpose == "" {
			purpose = "unknown"
		}
		if record.Purpose != purpose || record.Version != record.Producer.Version {
			return nil, nil, fmt.Errorf("%s command purpose/version does not match its lane/producer", test.name)
		}
		if test.missingApp && (record.DiagnosticCode != "SCN8001" || bytes.Contains(output, []byte("report_token"))) {
			return nil, nil, fmt.Errorf("missing app created an internal report: %s", output)
		}
		if test.name == "semantic_status" && !bytes.Contains(output, []byte("scenery ps -o json")) {
			return nil, nil, fmt.Errorf("status misuse omitted the current inspection command: %s", output)
		}
		verified = append(verified, test.name)
	}
	return map[string]any{
		"proof":          "real_cli_process_exit_codes_and_per_process_telemetry_verified",
		"verified_cases": verified,
	}, nil, nil
}
