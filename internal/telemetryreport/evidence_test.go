package telemetryreport

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestMalformedBuildErrorKeepsSourceAndOutcomeEvidence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	for _, joined := range []bool{false, true} {
		t.Run(fmt.Sprint(joined), func(t *testing.T) {
			events := []any{
				supervisorTimingEvent("build.step", "failed", "build.request", 0, false),
				supervisorEventLine("build.error", at, map[string]any{"operation_id": "failed", "error": 17}),
			}
			if joined {
				events = append(events, supervisorEventLine("build.error", at, map[string]any{"operation_id": "failed", "error": "known cause"}))
			}
			events = append(events, supervisorTimingEvent("build.step", "success", "build.request", 0, true))
			r := supervisorTimingFixture(t, events...)
			s := r.Sources
			if len(s.Supervisor) != 1 || s.SupervisorInvalid != 1 || s.SupervisorLogsPartial != 0 || s.SupervisorLogsFailed != 0 {
				t.Fatalf("global source evidence = %+v", s)
			}
			file := s.Supervisor[0]
			if file.Invalid != 1 || file.Status != "partial" || file.SnapshotBytes == nil || file.ReadBytes != *file.SnapshotBytes || file.Records != len(events) {
				t.Fatalf("physical and invalid evidence = %+v", file)
			}
			b := r.Builds
			if b.Rebuilds.Count != 2 || b.Rebuilds.FailureCount != 1 || b.Rebuilds.PercentileSampleCount != 1 || b.UnmatchedErrors != 0 || len(b.RebuildFailures) != 1 || b.RebuildFailures[0].Count != 1 {
				t.Fatalf("known outcomes = %+v", b)
			}
			if joined && b.RebuildFailures[0].Name != "known cause" {
				t.Fatalf("valid subsequent error lost its operation join: %+v", b.RebuildFailures)
			}
		})
	}
}

func TestBareSceneryIncludesAgentSession(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	for _, kind := range []string{"claude", "codex"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "session.jsonl")
			opts := Options{CommandFamilies: testFamilies, Until: at.Add(time.Hour)}
			if kind == "claude" {
				opts.ClaudeProjectsDir = root
				writeLines(t, path,
					map[string]any{"type": "assistant", "timestamp": at, "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "bare", "name": "Bash", "input": map[string]any{"command": "scenery"}}}}},
					map[string]any{"type": "user", "timestamp": at, "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "bare", "is_error": false, "content": "help"}}}},
				)
			} else {
				opts.CodexSessionsDir = root
				writeLines(t, path,
					map[string]any{"type": "response_item", "timestamp": at, "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "bare", "arguments": `{"cmd":"/owned/bin/scenery"}`}},
					map[string]any{"type": "response_item", "timestamp": at, "payload": map[string]any{"type": "function_call_output", "call_id": "bare", "output": "Process exited with code 0\nOutput:\nhelp"}},
				)
			}
			a, err := readAgents(opts)
			if err != nil {
				t.Fatal(err)
			}
			if a.ClaudeSessions+a.CodexSessions != 1 || (kind == "claude" && a.ClaudeSessions != 1) || (kind == "codex" && a.CodexSessions != 1) || a.ToolCalls != 1 || a.SceneryCommands != 1 || a.SceneryInvocations != 1 || a.SceneryAttributable != 1 || a.SceneryOutcomeUnknown != 0 || a.SceneryFailed != 0 || len(a.Commands) != 1 {
				t.Fatalf("bare invocation/session = %+v", a)
			}
			command := a.Commands[0]
			if command.Command != "help" || command.Count != 1 || command.Attributable != 1 || command.FailureCount != 0 || command.WallTimeMS != 0 || (kind == "claude" && (command.P50MS == nil || *command.P50MS != 0)) || (kind == "codex" && command.P50MS != nil) {
				t.Fatalf("help identity and timing = %+v", command)
			}
		})
	}
}
