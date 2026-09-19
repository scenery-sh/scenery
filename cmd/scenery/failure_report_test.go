package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/machine"
)

func TestAReportTokenResolvesToTheCauseItWithheld(t *testing.T) {
	home := t.TempDir()
	paths := localagent.PathsForHome(home)
	commandAgentPathsOverride = &paths
	t.Cleanup(func() {
		commandAgentPathsOverride = nil
		machine.SetInternalFailureSink(nil)
	})
	installFailureReports([]string{"db", "apply", "--app-root", "/apps/shop", "--url", "postgres://shop:hunter2@db.internal/shop"})

	diagnostic := cliErrorDiagnostic(errors.New("dial tcp 10.0.0.7:5432: connect: connection refused"))
	if diagnostic.Code != "SCN9000" || diagnostic.ReportToken == "" || strings.Contains(diagnostic.Message, "10.0.0.7") {
		t.Fatalf("public diagnostic = %+v", diagnostic)
	}
	report, err := readFailureReport(diagnostic.ReportToken)
	if err != nil {
		t.Fatal(err)
	}
	if report.Kind != failureReportKind || report.Code != "SCN9000" || report.Command != "db apply" ||
		report.Cause != "dial tcp 10.0.0.7:5432: connect: connection refused" || report.ReportToken != diagnostic.ReportToken {
		t.Fatalf("report = %+v", report)
	}
	if joined := strings.Join(report.Arguments, " "); strings.Contains(joined, "hunter2") || !strings.Contains(joined, "/apps/shop") {
		t.Fatalf("arguments were not redacted or lost: %q", joined)
	}
	info, err := os.Stat(filepath.Join(home, "reports", diagnostic.ReportToken+".json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("report file: %v %v", info, err)
	}

	for _, token := range []string{"rpt_absent", "../escape", ""} {
		if _, err := readFailureReport(token); err == nil || cliExitCode(err) != 2 {
			t.Fatalf("token %q: %v", token, err)
		}
	}
	// A classified failure withholds nothing, so it mints no token.
	if usage := cliErrorDiagnostic(usageErrorf("unexpected argument %q", "x")); usage.ReportToken != "" {
		t.Fatalf("usage diagnostic = %+v", usage)
	}
}

func TestFailureReportsAreBounded(t *testing.T) {
	home := t.TempDir()
	paths := localagent.PathsForHome(home)
	commandAgentPathsOverride = &paths
	previous := maxFailureReports
	maxFailureReports = 3
	t.Cleanup(func() { commandAgentPathsOverride, maxFailureReports = nil, previous })
	failureReportArguments.Store(&[]string{"check"})
	started := time.Now()
	for index := range 5 {
		token := fmt.Sprintf("rpt_%04d", index)
		if err := writeFailureReport(machine.InternalFailure{ReportToken: token, Code: "SCN9000", Cause: "cause"}, started); err != nil {
			t.Fatal(err)
		}
		// Every earlier report is older than the next one written.
		_ = os.Chtimes(filepath.Join(home, "reports", token+".json"), started, started.Add(time.Duration(index-10)*time.Minute))
	}
	entries, err := os.ReadDir(filepath.Join(home, "reports"))
	if err != nil || len(entries) != 3 {
		t.Fatalf("retained %d reports: %v", len(entries), err)
	}
	if _, err := readFailureReport("rpt_0000"); err == nil {
		t.Fatal("the oldest report was retained")
	}
	if _, err := readFailureReport("rpt_0004"); err != nil {
		t.Fatal(err)
	}
}
