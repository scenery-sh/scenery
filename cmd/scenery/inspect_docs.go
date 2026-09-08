package main

import (
	"fmt"
	"os"
	"path/filepath"

	"sort"

	"time"
)

func buildInspectDocsResponseForOptions(repoRoot string, opts inspectDocsOptions) (inspectDocsResponse, error) {
	if err := validateInspectDocsOptions(opts); err != nil {
		return inspectDocsResponse{}, err
	}
	index, err := readDocsKnowledgeIndex(repoRoot)
	if err != nil {
		return inspectDocsResponse{}, err
	}
	query, err := buildInspectDocsQuery(repoRoot, opts)
	if err != nil {
		return inspectDocsResponse{}, err
	}
	allAgents := buildInspectDocsAgents(repoRoot)
	resp := inspectDocsResponse{
		PayloadIdentity: newCLIPayloadIdentity(inspectDocsKind),
		Repo: harnessSelfRepo{
			Root:       repoRoot,
			ModulePath: "scenery.sh",
			GoModPath:  filepath.Join(repoRoot, "go.mod"),
		},
		Summary: inspectDocsSummary{
			Quality: map[string]int{},
		},
		Query:     query,
		Agents:    inspectDocsAgents{Scopes: []inspectDocsAgentScope{}},
		Documents: []inspectDocsDocument{},
	}

	today := time.Now().UTC()
	allDocuments := make([]inspectDocsDocument, 0, len(index.Documents))
	for _, doc := range index.Documents {
		item := inspectDocsDocument{KnowledgeDocument: doc}
		if info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(doc.Path))); err == nil {
			item.Exists = true
			item.SizeBytes = info.Size()
			item.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339Nano)
		} else {
			resp.Summary.MissingCount++
			resp.Warnings = append(resp.Warnings, "indexed document is missing: "+doc.Path)
		}
		item.ReviewDue = docsDocumentReviewDue(doc, today)
		item.Stale = doc.Freshness == "stale"
		if item.ReviewDue {
			resp.Summary.ReviewDueCount++
		}
		if item.Stale {
			resp.Summary.StaleCount++
		}
		resp.Summary.Quality[doc.Quality]++
		allDocuments = append(allDocuments, item)
	}
	resp.Summary.DocumentCount = len(allDocuments)
	resp.Summary.AgentScopeCount = len(allAgents.Scopes)
	resp.Summary.StaleChildIndexEntryCount = len(allAgents.StaleChildIndexEntries)
	resp.Summary.MissingChildIndexEntryCount = len(allAgents.MissingChildIndexEntries)
	for _, path := range allAgents.StaleChildIndexEntries {
		resp.Warnings = append(resp.Warnings, "child AGENTS.md index entry is stale: "+path)
	}
	for _, path := range allAgents.MissingChildIndexEntries {
		resp.Warnings = append(resp.Warnings, "child AGENTS.md is missing from Child Agent Index: "+path)
	}
	switch query.Mode {
	case "all":
		resp.Agents = allAgents
		resp.Documents = allDocuments
		plans := inspectDocsPlans{
			Active:    inspectDocsArtifact(repoRoot, index.Plans.Active),
			Completed: inspectDocsArtifact(repoRoot, index.Plans.Completed),
		}
		techDebt := inspectDocsArtifact(repoRoot, index.TechDebt)
		resp.Plans = &plans
		resp.TechDebt = &techDebt
	case "filter":
		resp.Documents = filterInspectDocsDocuments(allDocuments, query)
	case "path":
		route, err := buildInspectDocsPathRoute(repoRoot, query.ForPath, allDocuments, allAgents)
		if err != nil {
			return inspectDocsResponse{}, err
		}
		resp.Agents.Scopes = route.AgentScopes
		resp.Documents = route.Documents
		resp.VerificationCommands = route.VerificationCommands
	case "summary":
	default:
		return inspectDocsResponse{}, fmt.Errorf("unsupported inspect docs query mode %q", query.Mode)
	}
	resp.Summary.SelectedDocumentCount = len(resp.Documents)
	return resp, nil
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

func uniqueSortedStrings(values []string) []string {
	set := stringSet(values)
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
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
			SuggestedAction: "Fix docs/knowledge.json so it conforms to the current scenery.docs.index schema revision.",
		}}, summary
	}

	summary["indexed_documents"] = len(index.Documents)
	summary["owner_default"] = index.OwnerDefault
	agents := buildInspectDocsAgents(repoRoot)
	summary["agent_scopes"] = len(agents.Scopes)
	summary["stale_child_index_entries"] = len(agents.StaleChildIndexEntries)
	summary["missing_child_index_entries"] = len(agents.MissingChildIndexEntries)

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
		} else if docsDocumentReviewDue(doc, today) && doc.Freshness == "current" {
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
	for _, relPath := range agents.StaleChildIndexEntries {
		diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, "AGENTS.md", "stale Child Agent Index entry: "+relPath, "Remove the stale child entry from AGENTS.md or recreate the referenced child AGENTS.md."))
	}
	for _, relPath := range agents.MissingChildIndexEntries {
		diagnostics = append(diagnostics, docsIndexDiagnostic(repoRoot, "AGENTS.md", "child AGENTS.md is missing from Child Agent Index: "+relPath, "Add "+relPath+" to the Child Agent Index in AGENTS.md."))
	}
	return diagnostics, summary
}

var importantKnowledgeDocuments = []string{
	"SKILL.md",
	"docs/app-development-cookbook.md",
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
