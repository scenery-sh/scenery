package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateActiveExecPlanIndexRejectsLinkedPlanMissingKnowledgeEntry(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, "docs/plans/active.md", "## Active ExecPlans\n\n- [Plan](0001-plan.md)\n")
	writeTestAppFile(t, root, "docs/knowledge.json", testDocsIndexJSON(`[]`))

	diagnostics, _ := validateActiveExecPlanIndex(root)
	assertDiagnosticContains(t, diagnostics, "active ExecPlan is missing from docs/knowledge.json: docs/plans/0001-plan.md")
}

func TestValidateActiveExecPlanIndexRejectsIndexedPlanMissingActiveLink(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, "docs/plans/active.md", "## Active ExecPlans\n")
	writeTestAppFile(t, root, "docs/knowledge.json", testDocsIndexJSON(`[{
  "path":"docs/plans/0001-plan.md","title":"Plan","owner":"owner","status":"active",
  "quality":"B","freshness":"current","last_reviewed":"2026-07-22",
  "review_after":"2026-08-21","summary":"Plan.","tags":["plans","execplans"]
}]`))

	diagnostics, _ := validateActiveExecPlanIndex(root)
	assertDiagnosticContains(t, diagnostics, "indexed active ExecPlan is missing from docs/plans/active.md: docs/plans/0001-plan.md")
}

func TestValidateActiveExecPlanIndexRejectsLinkedPlanMarkedCompleted(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, "docs/plans/active.md", "## Active ExecPlans\n\n- [Plan](0001-plan.md)\n")
	writeTestAppFile(t, root, "docs/knowledge.json", testDocsIndexJSON(`[{
  "path":"docs/plans/0001-plan.md","title":"Plan","owner":"owner","status":"completed",
  "quality":"B","freshness":"current","last_reviewed":"2026-07-22",
  "review_after":"2026-08-21","summary":"Plan.","tags":["plans","execplans"]
}]`))

	diagnostics, _ := validateActiveExecPlanIndex(root)
	assertDiagnosticContains(t, diagnostics, "linked active ExecPlan is not marked active in docs/knowledge.json: docs/plans/0001-plan.md")
}

func TestValidateActiveExecPlanIndexAcceptsMatchingIndex(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, "docs/plans/active.md", "## Active ExecPlans\n\n- [Plan](0001-plan.md)\n")
	writeTestAppFile(t, root, "docs/knowledge.json", testDocsIndexJSON(`[{
  "path":"docs/plans/0001-plan.md","title":"Plan","owner":"owner","status":"active",
  "quality":"B","freshness":"current","last_reviewed":"2026-07-22",
  "review_after":"2026-08-21","summary":"Plan.","tags":["plans","execplans"]
}]`))

	diagnostics, summary := validateActiveExecPlanIndex(root)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if summary["active_exec_plan_links"] != 1 || summary["indexed_active_exec_plans"] != 1 {
		t.Fatalf("summary = %#v", summary)
	}
}

func testDocsIndexJSON(documents string) string {
	return fmt.Sprintf(`{
  "kind":"scenery.docs.index","schema_revision":%q,"generated_at":"2026-07-22T00:00:00Z",
  "owner_default":"owner","freshness_policy":{"default_review_days":30,"quality_grades":["B"],"freshness_states":["current"]},
  "documents":%s,"plans":{"active":"docs/plans/active.md","completed":"docs/plans/completed.md"},
  "tech_debt":"docs/tech-debt.md"
}`, docsIndexSchemaRevision, documents)
}

func assertDiagnosticContains(t *testing.T, diagnostics []checkDiagnostic, want string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, want) {
			return
		}
	}
	t.Fatalf("diagnostics %#v do not contain %q", diagnostics, want)
}

func TestCheckHarnessMarkdownLinksValidatesIntraDocumentAnchors(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, "docs/guide.md", strings.Join([]string{
		"# Guide",
		"",
		"- [CLI Grammar](#cli-grammar)",
		"- [Missing Section](#missing-section)",
		"",
		"## CLI Grammar",
		"",
		"```sh",
		"# this fenced comment is not a heading",
		"```",
	}, "\n"))

	files := harnessKnowledgeFiles(root, []string{"docs/guide.md"})
	checked, diagnostics := checkHarnessMarkdownLinks(root, files)
	if checked != 2 {
		t.Fatalf("checked = %d, want 2", checked)
	}
	assertDiagnosticContains(t, diagnostics, "intra-document anchor link has no matching heading: #missing-section")
	for _, diag := range diagnostics {
		if strings.Contains(diag.Message, "#cli-grammar") {
			t.Fatalf("valid anchor was reported broken: %+v", diag)
		}
	}
}

func TestMarkdownHeadingSlugMirrorsGitHubAnchors(t *testing.T) {
	tests := []struct{ title, want string }{
		{"Current Scenery contract", "current-scenery-contract"},
		{"`scenery inspect ui`", "scenery-inspect-ui"},
		{"Agent Thread Findings - 2026-07-03", "agent-thread-findings---2026-07-03"},
		{"Data: Databases, Snapshots, And Storage", "data-databases-snapshots-and-storage"},
	}
	for _, tt := range tests {
		if got := markdownHeadingSlug(tt.title); got != tt.want {
			t.Fatalf("markdownHeadingSlug(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}

func TestValidateInstructionDocBudgetsWarnsOnOversizedDocs(t *testing.T) {
	root := t.TempDir()
	small := strings.Repeat("word ", 100)
	big := strings.Repeat("word ", childInstructionDocWarnWords+1)
	writeTestAppFile(t, root, "AGENTS.md", small)
	writeTestAppFile(t, root, "internal/example/AGENTS.md", big)
	writeTestAppFile(t, root, "internal/example/testdata/AGENTS.md", big)

	diagnostics, summary := validateInstructionDocBudgets(root)
	assertDiagnosticContains(t, diagnostics, "instruction doc exceeds its lean budget")
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %d, want 1 (testdata must be skipped, root is within budget): %+v", len(diagnostics), diagnostics)
	}
	if diagnostics[0].Severity != "warning" {
		t.Fatalf("severity = %q, want warning", diagnostics[0].Severity)
	}
	if summary["instruction_docs_checked"] != 2 {
		t.Fatalf("instruction_docs_checked = %v, want 2", summary["instruction_docs_checked"])
	}
}

// The shipped instruction docs must stay within the budgets this harness
// enforces, so the check never cries wolf on a clean tree.
func TestShippedInstructionDocsWithinLeanBudgets(t *testing.T) {
	repoRoot := repoRootForTest(t)
	diagnostics, _ := validateInstructionDocBudgets(repoRoot)
	for _, diag := range diagnostics {
		t.Errorf("unexpected instruction-doc diagnostic: %s: %s", diag.File, diag.Message)
	}
}
