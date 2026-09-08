package main

import (
	json "encoding/json"
	os "os"
	filepath "path/filepath"
	strings "strings"
	testing "testing"
)

func TestValidateDocsKnowledgeDoesNotRequestHistoricalPlanReview(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	const planPath = "docs/plans/0002-history.md"
	writeTestAppFile(t, root, planPath, "# Completed\n")
	document := inspectDocsTestDocument(planPath, "History", "completed", []string{"plans", "execplans"})
	document.ReviewAfter = "2026-07-01"
	appendInspectDocsTestDocuments(t, root, document)

	diagnostics, _ := validateDocsKnowledge(root)
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, "document review is due") && strings.Contains(diagnostic.Message, planPath) {
			t.Fatalf("historical plan received review warning: %+v", diagnostic)
		}
	}
}

func appendInspectDocsTestDocuments(t *testing.T, root string, documents ...docsKnowledgeDocument) {
	t.Helper()
	index, err := readDocsKnowledgeIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	index.Documents = append(index.Documents, documents...)
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "knowledge.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func inspectDocsTestDocument(path, title, status string, tags []string) docsKnowledgeDocument {
	return docsKnowledgeDocument{
		Path:         path,
		Title:        title,
		Owner:        "test owner",
		Status:       status,
		Quality:      "A",
		Freshness:    "current",
		LastReviewed: "2026-07-23",
		ReviewAfter:  "2026-08-22",
		Summary:      title + " summary.",
		Tags:         tags,
	}
}
