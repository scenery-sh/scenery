package main

import (
	"encoding/json"
	"fmt"
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
	budgetDiagnostics, budgetSummary := validateInstructionDocBudgets(repoRoot)
	diagnostics = append(diagnostics, budgetDiagnostics...)
	for key, value := range budgetSummary {
		step.Summary[key] = value
	}
	installPolicyDiagnostics, installPolicySummary := validateSharedCLIInstallPolicy(repoRoot)
	diagnostics = append(diagnostics, installPolicyDiagnostics...)
	for key, value := range installPolicySummary {
		step.Summary[key] = value
	}
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
	indexDiagnostics, indexSummary := validateActiveExecPlanIndex(repoRoot)
	diagnostics = append(diagnostics, indexDiagnostics...)
	for key, value := range indexSummary {
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
		var slugs map[string]struct{}
		for _, raw := range markdownLinkTargets(string(data)) {
			if fragment, ok := strings.CutPrefix(strings.TrimSpace(raw), "#"); ok && fragment != "" {
				if slugs == nil {
					slugs = markdownHeadingSlugs(string(data))
				}
				checked++
				if _, ok := slugs[fragment]; !ok {
					diagnostics = append(diagnostics, checkDiagnostic{
						Stage:           "knowledge contract",
						Severity:        "error",
						File:            filepath.ToSlash(path),
						Message:         "intra-document anchor link has no matching heading: #" + fragment,
						SuggestedAction: "Fix the anchor or the heading it points to, then rerun `scenery harness self -o json`.",
					})
				}
				continue
			}
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

// markdownHeadingSlugs collects GitHub-style anchor slugs for every heading
// outside fenced code blocks.
func markdownHeadingSlugs(text string) map[string]struct{} {
	slugs := map[string]struct{}{}
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(trimmed, "#") {
			continue
		}
		title := strings.TrimLeft(trimmed, "#")
		if !strings.HasPrefix(title, " ") {
			continue
		}
		slugs[markdownHeadingSlug(strings.TrimSpace(title))] = struct{}{}
	}
	return slugs
}

// markdownHeadingSlug mirrors GitHub anchor generation: lowercase, spaces
// become hyphens, hyphens and underscores survive, other punctuation drops.
func markdownHeadingSlug(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// Instruction docs are loaded into every agent session, so self-harness warns
// when they outgrow lean-prompt budgets instead of silently accreting.
const (
	rootInstructionDocWarnWords  = 2500
	childInstructionDocWarnWords = 800
)

func validateInstructionDocBudgets(repoRoot string) ([]checkDiagnostic, map[string]any) {
	summary := map[string]any{}
	var diagnostics []checkDiagnostic
	checked := 0
	warned := 0
	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if architectureSkipDir(rel) || strings.HasSuffix(rel, "/testdata") || rel == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "AGENTS.md" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		checked++
		words := len(strings.Fields(string(data)))
		budget := childInstructionDocWarnWords
		if rel == "AGENTS.md" {
			budget = rootInstructionDocWarnWords
		}
		if words <= budget {
			return nil
		}
		warned++
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "knowledge contract",
			Severity:        "warning",
			File:            filepath.ToSlash(path),
			Message:         fmt.Sprintf("instruction doc exceeds its lean budget: %d words (budget %d)", words, budget),
			SuggestedAction: "Move detail into docs/agent-guide.md or docs/local-contract.md and keep the instruction doc as compact rules plus pointers.",
		})
		return nil
	})
	if err != nil {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "knowledge contract",
			Severity:        "error",
			Message:         err.Error(),
			SuggestedAction: "Fix the instruction-doc walk error, then rerun `scenery harness self -o json`.",
		})
	}
	summary["instruction_docs_checked"] = checked
	summary["instruction_doc_budget_warnings"] = warned
	return diagnostics, summary
}

const sharedCLIInstallCommand = "go install ./cmd/scenery"

var repositoryValidationInstructionDocs = []string{
	"AGENTS.md",
	"ARCHITECTURE.md",
	"PLANS.md",
	"SKILL.md",
	"docs/agent-guide.md",
	"docs/harness-engineering.md",
	"docs/local-contract.md",
}

// validateSharedCLIInstallPolicy keeps repository validation instructions from
// overwriting the shared installed CLI. Every occurrence must state its
// prohibition or human-only exception on the same line.
func validateSharedCLIInstallPolicy(repoRoot string) ([]checkDiagnostic, map[string]any) {
	summary := map[string]any{}
	var diagnostics []checkDiagnostic
	checked := 0
	occurrences := 0
	violations := 0

	for _, rel := range repositoryValidationInstructionDocs {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "knowledge contract",
				Severity:        "error",
				File:            filepath.ToSlash(path),
				Message:         err.Error(),
				SuggestedAction: "Fix the repository validation instruction so the self harness can verify the shared CLI install policy.",
			})
			continue
		}
		checked++
		lines := strings.Split(string(data), "\n")
		for index, line := range lines {
			if !strings.Contains(line, sharedCLIInstallCommand) {
				continue
			}
			occurrences++
			if sharedCLIInstallPolicyQualified(strings.ToLower(line)) {
				continue
			}
			violations++
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "knowledge contract",
				Severity:        "error",
				File:            filepath.ToSlash(path),
				Line:            index + 1,
				Message:         "repository validation instructions recommend overwriting the shared scenery CLI with `" + sharedCLIInstallCommand + "`",
				SuggestedAction: "Use the worktree-local `.scenery/harness/bin/scenery` for validation; reserve the shared install command for an explicit human request.",
			})
		}
	}

	summary["shared_cli_install_policy_docs_checked"] = checked
	summary["shared_cli_install_policy_occurrences"] = occurrences
	summary["shared_cli_install_policy_violations"] = violations
	return diagnostics, summary
}

func sharedCLIInstallPolicyQualified(context string) bool {
	for _, qualifier := range []string{
		"do not run `" + sharedCLIInstallCommand + "`",
		"must not run `" + sharedCLIInstallCommand + "`",
		"must not recommend `" + sharedCLIInstallCommand + "`",
		"do not write the shared `scenery` binary with `" + sharedCLIInstallCommand + "`",
		"unless a human explicitly",
		"only when a human explicitly",
		"only if a human explicitly",
		"reserved for an explicit human request",
		"reserved for an explicitly requested shared install",
	} {
		if strings.Contains(context, qualifier) {
			return true
		}
	}
	return false
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

func validateActiveExecPlanIndex(repoRoot string) ([]checkDiagnostic, map[string]any) {
	summary := map[string]any{}
	activePath := filepath.Join(repoRoot, "docs", "plans", "active.md")
	activeData, err := os.ReadFile(activePath)
	if err != nil {
		return []checkDiagnostic{execPlanDiagnostic(repoRoot, "docs/plans/active.md", 0, err.Error(), "Restore the active ExecPlan index.")}, summary
	}
	index, err := readDocsKnowledgeIndex(repoRoot)
	if err != nil {
		return []checkDiagnostic{execPlanDiagnostic(repoRoot, "docs/knowledge.json", 0, err.Error(), "Fix the docs knowledge index so active ExecPlans can be validated.")}, summary
	}

	linked := map[string]bool{}
	for _, raw := range markdownLinkTargets(string(activeData)) {
		target, ok := normalizeHarnessMarkdownLink(raw)
		if !ok || !strings.HasSuffix(target, ".md") {
			continue
		}
		path := filepath.ToSlash(filepath.Clean(filepath.Join("docs", "plans", filepath.FromSlash(target))))
		if path == "docs/plans/active.md" || path == "docs/plans/completed.md" || !strings.HasPrefix(path, "docs/plans/") {
			continue
		}
		linked[path] = true
	}

	indexed := map[string]docsKnowledgeDocument{}
	indexedActive := map[string]bool{}
	for _, doc := range index.Documents {
		indexed[doc.Path] = doc
		if doc.Status == "active" && strings.HasPrefix(doc.Path, "docs/plans/") && knowledgeTagsContain(doc.Tags, "execplans") {
			indexedActive[doc.Path] = true
		}
	}

	var diagnostics []checkDiagnostic
	for path := range linked {
		doc, ok := indexed[path]
		if !ok {
			diagnostics = append(diagnostics, execPlanDiagnostic(repoRoot, "docs/plans/active.md", 0, "active ExecPlan is missing from docs/knowledge.json: "+path, "Add the plan to docs/knowledge.json in the same change that activates it."))
			continue
		}
		if doc.Status != "active" {
			diagnostics = append(diagnostics, execPlanDiagnostic(repoRoot, "docs/knowledge.json", 0, "linked active ExecPlan is not marked active in docs/knowledge.json: "+path, "Mark the knowledge entry active or move the plan out of docs/plans/active.md."))
		}
	}
	for path := range indexedActive {
		if linked[path] {
			continue
		}
		diagnostics = append(diagnostics, execPlanDiagnostic(repoRoot, "docs/knowledge.json", 0, "indexed active ExecPlan is missing from docs/plans/active.md: "+path, "Link the plan from docs/plans/active.md or mark its knowledge entry completed."))
	}
	summary["active_exec_plan_links"] = len(linked)
	summary["indexed_active_exec_plans"] = len(indexedActive)
	return diagnostics, summary
}

func knowledgeTagsContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

var requiredSkillMentions = []string{
	"scenery harness ui -o json",
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
