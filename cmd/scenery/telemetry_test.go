package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestRecordCLITelemetryAppendsPrivateJSONL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
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
