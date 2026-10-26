package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/rotatinglog"
	"strings"
	"testing"
	"time"
)

func TestTelemetryClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args        []string
		wantCommand string
		wantMode    string
	}{
		{nil, "help", "oneshot"},
		{[]string{"build", "--target", "development"}, "build", "oneshot"},
		{[]string{"db", "seed", "--env", "dev"}, "db seed", "oneshot"},
		{[]string{"task", "run", "secret-argument"}, "task run", "oneshot"},
		{[]string{"deploy", "operator@private-host:/private/path"}, "deploy", "oneshot"},
		{[]string{"db", "/private/path"}, "db", "oneshot"},
		{[]string{"task", "private-task"}, "task", "oneshot"},
		{[]string{"logs", "private-token"}, "logs", "oneshot"},
		{[]string{"/private/path", "secret-argument"}, "unknown", "oneshot"},
		{[]string{"--private-token"}, "unknown", "oneshot"},
		{[]string{"config", "set", "private-key", "private-value"}, "config set", "oneshot"},
		{[]string{"generate", "sqlc", "--app-root", "/private/path"}, "generate sqlc", "oneshot"},
		{[]string{"inspect", "artifact"}, "inspect", "oneshot"},
		{[]string{"inspect", "harness", "artifact"}, "inspect harness", "oneshot"},
		{[]string{"feature", "land", "private-feature"}, "feature land", "oneshot"},
		{[]string{"feature", "private-feature"}, "feature", "oneshot"},
		{[]string{"feature", "list", "--watch=1", "-o", "jsonl"}, "feature list", "long_running"},
		{[]string{"feature", "list"}, "feature list", "oneshot"},
		{[]string{"up", "--detach"}, "up", "long_running"},
		{[]string{"worker"}, "worker", "long_running"},
		{[]string{"console"}, "console", "long_running"},
		{[]string{"logs", "--follow", "--app-root", "/private/path"}, "logs", "long_running"},
	}
	for _, test := range tests {
		if got := telemetryCommand(test.args); got != test.wantCommand {
			t.Errorf("telemetryCommand(%q) = %q, want %q", test.args, got, test.wantCommand)
		}
		if got := telemetryMode(test.args); got != test.wantMode {
			t.Errorf("telemetryMode(%q) = %q, want %q", test.args, got, test.wantMode)
		}
	}
}

func TestTelemetryClassificationUsesKnownCommandFamilies(t *testing.T) {
	t.Parallel()
	for root, subcommands := range telemetryReportCommandFamilies() {
		if got := telemetryCommand([]string{root, "private-operand"}); got != root {
			t.Errorf("%q retains an unknown operand: %q", root, got)
		}
		for _, subcommand := range subcommands {
			if got := telemetryCommand([]string{root, subcommand, "private-operand"}); got != root+" "+subcommand {
				t.Errorf("%q %q classification = %q", root, subcommand, got)
			}
		}
	}
}

func TestTelemetryQueryPreservesUnknownDirtyIdentity(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	var lines strings.Builder
	for _, variant := range []struct{ version, dirty string }{{"missing", ""}, {"clean", `,"dirty":false`}, {"dirty", `,"dirty":true`}} {
		fmt.Fprintf(&lines, `{"at":%q,"command":"check","duration_ms":1,"exit_code":0,"version":%q,"mode":"oneshot"%s}`+"\n", time.Now().UTC().Format(time.RFC3339Nano), variant.version, variant.dirty)
	}
	if err := os.WriteFile(path, []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	response, err := loadCLITelemetry(path, telemetryQueryOptions{Limit: 3})
	if err != nil || len(response.Records) != 3 {
		t.Fatalf("query = %+v: %v", response, err)
	}
	for _, record := range response.Records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		dirty, present := fields["dirty"]
		if record.Version == "missing" {
			if present {
				t.Fatalf("historical record invents dirty identity: %s", encoded)
			}
		} else if !present || dirty != (record.Version == "dirty") {
			t.Fatalf("explicit dirty identity changed: %s", encoded)
		}
	}
}

func TestNativeTelemetryUsesResolvedIdentityAndExplicitPurpose(t *testing.T) {
	t.Setenv("SCENERY_EXECUTION_PURPOSE", "verification")
	invocation := newCLITelemetryInvocation(time.Now(), []string{"help"})
	var record cliTelemetryRecord
	invocation.recorder = func(value cliTelemetryRecord) { record = value }
	invocation.finish(0)
	if record.Producer == nil || record.Version != record.Producer.Version || record.Purpose != "verification" || record.Dirty == nil || *record.Dirty != cliBuildDirty() {
		t.Fatalf("record identity = %+v", record)
	}
	for _, invalid := range []string{"", "dev", "fixture", "secret text"} {
		t.Setenv("SCENERY_EXECUTION_PURPOSE", invalid)
		if got := telemetryExecutionPurpose(); got != "unknown" {
			t.Fatalf("purpose %q = %q", invalid, got)
		}
	}
}

func TestRecordCLITelemetryAppendsPrivateJSONL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SCENERY_AGENT_HOME", filepath.Join(home, ".scenery"))
	record := cliTelemetryRecord{
		At:         time.Date(2026, 7, 13, 12, 41, 3, 421000000, time.UTC),
		Command:    "db seed",
		DurationMS: 219,
		ExitCode:   0,
		Version:    "dev",
		Mode:       "oneshot",
	}
	recordCLITelemetry(record)
	recordCLITelemetry(record)

	path := filepath.Join(home, ".scenery", "telemetry.jsonl")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(encoded)), "\n")
	if len(lines) != 2 {
		t.Fatalf("telemetry lines = %d, want 2", len(lines))
	}
	var got cliTelemetryRecord
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got != record {
		t.Fatalf("record = %#v, want %#v", got, record)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("telemetry permissions = %o, want 600", got)
	}
}

func TestCLITelemetryInvocationRecordsUpAtReadinessExactlyOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SCENERY_AGENT_HOME", filepath.Join(home, ".scenery"))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{
  "name": "Example App",
  "id": "example-app",
  "envs": {"local": {"default": true}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	started := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	invocation := newCLITelemetryInvocation(started, []string{"up", "--app-root", root})
	invocation.now = func() time.Time { return started.Add(1750 * time.Millisecond) }

	original := runWithWatchFunc
	t.Cleanup(func() { runWithWatchFunc = original })
	runWithWatchFunc = func(_ devListenRequest, _ bool, _ bool, _ bool, _, _ string, onReady func()) error {
		if onReady == nil {
			t.Fatal("up readiness callback is nil")
		}
		onReady()
		onReady()
		return nil
	}
	if err := upCommandWithTelemetry([]string{"--app-root", root, "-o", "jsonl"}, invocation); err != nil {
		t.Fatal(err)
	}
	// Finishing represents eventual supervisor shutdown. It must not append a
	// process-lifetime record after readiness was already captured.
	invocation.now = func() time.Time { return started.Add(24 * time.Hour) }
	invocation.finish(0)

	encoded, err := os.ReadFile(filepath.Join(home, ".scenery", "telemetry.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(encoded)), "\n")
	if len(lines) != 1 {
		t.Fatalf("telemetry lines = %d, want exactly one startup record", len(lines))
	}
	var record cliTelemetryRecord
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatal(err)
	}
	if record.Command != "up" || record.Measurement != cliTelemetryMeasurementStartup || record.DurationMS != 1750 || record.ExitCode != 0 {
		t.Fatalf("startup telemetry = %#v", record)
	}
	if record.App == nil || record.App.ID != "example-app" {
		t.Fatalf("startup telemetry app = %#v", record.App)
	}
}

func TestCLITelemetryDetachedLauncherDoesNotDuplicateOwnerStartup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SCENERY_AGENT_HOME", filepath.Join(home, ".scenery"))
	invocation := newCLITelemetryInvocation(time.Now(), []string{"up", "--detach"})

	original := runDetachedDevFunc
	t.Cleanup(func() { runDetachedDevFunc = original })
	runDetachedDevFunc = func(_ []string, _ devOptions) error { return nil }
	if err := upCommandWithTelemetry([]string{"--detach"}, invocation); err != nil {
		t.Fatal(err)
	}
	invocation.finish(0)
	if _, err := os.Stat(filepath.Join(home, ".scenery", "telemetry.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("detached launcher telemetry exists: %v", err)
	}
}

func TestTelemetryInvocationAppUsesConfiguredIdentityWithoutPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{
  "name": "Example App",
  "id": "example-app",
  "envs": {"local": {"default": true}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app := telemetryInvocationApp([]string{"check", "--app-root", root})
	if app == nil || app.ID != "example-app" || app.Name != "Example App" {
		t.Fatalf("app = %#v", app)
	}
	encoded, err := json.Marshal(app)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), root) {
		t.Fatalf("app telemetry leaked root: %s", encoded)
	}
	if app := telemetryInvocationAppFrom([]string{"test", "--", "--app-root", root}, t.TempDir()); app != nil {
		t.Fatalf("passthrough app root was attributed: %#v", app)
	}
}

func TestLoadCLITelemetryFiltersAndBoundsRecords(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	records := []cliTelemetryRecord{
		{At: now.Add(-2 * time.Hour), Command: "check", DurationMS: 900, ExitCode: 0, Version: "dev", Mode: "oneshot", App: &cliTelemetryApp{ID: "app-a", Name: "Alpha"}},
		{At: now.Add(-40 * time.Minute), Command: "check", DurationMS: 100, ExitCode: 0, Version: "dev", Mode: "oneshot", App: &cliTelemetryApp{ID: "app-a", Name: "Alpha"}},
		{At: now.Add(-30 * time.Minute), Command: "build", DurationMS: 300, ExitCode: 1, Version: "dev", Mode: "oneshot", App: &cliTelemetryApp{ID: "app-a", Name: "Alpha"}},
		{At: now.Add(-20 * time.Minute), Command: "check", DurationMS: 50, ExitCode: 0, Version: "dev", Mode: "oneshot", App: &cliTelemetryApp{ID: "app-b", Name: "Beta"}},
		{At: now.Add(-10 * time.Minute), Command: "version", DurationMS: 25, ExitCode: 0, Version: "dev", Mode: "oneshot"},
	}
	var encoded bytes.Buffer
	for _, record := range records {
		if err := json.NewEncoder(&encoded).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	encoded.WriteString("not-json\n")
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	response, err := loadCLITelemetry(path, telemetryQueryOptions{Since: now.Add(-time.Hour), Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary.Count != 4 || response.Summary.ReturnedCount != 2 || response.Summary.TotalDurationMS != 475 || response.Summary.AverageMS != 118.75 {
		t.Fatalf("summary = %#v", response.Summary)
	}
	if response.Summary.SuccessCount != 3 || response.Summary.FailureCount != 1 || response.Summary.UnattributedCount != 1 || response.Summary.InvalidCount != 1 {
		t.Fatalf("summary counts = %#v", response.Summary)
	}
	if got := []string{response.Records[0].Command, response.Records[1].Command}; strings.Join(got, ",") != "version,check" {
		t.Fatalf("recent commands = %v", got)
	}
	if len(response.Apps) != 2 || response.Apps[0].App.ID != "app-a" || response.Apps[0].Count != 2 || response.Apps[1].App.ID != "app-b" {
		t.Fatalf("apps = %#v", response.Apps)
	}
	if len(response.Warnings) != 1 || !strings.Contains(response.Warnings[0], "1 invalid") {
		t.Fatalf("warnings = %#v", response.Warnings)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.telemetry.schema.json"), response); len(diagnostics) > 0 {
		t.Fatalf("telemetry schema diagnostics = %v", diagnostics)
	}

	filtered, err := loadCLITelemetry(path, telemetryQueryOptions{Apps: []string{"Alpha"}, Commands: []string{"check"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Summary.Count != 2 || len(filtered.Records) != 2 || filtered.Apps[0].App.ID != "app-a" || len(filtered.Commands) != 1 || filtered.Commands[0].Command != "check" {
		t.Fatalf("filtered response = %#v", filtered)
	}
}

func TestLoadCLITelemetryStartupMeasurementAndPercentiles(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	records := []cliTelemetryRecord{
		{At: now.Add(-6 * time.Minute), Command: "up", DurationMS: 60_000, ExitCode: 0, Version: "old", Mode: "long_running"},
		{At: now.Add(-5 * time.Minute), Command: "up", DurationMS: 100, ExitCode: 0, Version: "dev", Mode: "long_running", Measurement: cliTelemetryMeasurementStartup},
		{At: now.Add(-4 * time.Minute), Command: "up", DurationMS: 200, ExitCode: 0, Version: "dev", Mode: "long_running", Measurement: cliTelemetryMeasurementStartup},
		{At: now.Add(-3 * time.Minute), Command: "up", DurationMS: 300, ExitCode: 0, Version: "dev", Mode: "long_running", Measurement: cliTelemetryMeasurementStartup},
		{At: now.Add(-2 * time.Minute), Command: "up", DurationMS: 400, ExitCode: 0, Version: "dev", Mode: "long_running", Measurement: cliTelemetryMeasurementStartup},
		{At: now.Add(-time.Minute), Command: "up", DurationMS: 500, ExitCode: 0, Version: "dev", Mode: "long_running", Measurement: cliTelemetryMeasurementStartup},
	}
	var encoded bytes.Buffer
	for _, record := range records {
		if err := json.NewEncoder(&encoded).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	response, err := loadCLITelemetry(path, telemetryQueryOptions{Commands: []string{"up"}, Measurements: []string{cliTelemetryMeasurementStartup}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary.Count != 5 || response.Summary.PercentileSampleCount != 5 || response.Summary.P50DurationMS != 300 || response.Summary.P95DurationMS != 500 {
		t.Fatalf("startup summary = %#v", response.Summary)
	}
	if len(response.Measurements) != 1 || response.Measurements[0].Measurement != cliTelemetryMeasurementStartup || response.Measurements[0].P50DurationMS != 300 {
		t.Fatalf("measurement groups = %#v", response.Measurements)
	}
	for _, record := range response.Records {
		if telemetryRecordMeasurement(record) != cliTelemetryMeasurementStartup {
			t.Fatalf("completion record leaked into startup query: %#v", record)
		}
	}
}

func TestLoadCLITelemetryPercentilesUseLatestBoundedSample(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for index := 0; index < maximumTelemetryLimit+5; index++ {
		if err := encoder.Encode(cliTelemetryRecord{
			At:          now.Add(time.Duration(index) * time.Millisecond),
			Command:     "up",
			DurationMS:  int64(index),
			ExitCode:    0,
			Version:     "dev",
			Mode:        "long_running",
			Measurement: cliTelemetryMeasurementStartup,
		}); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	response, err := loadCLITelemetry(path, telemetryQueryOptions{Measurements: []string{cliTelemetryMeasurementStartup}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary.Count != maximumTelemetryLimit+5 || response.Summary.PercentileSampleCount != maximumTelemetryLimit {
		t.Fatalf("bounded percentile summary = %#v", response.Summary)
	}
	if response.Summary.P50DurationMS != 5004 || response.Summary.P95DurationMS != 9504 {
		t.Fatalf("latest-sample percentiles = p50 %d p95 %d, want 5004/9504", response.Summary.P50DurationMS, response.Summary.P95DurationMS)
	}
}

func TestTelemetryCommandJSONAndFilters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SCENERY_AGENT_HOME", filepath.Join(home, ".scenery"))
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	recordCLITelemetry(cliTelemetryRecord{At: now, Command: "check", DurationMS: 12, ExitCode: 0, Version: "dev", Mode: "oneshot", App: &cliTelemetryApp{ID: "app-a", Name: "Alpha"}})

	opts, err := parseTelemetryArgs([]string{"--app", "app-b", "--app", "Alpha", "--app", "app-b", "--command", "check", "--measurement", "startup", "--since", "1h", "--limit", "7", "-o", "json"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(opts.Apps, ",") != "Alpha,app-b" || strings.Join(opts.Commands, ",") != "check" || strings.Join(opts.Measurements, ",") != "startup" || opts.Limit != 7 || !opts.JSON || !opts.Since.Equal(now.Add(-time.Hour)) {
		t.Fatalf("options = %#v", opts)
	}

	var output bytes.Buffer
	if err := runTelemetryCommand(&output, []string{"--app", "app-a", "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	var payload telemetryResponse
	if err := decodeCLIJSON(output.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Kind != cliTelemetryPayloadKind || payload.SchemaRevision != newCLIPayloadIdentity(cliTelemetryPayloadKind).SchemaRevision || payload.Summary.Count != 1 || payload.Records[0].App.ID != "app-a" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestTelemetryArgsRejectInvalidBounds(t *testing.T) {
	now := time.Now()
	for _, args := range [][]string{{"--limit", "0"}, {"--limit", "10001"}, {"--since", "0s"}, {"--app", " "}, {"--measurement", "lifetime"}, {"extra"}} {
		if _, err := parseTelemetryArgs(args, now); err == nil {
			t.Fatalf("parseTelemetryArgs(%q) succeeded", args)
		}
	}
}

func TestTelemetryHonorsPrivateAgentHomeAndNativeIdentity(t *testing.T) {
	home, agentHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SCENERY_AGENT_HOME", agentHome)
	var output bytes.Buffer
	code := executeCLIWith([]string{"unknown", "-o", "json"}, &output, &output, time.Now(), func([]string, *cliTelemetryInvocation) error { return usageErrorf("unknown command") }, recordCLITelemetry)
	if code != 2 {
		t.Fatalf("exit = %d", code)
	}
	data, err := os.ReadFile(filepath.Join(agentHome, "telemetry.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var record cliTelemetryRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.InvocationID == "" || record.Producer == nil || record.Producer.Toolchain.GoVersion == "" || record.DiagnosticCode != "SCN8001" {
		t.Fatalf("record = %+v", record)
	}
	if _, err := os.Stat(filepath.Join(home, ".scenery", "telemetry.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("personal telemetry touched: %v", err)
	}
}

func TestTelemetryPercentilesExcludeFastFailures(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	var data bytes.Buffer
	for _, record := range []cliTelemetryRecord{{At: time.Now(), Command: "check", DurationMS: 1, ExitCode: 2, Mode: "oneshot"}, {At: time.Now(), Command: "check", DurationMS: 1000, Mode: "oneshot"}} {
		if err := json.NewEncoder(&data).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	response, err := loadCLITelemetry(path, telemetryQueryOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary.Count != 2 || response.Summary.FailureCount != 1 || response.Summary.PercentileSampleCount != 1 || response.Summary.P50DurationMS != 1000 {
		t.Fatalf("summary = %+v", response.Summary)
	}
}

func TestRecordCLITelemetryRotatesArchiveAndLoadReadsArchivedSegments(t *testing.T) {
	home := t.TempDir()
	agentHome := filepath.Join(home, ".scenery")
	t.Setenv("HOME", home)
	t.Setenv("SCENERY_AGENT_HOME", agentHome)
	originalMax := cliTelemetryRotateMaxBytes
	cliTelemetryRotateMaxBytes = 250
	t.Cleanup(func() { cliTelemetryRotateMaxBytes = originalMax })

	base := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	for i := range 4 {
		recordCLITelemetry(cliTelemetryRecord{
			InvocationID: fmt.Sprintf("inv-%d", i+1),
			At:           base.Add(time.Duration(i) * time.Minute),
			Command:      "check",
			DurationMS:   int64(10 * (i + 1)),
			ExitCode:     0,
			Version:      "dev",
			Mode:         "oneshot",
		})
	}

	archives, err := filepath.Glob(filepath.Join(agentHome, "telemetry-archive", "*", "telemetry.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) == 0 {
		t.Fatal("expected at least one rotated telemetry archive segment")
	}

	for _, archive := range archives {
		if !strings.HasPrefix(filepath.Base(filepath.Dir(archive)), "cli-"+time.Now().UTC().Format("20060102")) {
			t.Fatalf("archive uses invocation start instead of rotation time: %s", archive)
		}
	}
	response, err := loadCLITelemetry(filepath.Join(agentHome, "telemetry.jsonl"), telemetryQueryOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if response.Summary.Count != 4 || len(response.Records) != 4 {
		t.Fatalf("expected 4 records across archive and active telemetry.jsonl, got summary=%+v records=%d", response.Summary, len(response.Records))
	}
	if response.Records[0].InvocationID != "inv-4" || response.Records[3].InvocationID != "inv-1" {
		t.Fatalf("unexpected record order: %+v", response.Records)
	}
}

func TestRunConsoleEventLogCapturesForegroundSupervisorEvents(t *testing.T) {
	t.Parallel()
	var humanOut, errOut, logOut bytes.Buffer
	console := newRunConsole(&humanOut, &errOut, false, false, "example-app", "/workspace/example")
	console.SetEventLog(&logOut)

	if err := console.Phase("Prepare runtime", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	console.Banner(runURLs{API: "http://127.0.0.1:4100"})
	console.RebuildDetected([]string{"app.scn"})
	console.Finish(nil)

	if !strings.Contains(humanOut.String(), "scenery development server running") {
		t.Fatalf("expected human banner on stdout, got %q", humanOut.String())
	}
	lines := strings.Split(strings.TrimSpace(logOut.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 JSONL event lines in foreground eventLog, got %d:\n%s", len(lines), logOut.String())
	}
	var kinds []string
	for _, line := range lines {
		var envelope struct {
			Event string `json:"event"`
			Data  struct {
				Type string `json:"type"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("unmarshal event line %q: %v", line, err)
		}
		if envelope.Event == "summary" {
			kinds = append(kinds, "summary")
		} else {
			kinds = append(kinds, envelope.Data.Type)
		}
	}
	if got, want := strings.Join(kinds, ","), "phase.start,phase.finish,run.ready,rebuild.detected,summary"; got != want {
		t.Fatalf("event sequence = %q, want %q", got, want)
	}
}

func TestTelemetryPrunePreservesForeignBundles(t *testing.T) {
	root := t.TempDir()
	for i := range cliTelemetryMaxArchiveBundles + 2 {
		dir := filepath.Join(root, fmt.Sprintf("cli-%03d", i))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "telemetry.jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	foreign := filepath.Join(root, "manual-reset")
	if err := os.MkdirAll(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	preserved := filepath.Join(root, "cli-000", "supervisor.log")
	if err := os.WriteFile(preserved, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	pruneCLITelemetryArchives(root)
	for _, path := range []string{foreign, preserved, filepath.Join(root, "cli-000", "telemetry.jsonl")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("preserved bundle lost: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "cli-001")); !os.IsNotExist(err) {
		t.Fatalf("old owned bundle was not removed: %v", err)
	}
}

func TestForegroundLogCapturesStartupAndSummaryInUniqueFiles(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", home)
	for range rotatinglog.Sessions + 2 {
		var output bytes.Buffer
		console := newRunConsole(&output, &output, false, false, "app", root)
		_, closeLog, err := attachForegroundEventLog(console, root)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer closeLog()
			defer console.Finish(nil)
			if err := console.Phase("Starting worktree control", func() error { return nil }); err != nil {
				t.Fatal(err)
			}
		}()
	}
	logs, err := filepath.Glob(filepath.Join(home, "worktrees", "*", "control", "dev", "*.log"))
	if err != nil || len(logs) != rotatinglog.Sessions+2 {
		t.Fatalf("logs=%v err=%v", logs, err)
	}
	for _, path := range logs {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte("phase.start")) || !bytes.Contains(data, []byte(`"event":"summary"`)) {
			t.Fatalf("incomplete events: %s", data)
		}
	}
}

func TestTelemetryQueryRefusesConcurrentRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	lock, err := localagent.AcquireProcessLock(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	if _, err := loadCLITelemetry(path, telemetryQueryOptions{Limit: 10}); err == nil {
		t.Fatal("query read a snapshot while rotation owned its lock")
	}
}
