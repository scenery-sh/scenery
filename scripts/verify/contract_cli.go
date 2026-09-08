package main

import (
	"os"
	"strings"
)

func buildHarnessCLIContractReport(repoRoot string, diagnostics []checkDiagnostic) (harnessCLIContractReport, []checkDiagnostic) {
	fixture, err := os.MkdirTemp("", "scenery-cli-contract-*")
	if err == nil {
		defer func() { _ = os.RemoveAll(fixture) }()
		err = copyHarnessBasicFixture(repoRoot, fixture)
	}
	if err == nil {
		var generated map[string]any
		err = readProductJSON(repoRoot, &generated, "generate", "--target", "contracts", "--app-root", fixture, "-o", "json")
	}
	if err != nil {
		return harnessCLIContractReport{}, append(diagnostics, checkDiagnostic{Stage: "contract drift checks", Severity: "error", Message: "prepare authored public CLI fixture: " + err.Error()})
	}
	return buildHarnessCLIContractReportWithReader(repoRoot, fixture, diagnostics, func(target any, args ...string) error {
		return readProductJSON(repoRoot, target, args...)
	})
}

func buildHarnessCLIContractReportWithReader(repoRoot, fixture string, diagnostics []checkDiagnostic, read func(any, ...string) error) (harnessCLIContractReport, []checkDiagnostic) {
	var help struct {
		Commands []struct {
			Usage []string `json:"usage"`
		} `json:"commands"`
	}
	helpErr := read(&help, "help", "-o", "json")
	var usages []string
	for _, command := range help.Commands {
		usages = append(usages, command.Usage...)
	}
	usage := strings.Join(usages, "\n")
	var report harnessCLIContractReport
	for _, spec := range []struct {
		name, needle string
		args         []string
	}{
		{"version", "scenery version [-o json]", []string{"version", "-o", "json"}},
		{"check", "scenery check [--app-root <path>] [-o json]", []string{"check", "--app-root", fixture, "-o", "json"}},
		{"inspect docs", "scenery inspect docs -o json [--repo-root <path>] [--for-path <path>|--tag <tag>|--status active|reference|completed|deprecated|--review-due|--all]", []string{"inspect", "docs", "--repo-root", repoRoot, "--all", "-o", "json"}},
		{"inspect ui", "scenery inspect ui [--frontend <name>] [--app-root <path>] [-o human|json]", []string{"inspect", "ui", "--app-root", fixture, "-o", "json"}},
		{"inspect harness", "scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] -o json [--app-root <path>] [--repo-root <path>]", []string{"inspect", "harness", "--repo-root", repoRoot, "-o", "json"}},
		{"ps", "scenery ps [-o json] [--app-root <path>] [--watch]", []string{"ps", "--app-root", fixture, "-o", "json"}},
	} {
		item := harnessCLIContractCommand{Name: spec.name, Usage: strings.Contains(usage, spec.needle), Mode: "execute"}
		if helpErr != nil {
			item.Error = helpErr.Error()
		} else if !item.Usage {
			item.Error = "usage text missing " + spec.needle
		}
		var payload map[string]any
		if err := read(&payload, spec.args...); err != nil {
			item.Error = strings.Trim(strings.Join([]string{item.Error, err.Error()}, "; "), "; ")
		} else {
			item.Smoke = true
		}
		if item.Error != "" {
			diagnostics = append(diagnostics, checkDiagnostic{Stage: "contract drift checks", Severity: "error", Message: spec.name + " CLI contract smoke failed: " + item.Error, SuggestedAction: "Fix the public command or its current help contract."})
		}
		report.Commands = append(report.Commands, item)
	}
	_, err := parseHarnessSelfArgs([]string{"--repo-root", repoRoot, "-o", "json"})
	item := harnessCLIContractCommand{Name: "repository verify", Usage: true, Mode: "parse", Smoke: err == nil}
	if err != nil {
		item.Error = err.Error()
		diagnostics = append(diagnostics, checkDiagnostic{Stage: "contract drift checks", Severity: "error", Message: "repository verifier arguments: " + err.Error()})
	}
	report.Commands = append(report.Commands, item)
	return report, diagnostics
}
