package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runHarnessKnowledgeStep(repoRoot string) harnessStep {
	started := time.Now()
	knowledge := buildHarnessSelfKnowledge(repoRoot)
	step := harnessStep{
		Name:    "knowledge contract",
		Command: []string{"scenery", "harness", "self", "internal:knowledge-check", repoRoot},
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
				SuggestedAction: "Fix the JSON syntax, then rerun `scenery harness self -o json`.",
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
					SuggestedAction: "Fix or remove the broken local link, then rerun `scenery harness self -o json`.",
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
			SuggestedAction: "Create PLANS.md with the scenery ExecPlan contract.",
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
	"scenery harness ui -o json",
	"docs/ui-agent-contract.md",
	"@scenery registry",
	"bun run shadcn:add @scenery/",
	"scenery harness self --summary --write",
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
			SuggestedAction: "Update SKILL.md so installed agents learn the current scenery workflow.",
		})
	}
	summary["skill_missing_mentions"] = missing
	return diagnostics, summary
}

func validateExecPlanSections(repoRoot, relPath, text string, standard bool) []checkDiagnostic {
	var diagnostics []checkDiagnostic
	if standard && !strings.Contains(text, "scenery Execution Plans") {
		diagnostics = append(diagnostics, execPlanDiagnostic(repoRoot, relPath, 1, "PLANS.md does not identify itself as the scenery ExecPlan standard", "Keep PLANS.md as the canonical scenery ExecPlan contract."))
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
