package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/graph"
)

func TestFindingsAreNotRecordedAsInternalFailures(t *testing.T) {
	formatting := graph.Diagnostic{Code: "SCN1019", Severity: "error", Message: "1 Scenery source files require formatting"}
	tests := []struct {
		name        string
		err         error
		wantExit    int
		wantCode    string
		wantReports int
		wantStderr  string
	}{
		{name: "rendered findings", err: &silentCLIError{err: errors.New("scenery validate failed")}, wantExit: 1},
		{name: "human findings", err: &codedCLIError{err: errors.New("scenery validate failed"), code: 1}, wantExit: 1, wantStderr: "scenery validate failed\n"},
		{name: "rendered diagnostic", err: &silentCLIError{err: findingsDiagnosticError(errors.New(formatting.Message), formatting), code: 1}, wantExit: 1, wantCode: "SCN1019"},
		{name: "human diagnostic", err: findingsDiagnosticError(errors.New("generated artifacts are stale: a.go"), graph.Diagnostic{Code: "SCN6204", Severity: "error", Message: "generated artifacts are stale: a.go"}), wantExit: 1, wantCode: "SCN6204", wantStderr: "generated artifacts are stale: a.go\n"},
		{name: "internal failure", err: errors.New("internal: compiler invariant"), wantExit: 10, wantCode: "SCN9000", wantReports: 1, wantStderr: "internal: compiler invariant\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("SCENERY_AGENT_HOME", home)
			var record cliTelemetryRecord
			var stdout, stderr strings.Builder
			exit := executeCLIWith([]string{"fmt", "--check"}, &stdout, &stderr, time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC), func([]string, *cliTelemetryInvocation) error {
				return test.err
			}, func(got cliTelemetryRecord) { record = got })
			if exit != test.wantExit || record.ExitCode != test.wantExit || record.DiagnosticCode != test.wantCode {
				t.Fatalf("exit %d, telemetry exit %d and diagnostic %q; want %d and %q", exit, record.ExitCode, record.DiagnosticCode, test.wantExit, test.wantCode)
			}
			reports, err := filepath.Glob(filepath.Join(home, "reports", "rpt_*.json"))
			if err != nil || len(reports) != test.wantReports {
				t.Fatalf("failure reports = %v (%v), want %d", reports, err, test.wantReports)
			}
			if stderr.String() != test.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantStderr)
			}
		})
	}
}
