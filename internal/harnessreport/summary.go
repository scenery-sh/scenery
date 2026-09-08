package harnessreport

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const SummaryKind = "scenery.harness.self.summary"

func BuildSummary(resp SelfResponse) SelfSummaryResponse {
	changedPaths := changedAreaPathSet(resp.ChangedArea)
	attention, architectureDebtWarnings, architectureChangedWarnings := buildHarnessSelfAttention(resp, changedPaths)
	status := classifyHarnessSelfSummaryStatus(resp.OK, attention, architectureDebtWarnings)
	return SelfSummaryResponse{
		PayloadIdentity:   newCLIPayloadIdentity(SummaryKind),
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
			"scenery inspect harness -o json",
			"scenery inspect harness artifact test-timing -o json",
			"scenery inspect harness artifact drift -o json",
			"scenery inspect harness diagnostics --severity warning -o json",
			"scenery inspect harness timing --top 10 -o json",
		},
		Wrote: normalizeRepoPath(resp.Repo.Root, resp.Wrote),
	}
}

func classifyHarnessSelfSummaryStatus(ok bool, attention []SelfAttentionItem, debtWarnings int) string {
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

func summaryRepo(repo SelfRepo) SelfSummaryRepo {
	return SelfSummaryRepo{Root: "$REPO", ModulePath: repo.ModulePath, GoModPath: normalizeRepoPath(repo.Root, repo.GoModPath)}
}

func summarizeChangedArea(report *ChangedAreaReport) SelfSummaryChanges {
	if report == nil {
		return SelfSummaryChanges{}
	}
	changed := append([]ChangedFile{}, report.ChangedFiles...)
	omitted := 0
	if len(changed) > 12 {
		omitted = len(changed) - 12
		changed = changed[:12]
	}
	ignored := append([]ChangedFile{}, report.IgnoredFiles...)
	if len(ignored) > 20 {
		ignored = ignored[:20]
	}
	return SelfSummaryChanges{
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

func summarizeDiagnostics(steps []Step) map[string]int {
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

func summarizeHarnessSteps(repoRoot string, steps []Step) []SelfSummaryStep {
	out := make([]SelfSummaryStep, 0, len(steps))
	for _, step := range steps {
		errs, warns := CountDiagnostics(step.Diagnostics)
		status := "pass"
		if !step.OK {
			status = "fail"
		} else if warns > 0 {
			status = "warning"
		}
		summary := SelfSummaryStep{
			ID:           ArtifactName(step.Name),
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
			summary.OutputTail = TailString(step.OutputTail, 2000)
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

func failingDiagnostics(step Step) []Diagnostic {
	if step.OK {
		return nil
	}
	return CapDiagnostics(step.Diagnostics, 3)
}

func artifactsFromStep(step Step) []Artifact {
	if step.Evidence == nil || len(step.Evidence.Artifacts) == 0 {
		return nil
	}
	out := make([]Artifact, 0, len(step.Evidence.Artifacts))
	for _, item := range step.Evidence.Artifacts {
		out = append(out, Artifact{Name: item.Name, Path: item.Path, Kind: item.Kind, SchemaRevision: item.SchemaRevision, Exists: true})
	}
	return out
}

func summarizeHarnessReports(resp SelfResponse, changedPaths map[string]bool, debtWarnings, changedWarnings int) SelfSummaryReports {
	return SelfSummaryReports{
		Drift:            summarizeDrift(resp.Drift),
		TestTiming:       summarizeTestTiming(resp.TestTiming),
		Knowledge:        summarizeKnowledge(resp),
		Architecture:     summarizeArchitecture(resp.Steps, changedPaths, debtWarnings, changedWarnings),
		SchemaValidation: summarizeSchemaValidation(resp.SchemaValidation),
		FixtureMatrix:    summarizeFixtureMatrix(resp.FixtureMatrix),
	}
}

func summarizeDrift(report *DriftReport) *SelfDriftSummary {
	if report == nil {
		return nil
	}
	return &SelfDriftSummary{EnvVarCount: len(report.Env.Variables), Diagnostics: len(report.Diagnostics), CLICommandCount: len(report.CLI.Commands), EmbedCount: len(report.Embeds.Embeds), Artifact: ".scenery/harness/drift-latest.json"}
}

func summarizeTestTiming(report *TestTimingReport) *SelfTestTimingSummary {
	if report == nil {
		return nil
	}
	warnings := 0
	for _, diag := range report.Diagnostics {
		if diag.Severity == "warning" {
			warnings++
		}
	}
	packages := append([]PackageTiming{}, report.Packages...)
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Seconds == packages[j].Seconds {
			return packages[i].Package < packages[j].Package
		}
		return packages[i].Seconds > packages[j].Seconds
	})
	summary := &SelfTestTimingSummary{
		Lane:                  report.Budgets.Lane,
		TotalSeconds:          IntOrFloat(report.TotalSeconds),
		ConfirmationSeconds:   IntOrFloat(report.ConfirmationSeconds),
		BudgetSeconds:         IntOrFloat(report.Budgets.TotalSeconds),
		TargetSeconds:         IntOrFloat(report.Budgets.TargetSeconds),
		PackageCount:          len(report.Packages),
		ObservedSlowTestCount: len(report.ObservedSlowTests),
		SlowTestCount:         len(report.SlowTests),
		WarningCount:          warnings,
		TopSlowTests:          CapTests(report.SlowTests, 5),
		TopSlowPackages:       CapPackages(packages, 5),
		Artifact:              ".scenery/harness/test-timing-latest.json",
	}
	if report.TestBinaries != nil {
		summary.TestPackageCount = report.TestBinaries.TestPackageCount
		summary.BuiltCount = report.TestBinaries.BuiltCount
	}
	return summary
}

func summarizeKnowledge(resp SelfResponse) SelfKnowledgeSummary {
	out := SelfKnowledgeSummary{EntrypointCount: len(resp.Knowledge.Entrypoints), SchemaCount: len(resp.Knowledge.Schemas), Drilldown: "scenery inspect docs --all -o json"}
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

func summarizeArchitecture(steps []Step, changedPaths map[string]bool, debtWarnings, changedWarnings int) SelfArchitectureSummary {
	out := SelfArchitectureSummary{DebtWarningCount: debtWarnings, ChangedAreaWarningCount: changedWarnings, Artifact: ".scenery/harness/self-latest.json"}
	for _, step := range steps {
		if step.Name != "architecture checks" {
			continue
		}
		out.BlockingCount, out.WarningCount = CountDiagnostics(step.Diagnostics)
		out.LargeFileCount = anyInt(step.Summary["large_files"])
		out.TopChangedWarnings = topChangedDiagnosticEntries(step.Diagnostics, changedPaths, 5)
		break
	}
	return out
}

func summarizeSchemaValidation(report *SchemaValidationReport) *SelfSchemaValidationSummary {
	if report == nil {
		return nil
	}
	out := &SelfSchemaValidationSummary{Artifact: ".scenery/harness/schema-validation-latest.json"}
	for _, item := range report.Validated {
		if item.OK {
			out.PassCount++
		} else {
			out.FailCount++
		}
	}
	return out
}

func summarizeFixtureMatrix(report *FixtureMatrixReport) *SelfFixtureMatrixSummary {
	if report == nil {
		return nil
	}
	out := &SelfFixtureMatrixSummary{Artifact: ".scenery/harness/fixture-matrix-latest.json"}
	for _, item := range report.Fixtures {
		if len(item.Diagnostics) == 0 {
			out.PassCount++
		} else {
			out.FailCount++
		}
	}
	return out
}

func buildHarnessSelfAttention(resp SelfResponse, changedPaths map[string]bool) ([]SelfAttentionItem, int, int) {
	var items []SelfAttentionItem
	architectureDebtWarnings := 0
	architectureChangedWarnings := 0
	for _, step := range resp.Steps {
		if !step.OK {
			items = append(items, SelfAttentionItem{Severity: "error", Category: ArtifactName(step.Name), Message: firstNonEmpty(step.Error, step.Name+" failed"), NextAction: firstDiagnosticAction(step.Diagnostics), TopEntries: topDiagnosticEntries(step.Diagnostics, "error", 3), Artifact: artifactForStepName(step.Name), Drilldown: drilldownForStepName(step.Name)})
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
				items = append(items, SelfAttentionItem{Severity: "warning", Category: "architecture", Message: fmt.Sprintf("%d architecture warnings intersect changed files", architectureChangedWarnings), NextAction: "Fix or split the changed architecture hotspot before expanding it.", TopEntries: topChangedDiagnosticEntries(step.Diagnostics, changedPaths, 5), Artifact: ".scenery/harness/self-latest.json", Drilldown: "scenery inspect harness diagnostics --severity warning -o json"})
			}
			continue
		}
		warnings := topDiagnosticEntries(step.Diagnostics, "warning", 3)
		if len(warnings) > 0 {
			items = append(items, SelfAttentionItem{Severity: "warning", Category: ArtifactName(step.Name), Message: fmt.Sprintf("%s reported %d warning(s)", step.Name, countSeverity(step.Diagnostics, "warning")), NextAction: firstDiagnosticAction(step.Diagnostics), TopEntries: warnings, Artifact: artifactForStepName(step.Name), Drilldown: drilldownForStepName(step.Name)})
		}
	}
	if len(items) > 10 {
		omitted := len(items) - 10
		items = items[:10]
		items[len(items)-1].OmittedCount += omitted
	}
	return items, architectureDebtWarnings, architectureChangedWarnings
}

func changedAreaPathSet(report *ChangedAreaReport) map[string]bool {
	set := map[string]bool{}
	if report == nil {
		return set
	}
	for _, file := range report.ChangedFiles {
		set[filepath.ToSlash(file.Path)] = true
	}
	return set
}

func diagnosticInChangedArea(diag Diagnostic, changed map[string]bool) bool {
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
	for _, prefix := range []string{"cmd/", "internal/", "runtime/", "auth/", "docs/", "ui/", "apps/console/", "testdata/", "scripts/"} {
		if strings.HasPrefix(path, prefix) {
			return path
		}
	}
	return strings.TrimPrefix(path, "$REPO/")
}

func topDiagnosticEntries(diags []Diagnostic, severity string, limit int) []string {
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

func topChangedDiagnosticEntries(diags []Diagnostic, changed map[string]bool, limit int) []string {
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

func diagnosticEntry(diag Diagnostic) string {
	file := NormalizeLikelyPath(diag.File)
	if diag.Line > 0 {
		file = fmt.Sprintf("%s:%d", file, diag.Line)
	}
	if file != "" {
		return file + ": " + diag.Message
	}
	return diag.Message
}

func firstDiagnosticAction(diags []Diagnostic) string {
	for _, diag := range diags {
		if diag.SuggestedAction != "" {
			return diag.SuggestedAction
		}
	}
	return ""
}

func countSeverity(diags []Diagnostic, severity string) int {
	count := 0
	for _, diag := range diags {
		if diag.Severity == severity {
			count++
		}
	}
	return count
}

func CapDiagnostics(diags []Diagnostic, limit int) []Diagnostic {
	if len(diags) == 0 || limit <= 0 {
		return nil
	}
	out := make([]Diagnostic, 0, limit)
	for _, diag := range diags {
		diag.File = NormalizeLikelyPath(diag.File)
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
		return "scenery inspect harness timing --top 10 -o json"
	case "contract drift checks":
		return "scenery inspect harness artifact drift -o json"
	default:
		return "scenery inspect harness diagnostics --severity warning -o json"
	}
}

func normalizeHarnessArtifacts(items []Artifact) []Artifact {
	out := make([]Artifact, 0, len(items))
	for _, item := range items {
		item.Path = NormalizeLikelyPath(item.Path)
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

func NormalizeLikelyPath(path string) string {
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(path)
	if idx := strings.Index(path, "/.scenery/"); idx >= 0 {
		return ".scenery/" + strings.TrimPrefix(path[idx+len("/.scenery/"):], "/")
	}
	for _, marker := range []string{"/cmd/", "/internal/", "/runtime/", "/auth/", "/docs/", "/ui/", "/apps/console/", "/testdata/", "/scripts/"} {
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

func CapTests(values []TestTiming, limit int) []TestTiming {
	if len(values) <= limit {
		return append([]TestTiming{}, values...)
	}
	return append([]TestTiming{}, values[:limit]...)
}

func CapPackages(values []PackageTiming, limit int) []PackageTiming {
	out := append([]PackageTiming{}, values...)
	if len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		out[i].Tests = nil
	}
	return out
}
