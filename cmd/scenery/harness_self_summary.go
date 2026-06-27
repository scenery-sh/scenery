package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const harnessSelfSummarySchema = "scenery.harness.self.summary.v1"

type harnessSelfSummaryResponse struct {
	SchemaVersion     string                     `json:"schema_version"`
	OK                bool                       `json:"ok"`
	Status            string                     `json:"status"`
	GeneratedAt       string                     `json:"generated_at"`
	Mode              string                     `json:"mode"`
	Repo              harnessSelfSummaryRepo     `json:"repo"`
	CanProceed        bool                       `json:"can_proceed"`
	ChangedArea       harnessSelfSummaryChanges  `json:"changed_area"`
	DiagnosticSummary map[string]int             `json:"diagnostic_summary"`
	Attention         []harnessSelfAttentionItem `json:"attention,omitempty"`
	Steps             []harnessSelfSummaryStep   `json:"steps"`
	Reports           harnessSelfSummaryReports  `json:"reports"`
	Artifacts         []harnessArtifact          `json:"artifacts"`
	Drilldowns        []string                   `json:"drilldowns"`
	Wrote             string                     `json:"wrote,omitempty"`
}

type harnessSelfSummaryRepo struct {
	Root       string `json:"root"`
	ModulePath string `json:"module_path"`
	GoModPath  string `json:"go_mod_path"`
}

type harnessSelfSummaryChanges struct {
	ChangedFiles     []harnessChangedFile `json:"changed_files"`
	ChangedFileCount int                  `json:"changed_file_count"`
	IgnoredFiles     []harnessChangedFile `json:"ignored_files,omitempty"`
	IgnoredFileCount int                  `json:"ignored_file_count,omitempty"`
	AffectedPackages []string             `json:"affected_packages,omitempty"`
	RiskFlags        []string             `json:"risk_flags,omitempty"`
	Recommended      []string             `json:"recommended_commands,omitempty"`
	RelevantDocs     []string             `json:"relevant_docs,omitempty"`
	OmittedFileCount int                  `json:"omitted_file_count,omitempty"`
}

type harnessSelfAttentionItem struct {
	Severity     string   `json:"severity"`
	Category     string   `json:"category"`
	Message      string   `json:"message"`
	NextAction   string   `json:"next_action,omitempty"`
	TopEntries   []string `json:"top_entries,omitempty"`
	OmittedCount int      `json:"omitted_count,omitempty"`
	Artifact     string   `json:"artifact,omitempty"`
	Drilldown    string   `json:"drilldown,omitempty"`
}

type harnessSelfSummaryStep struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	DurationMS   int64             `json:"duration_ms"`
	ErrorCount   int               `json:"error_count"`
	WarningCount int               `json:"warning_count"`
	Summary      map[string]any    `json:"summary,omitempty"`
	Diagnostics  []checkDiagnostic `json:"diagnostics,omitempty"`
	OutputTail   string            `json:"output_tail,omitempty"`
	Artifacts    []harnessArtifact `json:"artifacts,omitempty"`
}

type harnessSelfSummaryReports struct {
	Drift            *harnessSelfDriftSummary            `json:"drift,omitempty"`
	TestTiming       *harnessSelfTestTimingSummary       `json:"test_timing,omitempty"`
	Knowledge        harnessSelfKnowledgeSummary         `json:"knowledge"`
	Architecture     harnessSelfArchitectureSummary      `json:"architecture"`
	SchemaValidation *harnessSelfSchemaValidationSummary `json:"schema_validation,omitempty"`
	FixtureMatrix    *harnessSelfFixtureMatrixSummary    `json:"fixture_matrix,omitempty"`
}

type harnessSelfDriftSummary struct {
	EnvVarCount     int    `json:"env_var_count"`
	Diagnostics     int    `json:"diagnostics"`
	CLICommandCount int    `json:"cli_command_count"`
	EmbedCount      int    `json:"embed_count"`
	Artifact        string `json:"artifact"`
}

type harnessSelfTestTimingSummary struct {
	TotalSeconds    intOrFloat             `json:"total_seconds"`
	BudgetSeconds   intOrFloat             `json:"budget_seconds"`
	PackageCount    int                    `json:"package_count"`
	SlowTestCount   int                    `json:"slow_test_count"`
	WarningCount    int                    `json:"warning_count"`
	TopSlowTests    []harnessTestTiming    `json:"top_slow_tests,omitempty"`
	TopSlowPackages []harnessPackageTiming `json:"top_slow_packages,omitempty"`
	Artifact        string                 `json:"artifact"`
}

// intOrFloat preserves compact numeric JSON while keeping the summary structs simple.
type intOrFloat float64

type harnessSelfKnowledgeSummary struct {
	EntrypointCount int      `json:"entrypoint_count"`
	SchemaCount     int      `json:"schema_count"`
	ReviewDueCount  int      `json:"review_due_count"`
	StaleCount      int      `json:"stale_count"`
	TopReviewDue    []string `json:"top_review_due,omitempty"`
	Drilldown       string   `json:"drilldown"`
}

type harnessSelfArchitectureSummary struct {
	BlockingCount           int      `json:"blocking_count"`
	WarningCount            int      `json:"warning_count"`
	ChangedAreaWarningCount int      `json:"changed_area_warning_count"`
	DebtWarningCount        int      `json:"debt_warning_count"`
	LargeFileCount          int      `json:"large_file_count"`
	TopChangedWarnings      []string `json:"top_changed_warnings,omitempty"`
	Artifact                string   `json:"artifact"`
}

type harnessSelfSchemaValidationSummary struct {
	PassCount int    `json:"pass_count"`
	FailCount int    `json:"fail_count"`
	Artifact  string `json:"artifact"`
}

type harnessSelfFixtureMatrixSummary struct {
	PassCount int    `json:"pass_count"`
	FailCount int    `json:"fail_count"`
	Artifact  string `json:"artifact"`
}

func buildHarnessSelfSummary(resp harnessSelfResponse) harnessSelfSummaryResponse {
	changedPaths := changedAreaPathSet(resp.ChangedArea)
	attention, architectureDebtWarnings, architectureChangedWarnings := buildHarnessSelfAttention(resp, changedPaths)
	status := classifyHarnessSelfSummaryStatus(resp.OK, attention, architectureDebtWarnings)
	return harnessSelfSummaryResponse{
		SchemaVersion:     harnessSelfSummarySchema,
		OK:                resp.OK,
		Status:            status,
		GeneratedAt:       resp.GeneratedAt,
		Mode:              resp.Mode,
		Repo:              summaryRepo(resp.Repo),
		CanProceed:        resp.OK,
		ChangedArea:       summarizeChangedArea(resp.ChangedArea),
		DiagnosticSummary: summarizeDiagnostics(resp.Steps),
		Attention:         attention,
		Steps:             summarizeHarnessSteps(resp.Repo.Root, resp.Steps),
		Reports:           summarizeHarnessReports(resp, changedPaths, architectureDebtWarnings, architectureChangedWarnings),
		Artifacts:         normalizeHarnessArtifacts(resp.Artifacts),
		Drilldowns: []string{
			"scenery inspect harness --json",
			"scenery inspect harness artifact test-timing --json",
			"scenery inspect harness artifact drift --json",
			"scenery inspect harness diagnostics --severity warning --json",
			"scenery inspect harness timing --top 10 --json",
		},
		Wrote: normalizeRepoPath(resp.Repo.Root, resp.Wrote),
	}
}

func classifyHarnessSelfSummaryStatus(ok bool, attention []harnessSelfAttentionItem, debtWarnings int) string {
	if !ok {
		return "fail"
	}
	if len(attention) > 0 {
		return "pass_with_warnings"
	}
	if debtWarnings > 0 {
		return "pass_with_debt"
	}
	return "pass"
}

func summaryRepo(repo harnessSelfRepo) harnessSelfSummaryRepo {
	return harnessSelfSummaryRepo{Root: "$REPO", ModulePath: repo.ModulePath, GoModPath: normalizeRepoPath(repo.Root, repo.GoModPath)}
}

func summarizeChangedArea(report *harnessChangedAreaReport) harnessSelfSummaryChanges {
	if report == nil {
		return harnessSelfSummaryChanges{}
	}
	changed := append([]harnessChangedFile{}, report.ChangedFiles...)
	omitted := 0
	if len(changed) > 12 {
		omitted = len(changed) - 12
		changed = changed[:12]
	}
	ignored := append([]harnessChangedFile{}, report.IgnoredFiles...)
	if len(ignored) > 20 {
		ignored = ignored[:20]
	}
	return harnessSelfSummaryChanges{
		ChangedFiles:     changed,
		ChangedFileCount: len(report.ChangedFiles),
		IgnoredFiles:     ignored,
		IgnoredFileCount: len(report.IgnoredFiles),
		AffectedPackages: capStrings(report.AffectedPackages, 20),
		RiskFlags:        capStrings(report.RiskFlags, 20),
		Recommended:      capStrings(report.RecommendedCommands, 20),
		RelevantDocs:     capStrings(report.RelevantDocs, 20),
		OmittedFileCount: omitted,
	}
}

func summarizeDiagnostics(steps []harnessStep) map[string]int {
	out := map[string]int{"error": 0, "warning": 0}
	for _, step := range steps {
		for _, diag := range step.Diagnostics {
			key := diag.Severity
			if key == "" {
				key = "unknown"
			}
			out[key]++
			if diag.Stage != "" {
				out[diag.Stage+"."+key]++
			}
		}
	}
	return out
}

func summarizeHarnessSteps(repoRoot string, steps []harnessStep) []harnessSelfSummaryStep {
	out := make([]harnessSelfSummaryStep, 0, len(steps))
	for _, step := range steps {
		errs, warns := countDiagnosticsBySeverity(step.Diagnostics)
		status := "pass"
		if !step.OK {
			status = "fail"
		} else if warns > 0 {
			status = "warning"
		}
		summary := harnessSelfSummaryStep{
			ID:           sanitizeHarnessArtifactName(step.Name),
			Name:         step.Name,
			Status:       status,
			DurationMS:   step.DurationMS,
			ErrorCount:   errs,
			WarningCount: warns,
			Summary:      compactStepSummary(repoRoot, step.Summary),
			Diagnostics:  failingDiagnostics(step),
			Artifacts:    artifactsFromStep(step),
		}
		if !step.OK && step.OutputTail != "" {
			summary.OutputTail = tailString(step.OutputTail, 2000)
		}
		out = append(out, summary)
	}
	return out
}

func compactStepSummary(repoRoot string, in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for key, value := range in {
		if !summaryScalar(value) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 8 {
		keys = keys[:8]
	}
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		value := in[key]
		switch key {
		case "cwd", "path", "binary_path":
			if text, ok := value.(string); ok {
				out[key] = normalizeRepoPath(repoRoot, text)
				continue
			}
		}
		out[key] = value
	}
	return out
}

func summaryScalar(value any) bool {
	switch value.(type) {
	case nil, string, bool, int, int64, float64, float32:
		return value != nil
	default:
		return false
	}
}

func failingDiagnostics(step harnessStep) []checkDiagnostic {
	if step.OK {
		return nil
	}
	return capDiagnostics(step.Diagnostics, 3, "")
}

func artifactsFromStep(step harnessStep) []harnessArtifact {
	if step.Evidence == nil || len(step.Evidence.Artifacts) == 0 {
		return nil
	}
	out := make([]harnessArtifact, 0, len(step.Evidence.Artifacts))
	for _, item := range step.Evidence.Artifacts {
		out = append(out, harnessArtifact{Name: item.Name, Path: item.Path, SchemaVersion: item.SchemaVersion, Exists: true})
	}
	return out
}

func summarizeHarnessReports(resp harnessSelfResponse, changedPaths map[string]bool, debtWarnings, changedWarnings int) harnessSelfSummaryReports {
	return harnessSelfSummaryReports{
		Drift:            summarizeDrift(resp.Drift),
		TestTiming:       summarizeTestTiming(resp.TestTiming),
		Knowledge:        summarizeKnowledge(resp),
		Architecture:     summarizeArchitecture(resp.Steps, changedPaths, debtWarnings, changedWarnings),
		SchemaValidation: summarizeSchemaValidation(resp.SchemaValidation),
		FixtureMatrix:    summarizeFixtureMatrix(resp.FixtureMatrix),
	}
}

func summarizeDrift(report *harnessDriftReport) *harnessSelfDriftSummary {
	if report == nil {
		return nil
	}
	return &harnessSelfDriftSummary{EnvVarCount: len(report.Env.Variables), Diagnostics: len(report.Diagnostics), CLICommandCount: len(report.CLI.Commands), EmbedCount: len(report.Embeds.Embeds), Artifact: ".scenery/harness/drift-latest.json"}
}

func summarizeTestTiming(report *harnessTestTimingReport) *harnessSelfTestTimingSummary {
	if report == nil {
		return nil
	}
	warnings := 0
	for _, diag := range report.Diagnostics {
		if diag.Severity == "warning" {
			warnings++
		}
	}
	packages := append([]harnessPackageTiming{}, report.Packages...)
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Seconds == packages[j].Seconds {
			return packages[i].Package < packages[j].Package
		}
		return packages[i].Seconds > packages[j].Seconds
	})
	return &harnessSelfTestTimingSummary{
		TotalSeconds:    intOrFloat(report.TotalSeconds),
		BudgetSeconds:   intOrFloat(report.Budgets.TotalSeconds),
		PackageCount:    len(report.Packages),
		SlowTestCount:   len(report.SlowTests),
		WarningCount:    warnings,
		TopSlowTests:    capTests(report.SlowTests, 5),
		TopSlowPackages: capPackages(packages, 5),
		Artifact:        ".scenery/harness/test-timing-latest.json",
	}
}

func summarizeKnowledge(resp harnessSelfResponse) harnessSelfKnowledgeSummary {
	out := harnessSelfKnowledgeSummary{EntrypointCount: len(resp.Knowledge.Entrypoints), SchemaCount: len(resp.Knowledge.Schemas), Drilldown: "scenery inspect docs --json"}
	for _, step := range resp.Steps {
		if step.Name != "inspect docs" || step.Summary == nil {
			continue
		}
		out.ReviewDueCount = anyInt(step.Summary["review_due_count"])
		out.StaleCount = anyInt(step.Summary["stale_count"])
		break
	}
	return out
}

func summarizeArchitecture(steps []harnessStep, changedPaths map[string]bool, debtWarnings, changedWarnings int) harnessSelfArchitectureSummary {
	out := harnessSelfArchitectureSummary{DebtWarningCount: debtWarnings, ChangedAreaWarningCount: changedWarnings, Artifact: ".scenery/harness/self-latest.json"}
	for _, step := range steps {
		if step.Name != "architecture checks" {
			continue
		}
		out.BlockingCount, out.WarningCount = countDiagnosticsBySeverity(step.Diagnostics)
		out.LargeFileCount = anyInt(step.Summary["large_files"])
		out.TopChangedWarnings = topChangedDiagnosticEntries(step.Diagnostics, changedPaths, 5)
		break
	}
	return out
}

func summarizeSchemaValidation(report *harnessSchemaValidationReport) *harnessSelfSchemaValidationSummary {
	if report == nil {
		return nil
	}
	out := &harnessSelfSchemaValidationSummary{Artifact: ".scenery/harness/schema-validation-latest.json"}
	for _, item := range report.Validated {
		if item.OK {
			out.PassCount++
		} else {
			out.FailCount++
		}
	}
	return out
}

func summarizeFixtureMatrix(report *harnessFixtureMatrixReport) *harnessSelfFixtureMatrixSummary {
	if report == nil {
		return nil
	}
	out := &harnessSelfFixtureMatrixSummary{Artifact: ".scenery/harness/fixture-matrix-latest.json"}
	for _, item := range report.Fixtures {
		if len(item.Diagnostics) == 0 {
			out.PassCount++
		} else {
			out.FailCount++
		}
	}
	return out
}

func buildHarnessSelfAttention(resp harnessSelfResponse, changedPaths map[string]bool) ([]harnessSelfAttentionItem, int, int) {
	var items []harnessSelfAttentionItem
	architectureDebtWarnings := 0
	architectureChangedWarnings := 0
	for _, step := range resp.Steps {
		if !step.OK {
			items = append(items, harnessSelfAttentionItem{Severity: "error", Category: sanitizeHarnessArtifactName(step.Name), Message: firstNonEmpty(step.Error, step.Name+" failed"), NextAction: firstDiagnosticAction(step.Diagnostics), TopEntries: topDiagnosticEntries(step.Diagnostics, "error", 3), Artifact: artifactForStepName(step.Name), Drilldown: drilldownForStepName(step.Name)})
			continue
		}
		if step.Name == "architecture checks" {
			for _, diag := range step.Diagnostics {
				if diag.Severity != "warning" {
					continue
				}
				if diagnosticInChangedArea(diag, changedPaths) {
					architectureChangedWarnings++
				} else {
					architectureDebtWarnings++
				}
			}
			if architectureChangedWarnings > 0 {
				items = append(items, harnessSelfAttentionItem{Severity: "warning", Category: "architecture", Message: fmt.Sprintf("%d architecture warnings intersect changed files", architectureChangedWarnings), NextAction: "Fix or split the changed architecture hotspot before expanding it.", TopEntries: topChangedDiagnosticEntries(step.Diagnostics, changedPaths, 5), Artifact: ".scenery/harness/self-latest.json", Drilldown: "scenery inspect harness diagnostics --severity warning --json"})
			}
			continue
		}
		warnings := topDiagnosticEntries(step.Diagnostics, "warning", 3)
		if len(warnings) > 0 {
			items = append(items, harnessSelfAttentionItem{Severity: "warning", Category: sanitizeHarnessArtifactName(step.Name), Message: fmt.Sprintf("%s reported %d warning(s)", step.Name, countSeverity(step.Diagnostics, "warning")), NextAction: firstDiagnosticAction(step.Diagnostics), TopEntries: warnings, Artifact: artifactForStepName(step.Name), Drilldown: drilldownForStepName(step.Name)})
		}
	}
	if len(items) > 10 {
		omitted := len(items) - 10
		items = items[:10]
		items[len(items)-1].OmittedCount += omitted
	}
	return items, architectureDebtWarnings, architectureChangedWarnings
}

func changedAreaPathSet(report *harnessChangedAreaReport) map[string]bool {
	set := map[string]bool{}
	if report == nil {
		return set
	}
	for _, file := range report.ChangedFiles {
		set[filepath.ToSlash(file.Path)] = true
	}
	return set
}

func diagnosticInChangedArea(diag checkDiagnostic, changed map[string]bool) bool {
	if len(changed) == 0 || diag.File == "" {
		return false
	}
	file := summaryDiagnosticFile(diag.File)
	return changed[file]
}

func summaryDiagnosticFile(path string) string {
	path = filepath.ToSlash(path)
	if idx := strings.Index(path, "/cmd/"); idx >= 0 {
		return strings.TrimPrefix(path[idx+1:], "/")
	}
	for _, prefix := range []string{"cmd/", "internal/", "runtime/", "auth/", "docs/", "ui/", "testdata/", "scripts/"} {
		if strings.HasPrefix(path, prefix) {
			return path
		}
	}
	return strings.TrimPrefix(path, "$REPO/")
}

func topDiagnosticEntries(diags []checkDiagnostic, severity string, limit int) []string {
	var out []string
	for _, diag := range diags {
		if severity != "" && diag.Severity != severity {
			continue
		}
		out = append(out, diagnosticEntry(diag))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func topChangedDiagnosticEntries(diags []checkDiagnostic, changed map[string]bool, limit int) []string {
	var out []string
	for _, diag := range diags {
		if diag.Severity != "warning" || !diagnosticInChangedArea(diag, changed) {
			continue
		}
		out = append(out, diagnosticEntry(diag))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func diagnosticEntry(diag checkDiagnostic) string {
	file := normalizeLikelyPath(diag.File)
	if diag.Line > 0 {
		file = fmt.Sprintf("%s:%d", file, diag.Line)
	}
	if file != "" {
		return file + ": " + diag.Message
	}
	return diag.Message
}

func firstDiagnosticAction(diags []checkDiagnostic) string {
	for _, diag := range diags {
		if diag.SuggestedAction != "" {
			return diag.SuggestedAction
		}
	}
	return ""
}

func countSeverity(diags []checkDiagnostic, severity string) int {
	count := 0
	for _, diag := range diags {
		if diag.Severity == severity {
			count++
		}
	}
	return count
}

func capDiagnostics(diags []checkDiagnostic, limit int, severity string) []checkDiagnostic {
	if len(diags) == 0 || limit <= 0 {
		return nil
	}
	out := make([]checkDiagnostic, 0, limit)
	for _, diag := range diags {
		if severity != "" && diag.Severity != severity {
			continue
		}
		diag.File = normalizeLikelyPath(diag.File)
		out = append(out, diag)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func artifactForStepName(name string) string {
	switch name {
	case "go tests":
		return ".scenery/harness/test-timing-latest.json"
	case "contract drift checks":
		return ".scenery/harness/drift-latest.json"
	case "fixture matrix":
		return ".scenery/harness/fixture-matrix-latest.json"
	case "schema validation":
		return ".scenery/harness/schema-validation-latest.json"
	case "changed area oracle":
		return ".scenery/harness/changed-area-latest.json"
	case "toolchain preflight":
		return ".scenery/harness/toolchain-latest.json"
	default:
		return ".scenery/harness/self-latest.json"
	}
}

func drilldownForStepName(name string) string {
	switch name {
	case "go tests":
		return "scenery inspect harness timing --top 10 --json"
	case "contract drift checks":
		return "scenery inspect harness artifact drift --json"
	default:
		return "scenery inspect harness diagnostics --severity warning --json"
	}
}

func normalizeHarnessArtifacts(items []harnessArtifact) []harnessArtifact {
	out := make([]harnessArtifact, 0, len(items))
	for _, item := range items {
		item.Path = normalizeLikelyPath(item.Path)
		out = append(out, item)
	}
	return out
}

func normalizeRepoPath(repoRoot, path string) string {
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(path)
	repoRoot = filepath.ToSlash(repoRoot)
	if repoRoot != "" && strings.HasPrefix(path, repoRoot+"/") {
		return strings.TrimPrefix(path, repoRoot+"/")
	}
	if path == repoRoot {
		return "$REPO"
	}
	return path
}

func normalizeLikelyPath(path string) string {
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(path)
	if idx := strings.Index(path, "/.scenery/"); idx >= 0 {
		return ".scenery/" + strings.TrimPrefix(path[idx+len("/.scenery/"):], "/")
	}
	for _, marker := range []string{"/cmd/", "/internal/", "/runtime/", "/auth/", "/docs/", "/ui/", "/testdata/", "/scripts/"} {
		if idx := strings.Index(path, marker); idx >= 0 {
			return strings.TrimPrefix(path[idx+1:], "/")
		}
	}
	return path
}

func anyInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return 0
	}
}

func capStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return append([]string{}, values...)
	}
	return append([]string{}, values[:limit]...)
}

func capTests(values []harnessTestTiming, limit int) []harnessTestTiming {
	if len(values) <= limit {
		return append([]harnessTestTiming{}, values...)
	}
	return append([]harnessTestTiming{}, values[:limit]...)
}

func capPackages(values []harnessPackageTiming, limit int) []harnessPackageTiming {
	out := append([]harnessPackageTiming{}, values...)
	if len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		out[i].Tests = nil
	}
	return out
}
