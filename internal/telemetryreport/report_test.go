package telemetryreport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var testFamilies = map[string][]string{"up": nil, "check": nil, "logs": {"query", "tail"}, "ps": nil}

func writeLines(t *testing.T, path string, lines ...any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		text.Write(encoded)
		text.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(text.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func supervisorEventLine(kind string, at time.Time, data map[string]any) map[string]any {
	return map[string]any{"kind": "scenery.cli.event", "data": map[string]any{
		"type": kind, "time": at.Format(time.RFC3339Nano), "app": map[string]any{"name": "shop", "root": "/work/shop"}, "data": data,
	}}
}

// newReportFixture writes one of every source a report reads: CLI records with
// a failure burst, a detached supervisor log with a failure streak, and a Claude
// Code and a Codex transcript that ran Scenery.
func newReportFixture(t *testing.T, base time.Time) Options {
	t.Helper()
	root := t.TempDir()
	var records []any
	for i := range 70 {
		records = append(records, map[string]any{"at": base.Add(time.Duration(i) * 30 * time.Second), "command": "system agent", "duration_ms": 80, "exit_code": 10, "version": "dev", "mode": "oneshot"})
	}
	records = append(records,
		map[string]any{"at": base, "command": "up", "duration_ms": 4000, "exit_code": 0, "version": "v1.0.0", "mode": "long_running", "measurement": "startup", "app": map[string]any{"id": "shop", "name": "Shop"}},
		map[string]any{"at": base, "command": "check", "duration_ms": 500, "exit_code": 2, "version": "v1.0.0", "mode": "oneshot", "app": map[string]any{"id": "shop", "name": "Shop"}},
		map[string]any{"at": base.Add(-90 * 24 * time.Hour), "command": "ps", "duration_ms": 20, "exit_code": 0, "version": "dev", "mode": "oneshot"},
	)
	cliPath := filepath.Join(root, "telemetry.jsonl")
	writeLines(t, cliPath, records...)
	if file, err := os.OpenFile(cliPath, os.O_APPEND|os.O_WRONLY, 0); err == nil {
		_, _ = file.WriteString("not json\n")
		_ = file.Close()
	}

	home := filepath.Join(root, "agent-home")
	step := func(op, name string, at time.Time, ms float64, ok bool, reason string) map[string]any {
		return supervisorEventLine("build.step", at, map[string]any{"operation_id": op, "name": name, "started_at": at.Format(time.RFC3339Nano), "duration_ms": ms, "ok": ok, "reason": reason})
	}
	lines := []any{
		supervisorEventLine("run.start", base, map[string]any{}),
		step("op-0", "go.command", base, 900, true, "build"),
		step("op-0", "build.request", base, 12000, true, "initial_build"),
		step("op-1", "go.command", base.Add(time.Minute), 500, true, "build"),
		step("op-1", "build.request", base.Add(time.Minute), 1500, true, "source_rebuild"),
	}
	// A supervisor writes each failed build's build.request before its
	// build.error. The first five errors predate operation IDs; the sixth names
	// its operation and is followed by an error of no build (a failed framework
	// handoff), which no build is charged with.
	for i := range 6 {
		at := base.Add(time.Duration(2+i) * time.Minute)
		op := fmt.Sprintf("op-f%d", i)
		lines = append(lines, step(op, "build.request", at, 300, false, "source_rebuild"))
		failure := map[string]any{"error": fmt.Sprintf("application framework sha256:%064x does not match /Users/dev/work/app%d at 127.0.0.1:%d", i, i, 50000+i)}
		if i == 5 {
			failure["operation_id"] = op
		}
		lines = append(lines, supervisorEventLine("build.error", at, failure))
		if i == 5 {
			lines = append(lines, supervisorEventLine("build.error", at, map[string]any{"error": "handoff preparation failed"}))
		}
	}
	lines = append(lines, step("op-2", "build.request", base.Add(10*time.Minute), 1300, true, "source_rebuild"))
	writeLines(t, filepath.Join(home, "worktrees", strings.Repeat("ab", 32), "control", "dev", "shop-1.log"), lines...)

	claude := filepath.Join(root, "claude")
	use := func(id, name string, input map[string]any, at time.Time) map[string]any {
		return map[string]any{"type": "assistant", "timestamp": at.Format(time.RFC3339Nano), "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}}
	}
	result := func(id string, isError bool, text string, at time.Time) map[string]any {
		return map[string]any{"type": "user", "timestamp": at.Format(time.RFC3339Nano), "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": isError, "content": text}}}}
	}
	writeLines(t, filepath.Join(claude, "project", "session.jsonl"),
		use("a", "Bash", map[string]any{"command": "./scripts/scenery logs --help 2>&1 | head"}, base),
		result("a", true, "Exit code 2\nscenery: unknown flag \"--help\"", base.Add(time.Second)),
		use("b", "Bash", map[string]any{"command": "cd /Users/dev/scenery && scenery up --detach"}, base.Add(2*time.Second)),
		result("b", false, "started", base.Add(5*time.Second)),
		use("c", "Edit", map[string]any{"file_path": "/x"}, base.Add(6*time.Second)),
		result("c", true, "<tool_use_error>File has not been read yet. Read it first before writing to it.</tool_use_error>", base.Add(6*time.Second)),
		// One shell command, two invocations: counted, but its single
		// outcome and duration are attributed to neither.
		use("d", "Bash", map[string]any{"command": "scenery check && scenery up"}, base.Add(7*time.Second)),
		result("d", true, "Exit code 1\nboom", base.Add(9*time.Second)),
		use("e", "Bash", map[string]any{"command": "GOWORK=off ./scripts/scenery status 2>&1; echo \"try scenery up\""}, base.Add(10*time.Second)),
		result("e", true, "Exit code 2\nscenery: unknown command \"status\"; use `scenery help`", base.Add(11*time.Second)),
	)
	writeLines(t, filepath.Join(claude, "other", "unrelated.jsonl"),
		use("z", "Bash", map[string]any{"command": "ls ~/Repos/scenery/docs"}, base),
		result("z", false, "docs", base.Add(time.Second)),
	)
	codex := filepath.Join(root, "codex")
	writeLines(t, filepath.Join(codex, "2026", "09", "20", "rollout.jsonl"),
		map[string]any{"timestamp": base.Format(time.RFC3339Nano), "type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "k1",
			"input": `const r = await tools.exec_command({cmd:"scenery check -o json","max_output_tokens":500}); const s = await tools.exec_command({cmd:"scenery ps"}); text(r);`}},
		map[string]any{"timestamp": base.Add(9 * time.Second).Format(time.RFC3339Nano), "type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "k1",
			"output": []any{map[string]any{"type": "input_text", "text": `{"wall_time_seconds":1.5,"exit_code":3,"output":"SCN8003 failed_precondition: run scenery up first"}`}, map[string]any{"type": "input_text", "text": `{"wall_time_seconds":0.25,"exit_code":0,"output":"ok"}`}}}},
	)
	return Options{
		Since: base.Add(-24 * time.Hour), Until: base.Add(24 * time.Hour),
		CLITelemetryPath: cliPath, AgentHome: home,
		AgentTranscripts: true, ClaudeProjectsDir: claude, CodexSessionsDir: codex, CommandFamilies: testFamilies,
	}
}

func TestReportReconstructsRunsFromEverySource(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	report, err := Build(newReportFixture(t, base))
	if err != nil {
		t.Fatal(err)
	}
	cli := report.CLI
	if cli.Records != 72 || cli.Failures != 71 || cli.invalid != 1 || cli.Unattributed != 70 || cli.Unversioned != 70 {
		t.Fatalf("cli = records %d failures %d invalid %d unattributed %d unversioned %d", cli.Records, cli.Failures, cli.invalid, cli.Unattributed, cli.Unversioned)
	}
	if cli.Startup.Count != 1 || ms(cli.Startup.P50MS) != 4000 || len(cli.Apps) != 1 || cli.Apps[0].Count != 2 {
		t.Fatalf("startup = %+v", cli.Startup)
	}
	if len(cli.Bursts) != 1 || cli.Bursts[0].Command != "system agent" || cli.Bursts[0].ExitCode != 10 || cli.Bursts[0].Count != 70 {
		t.Fatalf("bursts = %+v", cli.Bursts)
	}

	builds := report.Builds
	if builds.Sessions != 1 || builds.Initial.Count != 1 || builds.Rebuilds.Count != 8 || builds.Rebuilds.FailureCount != 6 || ms(builds.Rebuilds.P50MS) != 1300 {
		t.Fatalf("builds = sessions %d initial %+v rebuilds %+v", builds.Sessions, builds.Initial, builds.Rebuilds)
	}
	if len(builds.Steps) != 1 || builds.Steps[0].Step != "go.command" || builds.Steps[0].Count != 1 {
		t.Fatalf("steps = %+v; only successful rebuilds count", builds.Steps)
	}
	wantCause := "application framework sha256:… does not match <path> at <address>"
	causes := 0
	for _, cause := range builds.Failures {
		causes += cause.Count
	}
	if causes != builds.Rebuilds.FailureCount+builds.Initial.FailureCount || len(builds.Failures) != 1 || builds.Failures[0].Name != wantCause || builds.Failures[0].Count != 6 {
		t.Fatalf("failure causes = %+v", builds.Failures)
	}
	if len(builds.Streaks) != 1 || builds.Streaks[0].Count != 6 || builds.Streaks[0].Cause != wantCause || builds.Worktrees[0].LongestFailureStreak != 6 {
		t.Fatalf("streaks = %+v worktrees = %+v", builds.Streaks, builds.Worktrees)
	}

	agents := report.Agents
	if agents == nil || agents.ClaudeSessions != 1 || agents.CodexSessions != 1 {
		t.Fatalf("agents = %+v; the session that never ran Scenery is excluded", agents)
	}
	if agents.ToolCalls != 7 || agents.ToolErrors != 5 || agents.SceneryCommands != 6 || agents.SceneryInvocations != 7 || agents.SceneryFailed != 4 {
		t.Fatalf("agent counts = %+v", agents)
	}
	commands := map[string]AgentCommand{}
	for _, command := range agents.Commands {
		commands[command.Command] = command
	}
	if up := commands["up"]; up.Count != 2 || up.Attributable != 1 || up.FailureCount != 0 || ms(up.P50MS) != 3000 {
		t.Fatalf("up = %+v; the compound command attributes nothing", up)
	}
	if check := commands["check"]; check.Count != 2 || check.Attributable != 1 || check.FailureCount != 1 || ms(check.P50MS) != 1500 {
		t.Fatalf("check = %+v; the Codex command keeps its own exit code and wall time", check)
	}
	if ps := commands["ps"]; ps.Attributable != 1 || ps.FailureCount != 0 || ms(ps.P50MS) != 250 {
		t.Fatalf("ps = %+v", ps)
	}
	if status := commands["unknown status"]; status.Count != 1 || status.FailureCount != 1 {
		t.Fatalf("unknown commands are attempts too: %+v", agents.Commands)
	}
	for _, rejectedInput := range []Count{{Name: `unknown flag "--help"`, Count: 1}, {Name: `unknown command "status"`, Count: 1}} {
		if !slices.Contains(agents.RejectedInputs, rejectedInput) {
			t.Fatalf("rejected inputs = %+v", agents.RejectedInputs)
		}
	}
	if !slices.Contains(agents.FailureClasses, Count{Name: "invalid invocation", Count: 2}) || !slices.Contains(agents.FailureClasses, Count{Name: "failed precondition", Count: 1}) {
		t.Fatalf("failure classes = %+v", agents.FailureClasses)
	}
	if !slices.Contains(agents.ToolErrorKinds, Count{Name: "edit or write before reading", Count: 1}) {
		t.Fatalf("tool error kinds = %+v", agents.ToolErrorKinds)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"/Users/dev", "head", "max_output_tokens", "run scenery up first"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("report kept transcript or message text %q: %s", private, encoded)
		}
	}

	var codes []string
	for _, finding := range report.Findings {
		codes = append(codes, finding.Severity+" "+finding.Code)
	}
	for _, want := range []string{"critical cli.failure_burst", "warning builds.failure_streak", "warning agents.invalid_invocations", "info cli.unattributed"} {
		if !slices.Contains(codes, want) {
			t.Fatalf("findings %v lack %q", codes, want)
		}
	}
	if report.Findings[0].Severity != severityCritical {
		t.Fatalf("findings are not ordered by severity: %v", codes)
	}
}

func TestReportWithoutSourcesIsEmpty(t *testing.T) {
	t.Parallel()

	report, err := Build(Options{CLITelemetryPath: filepath.Join(t.TempDir(), "missing.jsonl"), AgentHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if report.CLI.Records != 0 || report.Builds.Sessions != 0 || report.Agents != nil || len(report.Findings) != 1 || report.Findings[0].Code != "builds.no_history" {
		t.Fatalf("report = %+v", report)
	}
}

func TestSceneryCommandsFindAttemptsAtCommandPosition(t *testing.T) {
	t.Parallel()

	for shell, want := range map[string][]string{
		"./scripts/scenery logs tail --follow":           {"logs tail"},
		".scenery/harness/bin/scenery up && scenery ps":  {"up", "ps"},
		"cd ~/Repos/scenery\nsed -n 1p file":             nil,
		"go run ./cmd/scenery check -o json":             {"check"},
		"echo scenery up; scenery logs --since 1h":       {"logs"},
		"scenery --help":                                 {"--help"},
		"GOWORK=off ./scripts/scenery status --app-root": {"unknown status"},
		`grep -n "scenery up" docs/*.md`:                 nil,
	} {
		if got := sceneryCommands(shell, testFamilies); !slices.Equal(got, want) {
			t.Errorf("sceneryCommands(%q) = %v, want %v", shell, got, want)
		}
	}
}

func TestPercentileIsNearestRank(t *testing.T) {
	t.Parallel()

	values := make([]int64, 20)
	for i := range values {
		values[20-1-i] = int64(i + 1)
	}
	if p50, p95, p100 := percentile(values, 50), percentile(values, 95), percentile(values, 100); ms(p50) != 10 || ms(p95) != 19 || ms(p100) != 20 {
		t.Fatalf("p50 %d p95 %d p100 %d, want 10 19 20", ms(p50), ms(p95), ms(p100))
	}
	if percentile(nil, 50) != nil {
		t.Fatal("a percentile of no values is not unavailable")
	}
}

func TestFailureCausesAddUpToFailedBuildsBeyondTheNamedOnes(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	var lines []any
	for i := range 20 {
		at := base.Add(time.Duration(i) * time.Minute)
		op := fmt.Sprintf("op-%d", i)
		lines = append(lines,
			supervisorEventLine("build.step", at, map[string]any{"operation_id": op, "name": "build.request", "started_at": at.Format(time.RFC3339Nano), "duration_ms": 10, "ok": false, "reason": "source_rebuild"}),
			supervisorEventLine("build.error", at, map[string]any{"operation_id": op, "error": fmt.Sprintf("distinct failure %c", 'a'+i)}))
	}
	writeLines(t, filepath.Join(home, "worktrees", strings.Repeat("ef", 32), "control", "dev", "app.log"), lines...)
	report, err := Build(Options{Since: base.Add(-time.Hour), Until: base.Add(time.Hour), AgentHome: home})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, cause := range report.Builds.Failures {
		total += cause.Count
	}
	last := report.Builds.Failures[len(report.Builds.Failures)-1]
	if total != 20 || len(report.Builds.Failures) != 15 || last != (Count{Name: "other causes", Count: 6}) {
		t.Fatalf("failure causes = %+v", report.Builds.Failures)
	}
}
