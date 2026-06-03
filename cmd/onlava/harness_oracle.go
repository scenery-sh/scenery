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

	"github.com/pbrazdil/onlava/internal/envpolicy"
)

const (
	harnessChangedAreaSchema  = "onlava.harness.changed_area.v1"
	harnessTestTimingSchema   = "onlava.harness.test_timing.v1"
	harnessAgentContextSchema = "onlava.agent_context.v1"
)

type harnessChangedAreaReport struct {
	SchemaVersion       string               `json:"schema_version"`
	ChangedFiles        []harnessChangedFile `json:"changed_files"`
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
	SchemaVersion string                   `json:"schema_version"`
	Command       []string                 `json:"command"`
	Env           []string                 `json:"env,omitempty"`
	TotalSeconds  float64                  `json:"total_seconds"`
	Packages      []harnessPackageTiming   `json:"packages"`
	SlowTests     []harnessTestTiming      `json:"slow_tests,omitempty"`
	Budgets       harnessTestTimingBudgets `json:"budgets"`
	Diagnostics   []checkDiagnostic        `json:"diagnostics,omitempty"`
}

type harnessPackageTiming struct {
	Package string              `json:"package"`
	Seconds float64             `json:"seconds"`
	Tests   []harnessTestTiming `json:"slow_tests,omitempty"`
}

type harnessTestTiming struct {
	Name    string  `json:"name"`
	Package string  `json:"package"`
	Seconds float64 `json:"seconds"`
}

type harnessTestTimingBudgets struct {
	TotalSeconds   float64 `json:"total_seconds"`
	PackageSeconds float64 `json:"package_seconds"`
	TestSeconds    float64 `json:"test_seconds"`
	Mode           string  `json:"mode"`
}

type goTestJSONEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

type harnessAgentContext struct {
	SchemaVersion       string                    `json:"schema_version"`
	GeneratedAt         string                    `json:"generated_at"`
	Repo                harnessAgentContextRepo   `json:"repo"`
	CurrentBranch       string                    `json:"current_branch,omitempty"`
	CurrentCommit       string                    `json:"current_commit,omitempty"`
	DirtyFiles          []harnessChangedFile      `json:"dirty_files"`
	ChangedArea         *harnessChangedAreaReport `json:"changed_area,omitempty"`
	RecommendedCommands []string                  `json:"recommended_commands"`
	DocsEntrypoints     []string                  `json:"docs_entrypoints"`
	Schemas             []string                  `json:"schemas"`
	KnownFastLoop       string                    `json:"known_fast_loop"`
	KnownReleaseLoop    string                    `json:"known_release_loop"`
	ArchitectureRules   []string                  `json:"architecture_rules"`
	RecentFailures      []string                  `json:"recent_failures,omitempty"`
}

type harnessAgentContextRepo struct {
	Root       string `json:"root"`
	ModulePath string `json:"module_path"`
	GoModPath  string `json:"go_mod_path"`
}

func runHarnessChangedAreaStep(ctx context.Context, repoRoot string) (harnessStep, *harnessChangedAreaReport) {
	started := time.Now()
	report := buildHarnessChangedAreaReport(ctx, repoRoot)
	step := harnessStep{
		Name:       "changed area oracle",
		Command:    []string{"onlava", "harness", "self", "internal:changed-area", repoRoot},
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
		change.Category = classifyHarnessChangedFile(change.Path)
		if strings.HasSuffix(change.Path, ".go") {
			if pkg, ok := harnessPackageForFile(repoRoot, change.Path, packages); ok {
				change.Package = pkg.ImportPath
				packageSet[pkg.ImportPath] = true
				if pkg.RelDir == "." {
					commandSet["go test -count=1 ."] = true
				} else {
					commandSet["go test -count=1 ./"+filepath.ToSlash(pkg.RelDir)] = true
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
			"go test -count=1 ./...",
			"onlava harness self --json --write",
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
	case strings.HasPrefix(path, "cmd/onlava/"):
		return "cli"
	case strings.HasPrefix(path, "internal/"):
		return "internal"
	case strings.HasPrefix(path, "runtime") || strings.HasPrefix(path, "auth/") || strings.HasPrefix(path, "temporal/"):
		return "runtime"
	case strings.HasPrefix(path, "ui/"):
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
	if harnessDevEventBackendPath(path) {
		docs["docs/plans/0056-dev-event-backend-cutover-and-parity.md"] = true
		risks["victoria-dev-event-read-path"] = true
		commands["onlava logs --session current --backend victoria --limit 500 --jsonl"] = true
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
		docs["docs/ui-agent-contract.md"] = true
		risks["dashboard-ui"] = true
		commands["cd ui && bun run typecheck"] = true
		commands["cd ui && bun run build"] = true
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
		if strings.HasPrefix(path, "internal/workers/") {
			docs["docs/plans/0047-typescript-temporal-workers.md"] = true
			risks["temporal-runtime"] = true
		}
	}
}

func harnessDevEventBackendPath(path string) bool {
	switch path {
	case "cmd/onlava/logs.go",
		"cmd/onlava/logs_test.go",
		"cmd/onlava/dev_console.go",
		"cmd/onlava/dev_console_test.go",
		"cmd/onlava/dev_event_backend.go",
		"cmd/onlava/victoria_dev_logs.go",
		"internal/devdash/dev_events.go",
		"internal/devdash/store.go",
		"internal/devdash/store_test.go":
		return true
	default:
		return false
	}
}

func runHarnessGoTestTimingStep(ctx context.Context, repoRoot string) (harnessStep, *harnessTestTimingReport) {
	return runHarnessGoTestTimingStepWithBudgets(ctx, repoRoot, defaultHarnessTestTimingBudgets())
}

func runHarnessGoTestTimingStepForMode(ctx context.Context, repoRoot, mode string) (harnessStep, *harnessTestTimingReport) {
	budgets := defaultHarnessTestTimingBudgets()
	if mode == harnessSelfModeRelease {
		budgets.Mode = "enforce-total"
	}
	return runHarnessGoTestTimingStepWithBudgets(ctx, repoRoot, budgets)
}

func runHarnessGoTestTimingStepWithBudgets(ctx context.Context, repoRoot string, budgets harnessTestTimingBudgets) (harnessStep, *harnessTestTimingReport) {
	started := time.Now()
	command := harnessSelfGoTestCommand()
	testEnv := harnessSelfGoTestEnv()
	step := harnessStep{
		Name:    "go tests",
		Command: command,
	}
	path, err := exec.LookPath(command[0])
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
		return step, &harnessTestTimingReport{
			SchemaVersion: harnessTestTimingSchema,
			Command:       command,
			Env:           testEnv,
			TotalSeconds:  float64(step.DurationMS) / 1000,
			Budgets:       budgets,
			Diagnostics:   step.Diagnostics,
		}
	}
	cmd := commandTreeContext(ctx, path, command[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = envWithOverrides(envpolicy.Environ(), testEnv...)
	outputFile, err := os.CreateTemp("", "onlava-go-test-*.json")
	if err != nil {
		step.OK = false
		step.DurationMS = time.Since(started).Milliseconds()
		step.Error = "create go test timing output file: " + err.Error()
		step.Diagnostics = []checkDiagnostic{{
			Stage:           step.Name,
			Severity:        "error",
			Message:         step.Error,
			SuggestedAction: "Check temporary directory permissions and available disk space, then rerun `onlava harness self --json`.",
		}}
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
	cmd.Stdout = outputFile
	cmd.Stderr = outputFile
	runErr := cmd.Run()
	elapsed := time.Since(started)
	closeErr := outputFile.Close()
	output, readErr := os.ReadFile(outputPath)
	if readErr != nil && runErr == nil {
		runErr = readErr
	}
	if closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	report := parseHarnessGoTestTimingWithBudgets(output, command, elapsed, budgets)
	report.Env = append([]string{}, testEnv...)
	step.DurationMS = elapsed.Milliseconds()
	step.Summary = map[string]any{
		"packages":      len(report.Packages),
		"slow_tests":    len(report.SlowTests),
		"total_seconds": report.TotalSeconds,
		"env":           testEnv,
	}
	step.Diagnostics = report.Diagnostics
	if runErr != nil {
		step.OK = false
		step.Error = strings.TrimSpace(runErr.Error())
		step.OutputTail = tailString(string(output), 8192)
		step.Diagnostics = append(step.Diagnostics, checkDiagnostic{
			Stage:           step.Name,
			Severity:        "error",
			Message:         firstNonEmpty(strings.TrimSpace(step.OutputTail), step.Error),
			SuggestedAction: rerunSuggestion(command, repoRoot),
		})
		report.Diagnostics = step.Diagnostics
		return step, report
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step, report
}

func parseHarnessGoTestTiming(output []byte, command []string, elapsed time.Duration) *harnessTestTimingReport {
	return parseHarnessGoTestTimingWithBudgets(output, command, elapsed, defaultHarnessTestTimingBudgets())
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
			pkg = &harnessPackageTiming{Package: event.Package}
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
				Name:    event.Test,
				Package: event.Package,
				Seconds: roundSeconds(event.Elapsed),
			}
			pkg.Tests = append(pkg.Tests, timing)
			report.SlowTests = append(report.SlowTests, timing)
		}
	}
	for _, pkg := range packages {
		report.Packages = append(report.Packages, *pkg)
	}
	sort.Slice(report.Packages, func(i, j int) bool {
		return report.Packages[i].Package < report.Packages[j].Package
	})
	sort.Slice(report.SlowTests, func(i, j int) bool {
		if report.SlowTests[i].Seconds == report.SlowTests[j].Seconds {
			return report.SlowTests[i].Package+"."+report.SlowTests[i].Name < report.SlowTests[j].Package+"."+report.SlowTests[j].Name
		}
		return report.SlowTests[i].Seconds > report.SlowTests[j].Seconds
	})
	for i := range report.Packages {
		sort.Slice(report.Packages[i].Tests, func(a, b int) bool {
			if report.Packages[i].Tests[a].Seconds == report.Packages[i].Tests[b].Seconds {
				return report.Packages[i].Tests[a].Name < report.Packages[i].Tests[b].Name
			}
			return report.Packages[i].Tests[a].Seconds > report.Packages[i].Tests[b].Seconds
		})
		if report.Packages[i].Seconds >= report.Budgets.PackageSeconds {
			report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
				Stage:           "go tests",
				Severity:        "warning",
				Message:         fmt.Sprintf("package %s took %.3fs, over %.3fs budget", report.Packages[i].Package, report.Packages[i].Seconds, report.Budgets.PackageSeconds),
				SuggestedAction: "Inspect `.onlava/harness/test-timing-latest.json` and reduce repeated process startup or slow fixture setup.",
			})
		}
	}
	if report.TotalSeconds >= report.Budgets.TotalSeconds {
		severity := "warning"
		suggestion := "Review `.onlava/harness/test-timing-latest.json` for regressions; timing is advisory in default self-harness mode."
		if report.Budgets.Mode == "enforce-total" {
			severity = "error"
			suggestion = "Continue `docs/plans/0050-test-suite-speed-hardening.md` and reduce the full-suite runtime below the enforced harness budget."
		}
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "go tests",
			Severity:        severity,
			Message:         fmt.Sprintf("full Go suite took %.3fs, over %.3fs target", report.TotalSeconds, report.Budgets.TotalSeconds),
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

func defaultHarnessTestTimingBudgets() harnessTestTimingBudgets {
	return harnessTestTimingBudgets{
		TotalSeconds:   7,
		PackageSeconds: 2,
		TestSeconds:    0.5,
		Mode:           "observe-total",
	}
}

func buildHarnessAgentContext(repoRoot string, resp harnessSelfResponse) harnessAgentContext {
	var failures []string
	for _, step := range resp.Steps {
		if step.OK {
			continue
		}
		failures = append(failures, step.Name)
	}
	var entrypoints []string
	for _, item := range resp.Knowledge.Entrypoints {
		entrypoints = append(entrypoints, item.Path)
	}
	var schemas []string
	for _, item := range resp.Knowledge.Schemas {
		schemas = append(schemas, item.Path)
	}
	branch, _ := runHarnessGit(context.Background(), repoRoot, "branch", "--show-current")
	commit, _ := runHarnessGit(context.Background(), repoRoot, "rev-parse", "HEAD")
	contextPack := harnessAgentContext{
		SchemaVersion: harnessAgentContextSchema,
		GeneratedAt:   resp.GeneratedAt,
		Repo: harnessAgentContextRepo{
			Root:       resp.Repo.Root,
			ModulePath: resp.Repo.ModulePath,
			GoModPath:  resp.Repo.GoModPath,
		},
		CurrentBranch:       strings.TrimSpace(branch),
		CurrentCommit:       strings.TrimSpace(commit),
		DirtyFiles:          []harnessChangedFile{},
		ChangedArea:         resp.ChangedArea,
		RecommendedCommands: append([]string{}, resp.NextActions...),
		DocsEntrypoints:     entrypoints,
		Schemas:             schemas,
		KnownFastLoop:       "go test -count=1 ./... && onlava harness self --json --write",
		KnownReleaseLoop:    "scripts/release-gate.sh",
		ArchitectureRules: []string{
			"Prefer Go standard library dependencies unless the payoff is concrete.",
			"Do not add legacy aliases or backwards-compatibility shims for renamed onlava APIs.",
			"After repository changes, run go install ./cmd/onlava.",
			"For substantial repository changes, run onlava harness self --json --write when practical.",
		},
		RecentFailures: failures,
	}
	if resp.ChangedArea != nil {
		contextPack.DirtyFiles = resp.ChangedArea.ChangedFiles
		contextPack.RecommendedCommands = appendUniqueSorted(contextPack.RecommendedCommands, resp.ChangedArea.RecommendedCommands...)
	}
	if len(contextPack.RecommendedCommands) == 0 {
		contextPack.RecommendedCommands = []string{"go test -count=1 ./...", "onlava harness self --json --write"}
	}
	sort.Strings(contextPack.DocsEntrypoints)
	sort.Strings(contextPack.Schemas)
	sort.Strings(contextPack.RecentFailures)
	return contextPack
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
