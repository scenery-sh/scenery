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
