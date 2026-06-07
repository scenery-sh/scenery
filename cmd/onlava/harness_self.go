package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	appcfg "github.com/pbrazdil/onlava/internal/app"
)

type harnessSelfOptions struct {
	RepoRoot string
	JSON     bool
	Write    bool
	Mode     string
}

type harnessSelfResponse struct {
	SchemaVersion    string                         `json:"schema_version"`
	OK               bool                           `json:"ok"`
	GeneratedAt      string                         `json:"generated_at"`
	Mode             string                         `json:"mode"`
	Repo             harnessSelfRepo                `json:"repo"`
	Knowledge        harnessKnowledge               `json:"knowledge"`
	Toolchain        *harnessToolchainReport        `json:"toolchain,omitempty"`
	ChangedArea      *harnessChangedAreaReport      `json:"changed_area,omitempty"`
	Drift            *harnessDriftReport            `json:"drift,omitempty"`
	TestTiming       *harnessTestTimingReport       `json:"test_timing,omitempty"`
	FixtureMatrix    *harnessFixtureMatrixReport    `json:"fixture_matrix,omitempty"`
	SchemaValidation *harnessSchemaValidationReport `json:"schema_validation,omitempty"`
	Steps            []harnessStep                  `json:"steps"`
	Artifacts        []harnessArtifact              `json:"artifacts"`
	NextActions      []string                       `json:"next_actions,omitempty"`
	Wrote            string                         `json:"wrote,omitempty"`
}

type harnessSelfRepo struct {
	Root       string `json:"root"`
	ModulePath string `json:"module_path"`
	GoModPath  string `json:"go_mod_path"`
}

func runOnlavaHarnessSelf(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseHarnessSelfArgs(args)
	if err != nil {
		return err
	}
	if opts.Mode == "" {
		opts.Mode = harnessSelfModeDefault
	}

	repoRoot, err := discoverOnlavaRepoRoot(opts.RepoRoot)
	if err != nil {
		return err
	}

	resp := harnessSelfResponse{
		SchemaVersion: "onlava.harness.self.v1",
		OK:            true,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		Mode:          opts.Mode,
		Repo: harnessSelfRepo{
			Root:       repoRoot,
			ModulePath: "github.com/pbrazdil/onlava",
			GoModPath:  filepath.Join(repoRoot, "go.mod"),
		},
		Knowledge: buildHarnessSelfKnowledge(repoRoot),
	}
	artifactCtx := newHarnessArtifactContext(repoRoot, opts.Write)

	toolchainStep, toolchain := runHarnessToolchainPreflightStep(ctx, repoRoot)
	resp.Toolchain = toolchain
	changedAreaStep, changedArea := runHarnessChangedAreaStep(ctx, repoRoot)
	resp.ChangedArea = changedArea
	driftStep, drift := runHarnessDriftStep(ctx, repoRoot)
	resp.Drift = drift
	resp.Steps = append(resp.Steps,
		toolchainStep,
		runHarnessKnowledgeStep(repoRoot),
	)
	resp.Steps = append(resp.Steps,
		changedAreaStep,
		runHarnessInspectDocsStep(repoRoot),
		runHarnessArchitectureStep(repoRoot),
		driftStep,
		runHarnessUIStaticStep(repoRoot),
	)

	switch opts.Mode {
	case harnessSelfModeQuick:
		resp.Steps = append(resp.Steps, runHarnessAffectedPackageTestsStep(ctx, repoRoot, changedArea, artifactCtx))
	case harnessSelfModeDefault, harnessSelfModeRace, harnessSelfModeRelease:
		resp.Steps = append(resp.Steps,
			runHarnessExecStep(ctx, repoRoot, "install onlava binary", []string{"go", "install", "./cmd/onlava"}, artifactCtx),
			runHarnessOnlavaBinaryStep(repoRoot),
		)
		goTestStep, testTiming := runHarnessGoTestTimingStepForMode(ctx, repoRoot, opts.Mode, artifactCtx)
		resp.TestTiming = testTiming
		resp.Steps = append(resp.Steps,
			goTestStep,
			runHarnessParallelDevStep(ctx, repoRoot),
			runHarnessExecStep(ctx, filepath.Join(repoRoot, "ui"), "dashboard ui typecheck", []string{"bun", "run", "typecheck"}, artifactCtx),
			runHarnessExecStep(ctx, filepath.Join(repoRoot, "ui"), "dashboard ui build", []string{"bun", "run", "build"}, artifactCtx),
			runHarnessFreshnessStep("dashboard ui fresh", filepath.Join(repoRoot, "ui"), dashboardUIBuildStale, "Run `bun run build` inside `ui/`, then rerun `onlava harness self --json`."),
		)
		fixtureStep, fixtureMatrix := runHarnessFixtureMatrixStep(ctx, repoRoot)
		resp.FixtureMatrix = fixtureMatrix
		resp.Steps = append(resp.Steps, fixtureStep)
		if opts.Mode == harnessSelfModeRace {
			resp.Steps = append(resp.Steps, runHarnessExecStep(ctx, repoRoot, "race shortlist", []string{"go", "test", "-race", "./internal/agent", "./internal/localproxy", "./runtime", "./cmd/onlava"}, artifactCtx))
		}
		if opts.Mode == harnessSelfModeRelease {
			resp.Steps = append(resp.Steps, runHarnessExecStep(ctx, repoRoot, "race full suite", []string{"go", "test", "-race", "./..."}, artifactCtx))
		}
	default:
		return fmt.Errorf("unknown harness self mode %q", opts.Mode)
	}

	if opts.Write {
		resp.Wrote = filepath.Join(repoRoot, ".onlava", "harness", "self-latest.json")
	}
	resp.Artifacts = buildHarnessSelfArtifacts(repoRoot, opts.Write, resp)
	annotateHarnessStepEffects(resp.Steps)
	annotateHarnessEvidence(resp.Steps, repoRoot)

	schemaValidationStep, schemaValidation := runHarnessSchemaValidationStep(repoRoot, resp)
	resp.SchemaValidation = schemaValidation
	resp.Steps = append(resp.Steps, schemaValidationStep)
	annotateHarnessStepEffects(resp.Steps)
	annotateHarnessEvidence(resp.Steps, repoRoot)
	for _, step := range resp.Steps {
		if !step.OK {
			resp.OK = false
		}
	}
	resp.NextActions = buildHarnessNextActions(resp.Steps)

	if opts.Write {
		if err := writeHarnessSelfResult(resp.Wrote, resp); err != nil {
			return err
		}
		if err := writeHarnessSelfOracleArtifacts(repoRoot, resp); err != nil {
			return err
		}
	}

	if opts.JSON {
		if err := writeHarnessSelfJSON(stdout, resp); err != nil {
			return err
		}
		if !resp.OK {
			return &silentCLIError{err: fmt.Errorf("onlava harness self failed")}
		}
		return nil
	}

	if err := writeHarnessSelfText(stdout, resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("onlava harness self failed")
	}
	return nil
}

const (
	harnessSelfModeDefault = "default"
	harnessSelfModeQuick   = "quick"
	harnessSelfModeRace    = "race"
	harnessSelfModeRelease = "release"
)

func harnessSelfGoTestCommand() []string {
	return []string{"go", "test", "-count=1", "-json", "./..."}
}

func harnessSelfGoTestEnv() []string {
	return nil
}

func runHarnessInspectDocsStep(repoRoot string) harnessStep {
	started := time.Now()
	var out bytes.Buffer
	err := runOnlavaInspect([]string{"docs", "--repo-root", repoRoot, "--json"}, &out)
	step := harnessStep{
		Name:       "inspect docs",
		Command:    []string{"onlava", "inspect", "docs", "--repo-root", repoRoot, "--json"},
		OK:         err == nil,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		step.OutputTail = tailString(out.String(), 8192)
		step.Diagnostics = []checkDiagnostic{{
			Stage:           step.Name,
			Severity:        "error",
			Message:         firstNonEmpty(step.OutputTail, step.Error),
			SuggestedAction: "Run `onlava inspect docs --json`, fix the reported docs issue, then rerun `onlava harness self --json`.",
		}}
		return step
	}
	var payload inspectDocsResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		step.OK = false
		step.Error = "invalid inspect docs JSON: " + err.Error()
		step.Diagnostics = []checkDiagnostic{{
			Stage:           step.Name,
			Severity:        "error",
			Message:         step.Error,
			SuggestedAction: "Fix `onlava inspect docs --json` output so it conforms to onlava.inspect.docs.v1.",
		}}
		return step
	}
	step.Summary = map[string]any{
		"schema_version":   payload.SchemaVersion,
		"document_count":   payload.Summary.DocumentCount,
		"missing_count":    payload.Summary.MissingCount,
		"review_due_count": payload.Summary.ReviewDueCount,
		"stale_count":      payload.Summary.StaleCount,
	}
	if payload.SchemaVersion != inspectDocsSchema {
		step.OK = false
		step.Error = "unexpected schema_version " + payload.SchemaVersion
	}
	if payload.Summary.MissingCount > 0 || payload.Summary.StaleCount > 0 {
		step.OK = false
		step.Diagnostics = []checkDiagnostic{{
			Stage:           step.Name,
			Severity:        "error",
			Message:         "docs knowledge base has missing or stale entries",
			SuggestedAction: "Run `onlava inspect docs --json`, update docs/knowledge.json or the referenced docs, then rerun `onlava harness self --json`.",
		}}
	}
	return step
}

func parseHarnessSelfArgs(args []string) (harnessSelfOptions, error) {
	opts := harnessSelfOptions{Mode: harnessSelfModeDefault}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo-root":
			i++
			if i >= len(args) {
				return harnessSelfOptions{}, fmt.Errorf("missing value for --repo-root")
			}
			opts.RepoRoot = args[i]
		case "--json":
			opts.JSON = true
		case "--write":
			opts.Write = true
		case "--quick":
			if opts.Mode != harnessSelfModeDefault {
				return harnessSelfOptions{}, fmt.Errorf("only one harness self mode may be selected")
			}
			opts.Mode = harnessSelfModeQuick
		case "--race":
			if opts.Mode != harnessSelfModeDefault {
				return harnessSelfOptions{}, fmt.Errorf("only one harness self mode may be selected")
			}
			opts.Mode = harnessSelfModeRace
		case "--release":
			if opts.Mode != harnessSelfModeDefault {
				return harnessSelfOptions{}, fmt.Errorf("only one harness self mode may be selected")
			}
			opts.Mode = harnessSelfModeRelease
		default:
			return harnessSelfOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func discoverOnlavaRepoRoot(start string) (string, error) {
	if start == "" {
		if cwd, err := os.Getwd(); err == nil {
			if root, ok := findOnlavaRepoRoot(cwd); ok {
				return root, nil
			}
		}
		start = appcfg.RepoRoot()
	}
	root, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if found, ok := findOnlavaRepoRoot(root); ok {
		return found, nil
	}
	return "", fmt.Errorf("no onlava repo root found from %s", root)
}

func findOnlavaRepoRoot(start string) (string, bool) {
	dir := filepath.Clean(start)
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			text := string(data)
			if strings.HasPrefix(text, "module github.com/pbrazdil/onlava\n") || strings.Contains(text, "\nmodule github.com/pbrazdil/onlava\n") {
				return dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func runHarnessExecStep(ctx context.Context, dir, name string, command []string, artifactCtxs ...harnessArtifactContext) harnessStep {
	started := time.Now()
	evidence := newHarnessEvidence(command, dir, started)
	step := harnessStep{
		Name:       name,
		Command:    command,
		DurationMS: 0,
		Evidence:   &evidence,
	}
	if len(command) == 0 {
		step.OK = false
		step.Error = "missing command"
		code := 1
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, "", "", &code, nil)
		return step
	}
	path, err := exec.LookPath(command[0])
	if err != nil {
		step.OK = false
		step.DurationMS = time.Since(started).Milliseconds()
		step.Error = fmt.Sprintf("%s not found in PATH", command[0])
		step.Diagnostics = []checkDiagnostic{{
			Stage:           name,
			Severity:        "error",
			Message:         step.Error,
			SuggestedAction: installSuggestion(command[0]),
		}}
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, "", step.Error, exitCodeFromError(err), nil)
		return step
	}

	cmd := commandTreeContext(ctx, path, command[1:]...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	step.DurationMS = time.Since(started).Milliseconds()
	stdoutBytes := stdout.Bytes()
	stderrBytes := stderr.Bytes()
	artifacts, artifactDiagnostics := writeHarnessOutputEvidenceArtifacts(optionalHarnessArtifactContext(artifactCtxs), name, sanitizeHarnessArtifactName(name)+".stdout.log", "", stdoutBytes, stderrBytes)
	step.Summary = map[string]any{
		"cwd":          dir,
		"output_bytes": len(stdoutBytes) + len(stderrBytes),
	}
	step.Diagnostics = append(step.Diagnostics, artifactDiagnostics...)
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		step.OutputTail = tailString(firstNonEmpty(stderr.String(), stdout.String()), 8192)
		step.Diagnostics = append(step.Diagnostics, checkDiagnostic{
			Stage:           name,
			Severity:        "error",
			Message:         firstNonEmpty(strings.TrimSpace(step.OutputTail), step.Error),
			SuggestedAction: rerunSuggestion(command, dir),
		})
		finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, stdout.String(), stderr.String(), exitCodeFromError(err), artifacts)
		return step
	}
	step.OK = true
	finalizeHarnessEvidence(step.Evidence, time.Since(started), step.OK, stdout.String(), stderr.String(), exitCodeFromError(err), artifacts)
	return step
}

func runHarnessFreshnessStep(name, root string, staleFn func(string) (bool, error), suggestion string) harnessStep {
	started := time.Now()
	stale, err := staleFn(root)
	step := harnessStep{
		Name:       name,
		Command:    []string{"onlava", "harness", "self", "internal:freshness-check", root},
		DurationMS: time.Since(started).Milliseconds(),
		Summary: map[string]any{
			"path":  root,
			"stale": stale,
		},
	}
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		step.Diagnostics = []checkDiagnostic{{
			Stage:           name,
			Severity:        "error",
			Message:         step.Error,
			SuggestedAction: suggestion,
		}}
		return step
	}
	if stale {
		step.OK = false
		step.Diagnostics = []checkDiagnostic{{
			Stage:           name,
			Severity:        "error",
			Message:         filepath.ToSlash(root) + " build output is stale",
			SuggestedAction: suggestion,
		}}
		return step
	}
	step.OK = true
	return step
}

func runHarnessOnlavaBinaryStep(repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:       "onlava binary fresh",
		Command:    []string{"go", "install", "./cmd/onlava"},
		DurationMS: 0,
	}
	onlavaPath, err := exec.LookPath("onlava")
	if err != nil {
		step.OK = false
		step.DurationMS = time.Since(started).Milliseconds()
		step.Error = "onlava not found in PATH"
		step.Diagnostics = []checkDiagnostic{{
			Stage:           step.Name,
			Severity:        "error",
			Message:         step.Error,
			SuggestedAction: "Run `go install ./cmd/onlava` from the onlava repo and ensure your Go bin directory is in PATH.",
		}}
		return step
	}
	binaryInfo, err := os.Stat(onlavaPath)
	if err != nil {
		step.OK = false
		step.DurationMS = time.Since(started).Milliseconds()
		step.Error = err.Error()
		return step
	}
	latest, ok, err := latestHarnessSourceModTime(repoRoot)
	if err != nil {
		step.OK = false
		step.DurationMS = time.Since(started).Milliseconds()
		step.Error = err.Error()
		return step
	}
	step.Summary = map[string]any{
		"binary_path":        onlavaPath,
		"binary_mod_time":    binaryInfo.ModTime().UTC().Format(time.RFC3339Nano),
		"latest_source_time": latest.UTC().Format(time.RFC3339Nano),
	}
	if ok && binaryInfo.ModTime().Before(latest) {
		step.OK = false
		step.Diagnostics = []checkDiagnostic{{
			Stage:           step.Name,
			Severity:        "error",
			Message:         "installed onlava binary is older than repo sources",
			SuggestedAction: "Run `go install ./cmd/onlava` from the onlava repo.",
		}}
	} else {
		step.OK = true
	}
	step.DurationMS = time.Since(started).Milliseconds()
	return step
}

func runHarnessKnowledgeStep(repoRoot string) harnessStep {
	started := time.Now()
	knowledge := buildHarnessSelfKnowledge(repoRoot)
	step := harnessStep{
		Name:    "knowledge contract",
		Command: []string{"onlava", "harness", "self", "internal:knowledge-check", repoRoot},
		Summary: map[string]any{
			"entrypoints": len(knowledge.Entrypoints),
			"schemas":     len(knowledge.Schemas),
		},
	}

	var diagnostics []checkDiagnostic
	for _, item := range append(append([]harnessKnowledgeFile{}, knowledge.Entrypoints...), knowledge.Schemas...) {
		if item.Exists {
			continue
		}
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           step.Name,
			Severity:        "error",
			File:            filepath.ToSlash(filepath.Join(repoRoot, filepath.FromSlash(item.Path))),
			Message:         "required harness knowledge file is missing",
			SuggestedAction: "Create the missing file or remove it from the self-harness knowledge contract.",
		})
	}

	for _, item := range knowledge.Schemas {
		if !item.Exists {
			continue
		}
		path := filepath.Join(repoRoot, filepath.FromSlash(item.Path))
		data, err := os.ReadFile(path)
		if err != nil {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           step.Name,
				Severity:        "error",
				File:            filepath.ToSlash(path),
				Message:         err.Error(),
				SuggestedAction: "Fix the schema file so the self harness can read it.",
			})
			continue
		}
		if !json.Valid(data) {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           step.Name,
				Severity:        "error",
				File:            filepath.ToSlash(path),
				Message:         "schema file is not valid JSON",
				SuggestedAction: "Fix the JSON syntax, then rerun `onlava harness self --json`.",
			})
		}
	}

	linksChecked, linkDiagnostics := checkHarnessMarkdownLinks(repoRoot, knowledge.Entrypoints)
	diagnostics = append(diagnostics, linkDiagnostics...)
	docsDiagnostics, docsSummary := validateDocsKnowledge(repoRoot)
	diagnostics = append(diagnostics, docsDiagnostics...)
	step.Summary["links_checked"] = linksChecked
	for key, value := range docsSummary {
		step.Summary[key] = value
	}
	skillDiagnostics, skillSummary := validateSkillCoverage(repoRoot)
	diagnostics = append(diagnostics, skillDiagnostics...)
	for key, value := range skillSummary {
		step.Summary[key] = value
	}
	execPlanDiagnostics, execPlanSummary := validateExecPlanContract(repoRoot)
	diagnostics = append(diagnostics, execPlanDiagnostics...)
	for key, value := range execPlanSummary {
		step.Summary[key] = value
	}
	step.DurationMS = time.Since(started).Milliseconds()
	step.Diagnostics = diagnostics
	step.OK = !hasErrorDiagnostics(diagnostics)
	return step
}

func hasErrorDiagnostics(diagnostics []checkDiagnostic) bool {
	for _, diag := range diagnostics {
		if diag.Severity == "error" {
			return true
		}
	}
	return false
}

func checkHarnessMarkdownLinks(repoRoot string, files []harnessKnowledgeFile) (int, []checkDiagnostic) {
	var diagnostics []checkDiagnostic
	checked := 0
	for _, item := range files {
		if !item.Exists || !strings.HasSuffix(item.Path, ".md") {
			continue
		}
		path := filepath.Join(repoRoot, filepath.FromSlash(item.Path))
		data, err := os.ReadFile(path)
		if err != nil {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "knowledge contract",
				Severity:        "error",
				File:            filepath.ToSlash(path),
				Message:         err.Error(),
				SuggestedAction: "Fix the markdown file so the self harness can read it.",
			})
			continue
		}
		for _, raw := range markdownLinkTargets(string(data)) {
			target, ok := normalizeHarnessMarkdownLink(raw)
			if !ok {
				continue
			}
			checked++
			targetPath := target
			if !filepath.IsAbs(targetPath) {
				targetPath = filepath.Join(filepath.Dir(path), filepath.FromSlash(targetPath))
			}
			if _, err := os.Stat(targetPath); err != nil {
				diagnostics = append(diagnostics, checkDiagnostic{
					Stage:           "knowledge contract",
					Severity:        "error",
					File:            filepath.ToSlash(path),
					Message:         "local markdown link target does not exist: " + raw,
					SuggestedAction: "Fix or remove the broken local link, then rerun `onlava harness self --json`.",
				})
			}
		}
	}
	return checked, diagnostics
}

func markdownLinkTargets(text string) []string {
	var targets []string
	offset := 0
	for {
		idx := strings.Index(text[offset:], "](")
		if idx < 0 {
			return targets
		}
		start := offset + idx + len("](")
		end := strings.IndexByte(text[start:], ')')
		if end < 0 {
			return targets
		}
		targets = append(targets, text[start:start+end])
		offset = start + end + 1
	}
}

func normalizeHarnessMarkdownLink(raw string) (string, bool) {
	target := strings.TrimSpace(raw)
	if target == "" || strings.HasPrefix(target, "#") {
		return "", false
	}
	lower := strings.ToLower(target)
	if strings.Contains(lower, "://") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "tel:") {
		return "", false
	}
	if hash := strings.IndexByte(target, '#'); hash >= 0 {
		target = target[:hash]
	}
	if query := strings.IndexByte(target, '?'); query >= 0 {
		target = target[:query]
	}
	target = strings.Trim(strings.TrimSpace(target), "<>")
	if target == "" {
		return "", false
	}
	return target, true
}

var requiredExecPlanSections = []string{
	"## Purpose / Big Picture",
	"## Progress",
	"## Surprises & Discoveries",
	"## Decision Log",
	"## Outcomes & Retrospective",
	"## Context and Orientation",
	"## Milestones",
	"## Plan of Work",
	"## Concrete Steps",
	"## Validation and Acceptance",
	"## Idempotence and Recovery",
	"## Artifacts and Notes",
	"## Interfaces and Dependencies",
}

func validateExecPlanContract(repoRoot string) ([]checkDiagnostic, map[string]any) {
	summary := map[string]any{
		"exec_plan_required_sections": len(requiredExecPlanSections),
	}
	var diagnostics []checkDiagnostic

	standardPath := filepath.Join(repoRoot, "PLANS.md")
	standardData, err := os.ReadFile(standardPath)
	if err != nil {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "knowledge contract",
			Severity:        "error",
			File:            filepath.ToSlash(standardPath),
			Message:         err.Error(),
			SuggestedAction: "Create PLANS.md with the onlava ExecPlan contract.",
		})
	} else {
		diagnostics = append(diagnostics, validateExecPlanSections(repoRoot, "PLANS.md", string(standardData), true)...)
	}

	planFiles, err := filepath.Glob(filepath.Join(repoRoot, "docs", "plans", "*.md"))
	if err != nil {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "knowledge contract",
			Severity:        "error",
			File:            filepath.ToSlash(filepath.Join(repoRoot, "docs", "plans")),
			Message:         err.Error(),
			SuggestedAction: "Fix the docs/plans path so ExecPlans can be validated.",
		})
		return diagnostics, summary
	}
	checked := 0
	for _, path := range planFiles {
		switch filepath.Base(path) {
		case "active.md", "completed.md":
			continue
		}
		checked++
		data, err := os.ReadFile(path)
		relPath, _ := filepath.Rel(repoRoot, path)
		relPath = filepath.ToSlash(relPath)
		if err != nil {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "knowledge contract",
				Severity:        "error",
				File:            filepath.ToSlash(path),
				Message:         err.Error(),
				SuggestedAction: "Fix the ExecPlan file so the self harness can read it.",
			})
			continue
		}
		diagnostics = append(diagnostics, validateExecPlanSections(repoRoot, relPath, string(data), false)...)
	}
	summary["exec_plan_files"] = checked
	return diagnostics, summary
}

var requiredSkillMentions = []string{
	"onlava harness ui --json",
	"docs/ui-agent-contract.md",
	"@onlava registry",
	"bun run shadcn:add @onlava/",
	"onlava harness self --json --write",
}

func validateSkillCoverage(repoRoot string) ([]checkDiagnostic, map[string]any) {
	summary := map[string]any{
		"skill_required_mentions": len(requiredSkillMentions),
	}
	path := filepath.Join(repoRoot, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return []checkDiagnostic{{
			Stage:           "knowledge contract",
			Severity:        "error",
			File:            filepath.ToSlash(path),
			Message:         err.Error(),
			SuggestedAction: "Restore SKILL.md so installed agents have a current entrypoint.",
		}}, summary
	}
	text := string(data)
	missing := 0
	var diagnostics []checkDiagnostic
	for _, mention := range requiredSkillMentions {
		if strings.Contains(text, mention) {
			continue
		}
		missing++
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "knowledge contract",
			Severity:        "error",
			File:            filepath.ToSlash(path),
			Message:         "SKILL.md is missing required capability mention: " + mention,
			SuggestedAction: "Update SKILL.md so installed agents learn the current onlava workflow.",
		})
	}
	summary["skill_missing_mentions"] = missing
	return diagnostics, summary
}

func validateExecPlanSections(repoRoot, relPath, text string, standard bool) []checkDiagnostic {
	var diagnostics []checkDiagnostic
	if standard && !strings.Contains(text, "onlava Execution Plans") {
		diagnostics = append(diagnostics, execPlanDiagnostic(repoRoot, relPath, 1, "PLANS.md does not identify itself as the onlava ExecPlan standard", "Keep PLANS.md as the canonical onlava ExecPlan contract."))
	}
	for _, section := range requiredExecPlanSections {
		if strings.Contains(text, section) {
			continue
		}
		action := "Add the missing section heading exactly as `" + section + "`."
		if standard {
			action = "Document the required ExecPlan section heading `" + section + "` in PLANS.md."
		}
		diagnostics = append(diagnostics, execPlanDiagnostic(repoRoot, relPath, 0, "missing required ExecPlan section: "+section, action))
	}
	if !standard && !strings.Contains(text, "This ExecPlan is a living document") {
		diagnostics = append(diagnostics, execPlanDiagnostic(repoRoot, relPath, 1, "ExecPlan is missing the living-document statement", "Add a short statement near the top saying this ExecPlan is a living document and must be updated as work proceeds."))
	}
	return diagnostics
}

func execPlanDiagnostic(repoRoot, relPath string, line int, message, action string) checkDiagnostic {
	return checkDiagnostic{
		Stage:           "knowledge contract",
		Severity:        "error",
		File:            filepath.ToSlash(filepath.Join(repoRoot, filepath.FromSlash(relPath))),
		Line:            line,
		Message:         message,
		SuggestedAction: action,
	}
}

func latestHarnessSourceModTime(repoRoot string) (time.Time, bool, error) {
	paths := []string{
		"go.mod",
		"go.sum",
		"auth",
		"cmd",
		"cron",
		"data",
		"errs",
		"internal",
		"middleware",
		"pgxpool",
		"rlog",
		"runtime",
		"runtimeapp",
		"temporal",
	}
	var latest time.Time
	found := false
	for _, rel := range paths {
		modTime, ok, err := latestHarnessBinaryInputModTime(filepath.Join(repoRoot, rel))
		if err != nil {
			return time.Time{}, false, err
		}
		if ok && (!found || modTime.After(latest)) {
			latest = modTime
			found = true
		}
	}
	return latest, found, nil
}

func latestHarnessBinaryInputModTime(path string) (time.Time, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	if !info.IsDir() {
		if !harnessBinaryInputFile(path) {
			return time.Time{}, false, nil
		}
		return info.ModTime(), true, nil
	}
	var latest time.Time
	found := false
	err = filepath.WalkDir(path, func(walkPath string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if harnessBinaryInputSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !harnessBinaryInputFile(walkPath) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !found || info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		found = true
		return nil
	})
	if err != nil {
		return time.Time{}, false, err
	}
	return latest, found, nil
}

func harnessBinaryInputSkipDir(name string) bool {
	switch name {
	case ".git", ".onlava", "coverage", "dist", "node_modules":
		return true
	default:
		return false
	}
}

func harnessBinaryInputFile(path string) bool {
	base := filepath.Base(path)
	if base == "" || base == ".DS_Store" || strings.HasPrefix(base, ".env") || strings.HasPrefix(base, ".") {
		return false
	}
	if strings.HasSuffix(base, "_test.go") {
		return false
	}
	return true
}

func buildHarnessSelfKnowledge(repoRoot string) harnessKnowledge {
	entrypoints := []string{
		"AGENTS.md",
		"SKILL.md",
		"PLAN.md",
		"PLANS.md",
		"docs/index.md",
		"docs/knowledge.json",
		"docs/harness-engineering.md",
		"docs/local-contract.md",
		"docs/environment.md",
		"docs/environment.registry.json",
		"docs/grafana.md",
		"docs/app-development-cookbook.md",
		"docs/ui-agent-contract.md",
		"docs/plans/active.md",
		"docs/plans/completed.md",
		"docs/tech-debt.md",
	}
	schemas := []string{
		"docs/schemas/onlava.config.v1.schema.json",
		"docs/schemas/onlava.build.latest.v1.schema.json",
		"docs/schemas/onlava.docs.index.v1.schema.json",
		"docs/schemas/onlava.environment.registry.v1.schema.json",
		"docs/schemas/onlava.harness.artifact.v1.schema.json",
		"docs/schemas/onlava.harness.self.v1.schema.json",
		"docs/schemas/onlava.harness.toolchain.v1.schema.json",
		"docs/schemas/onlava.harness.changed_area.v1.schema.json",
		"docs/schemas/onlava.harness.drift.v1.schema.json",
		"docs/schemas/onlava.harness.test_timing.v1.schema.json",
		"docs/schemas/onlava.harness.fixture_matrix.v1.schema.json",
		"docs/schemas/onlava.harness.schema_validation.v1.schema.json",
		"docs/schemas/onlava.agent_context.v1.schema.json",
		"docs/schemas/onlava.harness.result.v1.schema.json",
		"docs/schemas/onlava.harness.ui.v1.schema.json",
		"docs/schemas/onlava.check.result.v1.schema.json",
		"docs/schemas/onlava.gen.manifest.v1.schema.json",
		"docs/schemas/onlava.inspect.app.v1.schema.json",
		"docs/schemas/onlava.inspect.build.v1.schema.json",
		"docs/schemas/onlava.inspect.docs.v1.schema.json",
		"docs/schemas/onlava.inspect.endpoints.v1.schema.json",
		"docs/schemas/onlava.inspect.harness.v1.schema.json",
		"docs/schemas/onlava.inspect.metrics.v1.schema.json",
		"docs/schemas/onlava.inspect.paths.v1.schema.json",
		"docs/schemas/onlava.inspect.temporal.v1.schema.json",
		"docs/schemas/onlava.inspect.routes.v1.schema.json",
		"docs/schemas/onlava.inspect.services.v1.schema.json",
		"docs/schemas/onlava.inspect.traces.v1.schema.json",
		"docs/schemas/onlava.task.inspect.v1.schema.json",
		"docs/schemas/onlava.task.list.v1.schema.json",
		"docs/schemas/onlava.task.graph.v1.schema.json",
		"docs/schemas/onlava.traces.clear.v1.schema.json",
		"docs/schemas/onlava.dev.event.v1.schema.json",
		"docs/schemas/onlava.logs.event.v1.schema.json",
		"docs/schemas/onlava.run.event.v1.schema.json",
		"docs/schemas/onlava.version.v1.schema.json",
		"docs/schemas/onlava.worker.manifest.v1.schema.json",
		"docs/schemas/onlava.worker.manifest.v2.schema.json",
		"docs/schemas/onlava.wire.capabilities.v1.schema.json",
	}
	return harnessKnowledge{
		Entrypoints: harnessKnowledgeFiles(repoRoot, entrypoints),
		Schemas:     harnessKnowledgeFiles(repoRoot, schemas),
	}
}

func buildHarnessSelfArtifacts(repoRoot string, selfWillExist bool, resp harnessSelfResponse) []harnessArtifact {
	artifacts := []harnessArtifact{
		{Name: "self-harness", Path: ".onlava/harness/self-latest.json", SchemaVersion: "onlava.harness.self.v1"},
		{Name: "toolchain", Path: ".onlava/harness/toolchain-latest.json", SchemaVersion: "onlava.harness.toolchain.v1"},
		{Name: "changed-area", Path: ".onlava/harness/changed-area-latest.json", SchemaVersion: "onlava.harness.changed_area.v1"},
		{Name: "drift", Path: ".onlava/harness/drift-latest.json", SchemaVersion: "onlava.harness.drift.v1"},
		{Name: "test-timing", Path: ".onlava/harness/test-timing-latest.json", SchemaVersion: "onlava.harness.test_timing.v1"},
		{Name: "fixture-matrix", Path: ".onlava/harness/fixture-matrix-latest.json", SchemaVersion: "onlava.harness.fixture_matrix.v1"},
		{Name: "schema-validation", Path: ".onlava/harness/schema-validation-latest.json", SchemaVersion: "onlava.harness.schema_validation.v1"},
		{Name: "agent-context", Path: ".onlava/harness/agent-context.json", SchemaVersion: "onlava.agent_context.v1"},
		{Name: "dashboard-ui", Path: "ui/dist/index.html"},
	}
	reportWillExist := map[string]bool{
		"self-harness":      selfWillExist,
		"toolchain":         selfWillExist && resp.Toolchain != nil,
		"changed-area":      selfWillExist && resp.ChangedArea != nil,
		"drift":             selfWillExist && resp.Drift != nil,
		"test-timing":       selfWillExist && resp.TestTiming != nil,
		"fixture-matrix":    selfWillExist && resp.FixtureMatrix != nil,
		"schema-validation": selfWillExist,
		"agent-context":     selfWillExist,
	}
	for i := range artifacts {
		if reportWillExist[artifacts[i].Name] {
			artifacts[i].Exists = true
			continue
		}
		_, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(artifacts[i].Path)))
		artifacts[i].Exists = err == nil
	}
	return artifacts
}

func writeHarnessSelfResult(path string, resp harnessSelfResponse) error {
	return writeHarnessJSONFile(path, resp)
}

func writeHarnessSelfOracleArtifacts(repoRoot string, resp harnessSelfResponse) error {
	harnessRoot := filepath.Join(repoRoot, ".onlava", "harness")
	if resp.Toolchain != nil {
		if err := writeHarnessJSONFile(filepath.Join(harnessRoot, "toolchain-latest.json"), resp.Toolchain); err != nil {
			return err
		}
	}
	if resp.ChangedArea != nil {
		if err := writeHarnessJSONFile(filepath.Join(harnessRoot, "changed-area-latest.json"), resp.ChangedArea); err != nil {
			return err
		}
	}
	if resp.Drift != nil {
		if err := writeHarnessJSONFile(filepath.Join(harnessRoot, "drift-latest.json"), resp.Drift); err != nil {
			return err
		}
	}
	if resp.TestTiming != nil {
		if err := writeHarnessJSONFile(filepath.Join(harnessRoot, "test-timing-latest.json"), resp.TestTiming); err != nil {
			return err
		}
	}
	if resp.FixtureMatrix != nil {
		if err := writeHarnessJSONFile(filepath.Join(harnessRoot, "fixture-matrix-latest.json"), resp.FixtureMatrix); err != nil {
			return err
		}
	}
	if resp.SchemaValidation != nil {
		if err := writeHarnessJSONFile(filepath.Join(harnessRoot, "schema-validation-latest.json"), resp.SchemaValidation); err != nil {
			return err
		}
	}
	contextPack := buildHarnessAgentContext(repoRoot, resp)
	if err := writeHarnessJSONFile(filepath.Join(harnessRoot, "agent-context.json"), contextPack); err != nil {
		return err
	}
	return nil
}

func writeHarnessJSONFile(path string, payload any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func writeHarnessSelfJSON(w io.Writer, payload harnessSelfResponse) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func writeHarnessSelfText(w io.Writer, resp harnessSelfResponse) error {
	status := "ok"
	if !resp.OK {
		status = "failed"
	}
	if _, err := fmt.Fprintf(w, "onlava: self harness %s\n", status); err != nil {
		return err
	}
	for _, step := range resp.Steps {
		marker := "ok"
		if !step.OK {
			marker = "failed"
		}
		if _, err := fmt.Fprintf(w, "  %s %-24s duration_ms=%d\n", marker, step.Name, step.DurationMS); err != nil {
			return err
		}
	}
	if resp.Wrote != "" {
		_, _ = fmt.Fprintf(w, "  wrote %s\n", resp.Wrote)
	}
	return nil
}

func installSuggestion(binary string) string {
	switch binary {
	case "bun":
		return "Install Bun or ensure it is available in PATH, then rerun `onlava harness self --json`."
	case "go":
		return "Install Go or ensure it is available in PATH, then rerun `onlava harness self --json`."
	default:
		return "Install `" + binary + "` or ensure it is available in PATH, then rerun `onlava harness self --json`."
	}
}

func rerunSuggestion(command []string, dir string) string {
	return "Run `" + strings.Join(command, " ") + "` in `" + dir + "`, fix the failure, then rerun `onlava harness self --json`."
}

func tailString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}
