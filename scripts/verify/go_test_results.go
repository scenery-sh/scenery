package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"scenery.sh/internal/harnessreport"
)

// Native case duration and enclosing command wall time have distinct boundaries.
// A cached package describes earlier execution and contributes no executed cases.
func normalizeGoCases(output []byte, report *harnessTestTimingReport, artifacts []harnessEvidenceArtifact) harnessreport.TestResults {
	result := harnessreport.TestResults{PayloadIdentity: newCLIPayloadIdentity(harnessTestResultsKind), Provenance: harnessreport.CurrentProvenance(), Context: report.Context,
		RunID: report.RunID + "/go-tests", ParentRunID: report.RunID, StepID: "go-tests", SourceCommit: report.SourceCommit, InputRevision: report.InputRevision,
		Purpose: "verification", Lane: report.Budgets.Lane, Runner: "go", RunnerVersion: report.Context.RunnerVersion, OS: report.Context.OS, Architecture: report.Context.Architecture,
		Command: append([]string{}, report.Command...), CWD: "", SelectedFiles: []string{}, SelectedPackages: []string{}, Filter: ".*", WallSeconds: report.TotalSeconds, Attempt: 1,
		Cases: []harnessreport.TestCaseResult{}, Artifacts: append([]harnessEvidenceArtifact{}, artifacts...), UnknownStages: []string{"case source files", "per-case hooks and cleanup", "cached prior execution attempt history"}, Outcome: "passed"}
	replayed := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		var event goTestJSONEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if strings.Contains(event.Output, "(cached)") {
			replayed[event.Package] = true
		}
		if event.Test == "" || (event.Action != "pass" && event.Action != "fail" && event.Action != "skip") {
			continue
		}
		outcome := map[string]string{"pass": "passed", "fail": "failed", "skip": "skipped"}[event.Action]
		parent := event.Package
		if offset := strings.LastIndex(event.Test, "/"); offset >= 0 {
			parent = event.Package + "::" + event.Test[:offset]
		}
		result.Cases = append(result.Cases, harnessreport.TestCaseResult{ID: event.Package + "::" + event.Test, ParentID: parent, Suite: event.Package, Name: event.Test, Outcome: outcome, FirstAttemptOutcome: outcome, Seconds: event.Elapsed, Attempt: 1, Boundary: "native runner-reported case; excludes parallel child lifetime"})
		if outcome == "failed" {
			result.Outcome = "failed"
		}
	}
	for i := range result.Cases {
		entry := &result.Cases[i]
		entry.Replayed = replayed[entry.Suite]
		if entry.Replayed {
			entry.FirstAttemptOutcome = "incomplete"
			entry.Boundary = "replayed prior runner-reported case"
		} else if entry.Outcome != "skipped" {
			result.Completeness.Executed++
		}
	}
	for _, pkg := range report.Packages {
		result.SelectedPackages = append(result.SelectedPackages, pkg.Package)
	}
	sort.Strings(result.SelectedPackages)
	result.Completeness.Expected = report.Discovery.TestStarts
	result.Completeness.Discovered = report.Discovery.TestStarts
	result.Completeness.Terminal = len(result.Cases)
	result.Completeness.ParserErrors = append([]string{}, report.Discovery.ParserErrors...)
	result.Completeness.MissingFiles = []string{}
	result.Completeness.Complete = report.Discovery.Complete
	if !result.Completeness.Complete {
		result.Outcome = "incomplete"
	}
	return result
}
