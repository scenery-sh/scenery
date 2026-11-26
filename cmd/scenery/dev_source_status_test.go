package main

import (
	"errors"
	"fmt"
	"testing"

	"scenery.sh/internal/build"
)

func TestGoBuildDiagnosticSurvivesOrchestration(t *testing.T) {
	underlying := errors.New("dependency/module is unavailable")
	err := fmt.Errorf("detached build: %w", &build.GoCommandError{Err: underlying})
	for _, wrapped := range []error{err, preserveCLIDiagnostic(err)} {
		diagnostic := cliErrorDiagnostic(wrapped)
		if cliExitCode(wrapped) != 3 || diagnostic.Code != "SCN6202" || diagnostic.ReportToken != "" || len(diagnostic.Suggestions) == 0 || !errors.Is(wrapped, underlying) {
			t.Fatalf("build failure = %v, %+v", wrapped, diagnostic)
		}
	}
}
