package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	harnessToolchainSchema     = "onlava.harness.toolchain.v1"
	harnessDriftSchema         = "onlava.harness.drift.v1"
	harnessFixtureMatrixSchema = "onlava.harness.fixture_matrix.v1"
)

type harnessToolchainReport struct {
	SchemaVersion string                 `json:"schema_version"`
	Tools         []harnessToolchainTool `json:"tools"`
	Env           []harnessEnvValue      `json:"env,omitempty"`
	Diagnostics   []checkDiagnostic      `json:"diagnostics,omitempty"`
}

type harnessToolchainTool struct {
	Name     string `json:"name"`
	Scope    string `json:"scope"`
	Required bool   `json:"required"`
	Present  bool   `json:"present"`
	Path     string `json:"path,omitempty"`
	Version  string `json:"version,omitempty"`
	Error    string `json:"error,omitempty"`
}

type harnessEnvValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harnessDriftReport struct {
	SchemaVersion string                       `json:"schema_version"`
	CLI           harnessCLIContractReport     `json:"cli"`
	Env           harnessEnvVarReport          `json:"env"`
	Artifacts     harnessArtifactHygieneReport `json:"artifacts"`
	Embeds        harnessEmbedReport           `json:"embeds"`
	Diagnostics   []checkDiagnostic            `json:"diagnostics,omitempty"`
}

type harnessCLIContractReport struct {
	Commands []harnessCLIContractCommand `json:"commands"`
}

type harnessCLIContractCommand struct {
	Name  string `json:"name"`
	Usage bool   `json:"usage"`
	Smoke bool   `json:"smoke"`
	Mode  string `json:"mode"`
	Error string `json:"error,omitempty"`
}

type harnessEnvVarReport struct {
	Variables []harnessEnvVarFinding `json:"variables"`
}

type harnessEnvVarFinding struct {
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	UsedInCode bool   `json:"used_in_code"`
	Documented bool   `json:"documented"`
}

type harnessArtifactHygieneReport struct {
	ForbiddenTracked []string `json:"forbidden_tracked"`
	WorkspaceRules   []string `json:"workspace_rules"`
}

type harnessEmbedReport struct {
	Embeds []harnessEmbedFinding `json:"embeds"`
}

type harnessEmbedFinding struct {
	File                     string   `json:"file"`
	Pattern                  string   `json:"pattern"`
	Resolved                 []string `json:"resolved"`
	CoveredByBinaryFreshness bool     `json:"covered_by_binary_freshness"`
}

type harnessFixtureMatrixReport struct {
	SchemaVersion string                 `json:"schema_version"`
	Fixtures      []harnessFixtureResult `json:"fixtures"`
	Diagnostics   []checkDiagnostic      `json:"diagnostics,omitempty"`
}

type harnessFixtureResult struct {
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	Check       bool              `json:"check"`
	Inspect     map[string]bool   `json:"inspect"`
	Diagnostics []checkDiagnostic `json:"diagnostics,omitempty"`
}

type harnessToolchainSpec struct {
	name     string
	scope    string
	required bool
	args     []string
}

var (
	harnessToolchainSpecs = []harnessToolchainSpec{
		{name: "go", scope: "required", required: true, args: []string{"version"}},
		{name: "git", scope: "required", required: true, args: []string{"version"}},
		{name: "onlava", scope: "required", required: true, args: []string{"version", "--json"}},
		{name: "bun", scope: "required-for-ui", args: []string{"--version"}},
		{name: "temporal", scope: "required-for-temporal-tests", args: []string{"--version"}},
		{name: "docker", scope: "optional", args: []string{"--version"}},
	}
	harnessProbeTool = probeHarnessTool
)

func runHarnessToolchainPreflightStep(ctx context.Context, repoRoot string) (harnessStep, *harnessToolchainReport) {
	started := time.Now()
	report := buildHarnessToolchainPreflightReport(ctx, repoRoot)
	step := harnessStep{
		Name:       "toolchain preflight",
		Command:    []string{"onlava", "harness", "self", "internal:toolchain-preflight", repoRoot},
		OK:         !hasErrorDiagnostics(report.Diagnostics),
		DurationMS: time.Since(started).Milliseconds(),
		Summary: map[string]any{
			"tools":       len(report.Tools),
			"env":         len(report.Env),
			"diagnostics": len(report.Diagnostics),
		},
		Diagnostics: report.Diagnostics,
	}
	if !step.OK {
		step.Error = "toolchain preflight failed"
	}
	return step, report
}

func buildHarnessToolchainPreflightReport(ctx context.Context, repoRoot string) *harnessToolchainReport {
	report := &harnessToolchainReport{SchemaVersion: harnessToolchainSchema}
	for _, spec := range harnessToolchainSpecs {
		tool := harnessProbeTool(ctx, spec.name, spec.scope, spec.required, spec.args)
		if !tool.Present && tool.Required {
			report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
				Stage:           "toolchain preflight",
				Severity:        "error",
				Message:         spec.name + " is required but was not found in PATH",
				SuggestedAction: installSuggestion(spec.name),
			})
		} else if !tool.Present {
			report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
				Stage:           "toolchain preflight",
				Severity:        "warning",
				Message:         spec.name + " is not available for " + spec.scope,
				SuggestedAction: "Install `" + spec.name + "` before running checks that require " + spec.scope + ".",
			})
		}
		report.Tools = append(report.Tools, tool)
	}
	for _, name := range sortedOnlavaEnv(os.Environ()) {
		report.Env = append(report.Env, harnessEnvValue{Name: name, Value: os.Getenv(name)})
	}
	_ = repoRoot
	return report
}

func probeHarnessTool(ctx context.Context, name, scope string, required bool, args []string) harnessToolchainTool {
	tool := harnessToolchainTool{Name: name, Scope: scope, Required: required}
	path, err := exec.LookPath(name)
	if err != nil {
		tool.Error = err.Error()
		return tool
	}
	tool.Present = true
	tool.Path = path
	if len(args) == 0 {
		return tool
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := commandTreeContext(checkCtx, path, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		tool.Error = strings.TrimSpace(err.Error() + ": " + string(output))
		return tool
	}
	tool.Version = firstLine(strings.TrimSpace(string(output)))
	return tool
}

func runHarnessDriftStep(ctx context.Context, repoRoot string) (harnessStep, *harnessDriftReport) {
	started := time.Now()
	report := buildHarnessDriftReport(ctx, repoRoot)
	step := harnessStep{
		Name:       "contract drift checks",
		Command:    []string{"onlava", "harness", "self", "internal:drift-check", repoRoot},
		OK:         !hasErrorDiagnostics(report.Diagnostics),
		DurationMS: time.Since(started).Milliseconds(),
		Summary: map[string]any{
			"cli_commands":      len(report.CLI.Commands),
			"env_vars":          len(report.Env.Variables),
			"forbidden_tracked": len(report.Artifacts.ForbiddenTracked),
			"embeds":            len(report.Embeds.Embeds),
			"diagnostics":       len(report.Diagnostics),
		},
		Diagnostics: report.Diagnostics,
	}
	if !step.OK {
		step.Error = "contract drift checks failed"
	}
	return step, report
}

func buildHarnessDriftReport(ctx context.Context, repoRoot string) *harnessDriftReport {
	report := &harnessDriftReport{SchemaVersion: harnessDriftSchema}
	report.CLI, report.Diagnostics = buildHarnessCLIContractReport(repoRoot, report.Diagnostics)
	report.Env, report.Diagnostics = buildHarnessEnvVarReport(repoRoot, report.Diagnostics)
	report.Artifacts, report.Diagnostics = buildHarnessArtifactHygieneReport(ctx, repoRoot, report.Diagnostics)
	report.Embeds, report.Diagnostics = buildHarnessEmbedReport(repoRoot, report.Diagnostics)
	return report
}

func buildHarnessCLIContractReport(repoRoot string, diagnostics []checkDiagnostic) (harnessCLIContractReport, []checkDiagnostic) {
	usage := usageError().Error()
	var report harnessCLIContractReport
	for _, spec := range []struct {
		name   string
		needle string
		mode   string
		smoke  func() error
	}{
		{name: "version", needle: "onlava version [--json]", mode: "execute", smoke: func() error {
			var out bytes.Buffer
			return writeVersionJSON(&out, buildVersionResponse())
		}},
		{name: "check", needle: "onlava check [--app-root <path>] [--json]", mode: "parse", smoke: func() error {
			_, err := parseCheckArgs([]string{"--app-root", filepath.Join(repoRoot, "testdata", "apps", "basic"), "--json"})
			return err
		}},
		{name: "inspect docs", needle: "onlava inspect docs --json [--repo-root <path>]", mode: "execute", smoke: func() error {
			var out bytes.Buffer
			return runOnlavaInspect([]string{"docs", "--repo-root", repoRoot, "--json"}, &out)
		}},
		{name: "harness self", needle: "onlava harness self [--repo-root <path>] [--json] [--write]", mode: "parse", smoke: func() error {
			_, err := parseHarnessSelfArgs([]string{"--repo-root", repoRoot, "--json"})
			return err
		}},
		{name: "status", needle: "onlava status --json [--app-root <path>] [--session <id>] [--watch]", mode: "parse", smoke: func() error {
			_, err := parseStatusArgs([]string{"--json", "--app-root", repoRoot, "--session", "current", "--watch"})
			return err
		}},
	} {
		item := harnessCLIContractCommand{Name: spec.name, Usage: strings.Contains(usage, spec.needle), Mode: spec.mode}
		if !item.Usage {
			item.Error = "usage text missing " + spec.needle
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "contract drift checks",
				Severity:        "error",
				File:            filepath.ToSlash(filepath.Join(repoRoot, "cmd", "onlava", "main.go")),
				Message:         item.Error,
				SuggestedAction: "Update usage text or the CLI contract smoke table so stable commands stay discoverable.",
			})
		}
		if err := spec.smoke(); err != nil {
			item.Error = strings.TrimSpace(err.Error())
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "contract drift checks",
				Severity:        "error",
				Message:         spec.name + " CLI contract smoke failed: " + item.Error,
				SuggestedAction: "Fix the command parser or update the CLI contract smoke if the public command changed.",
			})
		} else {
			item.Smoke = true
		}
		report.Commands = append(report.Commands, item)
	}
	return report, diagnostics
}

func buildHarnessEnvVarReport(repoRoot string, diagnostics []checkDiagnostic) (harnessEnvVarReport, []checkDiagnostic) {
	used := scanHarnessEnvVars(repoRoot, []string{".go"})
	documentedText := readOptionalText(filepath.Join(repoRoot, "docs", "environment.md")) + "\n" +
		readOptionalText(filepath.Join(repoRoot, "docs", "local-contract.md")) + "\n" +
		readOptionalText(filepath.Join(repoRoot, "docs", "grafana.md")) + "\n" +
		readOptionalText(filepath.Join(repoRoot, "SKILL.md"))
	var report harnessEnvVarReport
	for _, name := range sortedEnvVarNames(used) {
		scope := used[name]
		if scope == "" {
			scope = "runtime"
		}
		if scope == "test" || strings.HasPrefix(name, "ONLAVA_TEST_") || strings.HasPrefix(name, "ONLAVA_INTEGRATION_") {
			report.Variables = append(report.Variables, harnessEnvVarFinding{Name: name, Scope: "test", UsedInCode: true, Documented: strings.Contains(documentedText, name)})
			continue
		}
		documented := strings.Contains(documentedText, name)
		report.Variables = append(report.Variables, harnessEnvVarFinding{Name: name, Scope: "runtime", UsedInCode: true, Documented: documented})
		if !documented {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "contract drift checks",
				Severity:        "warning",
				File:            filepath.ToSlash(filepath.Join(repoRoot, "docs", "environment.md")),
				Message:         "ONLAVA environment variable used in code but not documented: " + name,
				SuggestedAction: "Document the variable in docs/environment.md or mark it test-only with an ONLAVA_TEST_ prefix.",
			})
		}
	}
	return report, diagnostics
}

func scanHarnessEnvVars(repoRoot string, extensions []string) map[string]string {
	allowedExt := map[string]bool{}
	for _, ext := range extensions {
		allowedExt[ext] = true
	}
	found := map[string]string{}
	_ = filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if architectureSkipDir(filepath.Dir(rel)) || !allowedExt[filepath.Ext(rel)] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		scope := "runtime"
		if strings.HasSuffix(rel, "_test.go") || strings.HasPrefix(rel, "testdata/") {
			scope = "test"
		}
		for _, token := range extractOnlavaEnvTokens(string(data)) {
			if found[token] != "runtime" {
				found[token] = scope
			}
		}
		return nil
	})
	return found
}

func sortedEnvVarNames(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func extractOnlavaEnvTokens(text string) []string {
	var tokens []string
	for i := 0; i < len(text); i++ {
		if !strings.HasPrefix(text[i:], "ONLAVA_") {
			continue
		}
		j := i + len("ONLAVA_")
		for j < len(text) {
			ch := text[j]
			if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
				j++
				continue
			}
			break
		}
		token := text[i:j]
		if strings.Trim(token, "_") != "ONLAVA" {
			tokens = append(tokens, token)
		}
		i = j
	}
	sort.Strings(tokens)
	return compactStrings(tokens)
}

func buildHarnessArtifactHygieneReport(ctx context.Context, repoRoot string, diagnostics []checkDiagnostic) (harnessArtifactHygieneReport, []checkDiagnostic) {
	var report harnessArtifactHygieneReport
	output, err := runHarnessGit(ctx, repoRoot, "ls-files")
	if err != nil {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "contract drift checks",
			Severity:        "warning",
			Message:         "git ls-files failed: " + err.Error(),
			SuggestedAction: "Run `git ls-files` from the repo root and inspect repository state.",
		})
	} else {
		for _, path := range splitCommandLines(output) {
			if forbiddenTrackedArtifact(path) {
				report.ForbiddenTracked = append(report.ForbiddenTracked, path)
				diagnostics = append(diagnostics, checkDiagnostic{
					Stage:           "contract drift checks",
					Severity:        "error",
					File:            filepath.ToSlash(filepath.Join(repoRoot, filepath.FromSlash(path))),
					Message:         "generated/local artifact is tracked: " + path,
					SuggestedAction: "Remove the generated artifact from git and keep it ignored.",
				})
			}
		}
	}
	source := readOptionalText(filepath.Join(repoRoot, "internal", "build", "build.go"))
	for _, token := range []string{".env", ".DS_Store", "__MACOSX", "node_modules", "coverage"} {
		ok := strings.Contains(source, token)
		report.WorkspaceRules = append(report.WorkspaceRules, token)
		if !ok {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "contract drift checks",
				Severity:        "error",
				File:            filepath.ToSlash(filepath.Join(repoRoot, "internal", "build", "build.go")),
				Message:         "build workspace copy exclusion is missing token: " + token,
				SuggestedAction: "Update build workspace copy rules so local/generated files cannot leak into builds.",
			})
		}
	}
	sort.Strings(report.ForbiddenTracked)
	return report, diagnostics
}

func forbiddenTrackedArtifact(path string) bool {
	path = filepath.ToSlash(path)
	if strings.Contains(path, "/.onlava/") || strings.HasPrefix(path, ".onlava/") {
		return true
	}
	if strings.Contains(path, "/coverage/") || strings.HasPrefix(path, "coverage/") {
		return true
	}
	if strings.Contains(path, "/oracle/") || strings.HasPrefix(path, "oracle/") {
		return true
	}
	return filepath.Base(path) == ".DS_Store" || strings.HasPrefix(path, ".codex-tmp/")
}

func buildHarnessEmbedReport(repoRoot string, diagnostics []checkDiagnostic) (harnessEmbedReport, []checkDiagnostic) {
	var report harnessEmbedReport
	_ = filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if architectureSkipDir(filepath.Dir(rel)) || filepath.Ext(rel) != ".go" || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		patterns := parseGoEmbedPatterns(string(data))
		if len(patterns) == 0 {
			return nil
		}
		pkgDir := filepath.Dir(rel)
		for _, pattern := range patterns {
			files := map[string]struct{}{}
			_ = addEmbeddedPatternFiles(repoRoot, pkgDir, pattern, files)
			resolved := sortedStructKeys(files)
			covered := len(resolved) > 0
			for _, resolvedPath := range resolved {
				if !harnessBinaryFreshnessCoversRel(resolvedPath) {
					covered = false
				}
			}
			finding := harnessEmbedFinding{
				File:                     rel,
				Pattern:                  pattern,
				Resolved:                 resolved,
				CoveredByBinaryFreshness: covered,
			}
			report.Embeds = append(report.Embeds, finding)
			if len(resolved) == 0 || !covered {
				diagnostics = append(diagnostics, checkDiagnostic{
					Stage:           "contract drift checks",
					Severity:        "error",
					File:            filepath.ToSlash(filepath.Join(repoRoot, filepath.FromSlash(rel))),
					Message:         "go:embed pattern is not covered by installed-binary freshness: " + pattern,
					SuggestedAction: "Update latestHarnessSourceModTime inputs so embedded files rebuild the installed onlava binary.",
				})
			}
		}
		return nil
	})
	sort.Slice(report.Embeds, func(i, j int) bool {
		if report.Embeds[i].File == report.Embeds[j].File {
			return report.Embeds[i].Pattern < report.Embeds[j].Pattern
		}
		return report.Embeds[i].File < report.Embeds[j].File
	})
	return report, diagnostics
}

func harnessBinaryFreshnessCoversRel(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, prefix := range []string{"auth/", "cmd/", "cron/", "errs/", "internal/", "middleware/", "pgxpool/", "rlog/", "runtime/", "runtimeapp/", "temporal/"} {
		if strings.HasPrefix(rel, prefix) && harnessBinaryInputFile(rel) {
			for _, part := range strings.Split(filepath.Dir(rel), "/") {
				if harnessBinaryInputSkipDir(part) {
					return false
				}
			}
			return true
		}
	}
	return rel == "go.mod" || rel == "go.sum"
}

func runHarnessFixtureMatrixStep(ctx context.Context, repoRoot string) (harnessStep, *harnessFixtureMatrixReport) {
	started := time.Now()
	report := buildHarnessFixtureMatrixReport(ctx, repoRoot)
	step := harnessStep{
		Name:       "fixture matrix",
		Command:    []string{"onlava", "harness", "self", "internal:fixture-matrix", repoRoot},
		OK:         !hasErrorDiagnostics(report.Diagnostics),
		DurationMS: time.Since(started).Milliseconds(),
		Summary: map[string]any{
			"fixtures":    len(report.Fixtures),
			"diagnostics": len(report.Diagnostics),
		},
		Diagnostics: report.Diagnostics,
	}
	if !step.OK {
		step.Error = "fixture matrix failed"
	}
	return step, report
}

func buildHarnessFixtureMatrixReport(ctx context.Context, repoRoot string) *harnessFixtureMatrixReport {
	report := &harnessFixtureMatrixReport{SchemaVersion: harnessFixtureMatrixSchema}
	fixtureRoot := filepath.Join(repoRoot, "testdata", "apps")
	entries, err := os.ReadDir(fixtureRoot)
	if err != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "fixture matrix",
			Severity:        "error",
			File:            filepath.ToSlash(fixtureRoot),
			Message:         err.Error(),
			SuggestedAction: "Restore testdata/apps fixture apps.",
		})
		return report
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		appRoot := filepath.Join(fixtureRoot, entry.Name())
		if _, err := os.Stat(filepath.Join(appRoot, ".onlava.json")); err != nil {
			continue
		}
		result := harnessFixtureResult{
			Name:    entry.Name(),
			Path:    filepath.ToSlash(filepath.Join("testdata", "apps", entry.Name())),
			Inspect: map[string]bool{},
		}
		checkStep, _ := runHarnessCheck(ctx, appRoot)
		result.Check = checkStep.OK
		if !checkStep.OK {
			result.Diagnostics = append(result.Diagnostics, checkStep.Diagnostics...)
			if checkStep.Error != "" {
				result.Diagnostics = append(result.Diagnostics, checkDiagnostic{
					Stage:    "fixture matrix",
					Severity: "error",
					File:     filepath.ToSlash(appRoot),
					Message:  checkStep.Error,
				})
			}
		}
		for _, subject := range []string{"app", "routes", "services", "endpoints"} {
			step := runHarnessFixtureInspect(repoRoot, subject, appRoot)
			result.Inspect[subject] = step.OK
			if !step.OK {
				result.Diagnostics = append(result.Diagnostics, checkDiagnostic{
					Stage:           "fixture matrix",
					Severity:        "error",
					File:            filepath.ToSlash(appRoot),
					Message:         "inspect " + subject + " failed: " + firstNonEmpty(step.Error, step.OutputTail),
					SuggestedAction: "Run `onlava inspect " + subject + " --json --app-root " + appRoot + "` and fix the fixture.",
				})
			}
		}
		if hasErrorDiagnostics(result.Diagnostics) {
			report.Diagnostics = append(report.Diagnostics, result.Diagnostics...)
		}
		report.Fixtures = append(report.Fixtures, result)
	}
	sort.Slice(report.Fixtures, func(i, j int) bool {
		return report.Fixtures[i].Name < report.Fixtures[j].Name
	})
	return report
}

func runHarnessFixtureInspect(repoRoot, subject, appRoot string) harnessStep {
	started := time.Now()
	var out bytes.Buffer
	err := runOnlavaInspect([]string{subject, "--app-root", appRoot, "--json"}, &out)
	step := harnessStep{
		Name:       "inspect " + subject,
		Command:    []string{"onlava", "inspect", subject, "--app-root", appRoot, "--json"},
		OK:         err == nil,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		return step
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		step.OK = false
		step.Error = "invalid inspect JSON: " + err.Error()
		return step
	}
	step.Summary = summarizeHarnessInspect(subject, payload)
	schemaRel := "docs/schemas/onlava.inspect." + subject + ".v1.schema.json"
	schemaPath := filepath.Join(repoRoot, filepath.FromSlash(schemaRel))
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, payload); len(diagnostics) > 0 {
		step.OK = false
		step.Error = subject + " inspect JSON does not conform to " + schemaRel + ": " + strings.Join(diagnostics, "; ")
	}
	return step
}

func runHarnessAffectedPackageTestsStep(ctx context.Context, repoRoot string, changedArea *harnessChangedAreaReport) harnessStep {
	patternSet := map[string]bool{}
	if changedArea != nil {
		for _, file := range changedArea.ChangedFiles {
			if file.Package == "" || !strings.HasPrefix(file.Package, "github.com/pbrazdil/onlava") {
				continue
			}
			rel := strings.TrimPrefix(file.Package, "github.com/pbrazdil/onlava")
			rel = strings.TrimPrefix(rel, "/")
			if rel == "" {
				patternSet["."] = true
			} else {
				patternSet["./"+rel] = true
			}
		}
	}
	patterns := sortedStringSet(patternSet)
	if len(patterns) == 0 {
		return harnessStep{
			Name:       "affected package tests",
			Command:    []string{"go", "test", "-count=1"},
			OK:         true,
			DurationMS: 0,
			Summary:    map[string]any{"packages": 0},
		}
	}
	command := append([]string{"go", "test", "-count=1"}, patterns...)
	return runHarnessExecStep(ctx, repoRoot, "affected package tests", command)
}

func annotateHarnessStepEffects(steps []harnessStep) {
	for i := range steps {
		steps[i].Effects = harnessStepEffects(steps[i])
	}
}

func harnessStepEffects(step harnessStep) []string {
	set := map[string]bool{}
	for _, arg := range step.Command {
		switch arg {
		case "go", "bun", "git":
			set["external-binary"] = true
		}
	}
	switch step.Name {
	case "parallel dev sessions":
		set["loopback-network"] = true
		set["ports"] = true
		set["agent-socket"] = true
		set["tempdir"] = true
	case "go tests", "go test timing", "affected package tests", "race shortlist", "race full suite":
		set["test-cache"] = true
		set["external-binary"] = true
	case "dashboard ui typecheck", "dashboard ui build":
		set["node-runtime"] = true
		set["external-binary"] = true
	case "fixture matrix":
		set["filesystem-cache"] = true
		set["external-binary"] = true
	case "install onlava binary":
		set["filesystem-write"] = true
		set["external-binary"] = true
	case "onlava binary fresh":
		set["path-binary"] = true
	case "toolchain preflight":
		set["external-binary"] = true
	case "schema validation", "changed area oracle", "contract drift checks", "knowledge contract", "inspect docs", "architecture checks", "ui static architecture", "dashboard ui fresh":
		set["filesystem-read"] = true
	}
	return sortedStringSet(set)
}

func sortedOnlavaEnv(env []string) []string {
	set := map[string]bool{}
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if ok && strings.HasPrefix(name, "ONLAVA_") {
			set[name] = true
		}
	}
	return sortedStringSet(set)
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	last := ""
	for _, value := range values {
		if value == "" || value == last {
			continue
		}
		out = append(out, value)
		last = value
	}
	return out
}

func sortedStructKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		return strings.TrimSpace(value[:idx])
	}
	return value
}
