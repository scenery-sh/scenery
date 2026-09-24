package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/telemetryreport"
)

func TestParseTelemetryReportArgs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	opts, err := parseTelemetryReportArgs(nil, now)
	if err != nil || !opts.Since.Equal(now.Add(-30*24*time.Hour)) || opts.AgentTranscripts || opts.JSON {
		t.Fatalf("defaults = %+v, %v", opts, err)
	}
	opts, err = parseTelemetryReportArgs([]string{"--since", "48h", "--agent-transcripts", "-o", "json"}, now)
	if err != nil || !opts.Since.Equal(now.Add(-48*time.Hour)) || !opts.AgentTranscripts || !opts.JSON {
		t.Fatalf("explicit = %+v, %v", opts, err)
	}
	for _, args := range [][]string{{"--since", "0s"}, {"extra"}, {"--app", "shop"}} {
		if _, err := parseTelemetryReportArgs(args, now); err == nil {
			t.Fatalf("parseTelemetryReportArgs(%v) accepted a wrongly written request", args)
		}
	}
	families := telemetryReportCommandFamilies()
	if _, ok := families["up"]; !ok || !strings.Contains(strings.Join(families["telemetry"], " "), "report") {
		t.Fatalf("command families = %v", families)
	}
}

// The JSON report satisfies its checked schema with every section populated,
// and the human report names the findings.
func TestTelemetryReportOutputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	var cli strings.Builder
	for i := range 61 {
		fmt.Fprintf(&cli, `{"at":%q,"command":"system agent","duration_ms":80,"exit_code":10,"version":"dev","mode":"oneshot"}`+"\n", base.Add(time.Duration(i)*time.Second).Format(time.RFC3339Nano))
	}
	fmt.Fprintf(&cli, `{"at":%q,"command":"up","duration_ms":4000,"exit_code":0,"version":"dev","mode":"long_running","measurement":"startup","app":{"id":"shop","name":"Shop"}}`+"\n", base.Format(time.RFC3339Nano))
	cliPath := filepath.Join(root, "telemetry.jsonl")
	if err := os.WriteFile(cliPath, []byte(cli.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	logPath := filepath.Join(home, "worktrees", strings.Repeat("cd", 32), "control", "dev", "shop.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	event := func(kind string, at time.Time, data string) {
		fmt.Fprintf(&log, `{"data":{"type":%q,"time":%q,"app":{"name":"shop","root":"/work/shop"},"data":%s}}`+"\n", kind, at.Format(time.RFC3339Nano), data)
	}
	request := func(op string, at time.Time, ok bool) {
		event("build.step", at, fmt.Sprintf(`{"operation_id":%q,"name":"go.command","started_at":%q,"duration_ms":500,"ok":true}`, op, at.Format(time.RFC3339Nano)))
		event("build.step", at, fmt.Sprintf(`{"operation_id":%q,"name":"build.request","started_at":%q,"duration_ms":1500,"ok":%t,"reason":"source_rebuild"}`, op, at.Format(time.RFC3339Nano), ok))
	}
	request("ok-1", base, true)
	for i := range 5 {
		at := base.Add(time.Duration(i+1) * time.Minute)
		event("build.error", at, `{"error":"generated TypeScript clients are stale"}`)
		request(fmt.Sprintf("failed-%d", i), at, false)
	}
	if err := os.WriteFile(logPath, []byte(log.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(root, "claude", "project", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"content":[{"type":"tool_use","id":"a","name":"Bash","input":{"command":"scenery logs --help"}}]}}
{"type":"user","timestamp":%q,"message":{"content":[{"type":"tool_result","tool_use_id":"a","is_error":true,"content":"Exit code 2\nunknown flag \"--help\""}]}}
`, base.Format(time.RFC3339Nano), base.Add(time.Second).Format(time.RFC3339Nano))
	if err := os.WriteFile(claude, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	options := telemetryReportBuildOptions(telemetryReportOptions{Since: base.Add(-time.Hour), AgentTranscripts: true}, cliPath, home)
	options.Until = base.Add(time.Hour)
	options.ClaudeProjectsDir, options.CodexSessionsDir = filepath.Join(root, "claude"), filepath.Join(root, "codex")
	report, err := telemetryreport.Build(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.CLI.Bursts) != 1 || len(report.Builds.Streaks) != 1 || report.Agents == nil || len(report.Agents.RejectedInputs) != 1 {
		t.Fatalf("fixture did not populate every section: %+v", report)
	}
	response := telemetryReportResponse{cliPayloadIdentity: newCLIPayloadIdentity(telemetryReportPayloadKind), Report: report}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var payload any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.telemetry.report.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("schema diagnostics = %v\n%s", diagnostics, encoded)
	}
	var human bytes.Buffer
	if err := writeTelemetryReportHuman(&human, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[critical] scenery system agent failed 61 times", "5 consecutive builds failed in /work/shop", `rejected unknown flag "--help"`} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("human report lacks %q:\n%s", want, human.String())
		}
	}
}
