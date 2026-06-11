package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseHarnessArgs(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	opts, err := parseHarnessSelfArgs([]string{"--repo-root", "/tmp/scenery", "--json", "--write", "--quick", "--fresh-tests"})
	if err != nil {
		t.Fatalf("parseHarnessSelfArgs returned error: %v", err)
	}
	if opts.RepoRoot != "/tmp/scenery" {
		t.Fatalf("repo root = %q", opts.RepoRoot)
	}
	if !opts.JSON || !opts.Write {
		t.Fatalf("opts = %+v, want json and write", opts)
	}
	if opts.Mode != harnessSelfModeQuick {
		t.Fatalf("mode = %q, want quick", opts.Mode)
	}
	if !opts.FreshTests {
		t.Fatalf("fresh tests = false, want true")
	}
	if _, err := parseHarnessSelfArgs([]string{"--quick", "--race"}); err == nil {
		t.Fatal("expected conflicting harness self modes to fail")
	}
	if _, err := parseHarnessSelfArgs([]string{"--with-neon-selfhost"}); err == nil {
		t.Fatal("expected removed Neon selfhost flag to fail")
	}
}

func TestHarnessSelfGoTestCommandRunsFullSuiteInJSONMode(t *testing.T) {
	t.Parallel()

	got := strings.Join(harnessSelfGoTestCommand(), " ")
	if got != "go test -json ./..." {
		t.Fatalf("harnessSelfGoTestCommand() = %q", got)
	}
}

func TestHarnessSelfFreshGoTestCommandDisablesTestCache(t *testing.T) {
	t.Parallel()

	got := strings.Join(harnessSelfFreshGoTestCommand(), " ")
	if got != "go test -count=1 -json ./..." {
		t.Fatalf("harnessSelfFreshGoTestCommand() = %q", got)
	}
}

func TestFindSceneryRepoRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "cmd", "scenery")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module scenery.sh\n\ngo 1.26.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := findSceneryRepoRoot(nested)
	if !ok {
		t.Fatal("expected repo root to be found")
	}
	if got != root {
		t.Fatalf("repo root = %q, want %q", got, root)
	}
}

func TestLatestHarnessSourceModTimeIncludesEmbeddedNonGoInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, "go.mod", "module scenery.sh\n")
	writeTestAppFile(t, root, "internal/devtools/versions.json", `{"grafana":"1.0.0"}`)
	writeTestAppFile(t, root, "internal/devtools/versions_test.go", "package devtools\n")
	writeTestAppFile(t, root, "internal/devtools/node_modules/ignored.json", `{"ignored":true}`)

	oldTime := time.Unix(1_700_000_000, 0)
	embedTime := oldTime.Add(1 * time.Hour)
	testTime := embedTime.Add(1 * time.Hour)
	ignoredTime := testTime.Add(1 * time.Hour)
	for path, modTime := range map[string]time.Time{
		filepath.Join(root, "go.mod"):                                      oldTime,
		filepath.Join(root, "internal/devtools/versions.json"):             embedTime,
		filepath.Join(root, "internal/devtools/versions_test.go"):          testTime,
		filepath.Join(root, "internal/devtools/node_modules/ignored.json"): ignoredTime,
	} {
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatalf("Chtimes(%s): %v", path, err)
		}
	}

	latest, ok, err := latestHarnessSourceModTime(root)
	if err != nil {
		t.Fatalf("latestHarnessSourceModTime() error = %v", err)
	}
	if !ok {
		t.Fatal("latestHarnessSourceModTime() ok = false")
	}
	if !latest.Equal(embedTime) {
		t.Fatalf("latest source time = %s, want embedded non-Go time %s", latest, embedTime)
	}
}

func TestBuildHarnessChangedAreaReportRecommendsPackageCommands(t *testing.T) {
	root := t.TempDir()

	oldCollect := harnessCollectChangedFiles
	oldList := harnessListGoPackages
	harnessCollectChangedFiles = func(context.Context, string) ([]harnessChangedFile, []checkDiagnostic) {
		return []harnessChangedFile{
			{Path: "cmd/scenery/main.go", Status: "modified"},
			{Path: "docs/schemas/example.schema.json", Status: "untracked"},
		}, nil
	}
	harnessListGoPackages = func(context.Context, string) ([]harnessPackageInfo, error) {
		return []harnessPackageInfo{{
			ImportPath: "example.com/oracle/cmd/scenery",
			Dir:        filepath.Join(root, "cmd", "scenery"),
			RelDir:     "cmd/scenery",
		}}, nil
	}
	t.Cleanup(func() {
		harnessCollectChangedFiles = oldCollect
		harnessListGoPackages = oldList
	})

	report := buildHarnessChangedAreaReport(context.Background(), root)
	if hasErrorDiagnostics(report.Diagnostics) {
		t.Fatalf("changed area diagnostics = %+v", report.Diagnostics)
	}
	var foundCLI bool
	for _, file := range report.ChangedFiles {
		if file.Path == "cmd/scenery/main.go" {
			foundCLI = true
			if file.Status != "modified" {
				t.Fatalf("status = %q, want modified", file.Status)
			}
			if file.Category != "cli" {
				t.Fatalf("category = %q, want cli", file.Category)
			}
			if file.Package != "example.com/oracle/cmd/scenery" {
				t.Fatalf("package = %q", file.Package)
			}
		}
	}
	if !foundCLI {
		t.Fatalf("changed files did not include cmd/scenery/main.go: %+v", report.ChangedFiles)
	}
	if !stringSliceContains(report.RecommendedCommands, "go test ./cmd/scenery") {
		t.Fatalf("recommended commands = %+v", report.RecommendedCommands)
	}
	if !stringSliceContains(report.RecommendedCommands, "scenery harness self --summary --write") {
		t.Fatalf("recommended commands = %+v", report.RecommendedCommands)
	}
	if !stringSliceContains(report.RiskFlags, "cli-contract") {
		t.Fatalf("risk flags = %+v", report.RiskFlags)
	}
	if !stringSliceContains(report.RiskFlags, "json-schema-contract") {
		t.Fatalf("risk flags = %+v", report.RiskFlags)
	}
}

func TestBuildHarnessChangedAreaReportRecommendsDevEventParity(t *testing.T) {
	root := t.TempDir()

	oldCollect := harnessCollectChangedFiles
	oldList := harnessListGoPackages
	harnessCollectChangedFiles = func(context.Context, string) ([]harnessChangedFile, []checkDiagnostic) {
		return []harnessChangedFile{
			{Path: "cmd/scenery/logs.go", Status: "modified"},
			{Path: "internal/devdash/dev_events.go", Status: "modified"},
		}, nil
	}
	harnessListGoPackages = func(context.Context, string) ([]harnessPackageInfo, error) {
		return []harnessPackageInfo{
			{ImportPath: "example.com/oracle/cmd/scenery", Dir: filepath.Join(root, "cmd", "scenery"), RelDir: "cmd/scenery"},
			{ImportPath: "example.com/oracle/internal/devdash", Dir: filepath.Join(root, "internal", "devdash"), RelDir: "internal/devdash"},
		}, nil
	}
	t.Cleanup(func() {
		harnessCollectChangedFiles = oldCollect
		harnessListGoPackages = oldList
	})

	report := buildHarnessChangedAreaReport(context.Background(), root)
	if !stringSliceContains(report.RecommendedCommands, "scenery logs --backend victoria --limit 500 --jsonl") {
		t.Fatalf("recommended commands = %+v", report.RecommendedCommands)
	}
	if !stringSliceContains(report.RiskFlags, "victoria-dev-event-read-path") {
		t.Fatalf("risk flags = %+v", report.RiskFlags)
	}
	if !stringSliceContains(report.RelevantDocs, "docs/plans/0056-dev-event-backend-cutover-and-parity.md") {
		t.Fatalf("relevant docs = %+v", report.RelevantDocs)
	}
}

func TestParseHarnessGoTestTimingReportsBudgets(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		`{"Action":"pass","Package":"example.com/oracle/cmd","Test":"TestSlow","Elapsed":0.7}`,
		`{"Action":"pass","Package":"example.com/oracle/cmd","Elapsed":2.3}`,
		`{"Action":"pass","Package":"example.com/oracle/internal","Elapsed":0.1}`,
	}, "\n")
	report := parseHarnessGoTestTiming([]byte(output), harnessSelfGoTestCommand(), 8*time.Second)

	if report.SchemaVersion != harnessTestTimingSchema {
		t.Fatalf("schema = %q", report.SchemaVersion)
	}
	if report.TotalSeconds != 8 {
		t.Fatalf("total seconds = %v, want 8", report.TotalSeconds)
	}
	if len(report.SlowTests) != 1 || report.SlowTests[0].Name != "TestSlow" {
		t.Fatalf("slow tests = %+v", report.SlowTests)
	}
	if report.Budgets.Mode != "observe-total" {
		t.Fatalf("budget mode = %q, want observe-total", report.Budgets.Mode)
	}
	var packageWarning, totalWarning bool
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Severity == "warning" && strings.Contains(diagnostic.Message, "package example.com/oracle/cmd took") {
			packageWarning = true
		}
		if diagnostic.Severity == "warning" && strings.Contains(diagnostic.Message, "full Go suite took 8.000s") {
			totalWarning = true
		}
	}
	if !packageWarning || !totalWarning {
		t.Fatalf("diagnostics = %+v, want package warning and total budget warning", report.Diagnostics)
	}
}

func TestParseHarnessGoTestTimingCanEnforceTotalBudget(t *testing.T) {
	t.Parallel()

	report := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommand(), 8*time.Second, harnessTestTimingBudgets{
		TotalSeconds:   7,
		PackageSeconds: 2,
		TestSeconds:    0.5,
		Mode:           "enforce-total",
	})
	if !hasErrorDiagnostics(report.Diagnostics) {
		t.Fatalf("diagnostics = %+v, want enforced total budget error", report.Diagnostics)
	}
}

func TestHarnessReleaseTimingBudgetAllowsCurrentSuiteVariance(t *testing.T) {
	t.Parallel()

	budgets := harnessTestTimingBudgetsForMode(harnessSelfModeRelease)
	if budgets.TotalSeconds != 20 {
		t.Fatalf("release total budget = %v, want 20", budgets.TotalSeconds)
	}
	if budgets.Mode != "enforce-total" {
		t.Fatalf("release budget mode = %q, want enforce-total", budgets.Mode)
	}
	report := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommand(), 15554*time.Millisecond, budgets)
	if hasErrorDiagnostics(report.Diagnostics) {
		t.Fatalf("diagnostics = %+v, want 15.554s release suite inside budget", report.Diagnostics)
	}
}

func TestWriteHarnessSelfOracleArtifacts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	resp := harnessSelfResponse{
		GeneratedAt: "2026-05-29T00:00:00Z",
		Mode:        harnessSelfModeDefault,
		Repo: harnessSelfRepo{
			Root:       root,
			ModulePath: "scenery.sh",
			GoModPath:  filepath.Join(root, "go.mod"),
		},
		Knowledge: harnessKnowledge{
			Entrypoints: []harnessKnowledgeFile{{Path: "AGENTS.md", Exists: true}},
			Schemas:     []harnessKnowledgeFile{{Path: "docs/schemas/scenery.agent_context.v1.schema.json", Exists: true}},
		},
		ChangedArea: &harnessChangedAreaReport{
			SchemaVersion:       harnessChangedAreaSchema,
			ChangedFiles:        []harnessChangedFile{{Path: "cmd/scenery/harness.go", Status: "modified", Category: "cli"}},
			RecommendedCommands: []string{"go test ./cmd/scenery"},
			RiskFlags:           []string{"harness-contract"},
		},
		TestTiming: &harnessTestTimingReport{
			SchemaVersion: harnessTestTimingSchema,
			Command:       harnessSelfGoTestCommand(),
			Budgets:       defaultHarnessTestTimingBudgets(),
		},
		Steps: []harnessStep{{
			Name:    "go tests",
			Command: harnessSelfGoTestCommand(),
			OK:      false,
			Error:   "exit status 1",
			Evidence: &harnessEvidence{
				SchemaVersion: harnessArtifactEvidenceSchema,
				Command:       harnessSelfGoTestCommand(),
				CWD:           root,
				StartedAt:     "2026-05-29T00:00:00Z",
				DurationMS:    1234,
				ExitCode:      intPtr(1),
				Artifacts: []harnessEvidenceArtifact{{
					Name:          "go-tests-stdout",
					Path:          ".scenery/harness/artifacts/20260529T000000Z/go-test.jsonl",
					SchemaVersion: "go.test.jsonl",
				}},
				ReproCommand: "cd " + root + " && go test -json ./...",
			},
		}},
		NextActions: []string{"scenery harness self --summary --write"},
	}

	if err := writeHarnessSelfOracleArtifacts(root, resp); err != nil {
		t.Fatalf("writeHarnessSelfOracleArtifacts: %v", err)
	}
	for _, rel := range []string{
		".scenery/harness/changed-area-latest.json",
		".scenery/harness/test-timing-latest.json",
		".scenery/harness/agent-context.json",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected %s: %v", rel, err)
		}
	}
	var contextPack harnessAgentContext
	data, err := os.ReadFile(filepath.Join(root, ".scenery", "harness", "agent-context.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &contextPack); err != nil {
		t.Fatalf("agent context JSON: %v", err)
	}
	if contextPack.SchemaVersion != harnessAgentContextSchema {
		t.Fatalf("agent context schema = %q", contextPack.SchemaVersion)
	}
	if !stringSliceContains(contextPack.RecommendedCommands, "go test ./cmd/scenery") {
		t.Fatalf("agent context commands = %+v", contextPack.RecommendedCommands)
	}
	if len(contextPack.FailingSteps) != 1 || contextPack.FailingSteps[0].FirstFileToRead != ".scenery/harness/test-timing-latest.json" {
		t.Fatalf("failing steps = %+v", contextPack.FailingSteps)
	}
	if !stringSliceContains(contextPack.RerunCommands, "cd "+root+" && go test -json ./...") {
		t.Fatalf("rerun commands = %+v", contextPack.RerunCommands)
	}
	if !stringSliceContains(contextPack.ChangedAreaRecommendedCommands, "go test ./cmd/scenery") {
		t.Fatalf("changed-area commands = %+v", contextPack.ChangedAreaRecommendedCommands)
	}
	if !stringSliceContains(contextPack.RiskClassification, "CLI contract") || !stringSliceContains(contextPack.RiskClassification, "release") {
		t.Fatalf("risk classification = %+v", contextPack.RiskClassification)
	}
	if len(contextPack.RecentFailedHarnessArtifacts) == 0 {
		t.Fatalf("recent failed artifacts = %+v", contextPack.RecentFailedHarnessArtifacts)
	}
}

func TestBuildHarnessAgentContextEmitsEmptyExecPlanArray(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	resp := harnessSelfResponse{
		SchemaVersion: "scenery.harness.self.v1",
		GeneratedAt:   "2026-06-08T00:00:00Z",
		Repo: harnessSelfRepo{
			Root:       root,
			ModulePath: "scenery.sh",
			GoModPath:  filepath.Join(root, "go.mod"),
		},
		Knowledge: harnessKnowledge{
			Entrypoints: []harnessKnowledgeFile{},
			Schemas:     []harnessKnowledgeFile{},
		},
		ChangedArea: &harnessChangedAreaReport{
			SchemaVersion:       harnessChangedAreaSchema,
			ChangedFiles:        []harnessChangedFile{},
			AffectedPackages:    []string{},
			RecommendedCommands: []string{},
			RelevantDocs:        []string{},
			RiskFlags:           []string{},
			Diagnostics:         []checkDiagnostic{},
		},
		Steps:       []harnessStep{},
		NextActions: []string{},
	}

	payload, err := json.Marshal(buildHarnessAgentContext(root, resp))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"relevant_active_execplans":null`) {
		t.Fatalf("relevant_active_execplans encoded as null: %s", payload)
	}
	if !strings.Contains(string(payload), `"relevant_active_execplans":[]`) {
		t.Fatalf("relevant_active_execplans did not encode as empty array: %s", payload)
	}
}

func TestRunHarnessKnowledgeStepValidAndInvalidFixtures(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
		writeTestAppFile(t, root, "docs/local-contract.md", "[self](harness-engineering.md)\n")
		writeTestAppFile(t, root, "docs/harness-engineering.md", "[schema](schemas/scenery.harness.self.v1.schema.json)\n")
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
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()

		root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
		writeTestAppFile(t, root, "docs/schemas/scenery.harness.self.v1.schema.json", `{`)
		writeTestAppFile(t, root, "docs/local-contract.md", "[missing](missing.md)\n")
		writeTestAppFile(t, root, "docs/plans/feature.md", "# Feature\n\nThis ExecPlan is a living document.\n\n## Purpose / Big Picture\n\nImplement a feature.\n")
		writeTestAppFile(t, root, "SKILL.md", "---\nname: scenery\n---\n\n# scenery\n\nOld skill.\n")
		writeTestAppFile(t, root, "docs/knowledge.json", `{
  "schema_version": "scenery.docs.index.v1",
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
      "path": "docs/missing.md",
      "title": "Missing",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Missing.",
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
      "schema_refs": ["docs/schemas/scenery.docs.index.v1.schema.json"]
    }
  ],
  "plans": {
    "active": "docs/plans/active.md",
    "completed": "docs/plans/completed.md"
  },
  "tech_debt": "docs/tech-debt.md"
}`)

		step := runHarnessKnowledgeStep(root)
		if step.OK {
			t.Fatalf("step ok = true, want false")
		}
		joined := diagnosticMessages(step.Diagnostics)
		for _, want := range []string{
			"schema file is not valid JSON",
			"local markdown link target does not exist",
			"missing required ExecPlan section",
			"SKILL.md is missing required capability mention",
			"indexed document does not exist",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("diagnostics did not include %q: %+v", want, step.Diagnostics)
			}
		}
	})
}

func TestArchitectureChecksIgnoreMarkdownLineCount(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	longMarkdown := strings.Repeat("line\n", architectureWarnLines+1)
	writeTestAppFile(t, root, "docs/plans/0060-long-plan.md", longMarkdown)
	writeTestAppFile(t, root, "docs/reference.md", longMarkdown)
	writeTestAppFile(t, root, "src/large.ts", strings.Repeat("const value = 1\n", architectureWarnLines+1))

	summary := architectureSummary{}
	diagnostics, err := checkArchitectureSource(root, &summary)
	if err != nil {
		t.Fatalf("checkArchitectureSource() error = %v", err)
	}

	var markdownWarned bool
	var sourceWarned bool
	for _, diag := range diagnostics {
		file := filepath.ToSlash(diag.File)
		if strings.HasSuffix(file, ".md") && strings.Contains(diag.Message, "over warning threshold") {
			markdownWarned = true
		}
		if strings.HasSuffix(file, "src/large.ts") && strings.Contains(diag.Message, "over warning threshold") {
			sourceWarned = true
		}
	}
	if markdownWarned {
		t.Fatalf("markdown produced architecture size diagnostic: %+v", diagnostics)
	}
	if !sourceWarned {
		t.Fatalf("ordinary long source file did not produce warning: %+v", diagnostics)
	}
}

func writeHarnessTestApp(t *testing.T, root, name, body string) {
	t.Helper()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"`+name+`","id":"`+name+`-id"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/"+name+"\n\ngo 1.26.3\n")
	writeTestAppFile(t, root, "svc/api.go", "package svc\n\nimport \"context\"\n\n//scenery:api public\nfunc Ping(context.Context) error { "+body+" }\n")
}

func writeHarnessSelfRepo(t *testing.T, schema string) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, "go.mod", "module scenery.sh\n\ngo 1.26.3\n")
	writeTestAppFile(t, root, "AGENTS.md", "See [harness](docs/harness-engineering.md).\n")
	writeTestAppFile(t, root, "SKILL.md", strings.Join(requiredSkillMentions, "\n")+"\n")
	writeTestAppFile(t, root, "PLAN.md", "See [docs](docs/index.md).\n")
	writeTestAppFile(t, root, "PLANS.md", validExecPlanStandardForTest())
	writeTestAppFile(t, root, "docs/index.md", "See [local](local-contract.md), [plans](plans/active.md), and [debt](tech-debt.md).\n")
	writeTestAppFile(t, root, "docs/local-contract.md", "Contract.\n")
	writeTestAppFile(t, root, "docs/environment.md", "Environment.\n")
	writeTestAppFile(t, root, "docs/environment.registry.json", `{"schema_version":"scenery.environment.registry.v1","variables":[{"name":"SCENERY_TEST_","match":"prefix","scope":"test_only","direction":"test_input","category":"tests","stability":"test_only","secret":false,"allowed_in":["docs","tests"],"owner":"scenery runtime","rationale":"Test-only controls.","preferred_surface":"tests","docs":["docs/environment.md"]}]}`)
	writeTestAppFile(t, root, "docs/app-development-cookbook.md", "Cookbook.\n")
	writeTestAppFile(t, root, "docs/ui-agent-contract.md", "UI contract.\n")
	writeTestAppFile(t, root, "docs/harness-engineering.md", "Harness.\n")
	writeTestAppFile(t, root, "docs/plans/active.md", "Active.\n")
	writeTestAppFile(t, root, "docs/plans/completed.md", "Completed.\n")
	writeTestAppFile(t, root, "docs/tech-debt.md", "Debt.\n")
	for _, path := range []string{
		"docs/schemas/scenery.config.v1.schema.json",
		"docs/schemas/scenery.build.latest.v1.schema.json",
		"docs/schemas/scenery.docs.index.v1.schema.json",
		"docs/schemas/scenery.environment.registry.v1.schema.json",
		"docs/schemas/scenery.harness.artifact.v1.schema.json",
		"docs/schemas/scenery.harness.self.v1.schema.json",
		"docs/schemas/scenery.harness.self.summary.v1.schema.json",
		"docs/schemas/scenery.harness.toolchain.v1.schema.json",
		"docs/schemas/scenery.harness.changed_area.v1.schema.json",
		"docs/schemas/scenery.harness.drift.v1.schema.json",
		"docs/schemas/scenery.harness.test_timing.v1.schema.json",
		"docs/schemas/scenery.harness.fixture_matrix.v1.schema.json",
		"docs/schemas/scenery.harness.schema_validation.v1.schema.json",
		"docs/schemas/scenery.agent_context.v1.schema.json",
		"docs/schemas/scenery.help.v1.schema.json",
		"docs/schemas/scenery.harness.result.v1.schema.json",
		"docs/schemas/scenery.harness.ui.v1.schema.json",
		"docs/schemas/scenery.harness.ui.dom.v1.schema.json",
		"docs/schemas/scenery.check.result.v1.schema.json",
		"docs/schemas/scenery.doctor.result.v1.schema.json",
		"docs/schemas/scenery.gen.manifest.v1.schema.json",
		"docs/schemas/scenery.inspect.app.v1.schema.json",
		"docs/schemas/scenery.inspect.build.v1.schema.json",
		"docs/schemas/scenery.inspect.docs.v1.schema.json",
		"docs/schemas/scenery.inspect.endpoints.v1.schema.json",
		"docs/schemas/scenery.inspect.harness.v1.schema.json",
		"docs/schemas/scenery.inspect.observability.v1.schema.json",
		"docs/schemas/scenery.inspect.metrics.v1.schema.json",
		"docs/schemas/scenery.inspect.paths.v1.schema.json",
		"docs/schemas/scenery.inspect.temporal.v1.schema.json",
		"docs/schemas/scenery.inspect.validation.v1.schema.json",
		"docs/schemas/scenery.inspect.routes.v1.schema.json",
		"docs/schemas/scenery.inspect.services.v1.schema.json",
		"docs/schemas/scenery.inspect.traces.v1.schema.json",
		"docs/schemas/scenery.task.inspect.v1.schema.json",
		"docs/schemas/scenery.task.list.v1.schema.json",
		"docs/schemas/scenery.task.graph.v1.schema.json",
		"docs/schemas/scenery.validation.graph.v1.schema.json",
		"docs/schemas/scenery.validation.inspect.v1.schema.json",
		"docs/schemas/scenery.validation.list.v1.schema.json",
		"docs/schemas/scenery.validation.plan.v1.schema.json",
		"docs/schemas/scenery.validation.result.v1.schema.json",
		"docs/schemas/scenery.traces.clear.v1.schema.json",
		"docs/schemas/scenery.dev.event.v1.schema.json",
		"docs/schemas/scenery.logs.event.v1.schema.json",
		"docs/schemas/scenery.logs.query.v1.schema.json",
		"docs/schemas/scenery.logs.tail.entry.v1.schema.json",
		"docs/schemas/scenery.metrics.labels.v1.schema.json",
		"docs/schemas/scenery.metrics.query.v1.schema.json",
		"docs/schemas/scenery.metrics.series.v1.schema.json",
		"docs/schemas/scenery.db.branch.registry.v2.schema.json",
		"docs/schemas/scenery.run.event.v1.schema.json",
		"docs/schemas/scenery.version.v1.schema.json",
		"docs/schemas/scenery.worker.manifest.v1.schema.json",
		"docs/schemas/scenery.worker.manifest.v2.schema.json",
		"docs/schemas/scenery.wire.capabilities.v1.schema.json",
	} {
		writeTestAppFile(t, root, path, schema)
	}
	writeTestAppFile(t, root, "docs/knowledge.json", `{
  "schema_version": "scenery.docs.index.v1",
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
      "schema_refs": ["docs/schemas/scenery.docs.index.v1.schema.json"]
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

func harnessArtifactExists(items []harnessArtifact, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return item.Exists
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func diagnosticMessages(diagnostics []checkDiagnostic) string {
	messages := make([]string, 0, len(diagnostics))
	for _, diag := range diagnostics {
		messages = append(messages, diag.Message)
	}
	return strings.Join(messages, "\n")
}
