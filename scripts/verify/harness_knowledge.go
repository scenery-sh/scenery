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
		Command: harnessStaticCheckCommand(repoRoot),
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
				SuggestedAction: "Fix the JSON syntax, then rerun `go run ./scripts/verify -o json`.",
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
	step.Summary["links_checked"] = linksChecked
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
						SuggestedAction: "Fix the anchor or the heading it points to, then rerun `go run ./scripts/verify -o json`.",
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
					SuggestedAction: "Fix or remove the broken local link, then rerun `go run ./scripts/verify -o json`.",
				})
			}
			if _, fragment, ok := strings.Cut(strings.TrimSpace(raw), "#"); ok && fragment != "" {
				targetData, err := os.ReadFile(targetPath)
				if err == nil {
					if _, exists := markdownHeadingSlugs(string(targetData))[fragment]; !exists {
						diagnostics = append(diagnostics, checkDiagnostic{Stage: "knowledge contract", Severity: "error", File: filepath.ToSlash(path), Message: "local markdown anchor has no matching heading: " + raw, SuggestedAction: "Fix the workflow link or its target heading."})
					}
				}
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
			SuggestedAction: "Trim AGENTS.md to compact rules plus pointers.",
		})
		return nil
	})
	if err != nil {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "knowledge contract",
			Severity:        "error",
			Message:         err.Error(),
			SuggestedAction: "Fix the instruction-doc walk error, then rerun `go run ./scripts/verify -o json`.",
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
	"SKILL.md",
}

// validateSharedCLIInstallPolicy keeps repository validation instructions from
// overwriting the shared installed CLI. Every occurrence must state its
// prohibition or human-only exception in the same Markdown block.
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
		blocks := instructionMarkdownBlocks(string(data))
		for _, block := range blocks {
			line := block.text
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
				Line:            block.line,
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
