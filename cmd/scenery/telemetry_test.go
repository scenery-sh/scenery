package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTelemetryClassification(t *testing.T) {
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
