package main

import (
	json "encoding/json"
	os "os"
	filepath "path/filepath"
	envpolicy "scenery.sh/internal/envpolicy"
	scn "scenery.sh/internal/scn"
	sort "sort"
	strings "strings"
	testing "testing"
)

func writeTestAppFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	contents = normalizeTestAppConfig(rel, contents)
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	if rel == ".scenery.json" {
		contractPath := filepath.Join(root, testAppFilename)
		if _, err := os.Stat(contractPath); os.IsNotExist(err) {
			contract := "application \"test\" {}\n"
			if err := os.WriteFile(contractPath, []byte(contract), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", contractPath, err)
			}
		}
	}
}

func normalizeTestAppConfig(rel, contents string) string {
	if rel == ".scenery.json" {
		var cfg map[string]any
		if json.Unmarshal([]byte(contents), &cfg) == nil {
			if _, ok := cfg["envs"]; !ok {
				cfg["envs"] = map[string]any{"local": map[string]any{"default": true}}
				if encoded, err := json.Marshal(cfg); err == nil {
					contents = string(encoded)
				}
			}
		}
	}
	return contents
}

func writeHarnessSelfRepo(t *testing.T, schema string, requestedSchemas ...string) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, "go.mod", "module scenery.sh\n\ngo 1.27.0\n")
	writeTestAppFile(t, root, "AGENTS.md", "See [harness](docs/harness-engineering.md).\n")
	writeTestAppFile(t, root, "SKILL.md", strings.Join(requiredSkillMentions, "\n")+"\n")
	writeTestAppFile(t, root, "PLAN.md", "See [docs](docs/index.md).\n")
	writeTestAppFile(t, root, "PLANS.md", validExecPlanStandardForTest())
	writeTestAppFile(t, root, "docs/index.md", "See [local](local-contract.md), [plans](plans/active.md), and [debt](tech-debt.md).\n")
	writeTestAppFile(t, root, "docs/local-contract.md", "Contract.\n")
	writeTestAppFile(t, root, "docs/environment.md", "Environment.\n")
	writeTestAppFile(t, root, "docs/environment.registry.json", `{"kind":"`+envpolicy.Kind+`","schema_revision":"`+envpolicy.SchemaRevision+`","variables":[{"name":"SCENERY_TEST_","match":"prefix","scope":"test_only","direction":"test_input","category":"tests","stability":"test_only","secret":false,"allowed_in":["docs","tests"],"owner":"scenery runtime","rationale":"Test-only controls.","preferred_surface":"tests","docs":["docs/environment.md"]}]}`)
	writeTestAppFile(t, root, "docs/app-development-cookbook.md", "Cookbook.\n")
	writeTestAppFile(t, root, "docs/ui-agent-contract.md", "UI contract.\n")
	writeTestAppFile(t, root, "docs/harness-engineering.md", "Harness.\n")
	writeTestAppFile(t, root, "docs/plans/active.md", "Active.\n")
	writeTestAppFile(t, root, "docs/plans/completed.md", "Completed.\n")
	writeTestAppFile(t, root, "docs/tech-debt.md", "Debt.\n")
	schemaNames := append([]string{"scenery.docs.index.schema.json"}, requestedSchemas...)
	sort.Strings(schemaNames)
	for i, name := range schemaNames {
		name = strings.TrimSpace(name)
		if name == "" || filepath.Base(name) != name || !strings.HasSuffix(name, ".schema.json") {
			t.Fatalf("invalid harness fixture schema name %q", name)
		}
		if i > 0 && name == strings.TrimSpace(schemaNames[i-1]) {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRootForTest(t), "docs", "schemas", name)); err != nil {
			t.Fatalf("unknown harness fixture schema %q: %v", name, err)
		}
		writeTestAppFile(t, root, filepath.Join("docs", "schemas", name), schema)
	}
	writeTestAppFile(t, root, "docs/knowledge.json", `{
  "kind": "`+docsIndexKind+`",
  "schema_revision": "`+docsIndexSchemaRevision+`",
  "generated_at": "2026-04-27T00:00:00Z",
  "owner_default": "scenery maintainers",
  "freshness_policy": {
    "default_review_days": 30,
    "quality_grades": ["A", "B", "C", "D"],
    "freshness_states": ["current", "review_due", "stale"]
  },
  "documents": [
    {
      "path": "SKILL.md",
      "title": "Skill",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Skill.",
      "tags": ["skill"]
    },
    {
      "path": "docs/index.md",
      "title": "Index",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Index.",
      "tags": ["docs"]
    },
    {
      "path": "docs/app-development-cookbook.md",
      "title": "Cookbook",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "B",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Cookbook.",
      "tags": ["cookbook"]
    },
    {
      "path": "docs/ui-agent-contract.md",
      "title": "UI contract",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "B",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "UI.",
      "tags": ["ui"]
    },
    {
      "path": "docs/local-contract.md",
      "title": "Contract",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Contract.",
      "tags": ["contract"],
      "schema_refs": ["docs/schemas/scenery.docs.index.schema.json"]
    }
  ],
  "plans": {
    "active": "docs/plans/active.md",
    "completed": "docs/plans/completed.md"
  },
  "tech_debt": "docs/tech-debt.md"
}`)
	return root
}

func validExecPlanStandardForTest() string {
	var b strings.Builder
	b.WriteString("# scenery Execution Plans\n\n")
	b.WriteString("## Required Sections\n\n")
	for _, section := range requiredExecPlanSections {
		b.WriteString("- `")
		b.WriteString(section)
		b.WriteString("`\n")
	}
	return b.String()
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(root)
}

func diagnosticMessages(diagnostics []checkDiagnostic) string {
	messages := make([]string, 0, len(diagnostics))
	for _, diag := range diagnostics {
		messages = append(messages, diag.Message)
	}
	return strings.Join(messages, "\n")
}

const testAppFilename = scn.AppFilename
