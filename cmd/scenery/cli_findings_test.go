package main

import (
	"errors"
	"os"
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

func TestHumanFormatCheckNamesDriftingFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := "application   \"demo\"   {\n}\n"
	for name, body := range map[string]string{".scenery.json": "{}\n", "app.scn": source} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout strings.Builder
	err := runContractFmt(&stdout, []string{"--check", "--app-root", root})
	reported, ok := errors.AsType[*cliDiagnosticError](err)
	if !ok || cliExitCode(err) != 1 || reported.diagnostic.Code != "SCN1019" {
		t.Fatalf("error = %#v, want SCN1019 findings with exit 1", err)
	}
	if _, silent := errors.AsType[*silentCLIError](err); silent || stdout.String() != "app.scn\n" || err.Error() != "1 Scenery source files require formatting" {
		t.Fatalf("stdout = %q, error = %q (silent %t)", stdout.String(), err, silent)
	}
	if kept, readErr := os.ReadFile(filepath.Join(root, "app.scn")); readErr != nil || string(kept) != source {
		t.Fatalf("check rewrote the source: %q (%v)", kept, readErr)
	}
}
