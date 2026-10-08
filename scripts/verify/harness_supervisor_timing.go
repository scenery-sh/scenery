package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/telemetryreport"
)

// The named process probe checks malformed retained supervisor input through
// the actual report CLI. Ordinary timing tests never launch a process.
func proveHarnessSupervisorTiming(parent context.Context, repo string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-supervisor-timing-probe-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	home := filepath.Join(root, "private-agent")
	logPath := filepath.Join(home, "agent", "dev", "owned.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, err
	}
	at := time.Now().UTC().Add(-time.Minute)
	var input bytes.Buffer
	event := func(kind string, data map[string]any) error {
		return json.NewEncoder(&input).Encode(map[string]any{"data": map[string]any{
			"type": kind, "time": at, "app": map[string]string{"root": root, "name": "owned timing fixture"}, "data": data,
		}})
	}
	step := func(op, name string, ms float64, ok bool, reason, outcome string) error {
		return event("build.step", map[string]any{"operation_id": op, "name": name, "duration_ms": ms, "ok": ok,
			"reason": reason, "outcome": outcome, "started_at": at, "cache": "hit", "files_hashed": 3})
	}
	for _, record := range []struct {
		op, name string
		ms       float64
	}{
		{"bad-success", "build.request", -.25},
		{"overflow", "build.request", 1e308},
		{"overflow-again", "build.request", 1e308},
		{"zero", "build.request", 0},
		{"fraction", "compile", .75}, {"fraction", "compile", .75}, {"fraction", "build.request", 12.75},
		{"child-overflow", "overflow-child", 5e18}, {"child-overflow", "overflow-child", 5e18}, {"child-overflow", "build.request", 20},
	} {
		if err := step(record.op, record.name, record.ms, true, "source_rebuild", ""); err != nil {
			return nil, err
		}
	}
	for _, token := range []string{"", "null", `"12"`, "-1e-1000000", "1e1000000"} {
		data := map[string]any{"operation_id": "raw-" + token, "name": "build.request", "ok": true,
			"reason": "source_rebuild", "started_at": at, "cache": "hit", "files_hashed": 3}
		if token != "" {
			data["duration_ms"] = json.RawMessage(token)
		}
		if err := event("build.step", data); err != nil {
			return nil, err
		}
	}
	if err := step("bad-initial", "build.request", -7, false, "initial_build", ""); err != nil {
		return nil, err
	}
	if err := event("build.error", map[string]any{"operation_id": "bad-initial", "error": "owned initial failure"}); err != nil {
		return nil, err
	}
	// Both explicit operation joins and the retained legacy immediate join work
	// when a failed terminal record has no admissible timing.
	for index := range 5 {
		op := fmt.Sprintf("failed-%d", index)
		if err := step(op, "build.request", -1, false, "source_rebuild", ""); err != nil {
			return nil, err
		}
		failure := map[string]any{"error": "owned rebuild failure"}
		if index%2 == 0 {
			failure["operation_id"] = op
		}
		if err := event("build.error", failure); err != nil {
			return nil, err
		}
	}
	for _, record := range []struct {
		name string
		ms   float64
	}{
		{"compile", -.25}, {"build.request", -.25}, {"compile", 1.5}, {"build.request", 10},
	} {
		if err := step("reused", record.name, record.ms, true, "source_rebuild", ""); err != nil {
			return nil, err
		}
	}
	for _, outcome := range []string{"superseded", "deferred"} {
		if err := step(outcome, "build.request", -.25, false, "source_rebuild", outcome); err != nil {
			return nil, err
		}
	}
	for _, ms := range []float64{1e20, 0, 12.75} {
		if err := event("phase.finish", map[string]any{"phase_id": "owned-phase", "duration_ms": ms, "ok": true}); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(logPath, input.Bytes(), 0o600); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, harnessLocalSceneryBinaryPath(repo), "telemetry", "report", "--since", "24h", "-o", "json")
	command.Dir = root
	command.Env = envWithOverrides(envpolicy.Environ(), "SCENERY_AGENT_HOME="+home)
	command.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("owned timing report failed: %w: %s", err, stderr.String())
	}
	var report telemetryreport.Report
	if err := decodeCLIJSON(output, &report); err != nil {
		return nil, err
	}
	var envelope struct {
		Data     map[string]any `json:"data"`
		Producer map[string]any `json:"producer"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, err
	}
	var rawEnvelope map[string]any
	if err := json.Unmarshal(output, &rawEnvelope); err != nil {
		return nil, err
	}
	if issues := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs", "schemas", "scenery.cli.schema.json"), rawEnvelope); len(issues) != 0 {
		return nil, fmt.Errorf("owned timing CLI envelope schema failed: %s", strings.Join(issues, "; "))
	}
	if issues := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs", "schemas", "scenery.telemetry.report.schema.json"), envelope.Data); len(issues) != 0 {
		return nil, fmt.Errorf("owned timing report schema failed: %s", strings.Join(issues, "; "))
	}
	b := report.Builds
	if b.Rebuilds.Count != 18 || b.Rebuilds.FailureCount != 5 || b.Rebuilds.PercentileSampleCount != 4 || b.Rebuilds.P50MS == nil || *b.Rebuilds.P50MS != 10 || b.Rebuilds.P95MS == nil || *b.Rebuilds.P95MS != 20 || len(b.Worktrees) != 1 || b.Worktrees[0].Rebuilds.PercentileSampleCount != 4 {
		return nil, fmt.Errorf("owned request outcome/timing cohorts = %+v", b.Rebuilds)
	}
	if b.Initial.Count != 1 || b.Initial.FailureCount != 1 || b.Initial.PercentileSampleCount != 0 || len(b.InitialFailures) != 1 || b.InitialFailures[0].Name != "owned initial failure" || b.UnmatchedErrors != 0 || len(b.RebuildFailures) != 1 || b.RebuildFailures[0].Count != 5 || b.RebuildFailures[0].Name != "owned rebuild failure" || len(b.Streaks) != 1 || b.Streaks[0].Count != 6 {
		return nil, fmt.Errorf("owned untimed failure joins/streaks were lost")
	}
	if b.Superseded != 1 || b.DeferredCandidates != 1 || report.Sources.SupervisorInvalid != 20 || len(report.Sources.Supervisor) != 1 || report.Sources.Supervisor[0].Invalid != 20 || report.Sources.Supervisor[0].Status != "partial" {
		return nil, fmt.Errorf("owned timing coverage/terminal outcomes = %+v", report.Sources)
	}
	if len(b.StartupPhases) != 1 || b.StartupPhases[0].Count != 3 || b.StartupPhases[0].PercentileSampleCount != 2 || b.StartupPhases[0].P50MS == nil || *b.StartupPhases[0].P50MS != 0 || b.StartupPhases[0].P95MS != nil || len(b.Steps) != 2 {
		return nil, fmt.Errorf("owned phase/step cohorts = %+v / %+v", b.StartupPhases, b.Steps)
	}
	for _, timing := range b.Steps {
		switch timing.Step {
		case "compile":
			if timing.Count != 3 || timing.PercentileSampleCount != 2 || timing.P50MS == nil || *timing.P50MS != 1 {
				return nil, fmt.Errorf("owned fractional sum/terminal cleanup = %+v", timing)
			}
		case "overflow-child":
			if timing.Count != 1 || timing.PercentileSampleCount != 0 || timing.P50MS != nil || timing.P95MS != nil {
				return nil, fmt.Errorf("owned overflow step = %+v", timing)
			}
		}
	}
	for _, cache := range b.CacheWork {
		if cache.Layer == "compile" && (cache.Samples != 4 || cache.TimingSampleCount != 3 || cache.AccumulatedMS == nil || *cache.AccumulatedMS != 3 || cache.FilesHashed != 12) {
			return nil, fmt.Errorf("owned admitted cache cohort = %+v", cache)
		}
	}
	latencyFound := false
	for _, finding := range report.Findings {
		if finding.Code == "builds.rebuild_latency" {
			latencyFound = strings.Contains(finding.Message, "over 4 timed successful rebuilds")
		}
	}
	if !latencyFound {
		return nil, fmt.Errorf("owned latency finding lost its timed cohort")
	}
	commandHuman := exec.CommandContext(ctx, harnessLocalSceneryBinaryPath(repo), "telemetry", "report", "--since", "24h", "-o", "human")
	commandHuman.Dir, commandHuman.Env, commandHuman.WaitDelay = root, command.Env, 2*time.Second
	human, err := commandHuman.Output()
	if err != nil {
		return nil, fmt.Errorf("owned human timing report failed: %w", err)
	}
	if !bytes.Contains(human, []byte("timed successes n=4")) || !bytes.Contains(human, []byte("known outcomes retained")) || !bytes.Contains(human, []byte("timed n=0, unavailable accumulated")) {
		return nil, fmt.Errorf("owned human timing output disagrees with JSON cohorts")
	}
	retained, err := os.ReadFile(logPath)
	if err != nil || !bytes.Equal(retained, input.Bytes()) {
		return nil, fmt.Errorf("owned report changed supervisor source bytes: %v", err)
	}
	fixtureHash := fmt.Sprintf("sha256:%x", sha256.Sum256(retained))
	if err := os.RemoveAll(root); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		return nil, fmt.Errorf("owned timing fixture cleanup was not verified: %v", err)
	}
	return map[string]any{
		"producer": envelope.Producer, "pid": command.Process.Pid, "human_pid": commandHuman.Process.Pid, "exit_code": 0,
		"fixture_sha256": fixtureHash, "supervisor_input_unchanged": true, "human_cohorts_verified": true,
		"proof":    "actual_cli_report_rejects_invalid_timing_preserves_outcomes_joins_fractional_sums_and_zero",
		"rebuilds": b.Rebuilds, "initial_builds": b.Initial, "steps": b.Steps, "phases": b.StartupPhases,
		"cache_work": b.CacheWork, "invalid_records": report.Sources.SupervisorInvalid,
		"schema_valid": true, "failure_joins": true, "terminal_cleanup": true,
		"cleanup": "actual child exited; owned private app/agent-home/logs removed and root absence verified",
	}, nil
}
