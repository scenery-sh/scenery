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

// Exercise ordinary malformed supervisor input and a real bare native command
// through disposable transcript fixtures and the actual opt-in report CLI.
func proveHarnessReportEvidence(parent context.Context, repo string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	cases := map[string]any{}
	for _, name := range []string{"malformed-error", "bare-claude", "bare-codex"} {
		for _, format := range []string{"json", "human"} {
			proof, err := proveHarnessReportEvidenceCase(ctx, repo, name, format)
			cases[name+"/"+format] = proof
			if err != nil {
				return map[string]any{"cases": cases}, fmt.Errorf("%s/%s: %w", name, format, err)
			}
		}
	}
	return map[string]any{"cases": cases, "limits": "owned actual native/report processes and synthetic retained supervisor/transcript evidence; no real agent UI or production incidence claim"}, nil
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
	var elapsed int64
	path := f.log
	transcripts := name != "malformed-error"
	if !transcripts {
		var input bytes.Buffer
		for _, row := range []map[string]any{
			{"type": "build.step", "data": map[string]any{"operation_id": "failed", "name": "build.request", "ok": false, "duration_ms": 0, "reason": "source_rebuild", "started_at": f.at}},
			{"type": "build.error", "data": map[string]any{"operation_id": "failed", "error": 17}},
			{"type": "build.error", "data": map[string]any{"operation_id": "failed", "error": "owned expected failure"}},
		} {
			row["time"], row["app"] = f.at, map[string]string{"root": "/owned", "name": "fixture"}
			if err := json.NewEncoder(&input).Encode(map[string]any{"data": row}); err != nil {
				return nil, err
			}
		}
		original = input.Bytes()
	} else {
		binary := harnessLocalSceneryBinaryPath(repo)
		// Quote the only shell word, the absolute prepared executable; no args.
		shell := "'" + strings.ReplaceAll(binary, "'", "'\"'\"'") + "'"
		command := exec.CommandContext(ctx, "/bin/sh", "-c", shell)
		command.Dir, command.WaitDelay = f.root, 2*time.Second
		command.Env = envWithOverrides(envpolicy.Environ(), "HOME="+f.root, "SCENERY_AGENT_HOME="+f.home)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		started := time.Now().UTC()
		output, err := command.Output()
		finished := time.Now().UTC()
		if err != nil {
			return nil, fmt.Errorf("owned bare native command: %w: %s", err, stderr.String())
		}
		elapsed = finished.Sub(started).Milliseconds()
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
		native = map[string]any{"shell_pid": command.Process.Pid, "exit_code": 0, "stdout": string(output), "stderr": stderr.String(), "telemetry": record, "started_at": started, "finished_at": finished}
		var rows []any
		if name == "bare-claude" {
			path = filepath.Join(f.root, ".claude", "projects", "owned", "session.jsonl")
			rows = []any{
				map[string]any{"type": "assistant", "timestamp": started, "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "bare", "name": "Bash", "input": map[string]any{"command": shell}}}}},
				map[string]any{"type": "user", "timestamp": finished, "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "bare", "is_error": false, "content": string(output)}}}},
			}
		} else {
			path = filepath.Join(f.root, ".codex", "sessions", "owned", "session.jsonl")
			arguments, err := json.Marshal(map[string]string{"cmd": shell})
			if err != nil {
				return nil, err
			}
			rows = []any{
				map[string]any{"type": "response_item", "timestamp": started, "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "bare", "arguments": string(arguments)}},
				map[string]any{"type": "response_item", "timestamp": finished, "payload": map[string]any{"type": "function_call_output", "call_id": "bare", "output": "Process exited with code 0\nOutput:\n" + string(output)}},
			}
		}
		var input bytes.Buffer
		for _, row := range rows {
			if err := json.NewEncoder(&input).Encode(row); err != nil {
				return nil, err
			}
		}
		original = input.Bytes()
	}
	if err := f.write(path, original); err != nil {
		return nil, err
	}
	r, proof, err := f.run(ctx, repo, format, transcripts, nil)
	if proof != nil {
		proof["native"] = native
	}
	if err != nil {
		return proof, err
	}
	retained, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(retained, original) {
		return proof, errors.New("report changed retained evidence input")
	}
	proof["input_unchanged"], proof["input_bytes"] = true, len(original)
	if format == "human" {
		output := proof["stdout"].(string)
		if !transcripts {
			want := fmt.Sprintf("  Supervisor %s (partial): read %d / captured %d bytes across 1 segments; retained 3, in window 3, invalid 1.\n", path, len(original), len(original))
			if !strings.Contains(output, want) || !strings.Contains(output, "0 supervisor logs unreadable or read in part; 1 event records with invalid evidence (known outcomes retained).\n") || !strings.Contains(output, "owned expected failure") {
				return proof, errors.New("human malformed-error source or preserved failure differs")
			}
		} else {
			claude, codex := 0, 1
			if name == "bare-claude" {
				claude, codex = 1, 0
			}
			if !strings.Contains(output, fmt.Sprintf("%d Claude Code and %d Codex sessions that ran Scenery.", claude, codex)) || !strings.Contains(output, "Agents: 1 tool calls, 0 errors; 1 shell commands ran Scenery (1 invocations): 1 recorded Scenery's own outcome (0 failed); as shell commands 0 failed and 0 recorded no outcome\n") {
				return proof, errors.New("human bare command session/outcome differs")
			}
			found := false
			for _, line := range strings.Split(output, "\n") {
				found = found || strings.HasPrefix(strings.Join(strings.Fields(line), " "), "help 1 attributable 1 failed 0 waited ")
			}
			if !found {
				return proof, errors.New("human bare command identity differs")
			}
		}
		return proof, nil
	}
	proof["sources"], proof["builds"], proof["agents"] = r.Sources, r.Builds, r.Agents
	if !transcripts {
		if len(r.Sources.Supervisor) != 1 || r.Sources.SupervisorInvalid != 1 || r.Sources.SupervisorLogsPartial != 0 || r.Sources.SupervisorLogsFailed != 0 {
			return proof, errors.New("malformed-error global coverage differs")
		}
		s := r.Sources.Supervisor[0]
		if s.Path != path || s.Status != "partial" || s.Invalid != 1 || !harnessReportExtent(s.SnapshotBytes, s.ReadBytes, int64(len(original))) || r.Builds.Rebuilds.Count != 1 || r.Builds.Rebuilds.FailureCount != 1 || r.Builds.UnmatchedErrors != 0 || len(r.Builds.RebuildFailures) != 1 || r.Builds.RebuildFailures[0].Name != "owned expected failure" {
			return proof, errors.New("malformed-error source or known failure join differs")
		}
	} else {
		a := r.Agents
		if a == nil || a.ClaudeSessions+a.CodexSessions != 1 || a.SceneryCommands != 1 || a.SceneryInvocations != 1 || a.SceneryAttributable != 1 || a.SceneryOutcomeUnknown != 0 || a.SceneryFailed != 0 || len(a.Commands) != 1 {
			return proof, errors.New("bare agent session/outcome differs")
		}
		c := a.Commands[0]
		if c.Command != "help" || c.Count != 1 || c.Attributable != 1 || c.FailureCount != 0 || (name == "bare-claude" && (a.ClaudeSessions != 1 || c.P50MS == nil || *c.P50MS != elapsed)) || (name == "bare-codex" && (a.CodexSessions != 1 || c.P50MS != nil)) {
			return proof, errors.New("bare help identity or timing differs")
		}
	}
	return proof, nil
}
