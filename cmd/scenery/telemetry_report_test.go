package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/telemetryreport"
)

func TestParseTelemetryReportArgs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	opts, err := parseTelemetryReportArgs(nil, now)
	if err != nil || !opts.Since.Equal(now.Add(-30*24*time.Hour)) || len(opts.Apps) != 0 || opts.JSON {
		t.Fatalf("defaults = %+v, %v", opts, err)
	}
	opts, err = parseTelemetryReportArgs([]string{"--since", "48h", "--app", "shop", "--app", "billing", "-o", "json"}, now)
	if err != nil || !opts.Since.Equal(now.Add(-48*time.Hour)) || !slices.Equal(opts.Apps, []string{"billing", "shop"}) || !opts.JSON {
		t.Fatalf("explicit = %+v, %v", opts, err)
	}
	for _, args := range [][]string{{"--since", "0s"}, {"extra"}, {"--app", ""}, {"--agent-transcripts"}} {
		if _, err := parseTelemetryReportArgs(args, now); err == nil {
			t.Fatalf("parseTelemetryReportArgs(%v) accepted a wrongly written request", args)
		}
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
	fmt.Fprintf(&cli, `{"at":%q,"command":"up","duration_ms":4000,"exit_code":0,"version":"dev","mode":"long_running","measurement":"startup","caller":"claude","app":{"id":"shop","name":"Shop"}}`+"\n", base.Format(time.RFC3339Nano))
	for range 3 {
		fmt.Fprintf(&cli, `{"at":%q,"command":"logs","duration_ms":10,"exit_code":2,"version":"dev","mode":"oneshot","caller":"claude","diagnostic_code":"SCN8001","rejected_token":"flag:--tail","app":{"id":"shop","name":"Shop"}}`+"\n", base.Format(time.RFC3339Nano))
	}
	fmt.Fprintf(&cli, `{"at":%q,"kind":"proxy_fault","app":{"id":"shop","name":"Shop"},"proxy_fault":{"backend":"api","status":502,"error_class":"connection_refused"}}`+"\n", base.Format(time.RFC3339Nano))
	fmt.Fprintf(&cli, `{"at":%q,"command":"check","duration_ms":-7,"exit_code":0,"mode":"oneshot"}`+"\n", base.Format(time.RFC3339Nano))
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
	event("phase.finish", base, `{"phase_id":"startup","duration_ms":-0.5,"ok":true}`)
	if err := os.WriteFile(logPath, []byte(log.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	options := telemetryReportBuildOptions(telemetryReportOptions{Since: base.Add(-time.Hour)}, cliPath, home)
	options.Until = base.Add(time.Hour)
	report, err := telemetryreport.Build(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.CLI.Bursts) != 1 || len(report.Builds.Streaks) != 1 || len(report.CLI.RejectedTokens) != 1 || len(report.CLI.ProxyFaults) != 1 {
		t.Fatalf("fixture did not populate every section: %+v", report)
	}
	if report.Sources.CLIInvalid != 1 || report.CLI.Records != 65 {
		t.Fatalf("negative CLI duration was not rejected: %+v / %+v", report.Sources, report.CLI)
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
	for _, want := range []string{
		fmt.Sprintf("read %d / captured %d bytes", cli.Len(), cli.Len()),
		fmt.Sprintf("read %d / captured %d bytes", log.Len(), log.Len()),
		"timed successes n=1",
		"known outcomes retained",
		"[critical] scenery system agent failed 61 times",
		"5 consecutive builds failed in /work/shop",
		"rejected flag:--tail: 3",
		"proxy fault api connection_refused (502): 1",
		"Daily CLI trend (UTC)",
		"caller claude: 4",
	} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("human report lacks %q:\n%s", want, human.String())
		}
	}
}
