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
	harnessChangedAreaSchema  = "scenery.harness.changed_area.v1"
	harnessTestTimingSchema   = "scenery.harness.test_timing.v1"
	harnessAgentContextSchema = "scenery.agent_context.v1"
)

const (
	harnessOptimizationTargetSeconds = 5
	cachedHarnessTotalSeconds        = 5
	freshHarnessTotalSeconds         = 5
	releaseHarnessTotalSeconds       = 30
	commandPackageTimingSeconds      = 5
	harnessTimingConfirmationRuns    = 3
)

type harnessChangedAreaReport struct {
	SchemaVersion       string               `json:"schema_version"`
	ChangedFiles        []harnessChangedFile `json:"changed_files"`
	IgnoredFiles        []harnessChangedFile `json:"ignored_files,omitempty"`
	AffectedPackages    []string             `json:"affected_packages"`
	RecommendedCommands []string             `json:"recommended_commands"`
	RelevantDocs        []string             `json:"relevant_docs"`
	RiskFlags           []string             `json:"risk_flags"`
	Diagnostics         []checkDiagnostic    `json:"diagnostics,omitempty"`
}

type harnessChangedFile struct {
	Path     string `json:"path"`
	Status   string `json:"status"`
	Category string `json:"category"`
	Package  string `json:"package,omitempty"`
}

type harnessPackageInfo struct {
	ImportPath string
	Dir        string
	RelDir     string
}

var (
	harnessCollectChangedFiles = collectHarnessChangedFiles
	harnessListGoPackages      = listHarnessGoPackages
)

type harnessTestTimingReport struct {
	SchemaVersion       string                   `json:"schema_version"`
	Command             []string                 `json:"command"`
	Env                 []string                 `json:"env,omitempty"`
	TotalSeconds        float64                  `json:"total_seconds"`
	ConfirmationSeconds float64                  `json:"confirmation_seconds,omitempty"`
	Packages            []harnessPackageTiming   `json:"packages"`
	ObservedSlowTests   []harnessTestTiming      `json:"observed_slow_tests,omitempty"`
	SlowTests           []harnessTestTiming      `json:"slow_tests,omitempty"`
	Budgets             harnessTestTimingBudgets `json:"budgets"`
	Diagnostics         []checkDiagnostic        `json:"diagnostics,omitempty"`
}

type harnessPackageTiming struct {
	Package         string              `json:"package"`
	Seconds         float64             `json:"seconds"`
	BudgetSeconds   float64             `json:"budget_seconds,omitempty"`
	IsolatedSeconds *float64            `json:"isolated_seconds,omitempty"`
	Tests           []harnessTestTiming `json:"slow_tests,omitempty"`
}

type harnessTestTiming struct {
	Name            string    `json:"name"`
	Package         string    `json:"package"`
	Seconds         float64   `json:"seconds"`
	BudgetSeconds   float64   `json:"budget_seconds,omitempty"`
	IsolatedSamples []float64 `json:"isolated_samples,omitempty"`
	IsolatedMedian  *float64  `json:"isolated_median_seconds,omitempty"`
}

type harnessTestTimingBudgets struct {
	Lane             string             `json:"lane,omitempty"`
	TargetSeconds    float64            `json:"target_seconds,omitempty"`
	TotalSeconds     float64            `json:"total_seconds"`
	PackageSeconds   float64            `json:"package_seconds"`
	PackageOverrides map[string]float64 `json:"package_overrides,omitempty"`
	TestSeconds      float64            `json:"test_seconds"`
	ConfirmationRuns int                `json:"confirmation_runs,omitempty"`
	Mode             string             `json:"mode"`
}

type goTestJSONEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

type harnessAgentContext struct {
	SchemaVersion                  string                       `json:"schema_version"`
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
	Step          string `json:"step"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	SchemaVersion string `json:"schema_version,omitempty"`
	RerunCommand  string `json:"rerun_command,omitempty"`
}

type harnessAgentDocsFreshness struct {
	SchemaVersion    string   `json:"schema_version,omitempty"`
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
		Command:    []string{"scenery", "harness", "self", "internal:changed-area", repoRoot},
		OK:         !hasErrorDiagnostics(report.Diagnostics),
		DurationMS: time.Since(started).Milliseconds(),
		Summary: map[string]any{
			"changed_files":        len(report.ChangedFiles),
			"affected_packages":    len(report.AffectedPackages),
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
		SchemaVersion:       harnessChangedAreaSchema,
		ChangedFiles:        []harnessChangedFile{},
		AffectedPackages:    []string{},
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

func populateHarnessChangedAreaReport(repoRoot string, report *harnessChangedAreaReport, changes []harnessChangedFile, packages []harnessPackageInfo, diagnostics []checkDiagnostic) {
	report.Diagnostics = append(report.Diagnostics, diagnostics...)
	packageSet := map[string]bool{}
	commandSet := map[string]bool{}
	docSet := map[string]bool{}
	riskSet := map[string]bool{}

	for _, change := range changes {
		if isIgnoredHarnessLocalArtifact(change.Path) {
			change.Category = "local-artifact"
			report.IgnoredFiles = append(report.IgnoredFiles, change)
			continue
		}
		change.Category = classifyHarnessChangedFile(change.Path)
		if strings.HasSuffix(change.Path, ".go") {
			if pkg, ok := harnessPackageForFile(repoRoot, change.Path, packages); ok {
				change.Package = pkg.ImportPath
				packageSet[pkg.ImportPath] = true
				if pkg.RelDir == "." {
					commandSet["go test ."] = true
				} else {
					commandSet["go test ./"+filepath.ToSlash(pkg.RelDir)] = true
				}
			}
		}
		addHarnessChangedAreaKnowledge(change.Path, change.Category, docSet, riskSet, commandSet)
		report.ChangedFiles = append(report.ChangedFiles, change)
	}

	report.AffectedPackages = sortedStringSet(packageSet)
	report.RecommendedCommands = sortedStringSet(commandSet)
	if len(report.ChangedFiles) > 0 {
		report.RecommendedCommands = appendUniqueSorted(report.RecommendedCommands,
			"go test ./...",
			"scenery harness self --summary --write",
		)
	}
	report.RelevantDocs = sortedStringSet(docSet)
	report.RiskFlags = sortedStringSet(riskSet)
	sort.Slice(report.ChangedFiles, func(i, j int) bool {
		if report.ChangedFiles[i].Path == report.ChangedFiles[j].Path {
			return report.ChangedFiles[i].Status < report.ChangedFiles[j].Status
		}
		return report.ChangedFiles[i].Path < report.ChangedFiles[j].Path
	})
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

func isIgnoredHarnessLocalArtifact(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	base := filepath.Base(path)
	switch {
	case path == "":
		return false
	case strings.HasPrefix(path, "docs/schemas/"):
		return false
	case path == ".claude" || strings.HasPrefix(path, ".claude/"):
		return true
	case strings.HasPrefix(path, ".scenery/"):
		return true
	case strings.HasPrefix(path, "coverage/"):
		return true
	case strings.HasPrefix(path, "test-results/"):
		return true
	case strings.Contains(base, ".harness") && strings.HasSuffix(base, ".json"):
		return true
	case strings.HasPrefix(base, "scenery-harness-self-") && strings.HasSuffix(base, ".json"):
		return true
	default:
		return false
	}
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
	cmd := commandTreeContext(ctx, path, "list", "-json", "./...")
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

func harnessPackageForFile(repoRoot, relPath string, packages []harnessPackageInfo) (harnessPackageInfo, bool) {
	abs := filepath.Join(repoRoot, filepath.FromSlash(relPath))
	for _, pkg := range packages {
		rel, err := filepath.Rel(pkg.Dir, abs)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
			return pkg, true
		}
	}
	return harnessPackageInfo{}, false
}

func classifyHarnessChangedFile(path string) string {
	switch {
	case strings.HasPrefix(path, "cmd/scenery/"):
		return "cli"
	case strings.HasPrefix(path, "internal/"):
		return "internal"
	case strings.HasPrefix(path, "runtime") || strings.HasPrefix(path, "auth/"):
		return "runtime"
	case strings.HasPrefix(path, "ui/"), strings.HasPrefix(path, "apps/consolenext/"):
		return "ui"
	case strings.HasPrefix(path, "docs/schemas/"):
		return "schema"
	case strings.HasPrefix(path, "docs/plans/"):
		return "exec-plan"
	case strings.HasPrefix(path, "docs/") || path == "AGENTS.md" || path == "SKILL.md" || path == "PLAN.md" || path == "PLANS.md":
		return "docs"
	case strings.HasPrefix(path, "testdata/"):
		return "fixture"
	case strings.HasPrefix(path, "scripts/"):
		return "script"
	case path == "go.mod" || path == "go.sum":
		return "dependency"
	default:
		return "other"
	}
}

func addHarnessChangedAreaKnowledge(path, category string, docs, risks, commands map[string]bool) {
	if harnessDevEventReadPath(path) {
		docs["docs/local-contract.md"] = true
		risks["victoria-dev-event-read-path"] = true
		commands["scenery logs --limit 500 -o jsonl"] = true
	}
	switch category {
	case "cli":
		docs["docs/harness-engineering.md"] = true
		risks["cli-contract"] = true
		if strings.Contains(path, "harness") {
			risks["harness-contract"] = true
			docs["docs/plans/0051-harness-self-agent-oracle.md"] = true
		}
	case "schema":
		docs["docs/knowledge.json"] = true
		risks["json-schema-contract"] = true
	case "exec-plan":
		docs["PLANS.md"] = true
		docs["docs/plans/active.md"] = true
		risks["exec-plan"] = true
	case "ui":
		risks["dashboard-ui"] = true
		if strings.HasPrefix(path, "apps/consolenext/") {
			docs["apps/consolenext/AGENTS.md"] = true
			commands["cd apps/consolenext && bun run lint && bun run typecheck && bun run build"] = true
		} else {
			docs["docs/ui-agent-contract.md"] = true
			commands["cd ui && bun run typecheck && bun run test"] = true
		}
	case "fixture":
		docs["docs/app-development-cookbook.md"] = true
		risks["fixture-contract"] = true
	case "dependency":
		docs["docs/harness-engineering.md"] = true
		risks["dependency-graph"] = true
	case "runtime":
		docs["docs/local-contract.md"] = true
		risks["runtime-contract"] = true
	case "internal":
		if strings.HasPrefix(path, "internal/build/") {
			docs["docs/plans/0050-test-suite-speed-hardening.md"] = true
			risks["build-cache"] = true
		}
	}
}

func harnessDevEventReadPath(path string) bool {
	switch path {
	case "cmd/scenery/logs.go",
		"cmd/scenery/logs_test.go",
		"cmd/scenery/dev_console.go",
		"cmd/scenery/dev_console_test.go",
		"cmd/scenery/victoria_dev_logs.go",
		"internal/devdash/dev_events.go",
		"internal/devdash/store.go",
		"internal/devdash/store_test.go":
		return true
	default:
		return false
	}
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
	}
	if mode == harnessSelfModeRelease {
		budgets.Lane = "release"
		budgets.TotalSeconds = releaseHarnessTotalSeconds
		budgets.Mode = "enforce-total"
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
			SchemaVersion: harnessTestTimingSchema,
			Command:       command,
			Env:           testEnv,
			TotalSeconds:  float64(step.DurationMS) / 1000,
			Budgets:       budgets,
			Diagnostics:   step.Diagnostics,
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
			SuggestedAction: "Check temporary directory permissions and available disk space, then rerun `scenery harness self -o json`.",
		}}
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, "", step.Error, exitCodeFromError(err), nil)
		return step, &harnessTestTimingReport{
			SchemaVersion: harnessTestTimingSchema,
			Command:       command,
			Env:           testEnv,
			TotalSeconds:  float64(step.DurationMS) / 1000,
			Budgets:       budgets,
			Diagnostics:   step.Diagnostics,
		}
	}
	outputPath := outputFile.Name()
	defer os.Remove(outputPath)
	var testResult testsuite.Result
	var runErr error
	if freshTests {
		testResult, runErr = testsuite.Run(ctx, testsuite.Options{
			RepoRoot:           repoRoot,
			CacheDir:           filepath.Join(repoRoot, ".scenery", "harness", "test-binaries"),
			RunPattern:         ".*",
			PackageParallelism: 3,
			BuildParallelism:   8,
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
	if runErr == nil && freshTests {
		confirmHarnessTimingOutliers(ctx, repoRoot, report, runHarnessTimingConfirmationCommand)
	}
	elapsed := time.Since(started)
	step.DurationMS = elapsed.Milliseconds()
	step.Summary = map[string]any{
		"packages":             len(report.Packages),
		"observed_slow_tests":  len(report.ObservedSlowTests),
		"confirmed_slow_tests": len(report.SlowTests),
		"total_seconds":        report.TotalSeconds,
		"confirmation_seconds": report.ConfirmationSeconds,
		"timing_lane":          report.Budgets.Lane,
		"env":                  testEnv,
	}
	if freshTests {
		step.Summary["test_results"] = testResult.TestResultCount
		step.Summary["test_binaries_built"] = testResult.BuiltCount
		step.Summary["test_manifest_hit"] = testResult.ManifestHit
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
		SchemaVersion: harnessTestTimingSchema,
		Command:       append([]string{}, command...),
		TotalSeconds:  roundSeconds(elapsed.Seconds()),
		Budgets:       budgets,
	}
	packages := map[string]*harnessPackageTiming{}
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
		if (event.Action == "pass" || event.Action == "fail") && event.Elapsed >= report.Budgets.TestSeconds {
			timing := harnessTestTiming{
				Name:          event.Test,
				Package:       event.Package,
				Seconds:       roundSeconds(event.Elapsed),
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
		PackageSeconds: 2,
		PackageOverrides: map[string]float64{
			"scenery.sh/cmd/scenery": commandPackageTimingSeconds,
		},
		TestSeconds: 0.5,
		Mode:        "observe-total",
	}
}

func harnessOnlvImpactingPath(path string) bool {
	for _, needle := range []string{
		"onlv",
		"cmd/scenery/dev_session",
		"cmd/scenery/dev_services",
		"cmd/scenery/dev_supervisor",
		"cmd/scenery/edge",
		"cmd/scenery/db",
		"internal/agent",
		"internal/localproxy",
		"docs/plans/0045-",
		"docs/plans/0048-",
		"docs/plans/0049-",
		"docs/plans/0063-",
	} {
		if strings.Contains(path, needle) {
			return true
		}
	}
	return false
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

func sortedStringSet(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedKeysChanged(values map[string]harnessChangedFile) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func appendUniqueSorted(base []string, values ...string) []string {
	set := map[string]bool{}
	for _, value := range base {
		if value != "" {
			set[value] = true
		}
	}
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	return sortedStringSet(set)
}

func roundSeconds(value float64) float64 {
	return float64(int(value*1000+0.5)) / 1000
}
