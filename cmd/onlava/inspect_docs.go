package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	docsIndexSchema   = "onlava.docs.index.v1"
	inspectDocsSchema = "onlava.inspect.docs.v1"
)

type docsKnowledgeIndex struct {
	SchemaVersion   string                  `json:"schema_version"`
	GeneratedAt     string                  `json:"generated_at"`
	OwnerDefault    string                  `json:"owner_default"`
	FreshnessPolicy docsFreshnessPolicy     `json:"freshness_policy"`
	Documents       []docsKnowledgeDocument `json:"documents"`
	Plans           docsPlans               `json:"plans"`
	TechDebt        string                  `json:"tech_debt"`
}

type docsFreshnessPolicy struct {
	DefaultReviewDays int      `json:"default_review_days"`
	QualityGrades     []string `json:"quality_grades"`
	FreshnessStates   []string `json:"freshness_states"`
}

type docsPlans struct {
	Active    string `json:"active"`
	Completed string `json:"completed"`
}

type docsKnowledgeDocument struct {
	Path         string   `json:"path"`
	Title        string   `json:"title"`
	Owner        string   `json:"owner"`
	Status       string   `json:"status"`
	Quality      string   `json:"quality"`
	Freshness    string   `json:"freshness"`
	LastReviewed string   `json:"last_reviewed"`
	ReviewAfter  string   `json:"review_after"`
	Summary      string   `json:"summary"`
	Tags         []string `json:"tags"`
	SchemaRefs   []string `json:"schema_refs,omitempty"`
}

type inspectDocsResponse struct {
	SchemaVersion string                 `json:"schema_version"`
	Repo          harnessSelfRepo        `json:"repo"`
	Summary       inspectDocsSummary     `json:"summary"`
	Warnings      []string               `json:"warnings,omitempty"`
	Documents     []inspectDocsDocument  `json:"documents"`
	Plans         inspectDocsPlans       `json:"plans"`
	TechDebt      inspectDocsArtifactRef `json:"tech_debt"`
}

type inspectDocsSummary struct {
	DocumentCount  int            `json:"document_count"`
	MissingCount   int            `json:"missing_count"`
	ReviewDueCount int            `json:"review_due_count"`
	StaleCount     int            `json:"stale_count"`
	Quality        map[string]int `json:"quality"`
}

type inspectDocsPlans struct {
	Active    inspectDocsArtifactRef `json:"active"`
	Completed inspectDocsArtifactRef `json:"completed"`
}

type inspectDocsArtifactRef struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type inspectDocsDocument struct {
	docsKnowledgeDocument
	Exists     bool   `json:"exists"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
	ReviewDue  bool   `json:"review_due"`
	Stale      bool   `json:"stale"`
}

func buildInspectDocsResponse(repoRoot string) (inspectDocsResponse, error) {
	index, err := readDocsKnowledgeIndex(repoRoot)
	if err != nil {
		return inspectDocsResponse{}, err
	}
	resp := inspectDocsResponse{
		SchemaVersion: inspectDocsSchema,
		Repo: harnessSelfRepo{
			Root:       repoRoot,
			ModulePath: "github.com/pbrazdil/onlava",
			GoModPath:  filepath.Join(repoRoot, "go.mod"),
		},
		Summary: inspectDocsSummary{
			Quality: map[string]int{},
		},
		Plans: inspectDocsPlans{
			Active:    inspectDocsArtifact(repoRoot, index.Plans.Active),
			Completed: inspectDocsArtifact(repoRoot, index.Plans.Completed),
		},
		TechDebt:  inspectDocsArtifact(repoRoot, index.TechDebt),
		Documents: []inspectDocsDocument{},
	}

	today := time.Now().UTC()
	for _, doc := range index.Documents {
		item := inspectDocsDocument{docsKnowledgeDocument: doc}
		if info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(doc.Path))); err == nil {
			item.Exists = true
			item.SizeBytes = info.Size()
			item.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339Nano)
		} else {
			resp.Summary.MissingCount++
			resp.Warnings = append(resp.Warnings, "indexed document is missing: "+doc.Path)
		}
		item.ReviewDue = docsReviewDue(doc.ReviewAfter, today)
		item.Stale = doc.Freshness == "stale"
		if item.ReviewDue {
			resp.Summary.ReviewDueCount++
		}
		if item.Stale {
			resp.Summary.StaleCount++
		}
		resp.Summary.Quality[doc.Quality]++
		resp.Documents = append(resp.Documents, item)
	}
	resp.Summary.DocumentCount = len(resp.Documents)
	return resp, nil
}

func readDocsKnowledgeIndex(repoRoot string) (docsKnowledgeIndex, error) {
	path := filepath.Join(repoRoot, "docs", "knowledge.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return docsKnowledgeIndex{}, err
	}
	var index docsKnowledgeIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return docsKnowledgeIndex{}, err
	}
	if index.SchemaVersion != docsIndexSchema {
		return docsKnowledgeIndex{}, fmt.Errorf("docs/knowledge.json schema_version = %q, want %q", index.SchemaVersion, docsIndexSchema)
	}
	return index, nil
}

func inspectDocsArtifact(repoRoot, relPath string) inspectDocsArtifactRef {
	ref := inspectDocsArtifactRef{Path: filepath.ToSlash(relPath)}
	if relPath == "" {
		return ref
	}
	_, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(relPath)))
	ref.Exists = err == nil
	return ref
}

func validateDocsKnowledge(repoRoot string) ([]checkDiagnostic, map[string]any) {
	summary := map[string]any{}
	index, err := readDocsKnowledgeIndex(repoRoot)
	if err != nil {
		return []checkDiagnostic{{
			Stage:           "knowledge contract",
			Severity:        "error",
			File:            filepath.ToSlash(filepath.Join(repoRoot, "docs", "knowledge.json")),
			Message:         err.Error(),
			SuggestedAction: "Fix docs/knowledge.json so it conforms to onlava.docs.index.v1.",
		}}, summary
	}

	summary["indexed_documents"] = len(index.Documents)
	summary["owner_default"] = index.OwnerDefault

	validQuality := stringSet(index.FreshnessPolicy.QualityGrades)
	if len(validQuality) == 0 {
		validQuality = stringSet([]string{"A", "B", "C", "D"})
	}
	validFreshness := stringSet(index.FreshnessPolicy.FreshnessStates)
	if len(validFreshness) == 0 {
		validFreshness = stringSet([]string{"current", "review_due", "stale"})
	}
	validStatus := stringSet([]string{"active", "reference", "completed", "deprecated"})

	var diagnostics []checkDiagnostic
	seen := make(map[string]struct{})
	today := time.Now().UTC()
	for _, doc := range index.Documents {
		if doc.Path == "" {
			diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, "docs/knowledge.json", "indexed document path is empty", "Add a non-empty path to every indexed document."))
			continue
		}
		if _, ok := seen[doc.Path]; ok {
			diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, "docs/knowledge.json", "duplicate indexed document: "+doc.Path, "Remove duplicate document entries from docs/knowledge.json."))
		}
		seen[doc.Path] = struct{}{}
		fullPath := filepath.Join(repoRoot, filepath.FromSlash(doc.Path))
		if _, err := os.Stat(fullPath); err != nil {
			diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, doc.Path, "indexed document does not exist", "Create the indexed document or remove it from docs/knowledge.json."))
		}
		if doc.Title == "" || doc.Owner == "" || doc.Summary == "" {
			diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, "docs/knowledge.json", "indexed document has empty title, owner, or summary: "+doc.Path, "Fill title, owner, and summary for every indexed document."))
		}
		if _, ok := validStatus[doc.Status]; !ok {
			diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, "docs/knowledge.json", "invalid document status for "+doc.Path+": "+doc.Status, "Use status active, reference, completed, or deprecated."))
		}
		if _, ok := validQuality[doc.Quality]; !ok {
			diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, "docs/knowledge.json", "invalid quality grade for "+doc.Path+": "+doc.Quality, "Use a configured quality grade, normally A, B, C, or D."))
		}
		if _, ok := validFreshness[doc.Freshness]; !ok {
			diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, "docs/knowledge.json", "invalid freshness state for "+doc.Path+": "+doc.Freshness, "Use current, review_due, or stale."))
		}
		if !validDocsDate(doc.LastReviewed) || !validDocsDate(doc.ReviewAfter) {
			diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, "docs/knowledge.json", "invalid review date for "+doc.Path, "Use YYYY-MM-DD dates for last_reviewed and review_after."))
		} else if docsReviewDue(doc.ReviewAfter, today) && doc.Freshness == "current" {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "knowledge contract",
				Severity:        "warning",
				File:            filepath.ToSlash(filepath.Join(repoRoot, "docs", "knowledge.json")),
				Message:         "document review is due but freshness is current: " + doc.Path,
				SuggestedAction: "Review the document and update review_after or freshness.",
			})
		}
		for _, schemaRef := range doc.SchemaRefs {
			if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(schemaRef))); err != nil {
				diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, schemaRef, "schema ref does not exist: "+schemaRef, "Create the schema or remove it from schema_refs."))
			}
		}
	}
	for _, relPath := range []string{index.Plans.Active, index.Plans.Completed, index.TechDebt} {
		if relPath == "" {
			diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, "docs/knowledge.json", "plans or tech_debt path is empty", "Fill plans.active, plans.completed, and tech_debt."))
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(relPath))); err != nil {
			diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, relPath, "knowledge base path does not exist: "+relPath, "Create the referenced file or update docs/knowledge.json."))
		}
	}
	for _, relPath := range importantKnowledgeDocuments {
		if _, ok := seen[relPath]; ok {
			continue
		}
		diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, "docs/knowledge.json", "important document is not indexed: "+relPath, "Add "+relPath+" to docs/knowledge.json so agents can discover it."))
	}
	return diagnostics, summary
}

var importantKnowledgeDocuments = []string{
	"SKILL.md",
	"docs/app-development-cookbook.md",
	"docs/data-platform-runbook.md",
	"docs/ui-agent-contract.md",
	"docs/local-contract.md",
}

func docsIndexDiagnostic(repoRoot, relPath, message, action string) checkDiagnostic {
	return checkDiagnostic{
		Stage:           "knowledge contract",
		Severity:        "error",
		File:            filepath.ToSlash(filepath.Join(repoRoot, filepath.FromSlash(relPath))),
		Message:         message,
		SuggestedAction: action,
	}
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func validDocsDate(value string) bool {
	if value == "" {
		return false
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func docsReviewDue(value string, now time.Time) bool {
	if value == "" {
		return false
	}
	reviewAfter, err := time.Parse("2006-01-02", value)
	if err != nil {
		return false
	}
	return !reviewAfter.After(now)
}
