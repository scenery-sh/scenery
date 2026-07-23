package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunSceneryInspectDocs(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)

	var out bytes.Buffer
	if err := runSceneryInspect([]string{"docs", "--repo-root", root, "--all", "-o", "json"}, &out); err != nil {
		t.Fatalf("inspect docs: %v\n%s", err, out.String())
	}

	var payload inspectDocsResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if payload.Kind != inspectDocsKind || payload.SchemaRevision != newCLIPayloadIdentity(inspectDocsKind).SchemaRevision {
		t.Fatalf("identity = %q %q", payload.Kind, payload.SchemaRevision)
	}
	if payload.Repo.Root != root || payload.Repo.ModulePath != "scenery.sh" {
		t.Fatalf("repo = %+v", payload.Repo)
	}
	if payload.Summary.DocumentCount == 0 || payload.Summary.MissingCount != 0 {
		t.Fatalf("summary = %+v", payload.Summary)
	}
	if payload.Query.Mode != "all" || payload.Summary.SelectedDocumentCount != payload.Summary.DocumentCount {
		t.Fatalf("query/summary = %+v %+v", payload.Query, payload.Summary)
	}
	if payload.Summary.AgentScopeCount != 1 || len(payload.Agents.Scopes) != 1 || payload.Agents.Scopes[0].Path != "AGENTS.md" {
		t.Fatalf("agents = %+v summary = %+v", payload.Agents, payload.Summary)
	}
	if len(payload.Agents.StaleChildIndexEntries) != 0 || len(payload.Agents.MissingChildIndexEntries) != 0 {
		t.Fatalf("child index drift = %+v", payload.Agents)
	}
	if len(payload.Documents) == 0 || !payload.Documents[0].Exists {
		t.Fatalf("documents = %+v", payload.Documents)
	}
	if payload.Plans == nil || payload.TechDebt == nil || !payload.Plans.Active.Exists || !payload.Plans.Completed.Exists || !payload.TechDebt.Exists {
		t.Fatalf("plans/debt = %+v %+v", payload.Plans, payload.TechDebt)
	}
}

func TestRunSceneryInspectDocsDefaultsToCompactSummary(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	var out bytes.Buffer
	if err := runSceneryInspect([]string{"docs", "--repo-root", root, "-o", "json"}, &out); err != nil {
		t.Fatalf("inspect docs: %v\n%s", err, out.String())
	}

	var payload inspectDocsResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if payload.Query.Mode != "summary" || len(payload.Documents) != 0 || payload.Summary.SelectedDocumentCount != 0 {
		t.Fatalf("summary query = %+v documents = %+v summary = %+v", payload.Query, payload.Documents, payload.Summary)
	}
	if payload.Summary.DocumentCount == 0 || payload.Plans != nil || payload.TechDebt != nil {
		t.Fatalf("compact summary leaked catalog details: summary=%+v plans=%+v debt=%+v", payload.Summary, payload.Plans, payload.TechDebt)
	}
	if out.Len() >= 10*1024 {
		t.Fatalf("compact summary = %d bytes, want < 10 KiB", out.Len())
	}
}

func TestReadDocsKnowledgeIndexRejectsLegacyAndUnknownFields(t *testing.T) {
	for _, content := range []string{
		`{"schema_version":"scenery.docs.index.` + "v1" + `"}`,
		`{"kind":"` + docsIndexKind + `","schema_revision":"` + docsIndexSchemaRevision + `","extra":true}`,
	} {
		root := t.TempDir()
		path := filepath.Join(root, "docs", "knowledge.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readDocsKnowledgeIndex(root); err == nil {
			t.Fatalf("readDocsKnowledgeIndex accepted %s", content)
		}
	}
}

func TestRunSceneryInspectDocsReportsAgentChildIndexDrift(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "AGENTS.md", `# Test Agent Instructions

### Child Agent Index

- `+"`docs/AGENTS.md`"+`: docs rules.
- `+"`stale/AGENTS.md`"+`: removed rules.

## Other Section
`)
	writeTestAppFile(t, root, "docs/AGENTS.md", "Docs rules.\n")
	writeTestAppFile(t, root, "cmd/AGENTS.md", "Command rules.\n")

	var out bytes.Buffer
	if err := runSceneryInspect([]string{"docs", "--repo-root", root, "--all", "-o", "json"}, &out); err != nil {
		t.Fatalf("inspect docs: %v\n%s", err, out.String())
	}

	var payload inspectDocsResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if payload.Summary.AgentScopeCount != 3 {
		t.Fatalf("agent_scope_count = %d, agents = %+v", payload.Summary.AgentScopeCount, payload.Agents)
	}
	if got := strings.Join(payload.Agents.ChildIndexEntries, ","); got != "docs/AGENTS.md,stale/AGENTS.md" {
		t.Fatalf("child_index_entries = %q", got)
	}
	if got := strings.Join(payload.Agents.StaleChildIndexEntries, ","); got != "stale/AGENTS.md" {
		t.Fatalf("stale_child_index_entries = %q", got)
	}
	if got := strings.Join(payload.Agents.MissingChildIndexEntries, ","); got != "cmd/AGENTS.md" {
		t.Fatalf("missing_child_index_entries = %q", got)
	}
	if payload.Summary.StaleChildIndexEntryCount != 1 || payload.Summary.MissingChildIndexEntryCount != 1 {
		t.Fatalf("summary = %+v", payload.Summary)
	}

	diagnostics, summary := validateDocsKnowledge(root)
	if got, _ := summary["agent_scopes"].(int); got != 3 {
		t.Fatalf("agent_scopes summary = %v", summary)
	}
	joined := diagnosticMessages(diagnostics)
	for _, want := range []string{
		"stale Child Agent Index entry: stale/AGENTS.md",
		"child AGENTS.md is missing from Child Agent Index: cmd/AGENTS.md",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diagnostics did not include %q: %+v", want, diagnostics)
		}
	}
}

func TestRunSceneryInspectDocsForPathReturnsBoundedTaskContext(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "AGENTS.md", `# Root instructions

### Child Agent Index

- `+"`internal/generate/AGENTS.md`"+`: generation rules.
`)
	writeTestAppFile(t, root, "internal/generate/AGENTS.md", `# Generation instructions

## Purpose

Own TypeScript client generation and related schemas.

## Verification

`+"```sh"+`
go test ./internal/generate
bun test internal/generate/testdata/typescript_client_conformance.test.ts
`+"```"+`
`)
	writeTestAppFile(t, root, "ARCHITECTURE.md", `# Architecture

## Code Map

### `+"`internal/generate`"+`

Owns TypeScript client generation, generated React adapters, and schemas.
`)
	writeTestAppFile(t, root, "docs/local-contract.md", `# Local Contract

## Generated TypeScript Clients

TypeScript client generation is deterministic and schema-checked.
`)
	writeTestAppFile(t, root, "docs/agent-guide.md", `# Agent Guide

## TypeScript Client Integration

Generate and validate the selected TypeScript client target.
`)
	writeTestAppFile(t, root, "docs/spec/typescript-client.md", `# TypeScript Client

## Client API

The generated client exposes typed operations.

## Generation Workflow

Generate and check the target before handoff.
`)
	writeTestAppFile(t, root, "docs/schemas/scenery.typescript-client-generated.schema.json", `{"type":"object"}`)
	writeTestAppFile(t, root, "docs/plans/0001-typescript-client.md", "# Active TypeScript Client Plan\n")
	writeTestAppFile(t, root, "docs/plans/0002-typescript-client-history.md", "# Completed TypeScript Client Plan\n")
	appendInspectDocsTestDocuments(t, root,
		inspectDocsTestDocument("ARCHITECTURE.md", "Architecture", "active", []string{"architecture", "generation"}),
		inspectDocsTestDocument("docs/agent-guide.md", "Agent Guide", "active", []string{"agents", "typescript-client"}),
		inspectDocsTestDocument("docs/spec/typescript-client.md", "TypeScript Client", "reference", []string{"spec", "typescript-client"}),
		inspectDocsTestDocument("docs/schemas/scenery.typescript-client-generated.schema.json", "TypeScript Client Schema", "active", []string{"schema", "typescript-client"}),
		inspectDocsTestDocument("docs/plans/0001-typescript-client.md", "TypeScript Client Work", "active", []string{"plans", "typescript-client", "generation"}),
		inspectDocsTestDocument("docs/plans/0002-typescript-client-history.md", "TypeScript Client History", "completed", []string{"plans", "typescript-client", "generation"}),
	)

	var out bytes.Buffer
	if err := runSceneryInspect([]string{"docs", "--repo-root", root, "--for-path", "internal/generate/client.go", "-o", "json"}, &out); err != nil {
		t.Fatalf("inspect docs --for-path: %v\n%s", err, out.String())
	}
	var payload inspectDocsResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if payload.Query.Mode != "path" || payload.Query.ForPath != "internal/generate/client.go" {
		t.Fatalf("query = %+v", payload.Query)
	}
	if got := inspectDocsScopePaths(payload.Agents.Scopes); got != "AGENTS.md,internal/generate/AGENTS.md" {
		t.Fatalf("agent scopes = %q", got)
	}
	if len(payload.Documents) == 0 || len(payload.Documents) > maxInspectDocsPathDocuments {
		t.Fatalf("documents = %d, want 1..%d: %+v", len(payload.Documents), maxInspectDocsPathDocuments, payload.Documents)
	}
	for _, role := range []string{"architecture", "contract", "active_execplan", "schema"} {
		if !inspectDocsHasRole(payload.Documents, role) {
			t.Fatalf("missing role %q in %+v", role, payload.Documents)
		}
	}
	if inspectDocsHasDocument(payload.Documents, "docs/plans/0002-typescript-client-history.md") {
		t.Fatalf("completed historical plan leaked into path result: %+v", payload.Documents)
	}
	for _, command := range []string{
		"go test ./internal/generate",
		"bun test internal/generate/testdata/typescript_client_conformance.test.ts",
		".scenery/harness/bin/scenery harness self --summary --write",
	} {
		if !stringSliceContains(payload.VerificationCommands, command) {
			t.Fatalf("missing command %q in %+v", command, payload.VerificationCommands)
		}
	}
	if out.Len() >= 10*1024 {
		t.Fatalf("path response = %d bytes, want < 10 KiB", out.Len())
	}
}

func TestRunSceneryInspectDocsCatalogFiltersAndDirectCompletedPlan(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "docs/spec/typescript-client.md", "# TypeScript Client\n")
	writeTestAppFile(t, root, "docs/plans/0002-typescript-client-history.md", "# Completed\n")
	writeTestAppFile(t, root, "docs/plans/0003-typescript-client-stale-history.md", "# Stale Completed\n")
	reviewDueDocument := inspectDocsTestDocument("docs/spec/typescript-client.md", "TypeScript Client", "reference", []string{"spec", "typescript-client"})
	reviewDueDocument.ReviewAfter = "2026-07-01"
	historicalDocument := inspectDocsTestDocument("docs/plans/0002-typescript-client-history.md", "TypeScript Client History", "completed", []string{"plans", "typescript-client"})
	historicalDocument.ReviewAfter = "2026-07-01"
	staleHistoricalDocument := inspectDocsTestDocument("docs/plans/0003-typescript-client-stale-history.md", "Stale TypeScript Client History", "completed", []string{"plans", "typescript-client"})
	staleHistoricalDocument.Freshness = "stale"
	staleHistoricalDocument.ReviewAfter = "2026-07-01"
	appendInspectDocsTestDocuments(t, root,
		reviewDueDocument,
		historicalDocument,
		staleHistoricalDocument,
	)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "tag", args: []string{"--tag", "typescript-client"}, want: "docs/spec/typescript-client.md"},
		{name: "status", args: []string{"--status", "reference"}, want: "docs/spec/typescript-client.md"},
		{name: "completed status", args: []string{"--status", "completed"}, want: "docs/plans/0002-typescript-client-history.md"},
		{name: "review due", args: []string{"--review-due"}, want: "docs/spec/typescript-client.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"docs", "--repo-root", root}, tc.args...)
			args = append(args, "-o", "json")
			if err := runSceneryInspect(args, &out); err != nil {
				t.Fatalf("inspect docs: %v\n%s", err, out.String())
			}
			var payload inspectDocsResponse
			if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Query.Mode != "filter" || !inspectDocsHasDocument(payload.Documents, tc.want) {
				t.Fatalf("query=%+v documents=%+v", payload.Query, payload.Documents)
			}
			switch tc.name {
			case "tag":
				if inspectDocsHasDocument(payload.Documents, historicalDocument.Path) {
					t.Fatalf("current historical plan leaked into tag results: %+v", payload.Documents)
				}
				if !inspectDocsHasDocument(payload.Documents, staleHistoricalDocument.Path) {
					t.Fatalf("explicitly stale historical plan missing from tag results: %+v", payload.Documents)
				}
			case "review due":
				if inspectDocsHasDocument(payload.Documents, historicalDocument.Path) ||
					inspectDocsHasDocument(payload.Documents, staleHistoricalDocument.Path) {
					t.Fatalf("historical plan leaked into review-due results: %+v", payload.Documents)
				}
			case "completed status":
				for _, document := range payload.Documents {
					if isCompletedExecPlanDocument(document.docsKnowledgeDocument) && document.ReviewDue {
						t.Fatalf("completed plan is review due: %+v", document)
					}
				}
			}
		})
	}

	var out bytes.Buffer
	if err := runSceneryInspect([]string{"docs", "--repo-root", root, "--for-path", "docs/plans/0002-typescript-client-history.md", "-o", "json"}, &out); err != nil {
		t.Fatalf("inspect direct completed plan: %v\n%s", err, out.String())
	}
	var payload inspectDocsResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !inspectDocsHasDocument(payload.Documents, "docs/plans/0002-typescript-client-history.md") {
		t.Fatalf("directly queried completed plan missing: %+v", payload.Documents)
	}
}

func TestCompletedExecPlanFreshnessPolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		doc  docsKnowledgeDocument
		want bool
	}{
		{
			name: "completed ExecPlan is historical",
			doc:  inspectDocsTestDocument("docs/plans/0002-history.md", "History", "completed", []string{"plans", "execplans"}),
			want: false,
		},
		{
			name: "active ExecPlan retains deadline",
			doc:  inspectDocsTestDocument("docs/plans/0003-active.md", "Active", "active", []string{"plans", "execplans"}),
			want: true,
		},
		{
			name: "living contract retains deadline",
			doc:  inspectDocsTestDocument("docs/local-contract.md", "Contract", "active", []string{"contract"}),
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.doc.ReviewAfter = "2026-07-23"
			if got := docsDocumentReviewDue(tc.doc, now); got != tc.want {
				t.Fatalf("docsDocumentReviewDue(%+v) = %t, want %t", tc.doc, got, tc.want)
			}
		})
	}
}

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

func TestParseInspectDocsFilters(t *testing.T) {
	t.Parallel()

	if _, err := parseInspectArgs([]string{"docs", "--all", "--status", "active", "-o", "json"}); err == nil || !strings.Contains(err.Error(), "--all cannot be combined") {
		t.Fatalf("all conflict error = %v", err)
	}
	if _, err := parseInspectArgs([]string{"docs", "--for-path", "../outside.go", "-o", "json"}); err != nil {
		t.Fatalf("path normalization happens after repo discovery: %v", err)
	}
	if _, err := normalizeInspectDocsQueryPath("/repo", "../outside.go"); err == nil || !strings.Contains(err.Error(), "within the repository") {
		t.Fatalf("path traversal error = %v", err)
	}
	if _, err := parseInspectArgs([]string{"app", "--tag", "contract", "-o", "json"}); err == nil || !strings.Contains(err.Error(), "--tag is only supported") {
		t.Fatalf("cross-subject tag error = %v", err)
	}
	if _, err := parseInspectArgs([]string{"docs", "--status", "old", "-o", "json"}); err == nil || !strings.Contains(err.Error(), "--status must be") {
		t.Fatalf("status error = %v", err)
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

func inspectDocsScopePaths(scopes []inspectDocsAgentScope) string {
	paths := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		paths = append(paths, scope.Path)
	}
	return strings.Join(paths, ",")
}

func inspectDocsHasRole(documents []inspectDocsDocument, role string) bool {
	for _, doc := range documents {
		if doc.Role == role {
			return true
		}
	}
	return false
}

func inspectDocsHasDocument(documents []inspectDocsDocument, path string) bool {
	for _, doc := range documents {
		if doc.Path == path {
			return true
		}
	}
	return false
}
