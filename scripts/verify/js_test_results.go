package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"scenery.sh/internal/harnessevidence"
	"scenery.sh/internal/harnessreport"
)

const harnessTestResultsKind = "scenery.harness.test_results"

func runHarnessBunStep(ctx context.Context, dir, name string, files []string, expected *int, artifacts harnessArtifactContext) harnessStep {
	return runHarnessJUnitStep(ctx, dir, name, "bun", files, expected, artifacts, func(report string) []string {
		command := []string{"bun", "test", "--reporter=junit", "--reporter-outfile=" + report}
		if strings.Contains(strings.Join(files, ";"), "query_table_") {
			command = append(command, "--tsconfig-override="+filepath.Join(dir, "tools", "typescript", "tsconfig.runtime.json"))
		}
		return append(command, files...)
	})
}

// The raw reporter and normalized cases share the enclosing immutable run.
// Evidence.command retains actual argv even when the probe supplies a rerun.
func runHarnessJUnitStep(ctx context.Context, dir, name, runner string, files []string, expected *int, artifacts harnessArtifactContext, command func(string) []string) harnessStep {
	metadataStarted := time.Now()
	temp, err := os.MkdirTemp("", "scenery-junit-*")
	if err != nil {
		return harnessStep{Name: name, Error: err.Error()}
	}
	defer func() { _ = os.RemoveAll(temp) }()
	reportPath := filepath.Join(temp, "results.xml")
	sourceRoot := artifacts.Root
	if sourceRoot == "" {
		sourceRoot = dir
	}
	input, inputErr := harnessInputRevision(ctx, sourceRoot)
	runnerPath, runnerErr := exec.LookPath(runner)
	argv := command(reportPath)
	if runnerErr == nil {
		argv[0] = runnerPath
	}
	versionCommand := exec.CommandContext(ctx, argv[0], "--version")
	versionBytes, versionErr := versionCommand.Output()
	sourceCommit, commitErr := runHarnessGit(ctx, sourceRoot, "rev-parse", "HEAD")
	metadataMS := time.Since(metadataStarted).Milliseconds()
	step := runHarnessExecStep(ctx, dir, name, argv, artifacts)
	reportStarted := time.Now()
	if step.Summary == nil {
		step.Summary = map[string]any{}
	}
	raw, readErr := os.ReadFile(reportPath)
	result := harnessevidence.ParseJUnit(raw, files, 1)
	result.PayloadIdentity = newCLIPayloadIdentity(harnessTestResultsKind)
	result.Provenance = harnessreport.CurrentProvenance()
	result.RunID = artifacts.RunID + "/" + sanitizeHarnessArtifactName(name)
	result.ParentRunID = artifacts.RunID
	result.StepID = sanitizeHarnessArtifactName(name)
	result.SourceCommit = strings.TrimSpace(string(sourceCommit))
	result.InputRevision = input
	result.Purpose = "verification"
	result.Lane = name
	result.Runner = runner
	result.RunnerVersion = strings.TrimSpace(string(versionBytes))
	result.OS, result.Architecture = runtime.GOOS, runtime.GOARCH
	result.Context = measurementContext(sourceRoot, runner, result.RunnerVersion, strings.Join(files, ";"), "no result replay", 1, 1)
	result.Outcome = "passed"
	if !step.OK {
		result.Outcome = "failed"
	}
	result.Command = append([]string{}, step.Evidence.Command...)
	result.CWD = dir
	result.WallSeconds = float64(step.DurationMS) / 1000
	finalInput, finalErr := harnessInputRevision(ctx, sourceRoot)
	for label, issue := range map[string]error{"raw JUnit": readErr, "input identity": inputErr, "final input identity": finalErr, "source commit": commitErr, "runner executable": runnerErr, "runner version": versionErr} {
		if issue != nil {
			result.Completeness.Complete = false
			result.Completeness.ParserErrors = append(result.Completeness.ParserErrors, label+": "+issue.Error())
		}
	}
	if input != finalInput {
		result.Completeness.Complete = false
		result.Completeness.ParserErrors = append(result.Completeness.ParserErrors, "source inputs changed during test execution")
	}
	if expected != nil && result.Completeness.Terminal != *expected {
		result.Completeness.Complete = false
		result.Completeness.ParserErrors = append(result.Completeness.ParserErrors, fmt.Sprintf("expected %d cases, observed %d", *expected, result.Completeness.Terminal))
	}
	if artifacts.Enabled && len(raw) > 0 {
		reference, writeErr := artifacts.Write(name+" JUnit", sanitizeHarnessArtifactName(name)+".junit.xml", "", raw)
		if writeErr != nil {
			result.Completeness.Complete = false
			result.Completeness.ParserErrors = append(result.Completeness.ParserErrors, "retain JUnit: "+writeErr.Error())
		} else {
			step.Evidence.Artifacts = append(step.Evidence.Artifacts, reference)
		}
	}
	if !result.Completeness.Complete {
		result.Outcome = "incomplete"
	}
	result.Artifacts = append(result.Artifacts, step.Evidence.Artifacts...)
	encoded, encodeErr := json.Marshal(result)
	reference, writeErr := artifacts.Write(name+" case results", sanitizeHarnessArtifactName(name)+".cases.json", harnessTestResultsKind, encoded)
	if encodeErr != nil || writeErr != nil {
		result.Completeness.Complete = false
		step.Diagnostics = append(step.Diagnostics, checkDiagnostic{Stage: name, Severity: "error", Message: "failed to retain normalized case results", SuggestedAction: "Inspect artifact permissions and reporter data."})
	}
	if reference.Path != "" {
		step.Evidence.Artifacts = append(step.Evidence.Artifacts, reference)
	}
	step.Summary["case_results"] = reference
	step.Summary["runner"] = runner
	step.Summary["runner_version"] = result.RunnerVersion
	step.Summary["executable"] = runnerPath
	step.Summary["selected_files"] = files
	step.Summary["case_completeness"] = result.Completeness
	step.Summary["measurement_boundary"] = "subprocess wall time; case times are runner-reported and are not summed"
	if !result.Completeness.Complete || result.Completeness.Executed == 0 {
		step.OK = false
		step.Diagnostics = append(step.Diagnostics, checkDiagnostic{Stage: name, Severity: "error", Message: fmt.Sprintf("incomplete case evidence: expected=%d terminal=%d executed=%d missing_files=%d errors=%s", result.Completeness.Expected, result.Completeness.Terminal, result.Completeness.Executed, len(result.Completeness.MissingFiles), strings.Join(result.Completeness.ParserErrors, "; ")), SuggestedAction: "Inspect retained raw JUnit and command output; incomplete evidence cannot establish this lane."})
	}
	step.Summary["metadata_ms"] = metadataMS
	step.Summary["reporting_ms"] = time.Since(reportStarted).Milliseconds()
	return step
}
