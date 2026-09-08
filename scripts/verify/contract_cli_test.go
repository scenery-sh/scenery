package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRepositoryCLIContractPublicBoundaryReportsFailure(t *testing.T) {
	t.Parallel()
	var commands []string
	report, diagnostics := buildHarnessCLIContractReportWithReader("/repo", "/fixture", nil, func(target any, args ...string) error {
		commands = append(commands, strings.Join(args, " "))
		if args[0] == "help" {
			return json.Unmarshal([]byte(`{"commands":[{"usage":["scenery version [-o json]"]}]}`), target)
		}
		if args[0] == "check" {
			return errors.New("fixture contract rejected")
		}
		return json.Unmarshal([]byte(`{}`), target)
	})
	if len(commands) != 7 || commands[0] != "help -o json" || commands[2] != "check --app-root /fixture -o json" {
		t.Fatalf("public calls = %v", commands)
	}
	if len(report.Commands) != 7 || !report.Commands[0].Usage || !report.Commands[0].Smoke {
		t.Fatalf("version evidence = %+v", report)
	}
	check := report.Commands[1]
	if check.Smoke || !strings.Contains(check.Error, "fixture contract rejected") {
		t.Fatalf("check evidence lost failure: %+v", check)
	}
	if !hasErrorDiagnostics(diagnostics) {
		t.Fatal("missing help and command failure were accepted")
	}
	if verifier := report.Commands[6]; verifier.Name != "repository verify" || !verifier.Smoke {
		t.Fatalf("verifier grammar = %+v", verifier)
	}
}
