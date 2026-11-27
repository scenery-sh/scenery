package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
)

// Exercise ordinary malformed supervisor input and a real bare native command
// through the report CLI.
func proveHarnessReportEvidence(parent context.Context, repo string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	cases := map[string]any{}
	for _, name := range []string{"malformed-error", "null-error", "null-data", "bare-native"} {
		for _, format := range []string{"json", "human"} {
			proof, err := proveHarnessReportEvidenceCase(ctx, repo, name, format)
			cases[name+"/"+format] = proof
			if err != nil {
				return map[string]any{"cases": cases}, fmt.Errorf("%s/%s: %w", name, format, err)
			}
		}
	}
	return map[string]any{"cases": cases, "limits": "owned actual native/report processes and synthetic retained supervisor evidence; no production incidence claim"}, nil
}

func proveHarnessReportEvidenceCase(ctx context.Context, repo, name, format string) (proof map[string]any, resultErr error) {
	f, err := newHarnessReportFixture()
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup := os.RemoveAll(f.root)
		_, absence := os.Lstat(f.root)
		if !os.IsNotExist(absence) {
			cleanup = errors.Join(cleanup, fmt.Errorf("report evidence fixture remains: %v", absence))
		}
		resultErr = errors.Join(resultErr, cleanup)
		if proof != nil && cleanup == nil {
			proof["cleanup"] = "owned native/report children exited; private root removed and absence verified"
		}
	}()
	var original []byte
	var native map[string]any
	path := f.log
	if name != "bare-native" {
		var input bytes.Buffer
		var malformed any = map[string]any{"operation_id": "failed", "error": 17}
		switch name {
		case "null-error":
			malformed = map[string]any{"operation_id": "failed", "error": nil}
		case "null-data":
			malformed = nil
		}
		for _, row := range []map[string]any{
			{"type": "build.step", "data": map[string]any{"operation_id": "failed", "name": "build.request", "ok": false, "duration_ms": 0, "reason": "source_rebuild", "started_at": f.at}},
			{"type": "build.error", "data": malformed},
			{"type": "build.error", "data": map[string]any{"operation_id": "failed", "error": "owned expected failure"}},
		} {
			row["time"], row["app"] = f.at, map[string]string{"root": "/owned", "name": "fixture"}
			if err := json.NewEncoder(&input).Encode(map[string]any{"data": row}); err != nil {
				return nil, err
			}
		}
		original = input.Bytes()
		if err := f.write(path, original); err != nil {
			return nil, err
		}
	} else {
		binary := harnessLocalSceneryBinaryPath(repo)
		shell := "'" + strings.ReplaceAll(binary, "'", "'\"'\"'") + "'"
		command := exec.CommandContext(ctx, "/bin/sh", "-c", shell)
		command.Dir, command.WaitDelay = f.root, 2*time.Second
		command.Env = envWithOverrides(envpolicy.Environ(), "HOME="+f.root, "SCENERY_AGENT_HOME="+f.home, "CLAUDECODE=1")
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			return nil, fmt.Errorf("owned bare native command: %w: %s", err, stderr.String())
		}
		data, err := os.ReadFile(f.cli)
		if err != nil {
			return nil, err
		}
		var record cliTelemetryRecord
		if err := json.Unmarshal(bytes.TrimSpace(data), &record); err != nil {
			return nil, err
		}
		if record.Command != "help" || record.ExitCode != 0 || record.InvocationID == "" || record.Producer == nil {
			return nil, fmt.Errorf("bare native help telemetry differs: %v", err)
		}
		path, original = f.cli, data
		native = map[string]any{"shell_pid": command.Process.Pid, "exit_code": 0, "stdout": string(output), "stderr": stderr.String(), "telemetry": record}
	}
	r, proof, err := f.run(ctx, repo, format, nil)
	if proof != nil {
		proof["native"] = native
	}
	if err != nil {
		return proof, err
	}
	if name != "bare-native" {
		retained, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(retained, original) {
			return proof, errors.New("report changed retained evidence input")
		}
	}
	proof["input_unchanged"], proof["input_bytes"] = true, len(original)
	if format == "human" {
		return proof, validateHarnessReportEvidenceHuman(proof["stdout"].(string), name, path, int64(len(original)))
	}
	proof["sources"], proof["builds"], proof["cli"] = r.Sources, r.Builds, r.CLI
	if name != "bare-native" {
		if len(r.Sources.Supervisor) != 1 || r.Sources.SupervisorInvalid != 1 || r.Sources.SupervisorLogsPartial != 0 || r.Sources.SupervisorLogsFailed != 0 {
			return proof, errors.New("malformed-error global coverage differs")
		}
		s := r.Sources.Supervisor[0]
		if s.Path != path || s.Status != "partial" || s.Invalid != 1 || s.Records != 3 || s.InWindow != 3 || !harnessReportExtent(s.SnapshotBytes, s.ReadBytes, int64(len(original))) || r.Builds.Rebuilds.Count != 1 || r.Builds.Rebuilds.FailureCount != 1 || r.Builds.Rebuilds.PercentileSampleCount != 0 || r.Builds.UnmatchedErrors != 0 || len(r.Builds.RebuildFailures) != 1 || r.Builds.RebuildFailures[0].Name != "owned expected failure" || r.Builds.RebuildFailures[0].Count != 1 || len(r.Builds.UnstartedErrors) != 0 {
			return proof, errors.New("malformed-error source or known failure join differs")
		}
	} else {
		if r.CLI.Records != 1 || len(r.CLI.Commands) != 1 || r.CLI.Commands[0].Command != "help" || len(r.CLI.Callers) != 1 || r.CLI.Callers[0].Name != "claude" || r.CLI.Callers[0].Count != 1 {
			return proof, fmt.Errorf("bare native CLI attribution differs: %+v", r.CLI)
		}
	}
	return proof, nil
}
