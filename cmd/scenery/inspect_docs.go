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
		route, err := buildInspectDocsPathsRoute(repoRoot, query.ForPaths, allDocuments, allAgents)
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
	if query.IncludeText {
		if err := attachInspectDocsExcerpts(repoRoot, resp.Documents); err != nil {
			return inspectDocsResponse{}, err
		}
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

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}
