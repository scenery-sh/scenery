package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRunOnlavaInspectDocs(t *testing.T) {
	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)

	var out bytes.Buffer
	if err := runOnlavaInspect([]string{"docs", "--repo-root", root, "--json"}, &out); err != nil {
		t.Fatalf("inspect docs: %v\n%s", err, out.String())
	}

	var payload inspectDocsResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != inspectDocsSchema {
		t.Fatalf("schema = %q", payload.SchemaVersion)
	}
	if payload.Repo.Root != root || payload.Repo.ModulePath != "onlava.com" {
		t.Fatalf("repo = %+v", payload.Repo)
	}
	if payload.Summary.DocumentCount == 0 || payload.Summary.MissingCount != 0 {
		t.Fatalf("summary = %+v", payload.Summary)
	}
	if len(payload.Documents) == 0 || !payload.Documents[0].Exists {
		t.Fatalf("documents = %+v", payload.Documents)
	}
	if !payload.Plans.Active.Exists || !payload.Plans.Completed.Exists || !payload.TechDebt.Exists {
		t.Fatalf("plans/debt = %+v %+v", payload.Plans, payload.TechDebt)
	}
}

func TestValidateDocsKnowledgeReportsMissingDocument(t *testing.T) {
	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "docs/knowledge.json", `{
  "schema_version": "onlava.docs.index.v1",
  "generated_at": "2026-04-27T00:00:00Z",
  "owner_default": "Onlava maintainers",
  "freshness_policy": {
    "default_review_days": 30,
    "quality_grades": ["A", "B", "C", "D"],
    "freshness_states": ["current", "review_due", "stale"]
  },
  "documents": [
    {
      "path": "docs/missing.md",
      "title": "Missing",
      "owner": "Onlava maintainers",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Missing.",
      "tags": ["docs"]
    }
  ],
  "plans": {
    "active": "docs/plans/active.md",
    "completed": "docs/plans/completed.md"
  },
  "tech_debt": "docs/tech-debt.md"
}`)

	diagnostics, _ := validateDocsKnowledge(root)
	if !hasErrorDiagnostics(diagnostics) {
		t.Fatalf("expected error diagnostics, got %+v", diagnostics)
	}
}
