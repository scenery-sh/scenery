package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunSceneryInspectDocs(t *testing.T) {
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
	if payload.SchemaVersion != inspectDocsSchema {
		t.Fatalf("schema = %q", payload.SchemaVersion)
	}
	if payload.Repo.Root != root || payload.Repo.ModulePath != "scenery.sh" {
		t.Fatalf("repo = %+v", payload.Repo)
	}
	if payload.Summary.DocumentCount == 0 || payload.Summary.MissingCount != 0 {
		t.Fatalf("summary = %+v", payload.Summary)
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
	if !payload.Plans.Active.Exists || !payload.Plans.Completed.Exists || !payload.TechDebt.Exists {
		t.Fatalf("plans/debt = %+v %+v", payload.Plans, payload.TechDebt)
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
	if err := runSceneryInspect([]string{"docs", "--repo-root", root, "-o", "json"}, &out); err != nil {
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
