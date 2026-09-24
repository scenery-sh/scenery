package telemetryreport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	bash := func(command string) map[string]any { return map[string]any{"command": command} }
	sessionPath := filepath.Join(claude, "project", "session.jsonl")
	writeLines(t, sessionPath,
		// A pipe reports the last command's status: Scenery's is unknown.
		use("a", "Bash", bash("./scripts/scenery logs --help 2>&1 | head"), base),
		result("a", true, "Exit code 2\nscenery: unknown flag \"--help\"", base.Add(time.Second)),
		use("b", "Bash", bash("cd /Users/dev/scenery && scenery up --detach"), base.Add(2*time.Second)),
		result("b", false, "started", base.Add(5*time.Second)),
		use("c", "Edit", map[string]any{"file_path": "/x"}, base.Add(6*time.Second)),
		result("c", true, "<tool_use_error>File has not been read yet. Read it first before writing to it.</tool_use_error>", base.Add(6*time.Second)),
		// One shell command, two invocations: counted, but its single
		// outcome and duration are attributed to neither.
		use("d", "Bash", bash("scenery check && scenery up"), base.Add(7*time.Second)),
		result("d", true, "Exit code 1\nboom", base.Add(9*time.Second)),
		use("e", "Bash", bash("GOWORK=off ./scripts/scenery status 2>&1; echo \"try scenery up\""), base.Add(10*time.Second)),
		result("e", true, "Exit code 2\nscenery: unknown command \"status\"; use `scenery help`", base.Add(11*time.Second)),
		// The only simple command: its outcome and duration are up's own.
		use("f", "Bash", bash("scenery up --detach"), base.Add(12*time.Second)),
		result("f", false, "started", base.Add(15*time.Second)),
		// A background command has no outcome yet.
		use("g", "Bash", map[string]any{"command": "scenery logs tail --follow", "run_in_background": true}, base.Add(16*time.Second)),
		result("g", false, "Command running in background with ID: b1", base.Add(16*time.Second)),
		// A quoted example runs no Scenery.
		use("h", "Bash", bash("printf '%s\\n' 'example; scenery up --detach'"), base.Add(17*time.Second)),
		result("h", false, "example; scenery up --detach", base.Add(17*time.Second)),
		// Scenery never ran; the shell's failure is not check's.
		use("i", "Bash", bash("false && scenery check"), base.Add(18*time.Second)),
		result("i", true, "Exit code 1", base.Add(18*time.Second)),
		// A result without its call, and a call without its result.
		result("orphan", false, "done", base.Add(19*time.Second)),
		use("open", "Bash", bash("scenery ps"), base.Add(20*time.Second)),
	)
	if file, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0); err == nil {
		_, _ = file.WriteString(`{"type":"assistant","message":{"content":[{"type":"tool_use"` + "\n")
		_ = file.Close()
	}
	writeLines(t, filepath.Join(claude, "other", "unrelated.jsonl"),
		use("z", "Bash", bash("ls ~/Repos/scenery/docs"), base),
		result("z", false, "docs", base.Add(time.Second)),
	)
	codex := filepath.Join(root, "codex")
	call := func(at time.Time, payload map[string]any) map[string]any {
		return map[string]any{"timestamp": at.Format(time.RFC3339Nano), "type": "response_item", "payload": payload}
	}
	texts := func(lines ...string) []any {
		var items []any
		for _, line := range lines {
			items = append(items, map[string]any{"type": "input_text", "text": line})
		}
		return items
	}
	writeLines(t, filepath.Join(codex, "2026", "09", "20", "rollout.jsonl"),
		// An exec script prints each exec_command result as JSON, in any
		// field order, possibly as a settled promise.
		call(base, map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "k1",
			"input": `const r = await tools.exec_command({cmd:"scenery check -o json","max_output_tokens":500}); const s = await tools.exec_command({"cmd": "scenery ps"}); text(r); text({i: 1, status: "fulfilled", value: s});`}),
		call(base.Add(9*time.Second), map[string]any{"type": "custom_tool_call_output", "call_id": "k1", "output": texts(
			"Script completed\nWall time 1.8 seconds\nOutput:\n",
			`{"exit_code":3,"chunk_id":"a1","output":"SCN8003 failed_precondition: run scenery up first","wall_time_seconds":1.5}`+"\n"+
				`{"i":1,"status":"fulfilled","value":{"chunk_id":"a2","wall_time_seconds":0.25,"exit_code":0,"output":"ok"}}`)}),
		call(base.Add(10*time.Second), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "k2", "arguments": `{"cmd":"scenery check","workdir":"/Users/dev/shop"}`}),
		call(base.Add(11*time.Second), map[string]any{"type": "function_call_output", "call_id": "k2", "output": "Chunk ID: c2\nWall time: 0.5000 seconds\nProcess exited with code 0\nOriginal token count: 1\nOutput:\nok"}),
		call(base.Add(12*time.Second), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "k3", "arguments": `{"cmd":"scenery up"}`}),
		call(base.Add(17*time.Second), map[string]any{"type": "function_call_output", "call_id": "k3", "output": "Chunk ID: c3\nWall time: 5.0000 seconds\nProcess running with session ID 7\nOriginal token count: 0\nOutput:\n"}),
		call(base.Add(18*time.Second), map[string]any{"type": "function_call", "name": "shell_command", "call_id": "k4", "arguments": `{"command":"scenery ps"}`}),
		call(base.Add(19*time.Second), map[string]any{"type": "function_call_output", "call_id": "k4", "output": "Exit code: 1\nWall time: 0.2 seconds\nOutput:\nSCN9000 internal tooling failure"}),
		call(base.Add(20*time.Second), map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "k5", "input": `text(await tools.exec_command({cmd:"scenery statuss"}))`}),
		call(base.Add(21*time.Second), map[string]any{"type": "custom_tool_call_output", "call_id": "k5", "output": texts("Script completed\nOutput:\n", `{"chunk_id":"a3","exit_code":2,"output":"unknown command \"statuss\""}`)}),
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
	if agents.ToolCalls != 14 || agents.ToolErrors != 8 || agents.SceneryCommands != 13 || agents.SceneryInvocations != 14 || agents.SceneryFailed != 7 || agents.SceneryOutcomeUnknown != 2 || agents.SceneryAttributable != 6 || agents.SceneryAttributableFailed != 3 {
		t.Fatalf("agent counts = %+v", agents)
	}
	commands := map[string]AgentCommand{}
	for _, command := range agents.Commands {
		commands[command.Command] = command
	}
	if up := commands["up"]; up.Count != 4 || up.Attributable != 1 || up.FailureCount != 0 || ms(up.P50MS) != 3000 {
		t.Fatalf("up = %+v; only the simple command with an outcome is attributed", up)
	}
	if check := commands["check"]; check.Count != 4 || check.Attributable != 2 || check.FailureCount != 1 || check.P50MS != nil || check.WallTimeMS != 0 {
		t.Fatalf("check = %+v; Codex commands keep their own exit codes but record no duration", check)
	}
	if ps := commands["ps"]; ps.Count != 2 || ps.Attributable != 2 || ps.FailureCount != 1 || ps.P50MS != nil {
		t.Fatalf("ps = %+v", ps)
	}
	if logs, tail := commands["logs"], commands["logs tail"]; logs.Count != 1 || logs.Attributable != 0 || tail.Count != 1 || tail.Attributable != 0 {
		t.Fatalf("logs = %+v, logs tail = %+v", logs, tail)
	}
	if status := commands["unknown status"]; status.Count != 1 || status.Attributable != 0 {
		t.Fatalf("unknown commands are attempts too: %+v", agents.Commands)
	}
	if statuss := commands["unknown statuss"]; statuss.Attributable != 1 || statuss.FailureCount != 1 || statuss.P50MS != nil {
		t.Fatalf("an outcome without a duration: %+v", statuss)
	}
	for _, rejectedInput := range []Count{{Name: `unknown flag "--help"`, Count: 1}, {Name: `unknown command "status"`, Count: 1}, {Name: `unknown command "statuss"`, Count: 1}} {
		if !slices.Contains(agents.RejectedInputs, rejectedInput) {
			t.Fatalf("rejected inputs = %+v", agents.RejectedInputs)
		}
	}
	for _, class := range []Count{{Name: "invalid invocation", Count: 3}, {Name: "other failure", Count: 2}, {Name: "failed precondition", Count: 1}, {Name: "internal failure", Count: 1}} {
		if !slices.Contains(agents.FailureClasses, class) {
			t.Fatalf("failure classes = %+v", agents.FailureClasses)
		}
	}
	if !slices.Contains(agents.ToolErrorKinds, Count{Name: "edit or write before reading", Count: 1}) {
		t.Fatalf("tool error kinds = %+v", agents.ToolErrorKinds)
	}
	if sources := report.Sources.Transcripts; sources == nil || *sources != (TranscriptSources{Read: 3, InvalidRecords: 1, UnmatchedResults: 1, UnansweredCalls: 1}) {
		t.Fatalf("transcript sources = %+v", sources)
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
	for _, want := range []string{"critical cli.failure_burst", "warning builds.failure_streak", "warning agents.invalid_invocations", "info agents.unattributable", "info sources.skipped_records", "info cli.unattributed"} {
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

func TestSceneryAttemptsFindCommandsAtCommandPosition(t *testing.T) {
	t.Parallel()

	for shell, want := range map[string]struct {
		commands     []string
		attributable bool
	}{
		"./scripts/scenery logs tail --follow":           {[]string{"logs tail"}, true},
		"scenery check -o json 2>&1 >/tmp/check.json":    {[]string{"check"}, true},
		"GOWORK=off ./scripts/scenery status --app-root": {[]string{"unknown status"}, true},
		"scenery --help":                                {[]string{"--help"}, true},
		"time env A=1 scenery ps":                       {[]string{"ps"}, true},
		".scenery/harness/bin/scenery up && scenery ps": {[]string{"up", "ps"}, false},
		"go run ./cmd/scenery check -o json":            {[]string{"check"}, false},
		"sudo scenery up":                               {[]string{"up"}, false},
		"echo scenery up; scenery logs --since 1h":      {[]string{"logs"}, false},
		"scenery up &":                                  {[]string{"up"}, false},
		"if scenery check; then echo ok; fi":            {[]string{"check"}, false},
		"x=$(scenery ps)":                               {[]string{"ps"}, false},
		// Short-circuits: the shell's outcome is not Scenery's.
		"false && scenery check": {[]string{"check"}, false},
		"scenery check && false": {[]string{"check"}, false},
		"scenery check || true":  {[]string{"check"}, false},
		"scenery check | tail":   {[]string{"check"}, false},
		// Mentions run no Scenery.
		"cd ~/Repos/scenery\nsed -n 1p file":                   {nil, false},
		`grep -n "scenery up" docs/*.md`:                       {nil, false},
		`printf '%s\n' 'example; scenery up --detach'`:         {nil, false},
		"echo scenery up # scenery check":                      {nil, false},
		"cat <<'EOF' > notes.md\nscenery up\nEOF\nwc notes.md": {nil, false},
		"cat <<-EOF\n\tscenery up\n\tEOF":                      {nil, false},
	} {
		commands, attributable := sceneryAttempts(shell, testFamilies)
		if !slices.Equal(commands, want.commands) || attributable != want.attributable {
			t.Errorf("sceneryAttempts(%q) = %v, %v; want %v, %v", shell, commands, attributable, want.commands, want.attributable)
		}
	}
}

func TestPercentilesAreNearestRankFromOneSort(t *testing.T) {
	t.Parallel()

	values := make([]int64, 20)
	for i := range values {
		values[20-1-i] = int64(i + 1)
	}
	p := percentiles(values, 50, 95, 100)
	if ms(p[0]) != 10 || ms(p[1]) != 19 || ms(p[2]) != 20 || values[0] != 20 {
		t.Fatalf("p50 %d p95 %d p100 %d, want 10 19 20 without reordering the input", ms(p[0]), ms(p[1]), ms(p[2]))
	}
	if percentiles(nil, 50)[0] != nil {
		t.Fatal("a percentile of no values is not unavailable")
	}
}

// Codex results are read by field name: a reordered object still carries its
// outcome, and a result without an exit code has none.
func TestCodexScriptRunsReadResultsByFieldName(t *testing.T) {
	t.Parallel()

	runs := codexScriptRuns("Script completed\nOutput:\n" +
		`{"exit_code":4,"chunk_id":"a","wall_time_seconds":2}` + "\n" +
		`{"chunk_id":"b","exit_code":0}` + "\n" +
		`{"chunk_id":"c","wall_time_seconds":5,"session_id":9}` + "\n" +
		`{"at":"2026-09-20T10:00:00Z","exit_code":1}` + "\n")
	if len(runs) != 3 {
		t.Fatalf("runs = %+v; only exec_command results count", runs)
	}
	if !runs[0].known || runs[0].exit != 4 {
		t.Fatalf("reordered result = %+v", runs[0])
	}
	if !runs[1].known || runs[1].exit != 0 {
		t.Fatalf("result = %+v", runs[1])
	}
	if runs[2].known {
		t.Fatalf("running command = %+v", runs[2])
	}
	if run := codexHeaderRun("Chunk ID: x\nWall time: 5.0 seconds\nProcess running with session ID 3\nOutput:\nProcess exited with code 0"); run.known {
		t.Fatalf("an exit status inside the output is not the header's: %+v", run)
	}
}

// A transcript whose reading fails part way keeps what it held before the
// failure, and says it was read in part.
func TestPartialTranscriptKeepsItsEvidence(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	valid := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"content":[{"type":"tool_use","id":"a","name":"Bash","input":{"command":"scenery check"}}]}}`+"\n"+
		`{"type":"user","timestamp":%q,"message":{"content":[{"type":"tool_result","tool_use_id":"a","is_error":true,"content":"Exit code 1\nSCN2001 broken"}]}}`+"\n",
		at.Format(time.RFC3339Nano), at.Add(time.Second).Format(time.RFC3339Nano))
	var calls []toolCall
	var sources TranscriptSources
	err := readClaudeTranscript(io.MultiReader(strings.NewReader(valid), failingReader{}), func(call toolCall) { calls = append(calls, call) }, &sources)
	if err == nil || len(calls) != 1 || calls[0].shells[0].outcome != outcomeFailed {
		t.Fatalf("partial read = %v, calls %+v", err, calls)
	}

	root := t.TempDir()
	path := filepath.Join(root, "p", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	tally := readTranscript(Options{CommandFamilies: testFamilies}, path, func(file io.Reader, visit func(toolCall), sources *TranscriptSources) error {
		return readClaudeTranscript(io.MultiReader(file, failingReader{}), visit, sources)
	})
	if tally.sources.Partial != 1 || tally.sources.Read != 0 || !tally.attemptedScenery || tally.failed != 1 || tally.classes["application diagnostic"] != 1 {
		t.Fatalf("partial tally = %+v", tally)
	}
	unreadable := readTranscript(Options{}, filepath.Join(root, "missing.jsonl"), readClaudeTranscript)
	if unreadable.sources.Failed != 1 {
		t.Fatalf("unreadable tally = %+v", unreadable.sources)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("input/output error") }

func TestReadLinesSkipsOversizedLinesAndContinues(t *testing.T) {
	t.Parallel()

	var lines []string
	oversized, err := readLines(strings.NewReader("short\n"+strings.Repeat("x", 100)+"\nafter\nlast"), 16, func(line []byte) { lines = append(lines, string(line)) })
	if err != nil || oversized != 1 || !slices.Equal(lines, []string{"short", "after", "last"}) {
		t.Fatalf("lines = %q, oversized %d, err %v", lines, oversized, err)
	}
}

// An error naming an operation the window does not hold is unmatched
// evidence: it is never charged to the failed build awaiting its own error.
func TestBuildErrorNamingAnUnknownOperationIsUnmatched(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	step := func(op string, at time.Time, ok bool) map[string]any {
		return supervisorEventLine("build.step", at, map[string]any{"operation_id": op, "name": "build.request", "started_at": at.Format(time.RFC3339Nano), "duration_ms": 10, "ok": ok, "reason": "source_rebuild"})
	}
	writeLines(t, filepath.Join(home, "worktrees", strings.Repeat("cd", 32), "control", "dev", "app.log"),
		step("op-a", base, false),
		supervisorEventLine("build.error", base, map[string]any{"operation_id": "op-b", "error": "belongs to b"}),
		supervisorEventLine("build.error", base, map[string]any{"operation_id": "op-a", "error": "belongs to a"}),
		step("op-c", base.Add(time.Minute), false),
		supervisorEventLine("build.error", base.Add(time.Minute), map[string]any{"operation_id": "op-d", "error": "belongs to d"}),
	)
	report, err := Build(Options{Since: base.Add(-time.Hour), Until: base.Add(time.Hour), AgentHome: home})
	if err != nil {
		t.Fatal(err)
	}
	builds := report.Builds
	if builds.UnmatchedErrors != 2 || !slices.Contains(builds.Failures, Count{Name: "belongs to a", Count: 1}) || !slices.Contains(builds.Failures, Count{Name: "no error recorded", Count: 1}) {
		t.Fatalf("builds = unmatched %d, causes %+v", builds.UnmatchedErrors, builds.Failures)
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
