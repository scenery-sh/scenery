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
// a failure burst, rejected tokens, structured lifecycle faults, and a detached
// supervisor log with a failure streak.
func newReportFixture(t *testing.T, base time.Time) Options {
	t.Helper()
	root := t.TempDir()
	var records []any
	for i := range 70 {
		records = append(records, map[string]any{"at": base.Add(time.Duration(i) * 30 * time.Second), "command": "system agent", "duration_ms": 80, "exit_code": 10, "version": "dev", "mode": "oneshot"})
	}
	records = append(records,
		map[string]any{"at": base, "command": "up", "duration_ms": 4000, "exit_code": 0, "version": "v1.0.0", "mode": "long_running", "measurement": "startup", "caller": "claude", "app": map[string]any{"id": "shop", "name": "Shop"}},
		map[string]any{"at": base, "command": "check", "duration_ms": 500, "exit_code": 2, "version": "v1.0.0", "mode": "oneshot", "caller": "claude", "diagnostic_code": "SCN8001", "rejected_token": "flag:--tail", "app": map[string]any{"id": "shop", "name": "Shop"}},
		map[string]any{"at": base.Add(time.Minute), "command": "unknown", "duration_ms": 10, "exit_code": 2, "version": "v1.0.0", "mode": "oneshot", "caller": "codex", "diagnostic_code": "SCN8001", "rejected_token": "command:status", "app": map[string]any{"id": "shop", "name": "Shop"}},
		map[string]any{"at": base.Add(2 * time.Minute), "command": "unknown", "duration_ms": 10, "exit_code": 2, "version": "v1.0.0", "mode": "oneshot", "caller": "codex", "diagnostic_code": "SCN8001", "rejected_token": "command:status", "app": map[string]any{"id": "shop", "name": "Shop"}},
		map[string]any{"at": base.Add(3 * time.Minute), "kind": "proxy_fault", "app": map[string]any{"id": "shop", "name": "Shop"}, "proxy_fault": map[string]any{"backend": "api", "status": 502, "error_class": "connection_refused"}},
		map[string]any{"at": base.Add(4 * time.Minute), "kind": "runtime_fault", "generation": 1, "app": map[string]any{"id": "shop", "name": "Shop"}, "runtime_fault": map[string]any{"component": "schedule_retention", "error_class": "deadlock"}},
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

	return Options{
		Since: base.Add(-24 * time.Hour), Until: base.Add(24 * time.Hour),
		CLITelemetryPath: cliPath, AgentHome: home,
	}
}

func TestReportReconstructsRunsFromEverySource(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	opts := newReportFixture(t, base)
	report, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	cli := report.CLI
	if cli.Records != 74 || cli.Failures != 73 || cli.invalid != 1 || cli.Unattributed != 70 || cli.Unversioned != 70 {
		t.Fatalf("cli = records %d failures %d invalid %d unattributed %d unversioned %d", cli.Records, cli.Failures, cli.invalid, cli.Unattributed, cli.Unversioned)
	}
	if cli.Startup.Count != 1 || ms(cli.Startup.P50MS) != 4000 || len(cli.Apps) != 1 || cli.Apps[0].Count != 4 {
		t.Fatalf("startup = %+v apps = %+v", cli.Startup, cli.Apps)
	}
	if len(cli.Bursts) != 1 || cli.Bursts[0].Command != "system agent" || cli.Bursts[0].ExitCode != 10 || cli.Bursts[0].Count != 70 {
		t.Fatalf("bursts = %+v", cli.Bursts)
	}
	if !slices.Contains(cli.Callers, Count{Name: "claude", Count: 2}) || !slices.Contains(cli.Callers, Count{Name: "codex", Count: 2}) {
		t.Fatalf("callers = %+v", cli.Callers)
	}
	if !slices.Contains(cli.RejectedTokens, Count{Name: "command:status", Count: 2}) || !slices.Contains(cli.RejectedTokens, Count{Name: "flag:--tail", Count: 1}) {
		t.Fatalf("rejected_tokens = %+v", cli.RejectedTokens)
	}
	if !slices.Contains(cli.ProxyFaults, Count{Name: "api connection_refused (502)", Count: 1}) {
		t.Fatalf("proxy_faults = %+v", cli.ProxyFaults)
	}
	if !slices.Contains(cli.RuntimeFaults, Count{Name: "schedule_retention deadlock", Count: 1}) {
		t.Fatalf("runtime_faults = %+v", cli.RuntimeFaults)
	}
	if len(cli.Daily) != 1 || cli.Daily[0].Date != "2026-09-20" || cli.Daily[0].Count != 74 || cli.Daily[0].StartupCount != 1 {
		t.Fatalf("daily = %+v", cli.Daily)
	}
	if len(cli.ProducerTrends) != 2 {
		t.Fatalf("producer_trends = %+v", cli.ProducerTrends)
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

	var codes []string
	for _, finding := range report.Findings {
		codes = append(codes, finding.Severity+" "+finding.Code)
	}
	for _, want := range []string{"critical cli.failure_burst", "warning builds.failure_streak", "warning cli.rejected_tokens", "warning events.proxy_faults", "warning events.runtime_faults", "info cli.unattributed"} {
		if !slices.Contains(codes, want) {
			t.Fatalf("findings %v lack %q", codes, want)
		}
	}
	if report.Findings[0].Severity != severityCritical {
		t.Fatalf("findings are not ordered by severity: %v", codes)
	}

	// App filter scopes CLI records, events, and supervisor worktrees.
	opts.Apps = []string{"shop"}
	filtered, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.CLI.Records != 4 || filtered.CLI.Unattributed != 0 || len(filtered.CLI.ProxyFaults) != 1 || filtered.Builds.Rebuilds.Count != 8 {
		t.Fatalf("filtered report = cli %+v builds %+v", filtered.CLI, filtered.Builds)
	}
	opts.Apps = []string{"other-app"}
	other, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	if other.CLI.Records != 0 || len(other.CLI.ProxyFaults) != 0 || other.Builds.Rebuilds.Count != 0 {
		t.Fatalf("other-app report = cli %+v builds %+v", other.CLI, other.Builds)
	}
}

func TestReportWithoutSourcesIsEmpty(t *testing.T) {
	t.Parallel()

	report, err := Build(Options{CLITelemetryPath: filepath.Join(t.TempDir(), "missing.jsonl"), AgentHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if report.CLI.Records != 0 || report.Builds.Sessions != 0 || len(report.Findings) != 1 || report.Findings[0].Code != "builds.no_history" {
		t.Fatalf("report = %+v", report)
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
