package telemetryreport

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"scenery.sh/internal/machine"
)

func TestCLIArchivesPreserveCoverageAndIndependentCohorts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	base := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	dirty := true
	app := &struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{ID: "clean-tech", Name: "ONLV"}
	shared := cliRecord{InvocationID: "shared", At: base, Command: "check", DurationMS: 100, Producer: &machine.Producer{Version: "dev", Commit: "commit"}, Purpose: "development", App: app}
	fixture := cliRecord{InvocationID: "fixture", At: base, Command: "check", DurationMS: 50, Producer: &machine.Producer{Version: "dev", Commit: "commit"}, Purpose: "verification", Dirty: &dirty}
	legacy := cliRecord{At: base, Command: "system agent", DurationMS: 80, ExitCode: 10, Version: "dev"}
	active := filepath.Join(root, "telemetry.jsonl")
	writeLines(t, active, shared, fixture, legacy)
	overlap := shared
	overlap.ExitCode = 10 // Active evidence must win a conflicting archive copy.
	old := legacy
	old.At = base.Add(-10 * 24 * time.Hour)
	archive := filepath.Join(root, "telemetry-archive", "snapshot", "telemetry.jsonl")
	writeLines(t, archive, overlap, legacy, old)
	file, err := os.OpenFile(archive, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("invalid\n")
	_ = file.Close()
	report, err := Build(Options{CLITelemetryPath: active, Since: base.Add(-time.Hour), Until: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if report.CLI.Records != 4 || report.CLI.Failures != 2 || report.CLI.IdentifiedInvocations != 2 || len(report.Sources.CLI) != 2 {
		t.Fatalf("archive totals = %+v", report.CLI)
	}
	coverage := report.Sources.CLI[1]
	if !coverage.Archived || coverage.Status != "complete" || coverage.Records != 3 || coverage.InWindow != 2 || coverage.Duplicates != 1 || coverage.Invalid != 1 || coverage.First != old.At.Format(time.RFC3339Nano) {
		t.Fatalf("archive coverage = %+v", coverage)
	}
	if len(report.CLI.Cohorts) != 3 {
		t.Fatalf("cohorts = %+v", report.CLI.Cohorts)
	}
	for _, cohort := range report.CLI.Cohorts {
		switch cohort.Purpose {
		case "unknown":
			if cohort.Count != 2 || cohort.FailureCount != 2 || cohort.Dirty != "unknown" {
				t.Fatalf("legacy cohort = %+v", cohort)
			}
		case "development":
			if cohort.AppID != "clean-tech" || cohort.Producer != "commit" || cohort.PercentileSampleCount != 1 || *cohort.P95MS != 100 {
				t.Fatalf("app cohort = %+v", cohort)
			}
		case "verification":
			if cohort.Dirty != "dirty" || cohort.AppID != "" {
				t.Fatalf("fixture cohort = %+v", cohort)
			}
		}
	}
	if err := os.Remove(active); err != nil {
		t.Fatal(err)
	}
	report, err = Build(Options{CLITelemetryPath: active, Since: base.Add(-time.Hour), Until: base.Add(time.Hour)})
	if err != nil || report.CLI.Records != 2 || report.Sources.CLI[0].Status != "missing" {
		t.Fatalf("archives without active file = %+v: %v", report.CLI, err)
	}
}
