package main

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
)

func buildHarnessAgentContext(repoRoot string, resp harnessSelfResponse) harnessAgentContext {
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
	failingSteps := buildHarnessAgentFailingSteps(repoRoot, resp.Steps)
	rerunCommands := harnessAgentRerunCommands(failingSteps)
	recentFailures := make([]string, 0, len(failingSteps))
	for _, step := range failingSteps {
		recentFailures = append(recentFailures, step.Name)
	}
	docsFreshness := buildHarnessAgentDocsFreshness(repoRoot)
	riskClassification := classifyHarnessAgentRisk(resp.ChangedArea)
	relevantPlans := buildHarnessAgentRelevantExecPlans(repoRoot, resp.ChangedArea)
	failedArtifacts := buildHarnessAgentFailedArtifacts(repoRoot, failingSteps)
	changedAreaCommands := []string{}
	if resp.ChangedArea != nil {
		changedAreaCommands = append(changedAreaCommands, resp.ChangedArea.RecommendedCommands...)
	}
	contextPack := harnessAgentContext{
		SchemaVersion: harnessAgentContextSchema,
		GeneratedAt:   resp.GeneratedAt,
		Repo: harnessAgentContextRepo{
			Root:       resp.Repo.Root,
			ModulePath: resp.Repo.ModulePath,
			GoModPath:  resp.Repo.GoModPath,
		},
		CurrentBranch:                  strings.TrimSpace(branch),
		CurrentCommit:                  strings.TrimSpace(commit),
		DirtyFiles:                     []harnessChangedFile{},
		ChangedArea:                    resp.ChangedArea,
		FailingSteps:                   failingSteps,
		RerunCommands:                  rerunCommands,
		ChangedAreaRecommendedCommands: changedAreaCommands,
		RecommendedCommands:            append([]string{}, resp.NextActions...),
		RelevantActiveExecPlans:        relevantPlans,
		RecentFailedHarnessArtifacts:   failedArtifacts,
		DocsFreshness:                  docsFreshness,
		RiskClassification:             riskClassification,
		DocsEntrypoints:                entrypoints,
		Schemas:                        schemas,
		KnownFastLoop:                  "scenery doctor --json\nscenery harness self --quick --summary --write\ncat .scenery/harness/agent-context.json\n# implement\nscenery harness self --summary --write",
		KnownReleaseLoop:               "scenery harness self --release --summary --write\nscripts/release-gate.sh",
		ArchitectureRules: []string{
			"Prefer Go standard library dependencies unless the payoff is concrete.",
			"Do not add legacy aliases or backwards-compatibility shims for renamed scenery APIs.",
			"Do not write the shared `scenery` binary with `go install ./cmd/scenery` unless a human explicitly asks; use self-harness' worktree-local `.scenery/harness/bin/scenery` build instead.",
			"For substantial repository changes, run scenery harness self --summary --write when practical.",
		},
		RecentFailures: recentFailures,
	}
	if resp.ChangedArea != nil {
		contextPack.DirtyFiles = resp.ChangedArea.ChangedFiles
		contextPack.RecommendedCommands = appendUniqueSorted(contextPack.RecommendedCommands, resp.ChangedArea.RecommendedCommands...)
	}
	contextPack.RecommendedCommands = appendUniqueSorted(contextPack.RecommendedCommands, contextPack.RerunCommands...)
	if len(contextPack.RecommendedCommands) == 0 {
		contextPack.RecommendedCommands = []string{"scenery doctor --json", "scenery harness self --quick --summary --write", "scenery harness self --summary --write"}
	}
	sort.Strings(contextPack.DocsEntrypoints)
	sort.Strings(contextPack.Schemas)
	sort.Strings(contextPack.RecentFailures)
	sort.Strings(contextPack.ChangedAreaRecommendedCommands)
	sort.Strings(contextPack.RerunCommands)
	sort.Strings(contextPack.RiskClassification)
	return contextPack
}

func buildHarnessAgentFailingSteps(repoRoot string, steps []harnessStep) []harnessAgentFailingStep {
	failures := []harnessAgentFailingStep{}
	for _, step := range steps {
		if step.OK {
			continue
		}
		item := harnessAgentFailingStep{
			Name:            step.Name,
			Error:           firstNonEmpty(step.Error, strings.TrimSpace(step.OutputTail)),
			FirstFileToRead: firstFileForHarnessStep(repoRoot, step),
			RerunCommand:    harnessStepRerunCommand(repoRoot, step),
			Diagnostics:     step.Diagnostics,
		}
		if step.Evidence != nil {
			item.Artifacts = append(item.Artifacts, step.Evidence.Artifacts...)
		}
		failures = append(failures, item)
	}
	sort.Slice(failures, func(i, j int) bool {
		return failures[i].Name < failures[j].Name
	})
	return failures
}

func harnessAgentRerunCommands(steps []harnessAgentFailingStep) []string {
	set := map[string]bool{}
	for _, step := range steps {
		if step.RerunCommand != "" {
			set[step.RerunCommand] = true
		}
	}
	return sortedStringSet(set)
}

func firstFileForHarnessStep(repoRoot string, step harnessStep) string {
	for _, diag := range step.Diagnostics {
		if strings.TrimSpace(diag.File) == "" {
			continue
		}
		return harnessRelPath(repoRoot, diag.File)
	}
	switch step.Name {
	case "toolchain preflight":
		return "docs/harness-engineering.md"
	case "knowledge contract", "inspect docs":
		return "docs/knowledge.json"
	case "changed area oracle":
		return ".scenery/harness/changed-area-latest.json"
	case "architecture checks":
		return "docs/harness-engineering.md"
	case "contract drift checks":
		return "docs/local-contract.md"
	case "ui static architecture":
		return "docs/ui-agent-contract.md"
	case "go tests":
		return ".scenery/harness/test-timing-latest.json"
	case "fixture matrix":
		return ".scenery/harness/fixture-matrix-latest.json"
	case "schema validation":
		return ".scenery/harness/schema-validation-latest.json"
	case "dashboard ui typecheck", "dashboard ui build", "dashboard ui fresh":
		return "ui"
	case "parallel worktree runtimes":
		return ".scenery/harness/agent-context.json"
	default:
		if step.Evidence != nil && len(step.Evidence.Artifacts) > 0 {
			return step.Evidence.Artifacts[0].Path
		}
		return ""
	}
}

func harnessStepRerunCommand(repoRoot string, step harnessStep) string {
	if step.Evidence != nil && strings.TrimSpace(step.Evidence.ReproCommand) != "" {
		return step.Evidence.ReproCommand
	}
	if len(step.Command) > 0 {
		return reproCommand(step.Command, firstNonEmpty(harnessStepEvidenceCWD(step), repoRoot))
	}
	return ""
}

func harnessRelPath(repoRoot, path string) string {
	path = filepath.Clean(path)
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(repoRoot, path); err == nil && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}

func buildHarnessAgentDocsFreshness(repoRoot string) harnessAgentDocsFreshness {
	resp, err := buildInspectDocsResponse(repoRoot)
	if err != nil {
		return harnessAgentDocsFreshness{
			SchemaVersion: inspectDocsSchema,
			Error:         err.Error(),
		}
	}
	state := harnessAgentDocsFreshness{
		SchemaVersion:  resp.SchemaVersion,
		DocumentCount:  resp.Summary.DocumentCount,
		MissingCount:   resp.Summary.MissingCount,
		ReviewDueCount: resp.Summary.ReviewDueCount,
		StaleCount:     resp.Summary.StaleCount,
	}
	for _, doc := range resp.Documents {
		if !doc.Exists {
			state.MissingDocuments = append(state.MissingDocuments, doc.Path)
		}
		if doc.ReviewDue {
			state.ReviewDueDocs = append(state.ReviewDueDocs, doc.Path)
		}
		if doc.Stale {
			state.StaleDocs = append(state.StaleDocs, doc.Path)
		}
	}
	sort.Strings(state.MissingDocuments)
	sort.Strings(state.ReviewDueDocs)
	sort.Strings(state.StaleDocs)
	return state
}

func buildHarnessAgentRelevantExecPlans(repoRoot string, changedArea *harnessChangedAreaReport) []harnessAgentExecPlan {
	if changedArea == nil {
		return []harnessAgentExecPlan{}
	}
	relevant := map[string]string{}
	for _, path := range changedArea.RelevantDocs {
		if strings.HasPrefix(path, "docs/plans/") && strings.HasSuffix(path, ".md") {
			relevant[path] = "changed-area relevant doc"
		}
	}
	for _, file := range changedArea.ChangedFiles {
		if strings.HasPrefix(file.Path, "docs/plans/") && strings.HasSuffix(file.Path, ".md") {
			relevant[file.Path] = "changed ExecPlan"
		}
	}
	if len(relevant) == 0 {
		return []harnessAgentExecPlan{}
	}
	index, err := readDocsKnowledgeIndex(repoRoot)
	if err != nil {
		return []harnessAgentExecPlan{}
	}
	plans := []harnessAgentExecPlan{}
	for _, doc := range index.Documents {
		reason, ok := relevant[doc.Path]
		if !ok || doc.Status != "active" {
			continue
		}
		plans = append(plans, harnessAgentExecPlan{
			Path:    doc.Path,
			Title:   doc.Title,
			Owner:   doc.Owner,
			Summary: doc.Summary,
			Reason:  reason,
		})
	}
	sort.Slice(plans, func(i, j int) bool {
		return plans[i].Path < plans[j].Path
	})
	return plans
}

func buildHarnessAgentFailedArtifacts(repoRoot string, failures []harnessAgentFailingStep) []harnessAgentFailedArtifact {
	items := map[string]harnessAgentFailedArtifact{}
	add := func(stepName, rerun string, artifacts []harnessEvidenceArtifact) {
		for _, artifact := range artifacts {
			if artifact.Path == "" {
				continue
			}
			item := harnessAgentFailedArtifact{
				Step:          stepName,
				Name:          artifact.Name,
				Path:          artifact.Path,
				SchemaVersion: artifact.SchemaVersion,
				RerunCommand:  rerun,
			}
			key := item.Name + "\x00" + item.Path
			if existing, ok := items[key]; ok {
				if existing.Step == "" {
					existing.Step = item.Step
				}
				if existing.SchemaVersion == "" {
					existing.SchemaVersion = item.SchemaVersion
				}
				if existing.RerunCommand == "" {
					existing.RerunCommand = item.RerunCommand
				}
				items[key] = existing
				continue
			}
			items[key] = item
		}
	}
	for _, failure := range failures {
		add(failure.Name, failure.RerunCommand, failure.Artifacts)
	}
	if inspectResp, err := buildInspectHarnessResponse(inspectOptions{Subject: "harness", RepoRoot: repoRoot}); err == nil {
		for _, evidence := range inspectResp.Evidence {
			if evidence.ExitCode == nil || *evidence.ExitCode == 0 {
				continue
			}
			stepName := firstNonEmpty(harnessEvidenceCommandName(evidence), "previous harness failure")
			add(stepName, evidence.ReproCommand, evidence.Artifacts)
		}
	}
	out := make([]harnessAgentFailedArtifact, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Step == out[j].Step {
			return out[i].Path < out[j].Path
		}
		return out[i].Step < out[j].Step
	})
	return out
}

func harnessEvidenceCommandName(evidence harnessEvidence) string {
	if len(evidence.Command) == 0 {
		return ""
	}
	return strings.Join(evidence.Command, " ")
}

func classifyHarnessAgentRisk(changedArea *harnessChangedAreaReport) []string {
	if changedArea == nil {
		return nil
	}
	classes := map[string]bool{}
	for _, file := range changedArea.ChangedFiles {
		switch file.Category {
		case "runtime", "internal", "dependency":
			classes["runtime"] = true
		case "cli":
			classes["CLI contract"] = true
		case "ui":
			classes["dashboard"] = true
		case "schema":
			classes["schema"] = true
		case "script":
			classes["release"] = true
		}
		if strings.HasPrefix(file.Path, "cmd/scenery/harness") || strings.HasPrefix(file.Path, "cmd/scenery/inspect") || strings.HasPrefix(file.Path, "docs/local-contract.md") {
			classes["CLI contract"] = true
		}
		if strings.HasPrefix(file.Path, "docs/schemas/") || file.Path == "docs/knowledge.json" {
			classes["schema"] = true
		}
		if harnessOnlvImpactingPath(file.Path) {
			classes["onlv-impacting"] = true
		}
		if file.Path == "go.mod" || file.Path == "go.sum" || strings.HasPrefix(file.Path, "scripts/") {
			classes["release"] = true
		}
	}
	for _, flag := range changedArea.RiskFlags {
		switch flag {
		case "runtime-contract", "build-cache", "dependency-graph":
			classes["runtime"] = true
		case "cli-contract", "harness-contract":
			classes["CLI contract"] = true
		case "dashboard-ui", "victoria-dev-event-read-path":
			classes["dashboard"] = true
		case "json-schema-contract":
			classes["schema"] = true
		case "exec-plan":
			classes["release"] = true
		}
	}
	if classes["schema"] || classes["CLI contract"] || classes["runtime"] {
		classes["release"] = true
	}
	return sortedStringSet(classes)
}
