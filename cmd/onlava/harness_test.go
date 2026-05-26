package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHarnessArgs(t *testing.T) {
	opts, err := parseHarnessArgs([]string{"--app-root", "/tmp/app", "--json", "--write"})
	if err != nil {
		t.Fatalf("parseHarnessArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" {
		t.Fatalf("app root = %q", opts.AppRoot)
	}
	if !opts.JSON || !opts.Write {
		t.Fatalf("opts = %+v, want json and write", opts)
	}
}

func TestParseHarnessSelfArgs(t *testing.T) {
	opts, err := parseHarnessSelfArgs([]string{"--repo-root", "/tmp/onlava", "--json", "--write"})
	if err != nil {
		t.Fatalf("parseHarnessSelfArgs returned error: %v", err)
	}
	if opts.RepoRoot != "/tmp/onlava" {
		t.Fatalf("repo root = %q", opts.RepoRoot)
	}
	if !opts.JSON || !opts.Write {
		t.Fatalf("opts = %+v, want json and write", opts)
	}
}

func TestFindOnlavaRepoRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "cmd", "onlava")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/pbrazdil/onlava\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := findOnlavaRepoRoot(nested)
	if !ok {
		t.Fatal("expected repo root to be found")
	}
	if got != root {
		t.Fatalf("repo root = %q, want %q", got, root)
	}
}

func TestRunHarnessKnowledgeStepSuccess(t *testing.T) {
	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "docs/local-contract.md", "[self](harness-engineering.md)\n")
	writeTestAppFile(t, root, "docs/harness-engineering.md", "[schema](schemas/onlava.harness.self.v1.schema.json)\n")
	writeTestAppFile(t, root, "docs/grafana.md", "Grafana.\n")

	step := runHarnessKnowledgeStep(root)
	if !step.OK {
		t.Fatalf("step failed: %+v", step)
	}
	if got, _ := step.Summary["links_checked"].(int); got < 3 {
		t.Fatalf("links_checked = %v, want at least 3", step.Summary["links_checked"])
	}
	if got, _ := step.Summary["indexed_documents"].(int); got == 0 {
		t.Fatalf("indexed_documents = %v, want > 0", step.Summary["indexed_documents"])
	}
}

func TestRunHarnessKnowledgeStepReportsInvalidSchemaAndBrokenLink(t *testing.T) {
	root := writeHarnessSelfRepo(t, `{`)
	writeTestAppFile(t, root, "docs/local-contract.md", "[missing](missing.md)\n")
	writeTestAppFile(t, root, "docs/harness-engineering.md", "\n")

	step := runHarnessKnowledgeStep(root)
	if step.OK {
		t.Fatalf("step ok = true, want false")
	}
	messages := make([]string, 0, len(step.Diagnostics))
	for _, diag := range step.Diagnostics {
		messages = append(messages, diag.Message)
	}
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, "schema file is not valid JSON") {
		t.Fatalf("diagnostics did not include invalid schema: %+v", step.Diagnostics)
	}
	if !strings.Contains(joined, "local markdown link target does not exist") {
		t.Fatalf("diagnostics did not include broken link: %+v", step.Diagnostics)
	}
}

func TestRunHarnessKnowledgeStepReportsInvalidExecPlan(t *testing.T) {
	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "docs/plans/feature.md", "# Feature\n\nThis ExecPlan is a living document.\n\n## Purpose / Big Picture\n\nImplement a feature.\n")

	step := runHarnessKnowledgeStep(root)
	if step.OK {
		t.Fatalf("step ok = true, want false")
	}
	messages := make([]string, 0, len(step.Diagnostics))
	for _, diag := range step.Diagnostics {
		messages = append(messages, diag.Message)
	}
	if !strings.Contains(strings.Join(messages, "\n"), "missing required ExecPlan section") {
		t.Fatalf("diagnostics did not include invalid ExecPlan: %+v", step.Diagnostics)
	}
}

func TestRunHarnessKnowledgeStepReportsStaleSkill(t *testing.T) {
	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "SKILL.md", "---\nname: onlava\n---\n\n# onlava\n\nOld skill.\n")

	step := runHarnessKnowledgeStep(root)
	if step.OK {
		t.Fatalf("step ok = true, want false")
	}
	messages := make([]string, 0, len(step.Diagnostics))
	for _, diag := range step.Diagnostics {
		messages = append(messages, diag.Message)
	}
	if !strings.Contains(strings.Join(messages, "\n"), "SKILL.md is missing required capability mention") {
		t.Fatalf("diagnostics did not include stale SKILL.md: %+v", step.Diagnostics)
	}
}

func TestRunOnlavaHarnessJSONSuccessWritesLatest(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeHarnessTestApp(t, root, "harnessapp", "return nil")

	var out bytes.Buffer
	if err := runOnlavaHarness(context.Background(), &out, []string{"--app-root", root, "--json", "--write"}); err != nil {
		t.Fatalf("runOnlavaHarness returned error: %v\n%s", err, out.String())
	}

	var payload harnessResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "onlava.harness.result.v1" || !payload.OK {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.App.Name != "harnessapp" || payload.App.ModulePath != "example.com/harnessapp" {
		t.Fatalf("app = %+v", payload.App)
	}
	if len(payload.Steps) != 10 {
		t.Fatalf("steps = %d, want 10", len(payload.Steps))
	}
	if payload.Wrote == "" {
		t.Fatal("expected wrote path")
	}
	if _, err := os.Stat(payload.Wrote); err != nil {
		t.Fatalf("expected harness result on disk: %v", err)
	}
	if !harnessArtifactExists(payload.Artifacts, "latest-harness") {
		t.Fatalf("expected latest-harness artifact to exist: %+v", payload.Artifacts)
	}
}

func TestRunOnlavaHarnessJSONFailureIncludesNextAction(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeHarnessTestApp(t, root, "harnessfail", "return MissingSymbol")

	var out bytes.Buffer
	err := runOnlavaHarness(context.Background(), &out, []string{"--app-root", root, "--json"})
	var silent *silentCLIError
	if !errors.As(err, &silent) {
		t.Fatalf("expected silentCLIError, got %v\n%s", err, out.String())
	}

	var payload harnessResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.OK {
		t.Fatalf("payload ok = true, want false")
	}
	if len(payload.NextActions) == 0 {
		t.Fatalf("expected next actions: %+v", payload)
	}
	if !strings.Contains(strings.Join(payload.NextActions, "\n"), "missing symbol") &&
		!strings.Contains(strings.Join(payload.NextActions, "\n"), "MissingSymbol") {
		t.Fatalf("next actions = %+v", payload.NextActions)
	}
}

func writeHarnessTestApp(t *testing.T, root, name, body string) {
	t.Helper()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"`+name+`","id":"`+name+`-id"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/"+name+"\n\ngo 1.26.0\n")
	writeTestAppFile(t, root, "svc/api.go", "package svc\n\nimport \"context\"\n\n//onlava:api public\nfunc Ping(context.Context) error { "+body+" }\n")
}

func writeHarnessSelfRepo(t *testing.T, schema string) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, "go.mod", "module github.com/pbrazdil/onlava\n\ngo 1.26.0\n")
	writeTestAppFile(t, root, "AGENTS.md", "See [harness](docs/harness-engineering.md).\n")
	writeTestAppFile(t, root, "SKILL.md", strings.Join(requiredSkillMentions, "\n")+"\n")
	writeTestAppFile(t, root, "PLAN.md", "See [docs](docs/index.md).\n")
	writeTestAppFile(t, root, "PLANS.md", validExecPlanStandardForTest())
	writeTestAppFile(t, root, "docs/index.md", "See [local](local-contract.md), [plans](plans/active.md), and [debt](tech-debt.md).\n")
	writeTestAppFile(t, root, "docs/local-contract.md", "Contract.\n")
	writeTestAppFile(t, root, "docs/data-platform.md", "Data platform.\n")
	writeTestAppFile(t, root, "docs/app-development-cookbook.md", "Cookbook.\n")
	writeTestAppFile(t, root, "docs/data-platform-runbook.md", "Runbook.\n")
	writeTestAppFile(t, root, "docs/ui-agent-contract.md", "UI contract.\n")
	writeTestAppFile(t, root, "docs/harness-engineering.md", "Harness.\n")
	writeTestAppFile(t, root, "docs/plans/active.md", "Active.\n")
	writeTestAppFile(t, root, "docs/plans/completed.md", "Completed.\n")
	writeTestAppFile(t, root, "docs/tech-debt.md", "Debt.\n")
	for _, path := range []string{
		"docs/schemas/onlava.admin.result.v1.schema.json",
		"docs/schemas/onlava.config.v1.schema.json",
		"docs/schemas/onlava.build.latest.v1.schema.json",
		"docs/schemas/onlava.docs.index.v1.schema.json",
		"docs/schemas/onlava.harness.self.v1.schema.json",
		"docs/schemas/onlava.harness.result.v1.schema.json",
		"docs/schemas/onlava.harness.ui.v1.schema.json",
		"docs/schemas/onlava.check.result.v1.schema.json",
		"docs/schemas/onlava.gen.manifest.v1.schema.json",
		"docs/schemas/onlava.inspect.app.v1.schema.json",
		"docs/schemas/onlava.inspect.build.v1.schema.json",
		"docs/schemas/onlava.data.export.v1.schema.json",
		"docs/schemas/onlava.inspect.data.v1.schema.json",
		"docs/schemas/onlava.inspect.docs.v1.schema.json",
		"docs/schemas/onlava.inspect.endpoints.v1.schema.json",
		"docs/schemas/onlava.inspect.metrics.v1.schema.json",
		"docs/schemas/onlava.inspect.paths.v1.schema.json",
		"docs/schemas/onlava.inspect.temporal.v1.schema.json",
		"docs/schemas/onlava.inspect.routes.v1.schema.json",
		"docs/schemas/onlava.inspect.services.v1.schema.json",
		"docs/schemas/onlava.inspect.traces.v1.schema.json",
		"docs/schemas/onlava.logs.event.v1.schema.json",
		"docs/schemas/onlava.run.event.v1.schema.json",
		"docs/schemas/onlava.version.v1.schema.json",
		"docs/schemas/onlava.worker.manifest.v1.schema.json",
		"docs/schemas/onlava.worker.manifest.v2.schema.json",
		"docs/schemas/onlava.wire.capabilities.v1.schema.json",
	} {
		writeTestAppFile(t, root, path, schema)
	}
	writeTestAppFile(t, root, "docs/knowledge.json", `{
  "schema_version": "onlava.docs.index.v1",
  "generated_at": "2026-04-27T00:00:00Z",
  "owner_default": "onlava maintainers",
  "freshness_policy": {
    "default_review_days": 30,
    "quality_grades": ["A", "B", "C", "D"],
    "freshness_states": ["current", "review_due", "stale"]
  },
  "documents": [
    {
      "path": "SKILL.md",
      "title": "Skill",
      "owner": "onlava maintainers",
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
      "owner": "onlava maintainers",
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
      "owner": "onlava maintainers",
      "status": "active",
      "quality": "B",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Cookbook.",
      "tags": ["cookbook"]
    },
    {
      "path": "docs/data-platform-runbook.md",
      "title": "Data runbook",
      "owner": "onlava maintainers",
      "status": "active",
      "quality": "B",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Runbook.",
      "tags": ["data"]
    },
    {
      "path": "docs/ui-agent-contract.md",
      "title": "UI contract",
      "owner": "onlava maintainers",
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
      "owner": "onlava maintainers",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Contract.",
      "tags": ["contract"],
      "schema_refs": ["docs/schemas/onlava.docs.index.v1.schema.json"]
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
	b.WriteString("# onlava Execution Plans\n\n")
	b.WriteString("## Required Sections\n\n")
	for _, section := range requiredExecPlanSections {
		b.WriteString("- `")
		b.WriteString(section)
		b.WriteString("`\n")
	}
	return b.String()
}

func harnessArtifactExists(items []harnessArtifact, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return item.Exists
		}
	}
	return false
}
