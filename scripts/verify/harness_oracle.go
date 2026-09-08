package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/testsuite"
)

const (
	harnessChangedAreaKind  = "scenery.harness.changed_area"
	harnessTestTimingKind   = "scenery.harness.test_timing"
	harnessAgentContextKind = "scenery.agent_context"
)

const (
	harnessOptimizationTargetSeconds = 5
	cachedHarnessTotalSeconds        = 5
	freshHarnessTotalSeconds         = 5
	releaseHarnessTotalSeconds       = 30
	commandPackageTimingSeconds      = 15
	harnessTimingConfirmationRuns    = 20
	// Cold-prepare budgets hold the current suite, not the 0050-era target.
	// 60 is today's packages-with-tests count. The wall-time budget is measured
	// at the pinned four-build concurrency; summed build elapsed is attribution
	// only because concurrent subprocess durations overlap.
	harnessTestBinaryCountBudget    = 60
	harnessColdPrepareSecondsBudget = 30
)

const (
	harnessConfirmationScopeRegressions = "regressions"
	harnessConfirmationScopeAll         = "all"
)

var (
	harnessCollectChangedFiles = collectHarnessChangedFiles
	harnessListGoPackages      = listHarnessGoPackages
)

type goTestJSONEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Elapsed float64   `json:"Elapsed"`
	Output  string    `json:"Output"`
}

type harnessAgentContext struct {
	cliPayloadIdentity
	GeneratedAt                    string                       `json:"generated_at"`
	Repo                           harnessAgentContextRepo      `json:"repo"`
	CurrentBranch                  string                       `json:"current_branch,omitempty"`
	CurrentCommit                  string                       `json:"current_commit,omitempty"`
	DirtyFiles                     []harnessChangedFile         `json:"dirty_files"`
	ChangedArea                    *harnessChangedAreaReport    `json:"changed_area,omitempty"`
	FailingSteps                   []harnessAgentFailingStep    `json:"failing_steps"`
	RerunCommands                  []string                     `json:"rerun_commands"`
	ChangedAreaRecommendedCommands []string                     `json:"changed_area_recommended_commands"`
	RecommendedCommands            []string                     `json:"recommended_commands"`
	RelevantActiveExecPlans        []harnessAgentExecPlan       `json:"relevant_active_execplans"`
	RecentFailedHarnessArtifacts   []harnessAgentFailedArtifact `json:"recent_failed_harness_artifacts"`
	DocsFreshness                  harnessAgentDocsFreshness    `json:"docs_freshness"`
	ValidationClassification       []string                     `json:"validation_classification"`
	RiskClassification             []string                     `json:"risk_classification"`
	DocsEntrypoints                []string                     `json:"docs_entrypoints"`
	Schemas                        []string                     `json:"schemas"`
	KnownFastLoop                  string                       `json:"known_fast_loop"`
	KnownReleaseLoop               string                       `json:"known_release_loop"`
	ArchitectureRules              []string                     `json:"architecture_rules"`
	RecentFailures                 []string                     `json:"recent_failures,omitempty"`
}

type harnessAgentContextRepo struct {
	Root       string `json:"root"`
	ModulePath string `json:"module_path"`
	GoModPath  string `json:"go_mod_path"`
}

type harnessAgentFailingStep struct {
	Name            string                    `json:"name"`
	Error           string                    `json:"error,omitempty"`
	FirstFileToRead string                    `json:"first_file_to_read,omitempty"`
	RerunCommand    string                    `json:"rerun_command,omitempty"`
	Artifacts       []harnessEvidenceArtifact `json:"artifacts,omitempty"`
	Diagnostics     []checkDiagnostic         `json:"diagnostics,omitempty"`
}

type harnessAgentExecPlan struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Owner   string `json:"owner,omitempty"`
	Summary string `json:"summary,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type harnessAgentFailedArtifact struct {
	Step           string `json:"step"`
	Name           string `json:"name"`
	Path           string `json:"path"`
	Kind           string `json:"kind,omitempty"`
	SchemaRevision string `json:"schema_revision,omitempty"`
	RerunCommand   string `json:"rerun_command,omitempty"`
}

type harnessAgentDocsFreshness struct {
	Kind             string   `json:"kind,omitempty"`
	SchemaRevision   string   `json:"schema_revision,omitempty"`
	DocumentCount    int      `json:"document_count"`
	MissingCount     int      `json:"missing_count"`
	ReviewDueCount   int      `json:"review_due_count"`
	StaleCount       int      `json:"stale_count"`
	MissingDocuments []string `json:"missing_documents,omitempty"`
	ReviewDueDocs    []string `json:"review_due_docs,omitempty"`
	StaleDocs        []string `json:"stale_docs,omitempty"`
	Error            string   `json:"error,omitempty"`
}

func runHarnessChangedAreaStep(ctx context.Context, repoRoot string) (harnessStep, *harnessChangedAreaReport) {
	started := time.Now()
	report := buildHarnessChangedAreaReport(ctx, repoRoot)
	step := harnessStep{
		Name:       "changed area oracle",
		Command:    []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary", "--write"},
		OK:         !hasErrorDiagnostics(report.Diagnostics),
		DurationMS: time.Since(started).Milliseconds(),
		Summary: map[string]any{
			"changed_files":        len(report.ChangedFiles),
			"affected_packages":    len(report.AffectedPackages),
			"validation_classes":   len(report.ValidationClasses),
			"recommended_commands": len(report.RecommendedCommands),
			"risk_flags":           len(report.RiskFlags),
		},
		Diagnostics: report.Diagnostics,
	}
	if !step.OK {
		step.Error = "changed-area oracle failed"
	}
	return step, report
}

func buildHarnessChangedAreaReport(ctx context.Context, repoRoot string) *harnessChangedAreaReport {
	report := &harnessChangedAreaReport{
		PayloadIdentity:     newCLIPayloadIdentity(harnessChangedAreaKind),
		ChangedFiles:        []harnessChangedFile{},
		AffectedPackages:    []string{},
		ValidationClasses:   []string{},
		RecommendedCommands: []string{},
		RelevantDocs:        []string{},
		RiskFlags:           []string{},
		Diagnostics:         []checkDiagnostic{},
	}
	changes, diagnostics := harnessCollectChangedFiles(ctx, repoRoot)

	packages, err := harnessListGoPackages(ctx, repoRoot)
	if err != nil {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "changed area oracle",
			Severity:        "warning",
			Message:         "failed to list Go packages: " + err.Error(),
			SuggestedAction: "Run `go list ./...` from the repo root and fix package loading errors.",
		})
	}
	populateHarnessChangedAreaReport(repoRoot, report, changes, packages, diagnostics)
	return report
}

func collectHarnessChangedFiles(ctx context.Context, repoRoot string) ([]harnessChangedFile, []checkDiagnostic) {
	type source struct {
		status string
		args   []string
	}
	sources := []source{
		{status: "modified", args: []string{"diff", "--name-only", "HEAD"}},
		{status: "staged", args: []string{"diff", "--name-only", "--cached"}},
		{status: "untracked", args: []string{"ls-files", "--others", "--exclude-standard"}},
	}

	byPath := map[string]harnessChangedFile{}
	var diagnostics []checkDiagnostic
	for _, src := range sources {
		output, err := runHarnessGit(ctx, repoRoot, src.args...)
		if err != nil {
			if src.status == "modified" && strings.Contains(err.Error(), "bad revision") {
				continue
			}
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "changed area oracle",
				Severity:        "warning",
				Message:         "git " + strings.Join(src.args, " ") + " failed: " + err.Error(),
				SuggestedAction: "Run `git status --short` from the repo root and fix repository state if needed.",
			})
			continue
		}
		for _, path := range splitCommandLines(output) {
			path = filepath.ToSlash(strings.TrimSpace(path))
			if path == "" {
				continue
			}
			existing := byPath[path]
			existing.Path = path
			existing.Status = mergeHarnessChangeStatus(existing.Status, src.status)
			byPath[path] = existing
		}
	}

	paths := sortedKeysChanged(byPath)
	changes := make([]harnessChangedFile, 0, len(paths))
	for _, path := range paths {
		changes = append(changes, byPath[path])
	}
	return changes, diagnostics
}

func runHarnessGit(ctx context.Context, repoRoot string, args ...string) (string, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return "", err
	}
	cmd := commandTreeContext(ctx, path, args...)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func listHarnessGoPackages(ctx context.Context, repoRoot string) ([]harnessPackageInfo, error) {
	path, err := exec.LookPath("go")
	if err != nil {
		return nil, err
	}
	// Only ImportPath and Dir are decoded below. Selecting them keeps the
	// decode path identical while cutting what `go list` has to compute and
	// emit from ~360KB to ~8KB for this repo.
	cmd := commandTreeContext(ctx, path, "list", "-json=ImportPath,Dir", "./...")
	cmd.Dir = repoRoot
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(output))
	var packages []harnessPackageInfo
	for {
		var payload struct {
			ImportPath string `json:"ImportPath"`
			Dir        string `json:"Dir"`
		}
		if err := dec.Decode(&payload); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if payload.ImportPath == "" || payload.Dir == "" {
			continue
		}
		rel, err := filepath.Rel(repoRoot, payload.Dir)
		if err != nil {
			rel = payload.Dir
		}
		packages = append(packages, harnessPackageInfo{
			ImportPath: payload.ImportPath,
			Dir:        filepath.Clean(payload.Dir),
			RelDir:     filepath.ToSlash(filepath.Clean(rel)),
		})
	}
	sort.Slice(packages, func(i, j int) bool {
		return len(packages[i].Dir) > len(packages[j].Dir)
	})
	return packages, nil
}

func runHarnessGoTestTimingStepForMode(ctx context.Context, repoRoot, mode string, freshTests bool, artifactCtxs ...harnessArtifactContext) (harnessStep, *harnessTestTimingReport) {
	return runHarnessGoTestTimingStepWithBudgets(ctx, repoRoot, harnessTestTimingBudgetsForMode(mode, freshTests), freshTests, artifactCtxs...)
}

func harnessTestTimingBudgetsForMode(mode string, freshTests bool) harnessTestTimingBudgets {
	budgets := defaultHarnessTestTimingBudgets()
	if freshTests {
		budgets.Lane = "fresh"
		budgets.TotalSeconds = freshHarnessTotalSeconds
		budgets.ConfirmationRuns = harnessTimingConfirmationRuns
		budgets.ConfirmationScope = harnessConfirmationScopeRegressions
	}
	if mode == harnessSelfModeRelease {
		budgets.Lane = "release"
		budgets.TotalSeconds = releaseHarnessTotalSeconds
		budgets.Mode = "enforce-total"
		if budgets.ConfirmationRuns > 0 {
			budgets.ConfirmationScope = harnessConfirmationScopeAll
		}
	}
	return budgets
}

func runHarnessGoTestTimingStepWithBudgets(ctx context.Context, repoRoot string, budgets harnessTestTimingBudgets, freshTests bool, artifactCtxs ...harnessArtifactContext) (harnessStep, *harnessTestTimingReport) {
	started := time.Now()
	command := harnessSelfGoTestCommandWithCacheMode(freshTests)
	testEnv := harnessSelfGoTestEnv()
	evidence := newHarnessEvidence(command, repoRoot, started)
	step := harnessStep{
		Name:     "go tests",
		Command:  command,
		Evidence: &evidence,
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		step.OK = false
		step.DurationMS = time.Since(started).Milliseconds()
		step.Error = "go not found in PATH"
		step.Diagnostics = []checkDiagnostic{{
			Stage:           step.Name,
			Severity:        "error",
			Message:         step.Error,
			SuggestedAction: installSuggestion("go"),
		}}
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, "", step.Error, exitCodeFromError(err), nil)
		return step, &harnessTestTimingReport{
			PayloadIdentity: newCLIPayloadIdentity(harnessTestTimingKind),
			Command:         command,
			Env:             testEnv,
			TotalSeconds:    float64(step.DurationMS) / 1000,
			Budgets:         budgets,
			Diagnostics:     step.Diagnostics,
		}
	}
	outputFile, err := os.CreateTemp("", "scenery-go-test-*.json")
	if err != nil {
		step.OK = false
		step.DurationMS = time.Since(started).Milliseconds()
		step.Error = "create go test timing output file: " + err.Error()
		step.Diagnostics = []checkDiagnostic{{
			Stage:           step.Name,
			Severity:        "error",
			Message:         step.Error,
			SuggestedAction: "Check temporary directory permissions and available disk space, then rerun `go run ./scripts/verify -o json`.",
		}}
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, "", step.Error, exitCodeFromError(err), nil)
		return step, &harnessTestTimingReport{
			PayloadIdentity: newCLIPayloadIdentity(harnessTestTimingKind),
			Command:         command,
			Env:             testEnv,
			TotalSeconds:    float64(step.DurationMS) / 1000,
			Budgets:         budgets,
			Diagnostics:     step.Diagnostics,
		}
	}
	outputPath := outputFile.Name()
	defer func() { _ = os.Remove(outputPath) }()
	var testResult testsuite.Result
	var runErr error
	if freshTests {
		testResult, runErr = testsuite.Run(ctx, testsuite.Options{
			RepoRoot:           repoRoot,
			CacheDir:           filepath.Join(repoRoot, ".scenery", "harness", "test-binaries"),
			RunPattern:         ".*",
			PackageParallelism: testsuite.DefaultPackageParallelism,
			BuildParallelism:   testsuite.DefaultBuildParallelism,
			RecordTimings:      true,
			Output:             outputFile,
			Env:                envWithOverrides(envpolicy.Environ(), testEnv...),
		})
	} else {
		cmd := commandTreeContext(ctx, goPath, command[1:]...)
		cmd.Dir = repoRoot
		cmd.Env = envWithOverrides(envpolicy.Environ(), testEnv...)
		cmd.Stdout = outputFile
		cmd.Stderr = outputFile
		runErr = cmd.Run()
	}
	suiteElapsed := time.Since(started)
	closeErr := outputFile.Close()
	output, readErr := os.ReadFile(outputPath)
	if readErr != nil && runErr == nil {
		runErr = readErr
	}
	if closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	report := parseHarnessGoTestTimingWithBudgets(output, command, suiteElapsed, budgets)
	report.Env = append([]string{}, testEnv...)
	if freshTests {
		report.TestBinaries = harnessTestBinaryTimingFromResult(testResult)
		applyHarnessColdBinaryBudgets(report)
	}
	if runErr == nil && freshTests {
		selectHarnessTimingConfirmations(report, readHarnessTimingBaseline(repoRoot))
		confirmHarnessTimingOutliers(ctx, repoRoot, report, runHarnessTimingConfirmationCommand)
	}
	elapsed := time.Since(started)
	step.DurationMS = elapsed.Milliseconds()
	step.Summary = map[string]any{
		"packages":                   len(report.Packages),
		"observed_slow_tests":        len(report.ObservedSlowTests),
		"observed_integration_tests": len(report.ObservedIntegrationTests),
		"confirmed_slow_tests":       len(report.SlowTests),
		"total_seconds":              report.TotalSeconds,
		"confirmation_seconds":       report.ConfirmationSeconds,
		"timing_lane":                report.Budgets.Lane,
		"env":                        testEnv,
	}
	if freshTests {
		step.Summary["test_results"] = testResult.TestResultCount
		step.Summary["test_binaries_built"] = testResult.BuiltCount
		step.Summary["test_package_count"] = testResult.TestPackageCount
		step.Summary["test_manifest_hit"] = testResult.ManifestHit
		step.Summary["test_binary_prepare_seconds"] = roundSeconds(testResult.Prepare.Elapsed.Seconds())
		step.Summary["test_binary_build_parallelism"] = testResult.BuildParallelism
		step.Summary["test_binary_aggregate_build_seconds"] = roundSeconds(testResult.Prepare.AggregateBuildElapsed().Seconds())
		step.Summary["deferred_confirmations"] = len(report.DeferredConfirmations)
	}
	step.Diagnostics = report.Diagnostics
	artifacts, artifactDiagnostics := writeHarnessOutputEvidenceArtifacts(optionalHarnessArtifactContext(artifactCtxs), step.Name, "go-test.jsonl", "go.test.jsonl", output, nil)
	step.Diagnostics = append(step.Diagnostics, artifactDiagnostics...)
	if runErr != nil {
		step.OK = false
		step.Error = strings.TrimSpace(runErr.Error())
		step.OutputTail = tailString(string(output), 8192)
		failureSummary := summarizeGoTestFailures(output)
		step.Diagnostics = append(step.Diagnostics, checkDiagnostic{
			Stage:           step.Name,
			Severity:        "error",
			Message:         firstNonEmpty(failureSummary, strings.TrimSpace(step.OutputTail), step.Error),
			SuggestedAction: rerunSuggestion(command, repoRoot),
		})
		report.Diagnostics = step.Diagnostics
		finalizeHarnessEvidence(step.Evidence, elapsed, step.OK, string(output), "", exitCodeFromError(runErr), artifacts)
		return step, report
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	finalizeHarnessEvidence(step.Evidence, elapsed, step.OK, string(output), "", exitCodeFromError(runErr), artifacts)
	return step, report
}

func parseHarnessGoTestTimingWithBudgets(output []byte, command []string, elapsed time.Duration, budgets harnessTestTimingBudgets) *harnessTestTimingReport {
	report := &harnessTestTimingReport{
		PayloadIdentity: newCLIPayloadIdentity(harnessTestTimingKind),
		Command:         append([]string{}, command...),
		TotalSeconds:    roundSeconds(elapsed.Seconds()),
		Budgets:         budgets,
	}
	packages := map[string]*harnessPackageTiming{}
	rootClock := newGoTestRootClock()
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event goTestJSONEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		if event.Package == "" {
			continue
		}
		pkg := packages[event.Package]
		if pkg == nil {
			pkg = &harnessPackageTiming{
				Package:       event.Package,
				BudgetSeconds: harnessPackageTimingBudget(event.Package, report.Budgets),
			}
			packages[event.Package] = pkg
		}
		if event.Test == "" {
			if (event.Action == "pass" || event.Action == "fail") && event.Elapsed > 0 {
				pkg.Seconds = roundSeconds(event.Elapsed)
			}
			continue
		}
		rootSeconds, rootFinished := rootClock.observe(event)
		if rootFinished {
			if exception, excepted := harnessTimingIntegrationException(event.Package, event.Test, report.Budgets.IntegrationExceptions); excepted {
				if rootSeconds >= exception.TargetSeconds {
					report.ObservedIntegrationTests = append(report.ObservedIntegrationTests, harnessTestTiming{
						Name: event.Test, Package: event.Package, Class: exception.Class,
						Seconds: roundSeconds(rootSeconds), TargetSeconds: exception.TargetSeconds, BudgetSeconds: exception.BudgetSeconds,
						ClassificationReason: exception.BoundaryReason,
					})
				}
				continue
			}
			if rootSeconds < report.Budgets.TestTargetSeconds {
				continue
			}
			timing := harnessTestTiming{
				Name:          event.Test,
				Package:       event.Package,
				Class:         report.Budgets.DefaultTestClass,
				Seconds:       roundSeconds(rootSeconds),
				TargetSeconds: report.Budgets.TestTargetSeconds,
				BudgetSeconds: report.Budgets.TestSeconds,
			}
			report.ObservedSlowTests = append(report.ObservedSlowTests, timing)
		}
	}
	for _, pkg := range packages {
		report.Packages = append(report.Packages, *pkg)
	}
	sort.Slice(report.Packages, func(i, j int) bool {
		return report.Packages[i].Package < report.Packages[j].Package
	})
	sort.Slice(report.ObservedSlowTests, func(i, j int) bool {
		if report.ObservedSlowTests[i].Seconds == report.ObservedSlowTests[j].Seconds {
			return report.ObservedSlowTests[i].Package+"."+report.ObservedSlowTests[i].Name < report.ObservedSlowTests[j].Package+"."+report.ObservedSlowTests[j].Name
		}
		return report.ObservedSlowTests[i].Seconds > report.ObservedSlowTests[j].Seconds
	})
	sort.Slice(report.ObservedIntegrationTests, func(i, j int) bool {
		if report.ObservedIntegrationTests[i].Seconds == report.ObservedIntegrationTests[j].Seconds {
			return report.ObservedIntegrationTests[i].Package+"."+report.ObservedIntegrationTests[i].Name < report.ObservedIntegrationTests[j].Package+"."+report.ObservedIntegrationTests[j].Name
		}
		return report.ObservedIntegrationTests[i].Seconds > report.ObservedIntegrationTests[j].Seconds
	})
	for _, timing := range report.ObservedIntegrationTests {
		if timing.Seconds < timing.BudgetSeconds {
			continue
		}
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "go tests",
			Severity:        "warning",
			Message:         fmt.Sprintf("integration test %s.%s took %.3fs in one observation, at or over %.3fs visibility budget", timing.Package, timing.Name, timing.Seconds, timing.BudgetSeconds),
			SuggestedAction: "Reduce avoidable external-boundary setup or review this exact integration exception and its visibility budget; this warning is not a fast-test gate failure.",
		})
	}
	if report.TotalSeconds >= report.Budgets.TotalSeconds {
		severity := "warning"
		suggestion := "Review `.scenery/harness/test-timing-latest.json` for regressions; timing is advisory in default self-harness mode."
		if report.Budgets.Mode == "enforce-total" {
			severity = "error"
			suggestion = "Continue `docs/plans/0050-test-suite-speed-hardening.md` and reduce the full-suite runtime below the enforced harness budget."
		}
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "go tests",
			Severity:        severity,
			Message:         fmt.Sprintf("full Go suite took %.3fs, over %.3fs %s budget", report.TotalSeconds, report.Budgets.TotalSeconds, report.Budgets.Lane),
			SuggestedAction: suggestion,
		})
	}
	if err := scanner.Err(); err != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "go tests",
			Severity:        "warning",
			Message:         "failed to scan complete go test JSON output: " + err.Error(),
			SuggestedAction: "Rerun `" + strings.Join(report.Command, " ") + "` and inspect the raw output.",
		})
	}
	return report
}

func harnessPackageTimingBudget(packageName string, budgets harnessTestTimingBudgets) float64 {
	if seconds, ok := budgets.PackageOverrides[packageName]; ok {
		return seconds
	}
	return budgets.PackageSeconds
}

func summarizeGoTestFailures(output []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	recentOutput := map[string]string{}
	hasTestFailure := map[string]bool{}
	var failures []string
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event goTestJSONEvent
		if err := json.Unmarshal(line, &event); err != nil || event.Package == "" {
			continue
		}
		key := event.Package + "\x00" + event.Test
		if event.Action == "output" && strings.TrimSpace(event.Output) != "" {
			recentOutput[key] = tailString(recentOutput[key]+event.Output, 1200)
			continue
		}
		if event.Action != "fail" {
			continue
		}
		if event.Test == "" && hasTestFailure[event.Package] {
			continue
		}
		label := event.Package
		if event.Test != "" {
			label += " " + event.Test
			hasTestFailure[event.Package] = true
		}
		detail := strings.TrimSpace(recentOutput[key])
		if detail == "" && event.Test != "" {
			detail = strings.TrimSpace(recentOutput[event.Package+"\x00"])
		}
		if detail == "" {
			failures = append(failures, label+" failed")
		} else {
			failures = append(failures, label+": "+detail)
		}
		if len(failures) >= 5 {
			break
		}
	}
	return strings.Join(failures, "\n")
}

func defaultHarnessTestTimingBudgets() harnessTestTimingBudgets {
	return harnessTestTimingBudgets{
		Lane:           "cached",
		TargetSeconds:  harnessOptimizationTargetSeconds,
		TotalSeconds:   cachedHarnessTotalSeconds,
		PackageSeconds: 10,
		PackageOverrides: map[string]float64{
			"scenery.sh/cmd/scenery": commandPackageTimingSeconds,
		},
		DefaultTestClass:       harnessTestClassFast,
		TestTargetSeconds:      harnessFastTestTargetSeconds,
		TestSeconds:            harnessFastTestBudgetSeconds,
		IntegrationExceptions:  harnessTimingIntegrationExceptions(),
		ConfirmationPercentile: harnessTimingConfirmationPercentile,
		TestBinaryCount:        harnessTestBinaryCountBudget,
		ColdPrepareSeconds:     harnessColdPrepareSecondsBudget,
		Mode:                   "observe-total",
	}
}

func splitCommandLines(output string) []string {
	lines := strings.Split(output, "\n")
	out := lines[:0]
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func mergeHarnessChangeStatus(existing, next string) string {
	if existing == "" {
		return next
	}
	if existing == next || strings.Contains(existing, next) {
		return existing
	}
	parts := strings.Split(existing, ",")
	parts = append(parts, next)
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func sortedKeysChanged(values map[string]harnessChangedFile) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func roundSeconds(value float64) float64 {
	return float64(int(value*1000+0.5)) / 1000
}
