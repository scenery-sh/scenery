package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/spec"
)

const (
	docsIndexKind       = "scenery.docs.index"
	docsIndexDescriptor = `{"kind":"scenery.docs.index","identity":"source","generated_at":"datetime","owner_default":"string","freshness_policy":"policy","documents":"documents","plans":"paths","tech_debt":"path"}`
	inspectDocsKind     = "scenery.inspect.docs"
)

var docsIndexSchemaRevision = string(spec.SchemaRevision(docsIndexDescriptor))

type docsKnowledgeIndex struct {
	Kind            string                  `json:"kind"`
	SchemaRevision  string                  `json:"schema_revision"`
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

type inspectDocsOptions struct {
	ForPath   string
	Tag       string
	Status    string
	ReviewDue bool
	All       bool
}

type inspectDocsQuery struct {
	Mode      string `json:"mode"`
	ForPath   string `json:"for_path,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Status    string `json:"status,omitempty"`
	ReviewDue bool   `json:"review_due,omitempty"`
	All       bool   `json:"all,omitempty"`
}

type inspectDocsResponse struct {
	cliPayloadIdentity
	Repo                 harnessSelfRepo         `json:"repo"`
	Query                inspectDocsQuery        `json:"query"`
	Summary              inspectDocsSummary      `json:"summary"`
	Warnings             []string                `json:"warnings,omitempty"`
	Agents               inspectDocsAgents       `json:"agents"`
	Documents            []inspectDocsDocument   `json:"documents"`
	VerificationCommands []string                `json:"verification_commands,omitempty"`
	Plans                *inspectDocsPlans       `json:"plans,omitempty"`
	TechDebt             *inspectDocsArtifactRef `json:"tech_debt,omitempty"`
}

type inspectDocsSummary struct {
	DocumentCount               int            `json:"document_count"`
	SelectedDocumentCount       int            `json:"selected_document_count"`
	MissingCount                int            `json:"missing_count"`
	ReviewDueCount              int            `json:"review_due_count"`
	StaleCount                  int            `json:"stale_count"`
	AgentScopeCount             int            `json:"agent_scope_count"`
	StaleChildIndexEntryCount   int            `json:"stale_child_index_entry_count"`
	MissingChildIndexEntryCount int            `json:"missing_child_index_entry_count"`
	Quality                     map[string]int `json:"quality"`
}

type inspectDocsPlans struct {
	Active    inspectDocsArtifactRef `json:"active"`
	Completed inspectDocsArtifactRef `json:"completed"`
}

type inspectDocsArtifactRef struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type inspectDocsAgents struct {
	Scopes                   []inspectDocsAgentScope `json:"scopes"`
	ChildIndexPath           string                  `json:"child_index_path,omitempty"`
	ChildIndexEntries        []string                `json:"child_index_entries,omitempty"`
	StaleChildIndexEntries   []string                `json:"stale_child_index_entries,omitempty"`
	MissingChildIndexEntries []string                `json:"missing_child_index_entries,omitempty"`
}

type inspectDocsAgentScope struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
}

type inspectDocsDocument struct {
	docsKnowledgeDocument
	Exists     bool                 `json:"exists"`
	SizeBytes  int64                `json:"size_bytes,omitempty"`
	ModifiedAt string               `json:"modified_at,omitempty"`
	ReviewDue  bool                 `json:"review_due"`
	Stale      bool                 `json:"stale"`
	Role       string               `json:"role,omitempty"`
	Reason     string               `json:"reason,omitempty"`
	Sections   []inspectDocsSection `json:"sections,omitempty"`
}

type inspectDocsSection struct {
	Heading   string `json:"heading"`
	Anchor    string `json:"anchor"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func buildInspectDocsResponse(repoRoot string) (inspectDocsResponse, error) {
	return buildInspectDocsResponseForOptions(repoRoot, inspectDocsOptions{All: true})
}

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
		cliPayloadIdentity: newCLIPayloadIdentity(inspectDocsKind),
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

func readDocsKnowledgeIndex(repoRoot string) (docsKnowledgeIndex, error) {
	path := filepath.Join(repoRoot, "docs", "knowledge.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return docsKnowledgeIndex{}, err
	}
	var index docsKnowledgeIndex
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&index); err != nil {
		return docsKnowledgeIndex{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return docsKnowledgeIndex{}, fmt.Errorf("docs/knowledge.json contains trailing JSON")
	}
	if index.Kind != docsIndexKind || index.SchemaRevision != docsIndexSchemaRevision {
		return docsKnowledgeIndex{}, fmt.Errorf("docs/knowledge.json identity = %q at %q, want %q at %q", index.Kind, index.SchemaRevision, docsIndexKind, docsIndexSchemaRevision)
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

func buildInspectDocsAgents(repoRoot string) inspectDocsAgents {
	scopes := discoverInspectDocsAgentScopes(repoRoot)
	childIndexEntries := readChildAgentIndexEntries(repoRoot)

	discoveredChildren := make(map[string]struct{})
	for _, scope := range scopes {
		if scope.Path != "AGENTS.md" {
			discoveredChildren[scope.Path] = struct{}{}
		}
	}
	indexedChildren := stringSet(childIndexEntries)

	stale := []string{}
	for _, path := range childIndexEntries {
		if _, ok := discoveredChildren[path]; !ok {
			stale = append(stale, path)
		}
	}
	missing := []string{}
	for path := range discoveredChildren {
		if _, ok := indexedChildren[path]; !ok {
			missing = append(missing, path)
		}
	}
	sort.Strings(stale)
	sort.Strings(missing)

	return inspectDocsAgents{
		Scopes:                   scopes,
		ChildIndexPath:           "AGENTS.md#child-agent-index",
		ChildIndexEntries:        childIndexEntries,
		StaleChildIndexEntries:   stale,
		MissingChildIndexEntries: missing,
	}
}

func discoverInspectDocsAgentScopes(repoRoot string) []inspectDocsAgentScope {
	scopes := []inspectDocsAgentScope{}
	_ = filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if appwalk.SkipDir(repoRoot, path) || (path != repoRoot && shouldSkipAgentScopeDir(entry.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "AGENTS.md" {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		scope := filepath.ToSlash(filepath.Dir(rel))
		if scope == "." {
			scope = "."
		}
		scopes = append(scopes, inspectDocsAgentScope{Path: rel, Scope: scope})
		return nil
	})
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].Path == "AGENTS.md" {
			return true
		}
		if scopes[j].Path == "AGENTS.md" {
			return false
		}
		return scopes[i].Path < scopes[j].Path
	})
	return scopes
}

// shouldSkipAgentScopeDir lists docs-scan-specific skips on top of the shared
// appwalk policy.
func shouldSkipAgentScopeDir(name string) bool {
	switch name {
	case ".direnv", ".idea", ".vscode", "vendor", "build", "coverage":
		return true
	default:
		return false
	}
}

var markdownAgentIndexLink = regexp.MustCompile(`\[[^\]]*AGENTS\.md[^\]]*\]\(([^)]+)\)`)

func readChildAgentIndexEntries(repoRoot string) []string {
	data, err := os.ReadFile(filepath.Join(repoRoot, "AGENTS.md"))
	if err != nil {
		return nil
	}
	var entries []string
	inSection := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "## ") {
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if strings.EqualFold(title, "Child Agent Index") {
				inSection = true
				continue
			}
			if inSection {
				break
			}
		}
		if !inSection || !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		if path, ok := childAgentIndexPathFromBullet(repoRoot, strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))); ok {
			entries = append(entries, path)
		}
	}
	entries = uniqueSortedStrings(entries)
	return entries
}

func childAgentIndexPathFromBullet(repoRoot, bullet string) (string, bool) {
	lower := strings.ToLower(bullet)
	if strings.Contains(lower, "no child") || strings.Contains(lower, "when adding") {
		return "", false
	}
	if match := markdownAgentIndexLink.FindStringSubmatch(bullet); len(match) == 2 {
		return normalizeChildAgentIndexPath(repoRoot, match[1])
	}
	for {
		start := strings.Index(bullet, "`")
		if start < 0 {
			break
		}
		rest := bullet[start+1:]
		end := strings.Index(rest, "`")
		if end < 0 {
			break
		}
		if path, ok := normalizeChildAgentIndexPath(repoRoot, rest[:end]); ok {
			return path, true
		}
		bullet = rest[end+1:]
	}
	for _, field := range strings.Fields(bullet) {
		if path, ok := normalizeChildAgentIndexPath(repoRoot, strings.Trim(field, "`[]():,;")); ok {
			return path, true
		}
	}
	return "", false
}

func normalizeChildAgentIndexPath(repoRoot, raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "./")
	if strings.Contains(value, "#") {
		value = strings.SplitN(value, "#", 2)[0]
	}
	if value == "" {
		return "", false
	}
	if filepath.IsAbs(value) {
		rel, err := filepath.Rel(repoRoot, value)
		if err != nil {
			return "", false
		}
		value = rel
	}
	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if value == "." || value == "AGENTS.md" || !strings.HasSuffix(value, "/AGENTS.md") {
		return "", false
	}
	if strings.HasPrefix(value, "../") {
		return "", false
	}
	return value, true
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
