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
	validationClassification := []string{}
	if resp.ChangedArea != nil {
		changedAreaCommands = append(changedAreaCommands, resp.ChangedArea.RecommendedCommands...)
		validationClassification = append(validationClassification, resp.ChangedArea.ValidationClasses...)
	}
	contextPack := harnessAgentContext{
		cliPayloadIdentity: newCLIPayloadIdentity(harnessAgentContextKind),
		GeneratedAt:        resp.GeneratedAt,
		Run:                resp.Run,
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
		ValidationClassification:       validationClassification,
		RiskClassification:             riskClassification,
		DocsEntrypoints:                entrypoints,
		Schemas:                        schemas,
		KnownFastLoop:                  harnessAgentFastLoop(changedAreaCommands),
		KnownReleaseLoop:               "scripts/release-gate.sh",
		ArchitectureRules: []string{
			"Prefer Go standard library dependencies unless the payoff is concrete.",
			"Do not add legacy aliases or backwards-compatibility shims for renamed scenery APIs.",
			"Do not write the shared `scenery` binary with `go install ./cmd/scenery` unless a human explicitly asks; use self-harness' worktree-local `.scenery/harness/bin/scenery` build instead.",
			"Run the exact union in changed_area.recommended_commands for the calculated validation_classification; report an exact blocking condition for every skipped command.",
		},
		RecentFailures: recentFailures,
	}
	if resp.ChangedArea != nil {
		contextPack.DirtyFiles = resp.ChangedArea.ChangedFiles
		contextPack.RecommendedCommands = appendUniqueSorted(contextPack.RecommendedCommands, resp.ChangedArea.RecommendedCommands...)
	}
	contextPack.RecommendedCommands = appendUniqueSorted(contextPack.RecommendedCommands, contextPack.RerunCommands...)
	sort.Strings(contextPack.DocsEntrypoints)
	sort.Strings(contextPack.Schemas)
	sort.Strings(contextPack.RecentFailures)
	sort.Strings(contextPack.ChangedAreaRecommendedCommands)
	sort.Strings(contextPack.RerunCommands)
	sort.Strings(contextPack.RiskClassification)
	sort.Strings(contextPack.ValidationClassification)
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
		return "AGENTS.md"
	case "knowledge contract":
		return "AGENTS.md"
	case "changed area oracle":
		return ".scenery/harness/changed-area-latest.json"
	case "architecture checks":
		return "ARCHITECTURE.md"
	case "contract drift checks":
		return "ARCHITECTURE.md"
	case "go tests":
		return ".scenery/harness/test-timing-latest.json"
	case "fixture matrix":
		return ".scenery/harness/fixture-matrix-latest.json"
	case harnessEdgeProcessProbeName:
		return "scripts/verify/harness_self_edge.go"
	case harnessGenerationCompileProbeName:
		return "scripts/verify/harness_self_generate.go"
	case harnessNativeContractApplicationProbeName:
		return "scripts/verify/harness_self_native_contract.go"
	case harnessTypeScriptCheckerProbeName:
		return "scripts/verify/harness_self_tscheck.go"
	case harnessCodeTaskProcessProbeName:
		return "scripts/verify/harness_self_script.go"
	case harnessVictoriaProcessProbeName:
		return "scripts/verify/harness_self_victoria.go"
	case harnessDesktopProcessProbeName:
		return "scripts/verify/harness_self_desktop.go"
	case harnessValidationGitProbeName:
		return "scripts/verify/harness_self_validation.go"
	case harnessTestsuiteCacheProbeName:
		return "scripts/verify/harness_self_testsuite.go"
	case harnessWorktreeGitProbeName:
		return "scripts/verify/harness_self_worktree.go"
	case harnessBuildInfoProbeName:
		return "scripts/verify/harness_self_build_info.go"
	case harnessAgentRestartProbeName:
		return "scripts/verify/harness_self_agent_restart.go"
	case harnessAssistantProductionProbeName:
		return "scripts/verify/harness_self_assistant_production.go"
	case harnessCLIProcessProbeName:
		return "scripts/verify/harness_self_cli_process.go"
	case harnessDevNamedLockProbeName:
		return "scripts/verify/harness_self_dev_lock.go"
	case "schema validation":
		return ".scenery/harness/schema-validation-latest.json"
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
	knowledge := buildHarnessSelfKnowledge(repoRoot)
	state := harnessAgentDocsFreshness{
		DocumentCount: len(knowledge.Entrypoints),
	}
	for _, item := range knowledge.Entrypoints {
		if !item.Exists {
			state.MissingCount++
			state.MissingDocuments = append(state.MissingDocuments, item.Path)
		}
	}
	sort.Strings(state.MissingDocuments)
	return state
}

func buildHarnessAgentRelevantExecPlans(_ string, _ *harnessChangedAreaReport) []harnessAgentExecPlan {
	return []harnessAgentExecPlan{}
}

func buildHarnessAgentFailedArtifacts(repoRoot string, failures []harnessAgentFailingStep) []harnessAgentFailedArtifact {
	items := map[string]harnessAgentFailedArtifact{}
	add := func(stepName, rerun string, artifacts []harnessEvidenceArtifact) {
		for _, artifact := range artifacts {
			if artifact.Path == "" {
				continue
			}
			item := harnessAgentFailedArtifact{
				Step:           stepName,
				Name:           artifact.Name,
				Path:           artifact.Path,
				Kind:           artifact.Kind,
				SchemaRevision: artifact.SchemaRevision,
				RerunCommand:   rerun,
			}
			key := item.Name + "\x00" + item.Path
			if existing, ok := items[key]; ok {
				if existing.Step == "" {
					existing.Step = item.Step
				}
				if existing.Kind == "" {
					existing.Kind = item.Kind
					existing.SchemaRevision = item.SchemaRevision
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
	if inspectResp, err := readProductHarness(repoRoot); err == nil {
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
	for _, validationClass := range changedArea.ValidationClasses {
		switch validationClass {
		case harnessValidationCLIJSONContract:
			classes["CLI contract"] = true
		case harnessValidationUICatalog:
			classes["ui"] = true
		case harnessValidationReleaseRuntime:
			classes["runtime"] = true
			classes["release"] = true
		}
	}
	for _, file := range changedArea.ChangedFiles {
		switch file.Category {
		case "schema":
			classes["schema"] = true
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
		case "victoria-dev-event-read-path":
			classes["runtime"] = true
		case "json-schema-contract":
			classes["schema"] = true
		}
	}
	return sortedStringSet(classes)
}

// Commands come from the shared classifier; guidance must not invent a preflight.
func harnessAgentFastLoop(commands []string) string {
	if len(commands) == 0 {
		return "No changed paths require validation. After editing, classify the final changed paths using the root validation matrix. Use doctor only for unknown environment readiness or a failed prerequisite."
	}
	return "After editing, fulfill this changed-area command union. Run full directly when selected; do not precede it with quick. Reuse successful checks for unchanged inputs and scope; a full verifier covers its successful repository Go suite.\n" + strings.Join(commands, "\n") + "\nRead the agent context from that run's archive. Reclassify after further edits. Use doctor only for unknown environment readiness or a failed prerequisite."
}

// Static checks are available in quick mode. A failed documentation/schema
// check never authorizes release probes merely to reproduce its diagnostic.
func harnessStaticCheckCommand(repoRoot string) []string {
	return []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--quick", "--summary", "--write"}
}
