package main

import (
	"os"
	"strings"
	"sync"
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
	calls := [][]string{harnessCLIHelpArgs}
	for _, smoke := range harnessCLIContractSmokes(repoRoot, fixture) {
		calls = append(calls, smoke.args)
	}
	return buildHarnessCLIContractReportWithReader(repoRoot, fixture, diagnostics, prefetchProductJSON(repoRoot, calls))
}

// prefetchProductJSON runs independent read-only product commands
// concurrently. The returned reader serves their recorded results to the
// ordered report and runs any other command directly.
func prefetchProductJSON(repoRoot string, calls [][]string) func(any, ...string) error {
	type result struct {
		output []byte
		err    error
	}
	results := make([]result, len(calls))
	var wg sync.WaitGroup
	for i, args := range calls {
		wg.Go(func() { results[i].output, results[i].err = runProductJSON(repoRoot, args...) })
	}
	wg.Wait()
	byArgs := make(map[string]result, len(calls))
	for i, args := range calls {
		byArgs[strings.Join(args, "\x00")] = results[i]
	}
	return func(target any, args ...string) error {
		recorded, ok := byArgs[strings.Join(args, "\x00")]
		if !ok {
			return readProductJSON(repoRoot, target, args...)
		}
		if recorded.err != nil {
			return recorded.err
		}
		return decodeCLIJSON(recorded.output, target)
	}
}

var harnessCLIHelpArgs = []string{"help", "-o", "json"}

type harnessCLIContractSmoke struct {
	name, needle string
	args         []string
}

func harnessCLIContractSmokes(repoRoot, fixture string) []harnessCLIContractSmoke {
	return []harnessCLIContractSmoke{
		{"version", "scenery version [-o json]", []string{"version", "-o", "json"}},
		{"check", "scenery check [--app-root <path>] [-o json]", []string{"check", "--app-root", fixture, "-o", "json"}},
		{"metrics", "scenery metrics [-o json]", []string{"help", "metrics", "-o", "json"}},
		{"inspect ui", "scenery inspect ui [--frontend <name>] [--app-root <path>] [-o human|json]", []string{"inspect", "ui", "--app-root", fixture, "-o", "json"}},
		{"inspect harness", "scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] -o json [--app-root <path>] [--repo-root <path>]", []string{"inspect", "harness", "--repo-root", repoRoot, "-o", "json"}},
		{"ps", "scenery ps [-o json] [--app-root <path>] [--watch]", []string{"ps", "--app-root", fixture, "-o", "json"}},
		{"context", "scenery context --for <module|service|route|file>", []string{"context", "--for", "service", "--app-root", fixture, "-o", "json"}},
		{"test", "scenery test [<module>...]", []string{"test", "service", "--app-root", fixture, "-o", "json"}},
		{"db query", "scenery db query --service <module>", []string{"help", "db", "query", "-o", "json"}},
		{"durable run", "scenery durable run <target>", []string{"help", "durable", "run", "-o", "json"}},
		{"add module", "scenery add module <name>", []string{"help", "add", "module", "-o", "json"}},
		{"call", "scenery call <target>", []string{"help", "call", "-o", "json"}},
	}
}

func buildHarnessCLIContractReportWithReader(repoRoot, fixture string, diagnostics []checkDiagnostic, read func(any, ...string) error) (harnessCLIContractReport, []checkDiagnostic) {
	var help struct {
		Commands []struct {
			Usage []string `json:"usage"`
		} `json:"commands"`
	}
	helpErr := read(&help, harnessCLIHelpArgs...)
	var usages []string
	for _, command := range help.Commands {
		usages = append(usages, command.Usage...)
	}
	usage := strings.Join(usages, "\n")
	var report harnessCLIContractReport
	for _, spec := range harnessCLIContractSmokes(repoRoot, fixture) {
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
