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
	"time"

	"github.com/pbrazdil/onlava/internal/build"
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

	opts, err := parseHarnessSelfArgs([]string{"--repo-root", "/tmp/onlava", "--json", "--write", "--quick"})
	if err != nil {
		t.Fatalf("parseHarnessSelfArgs returned error: %v", err)
	}
	if opts.RepoRoot != "/tmp/onlava" {
		t.Fatalf("repo root = %q", opts.RepoRoot)
	}
	if !opts.JSON || !opts.Write {
		t.Fatalf("opts = %+v, want json and write", opts)
	}
	if opts.Mode != harnessSelfModeQuick {
		t.Fatalf("mode = %q, want quick", opts.Mode)
	}
	if _, err := parseHarnessSelfArgs([]string{"--quick", "--race"}); err == nil {
		t.Fatal("expected conflicting harness self modes to fail")
	}
}

func TestHarnessSelfGoTestCommandRunsFullSuiteInJSONMode(t *testing.T) {
	t.Parallel()

	got := strings.Join(harnessSelfGoTestCommand(), " ")
	if got != "go test -count=1 -json ./..." {
		t.Fatalf("harnessSelfGoTestCommand() = %q", got)
	}
}

func TestFindOnlavaRepoRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "cmd", "onlava")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/pbrazdil/onlava\n\ngo 1.26.3\n"), 0o644); err != nil {
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

func TestLatestHarnessSourceModTimeIncludesEmbeddedNonGoInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, "go.mod", "module github.com/pbrazdil/onlava\n")
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
			{Path: "cmd/onlava/main.go", Status: "modified"},
			{Path: "docs/schemas/example.schema.json", Status: "untracked"},
		}, nil
	}
	harnessListGoPackages = func(context.Context, string) ([]harnessPackageInfo, error) {
		return []harnessPackageInfo{{
			ImportPath: "example.com/oracle/cmd/onlava",
			Dir:        filepath.Join(root, "cmd", "onlava"),
			RelDir:     "cmd/onlava",
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
		if file.Path == "cmd/onlava/main.go" {
			foundCLI = true
			if file.Status != "modified" {
				t.Fatalf("status = %q, want modified", file.Status)
			}
			if file.Category != "cli" {
				t.Fatalf("category = %q, want cli", file.Category)
			}
			if file.Package != "example.com/oracle/cmd/onlava" {
				t.Fatalf("package = %q", file.Package)
			}
		}
	}
	if !foundCLI {
		t.Fatalf("changed files did not include cmd/onlava/main.go: %+v", report.ChangedFiles)
	}
	if !stringSliceContains(report.RecommendedCommands, "go test -count=1 ./cmd/onlava") {
		t.Fatalf("recommended commands = %+v", report.RecommendedCommands)
	}
	if !stringSliceContains(report.RecommendedCommands, "onlava harness self --json --write") {
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
			{Path: "cmd/onlava/logs.go", Status: "modified"},
			{Path: "internal/devdash/dev_events.go", Status: "modified"},
		}, nil
	}
	harnessListGoPackages = func(context.Context, string) ([]harnessPackageInfo, error) {
		return []harnessPackageInfo{
			{ImportPath: "example.com/oracle/cmd/onlava", Dir: filepath.Join(root, "cmd", "onlava"), RelDir: "cmd/onlava"},
			{ImportPath: "example.com/oracle/internal/devdash", Dir: filepath.Join(root, "internal", "devdash"), RelDir: "internal/devdash"},
		}, nil
	}
	t.Cleanup(func() {
		harnessCollectChangedFiles = oldCollect
		harnessListGoPackages = oldList
	})

	report := buildHarnessChangedAreaReport(context.Background(), root)
	if !stringSliceContains(report.RecommendedCommands, "onlava logs --session current --backend victoria --limit 500 --jsonl") {
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
			ModulePath: "github.com/pbrazdil/onlava",
			GoModPath:  filepath.Join(root, "go.mod"),
		},
		Knowledge: harnessKnowledge{
			Entrypoints: []harnessKnowledgeFile{{Path: "AGENTS.md", Exists: true}},
			Schemas:     []harnessKnowledgeFile{{Path: "docs/schemas/onlava.agent_context.v1.schema.json", Exists: true}},
		},
		ChangedArea: &harnessChangedAreaReport{
			SchemaVersion:       harnessChangedAreaSchema,
			ChangedFiles:        []harnessChangedFile{{Path: "cmd/onlava/harness.go", Status: "modified", Category: "cli"}},
			RecommendedCommands: []string{"go test -count=1 ./cmd/onlava"},
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
					Path:          ".onlava/harness/artifacts/20260529T000000Z/go-test.jsonl",
					SchemaVersion: "go.test.jsonl",
				}},
				ReproCommand: "cd " + root + " && go test -count=1 -json ./...",
			},
		}},
		NextActions: []string{"onlava harness self --json --write"},
	}

	if err := writeHarnessSelfOracleArtifacts(root, resp); err != nil {
		t.Fatalf("writeHarnessSelfOracleArtifacts: %v", err)
	}
	for _, rel := range []string{
		".onlava/harness/changed-area-latest.json",
		".onlava/harness/test-timing-latest.json",
		".onlava/harness/agent-context.json",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected %s: %v", rel, err)
		}
	}
	var contextPack harnessAgentContext
	data, err := os.ReadFile(filepath.Join(root, ".onlava", "harness", "agent-context.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &contextPack); err != nil {
		t.Fatalf("agent context JSON: %v", err)
	}
	if contextPack.SchemaVersion != harnessAgentContextSchema {
		t.Fatalf("agent context schema = %q", contextPack.SchemaVersion)
	}
	if !stringSliceContains(contextPack.RecommendedCommands, "go test -count=1 ./cmd/onlava") {
		t.Fatalf("agent context commands = %+v", contextPack.RecommendedCommands)
	}
	if len(contextPack.FailingSteps) != 1 || contextPack.FailingSteps[0].FirstFileToRead != ".onlava/harness/test-timing-latest.json" {
		t.Fatalf("failing steps = %+v", contextPack.FailingSteps)
	}
	if !stringSliceContains(contextPack.RerunCommands, "cd "+root+" && go test -count=1 -json ./...") {
		t.Fatalf("rerun commands = %+v", contextPack.RerunCommands)
	}
	if !stringSliceContains(contextPack.ChangedAreaRecommendedCommands, "go test -count=1 ./cmd/onlava") {
		t.Fatalf("changed-area commands = %+v", contextPack.ChangedAreaRecommendedCommands)
	}
	if !stringSliceContains(contextPack.RiskClassification, "CLI contract") || !stringSliceContains(contextPack.RiskClassification, "release") {
		t.Fatalf("risk classification = %+v", contextPack.RiskClassification)
	}
	if len(contextPack.RecentFailedHarnessArtifacts) == 0 {
		t.Fatalf("recent failed artifacts = %+v", contextPack.RecentFailedHarnessArtifacts)
	}
}

func TestValidateHarnessJSONSchemaFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	schemaPath := filepath.Join(root, "schema.json")
	if err := os.WriteFile(filepath.Join(root, "external.json"), []byte(`{
  "type": "object",
  "$defs": {
    "owner": {
      "type": "object",
      "required": ["name"],
      "properties": {
        "name": {"type": "string"}
      },
      "additionalProperties": false
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath, []byte(`{
  "type": "object",
  "required": ["schema_version", "items", "owner"],
  "properties": {
    "schema_version": {"const": "example.v1"},
    "owner": {"$ref": "external.json#/$defs/owner"},
    "items": {
      "type": "array",
      "items": {"$ref": "#/$defs/item"}
    }
  },
  "additionalProperties": false,
  "$defs": {
    "item": {
      "type": "object",
      "required": ["name", "count"],
      "properties": {
        "name": {"type": "string"},
        "count": {"type": "integer", "minimum": 1}
      },
      "additionalProperties": false
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	valid := map[string]any{
		"schema_version": "example.v1",
		"owner": map[string]any{
			"name": "agent",
		},
		"items": []map[string]any{{
			"name":  "alpha",
			"count": 1,
		}},
	}
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, valid); len(diagnostics) != 0 {
		t.Fatalf("valid diagnostics = %+v", diagnostics)
	}

	invalid := map[string]any{
		"schema_version": "example.v2",
		"owner": map[string]any{
			"extra": true,
		},
		"items": []map[string]any{{
			"name":  "alpha",
			"count": 0,
			"extra": true,
		}},
	}
	diagnostics := validateHarnessJSONSchemaFile(schemaPath, invalid)
	joined := strings.Join(diagnostics, "\n")
	for _, want := range []string{"does not equal const", "less than minimum", "additional property"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diagnostics %q missing %q", joined, want)
		}
	}
}

func TestBuildHarnessSchemaValidationReport(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	resp := harnessSelfResponse{
		SchemaVersion: "onlava.harness.self.v1",
		OK:            true,
		GeneratedAt:   "2026-05-29T00:00:00Z",
		Mode:          harnessSelfModeDefault,
		Repo: harnessSelfRepo{
			Root:       root,
			ModulePath: "github.com/pbrazdil/onlava",
			GoModPath:  filepath.Join(root, "go.mod"),
		},
		Knowledge: buildHarnessSelfKnowledge(root),
		ChangedArea: &harnessChangedAreaReport{
			SchemaVersion: harnessChangedAreaSchema,
		},
		TestTiming: &harnessTestTimingReport{
			SchemaVersion: harnessTestTimingSchema,
			Command:       harnessSelfGoTestCommand(),
			Budgets:       defaultHarnessTestTimingBudgets(),
		},
		Steps:     []harnessStep{{Name: "test", Command: []string{"true"}, OK: true}},
		Artifacts: []harnessArtifact{{Name: "self-harness", Path: ".onlava/harness/self-latest.json", Exists: true}},
	}
	report := buildHarnessSchemaValidationReport(root, resp)
	if len(report.Validated) != 11 {
		t.Fatalf("validated = %+v", report.Validated)
	}
	if hasErrorDiagnostics(report.Diagnostics) {
		t.Fatalf("schema diagnostics = %+v", report.Diagnostics)
	}
}

func TestBuildHarnessToolchainPreflightReport(t *testing.T) {
	oldProbe := harnessProbeTool
	harnessProbeTool = func(_ context.Context, name, scope string, required bool, _ []string) harnessToolchainTool {
		tool := harnessToolchainTool{
			Name:     name,
			Scope:    scope,
			Required: required,
			Present:  true,
			Path:     "/test/bin/" + name,
			Version:  name + " version test",
		}
		return tool
	}
	t.Cleanup(func() { harnessProbeTool = oldProbe })

	report := buildHarnessToolchainPreflightReport(context.Background(), t.TempDir())
	if report.SchemaVersion != harnessToolchainSchema {
		t.Fatalf("schema = %q", report.SchemaVersion)
	}
	var foundGo bool
	for _, tool := range report.Tools {
		if tool.Name == "go" {
			foundGo = true
			if !tool.Present || tool.Version == "" {
				t.Fatalf("go tool = %+v", tool)
			}
		}
	}
	if !foundGo {
		t.Fatalf("tools = %+v", report.Tools)
	}
}

func TestBuildHarnessDriftReport(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "docs/environment.md", "Environment.\n\n`ONLAVA_APP_ID`\n")
	writeTestAppFile(t, root, "docs/environment.registry.json", `{
  "schema_version": "onlava.environment.registry.v1",
  "variables": [
    {
      "name": "ONLAVA_APP_ID",
      "match": "exact",
      "scope": "runtime",
      "direction": "injected",
      "category": "app.identity",
      "stability": "injected",
      "secret": false,
      "allowed_in": ["code", "docs", "tests"],
      "owner": "onlava runtime",
      "rationale": "Injected app identity.",
      "preferred_surface": ".onlava.json",
      "docs": ["docs/environment.md"]
    }
  ]
}`)
	writeTestAppFile(t, root, "cmd/onlava/env.go", "package main\n\nconst _ = \"ONLAVA_APP_ID\"\n")
	writeTestAppFile(t, root, "internal/build/build.go", "package build\n\nconst _ = `.env .DS_Store __MACOSX node_modules coverage`\n")

	report := buildHarnessDriftReport(context.Background(), root)
	if report.SchemaVersion != harnessDriftSchema {
		t.Fatalf("schema = %q", report.SchemaVersion)
	}
	if len(report.CLI.Commands) == 0 {
		t.Fatalf("expected CLI contract commands")
	}
	if len(report.Env.Variables) == 0 {
		t.Fatalf("expected env var inventory")
	}
	if hasErrorDiagnostics(report.Diagnostics) {
		t.Fatalf("drift diagnostics = %+v", report.Diagnostics)
	}
}

func TestBuildHarnessEnvVarReportInvalidRuntimeEnvDiagnostics(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "docs/environment.registry.json", `{
  "schema_version": "onlava.environment.registry.v1",
  "variables": [
    {
      "name": "ONLAVA_TEST_",
      "match": "prefix",
      "scope": "test_only",
      "direction": "test_input",
      "category": "tests",
      "stability": "test_only",
      "secret": false,
      "allowed_in": ["docs", "tests"],
      "owner": "onlava runtime",
      "rationale": "Test-only controls.",
      "preferred_surface": "tests",
      "docs": ["docs/environment.md"]
    }
  ]
}`)
	writeTestAppFile(t, root, "cmd/onlava/env.go", "package main\n\nconst _ = \"ONLAVA_FAKE_NEW_ENV\"\nconst _ = \"ONLAVA_TEST_ONLY_EXAMPLE\"\n")

	report, diagnostics := buildHarnessEnvVarReport(root, nil)
	if !hasErrorDiagnostics(diagnostics) {
		t.Fatalf("expected env diagnostics, got report %+v", report)
	}
	if !diagnosticsContain(diagnostics, "ONLAVA_FAKE_NEW_ENV") {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	if !diagnosticsContain(diagnostics, "test-only environment variable used by production code") {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestBuildHarnessToolchainPreflightReportRedactsSecretEnv(t *testing.T) {
	t.Setenv("ONLAVA_AUTH_JWT_SECRET", "example")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", "cache")

	oldProbe := harnessProbeTool
	harnessProbeTool = func(_ context.Context, name, scope string, required bool, _ []string) harnessToolchainTool {
		return harnessToolchainTool{Name: name, Scope: scope, Required: required, Present: true}
	}
	t.Cleanup(func() { harnessProbeTool = oldProbe })

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "docs/environment.registry.json", `{
  "schema_version": "onlava.environment.registry.v1",
  "variables": [
    {
      "name": "ONLAVA_AUTH_JWT_SECRET",
      "match": "exact",
      "scope": "runtime",
      "direction": "user_input",
      "category": "auth",
      "stability": "secret",
      "secret": true,
      "allowed_in": ["code", "docs", "tests"],
      "owner": "onlava runtime",
      "rationale": "JWT signing secret.",
      "preferred_surface": "secret manager or local env",
      "docs": ["docs/environment.md"]
    },
    {
      "name": "ONLAVA_DEV_CACHE_DIR",
      "match": "exact",
      "scope": "runtime",
      "direction": "user_input",
      "category": "dev",
      "stability": "dev_escape_hatch",
      "secret": false,
      "allowed_in": ["code", "docs", "tests"],
      "owner": "onlava runtime",
      "rationale": "Cache override.",
      "preferred_surface": ".onlava.json",
      "docs": ["docs/environment.md"]
    }
  ]
}`)

	report := buildHarnessToolchainPreflightReport(context.Background(), root)
	values := map[string]string{}
	for _, item := range report.Env {
		values[item.Name] = item.Value
	}
	if values["ONLAVA_AUTH_JWT_SECRET"] != "<redacted>" {
		t.Fatalf("secret env was not redacted: %+v", report.Env)
	}
	if values["ONLAVA_DEV_CACHE_DIR"] != "cache" {
		t.Fatalf("non-secret env was redacted or missing: %+v", report.Env)
	}
}

func TestDirectOSEnvUsagesCatchProductionCode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, "cmd/onlava/bad.go", "package main\n\nimport \"os\"\n\nvar _ = os.Getenv(\"ONLAVA_BAD\")\n")
	writeTestAppFile(t, root, "cmd/onlava/bad_test.go", "package main\n\nimport \"os\"\n\nvar _ = os.Getenv(\"ONLAVA_TEST_OK\")\n")
	writeTestAppFile(t, root, "internal/envpolicy/lookup.go", "package envpolicy\n\nimport \"os\"\n\nfunc Get(k string) string { return os.Getenv(k) }\n")

	got := directOSEnvUsages(root)
	if len(got) != 1 || got[0] != "cmd/onlava/bad.go" {
		t.Fatalf("directOSEnvUsages() = %+v", got)
	}
}

func TestBuildHarnessEmbedReportChecksBinaryFreshnessCoverage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, "go.mod", "module github.com/pbrazdil/onlava\n")
	writeTestAppFile(t, root, "internal/devtools/versions.go", "package devtools\n\nimport \"embed\"\n\n//go:embed versions.json\nvar _ embed.FS\n")
	writeTestAppFile(t, root, "internal/devtools/versions.json", "{}\n")

	report, diagnostics := buildHarnessEmbedReport(root, nil)
	if hasErrorDiagnostics(diagnostics) {
		t.Fatalf("embed diagnostics = %+v", diagnostics)
	}
	if len(report.Embeds) != 1 {
		t.Fatalf("embeds = %+v", report.Embeds)
	}
	if !report.Embeds[0].CoveredByBinaryFreshness {
		t.Fatalf("embed not covered: %+v", report.Embeds[0])
	}
}

func TestBuildHarnessFixtureMatrixReport(t *testing.T) {
	useFakeBuildGoRunner(t)

	root := t.TempDir()
	for _, subject := range []string{"app", "routes", "services", "endpoints"} {
		writeTestAppFile(t, root, "docs/schemas/onlava.inspect."+subject+".v1.schema.json", `{"type":"object","required":["schema_version"],"properties":{"schema_version":{"const":"onlava.inspect.`+subject+`.v1"}}}`)
	}
	writeHarnessTestApp(t, filepath.Join(root, "testdata", "apps", "basic"), "basic", "return nil")

	report := buildHarnessFixtureMatrixReport(context.Background(), root)
	if report.SchemaVersion != harnessFixtureMatrixSchema {
		t.Fatalf("schema = %q", report.SchemaVersion)
	}
	if len(report.Fixtures) != 1 {
		t.Fatalf("fixtures = %+v", report.Fixtures)
	}
	if !report.Fixtures[0].Check || !report.Fixtures[0].Inspect["app"] || !report.Fixtures[0].Inspect["routes"] || !report.Fixtures[0].Inspect["services"] || !report.Fixtures[0].Inspect["endpoints"] {
		t.Fatalf("fixture result = %+v", report.Fixtures[0])
	}
}

func TestRunHarnessKnowledgeStepValidAndInvalidFixtures(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

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
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()

		root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
		writeTestAppFile(t, root, "docs/schemas/onlava.harness.self.v1.schema.json", `{`)
		writeTestAppFile(t, root, "docs/local-contract.md", "[missing](missing.md)\n")
		writeTestAppFile(t, root, "docs/plans/feature.md", "# Feature\n\nThis ExecPlan is a living document.\n\n## Purpose / Big Picture\n\nImplement a feature.\n")
		writeTestAppFile(t, root, "SKILL.md", "---\nname: onlava\n---\n\n# onlava\n\nOld skill.\n")
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
      "path": "docs/missing.md",
      "title": "Missing",
      "owner": "onlava maintainers",
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

func TestRunOnlavaHarnessJSONSuccessWritesLatest(t *testing.T) {
	useFakeBuildGoRunner(t)

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
	if payload.Steps[0].Evidence == nil || payload.Steps[0].Evidence.ReproCommand == "" {
		t.Fatalf("expected step evidence with repro command: %+v", payload.Steps[0])
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

	var inspectOut bytes.Buffer
	if err := runOnlavaInspect([]string{"harness", "--app-root", root, "--json"}, &inspectOut); err != nil {
		t.Fatalf("inspect harness: %v\n%s", err, inspectOut.String())
	}
	var inspectPayload inspectHarnessResponse
	if err := json.Unmarshal(inspectOut.Bytes(), &inspectPayload); err != nil {
		t.Fatalf("decode inspect harness: %v\n%s", err, inspectOut.String())
	}
	if inspectPayload.SchemaVersion != inspectHarnessSchema || len(inspectPayload.Evidence) == 0 {
		t.Fatalf("inspect harness payload = %+v", inspectPayload)
	}
}

func TestRunOnlavaHarnessJSONFailureIncludesNextAction(t *testing.T) {
	restoreRunner := build.SetGoRunnerForTesting(func(_ context.Context, _ string, args ...string) error {
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			return nil
		}
		if len(args) >= 4 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" {
			return errors.New("go build -buildvcs=false failed: exit status 1\nsvc/api.go:6:37: undefined: MissingSymbol")
		}
		return errors.New("unexpected fake go command: " + strings.Join(args, " "))
	})
	t.Cleanup(restoreRunner)

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

func TestRunHarnessParallelDevStep(t *testing.T) {
	prev := runHarnessParallelDevCheckFunc
	t.Cleanup(func() { runHarnessParallelDevCheckFunc = prev })
	runHarnessParallelDevCheckFunc = func(context.Context) (map[string]any, []checkDiagnostic, error) {
		return map[string]any{
			"sessions":  2,
			"databases": 2,
		}, nil, nil
	}

	step := runHarnessParallelDevStep(context.Background(), t.TempDir())
	if !step.OK {
		t.Fatalf("parallel dev step failed: error=%s diagnostics=%+v summary=%+v", step.Error, step.Diagnostics, step.Summary)
	}
	if got, _ := step.Summary["sessions"].(int); got != 2 {
		t.Fatalf("sessions summary = %v, want 2", step.Summary["sessions"])
	}
	if got, _ := step.Summary["databases"].(int); got != 2 {
		t.Fatalf("databases summary = %v, want 2", step.Summary["databases"])
	}
}

func writeHarnessTestApp(t *testing.T, root, name, body string) {
	t.Helper()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"`+name+`","id":"`+name+`-id"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/"+name+"\n\ngo 1.26.3\n")
	writeTestAppFile(t, root, "svc/api.go", "package svc\n\nimport \"context\"\n\n//onlava:api public\nfunc Ping(context.Context) error { "+body+" }\n")
}

func writeHarnessSelfRepo(t *testing.T, schema string) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, "go.mod", "module github.com/pbrazdil/onlava\n\ngo 1.26.3\n")
	writeTestAppFile(t, root, "AGENTS.md", "See [harness](docs/harness-engineering.md).\n")
	writeTestAppFile(t, root, "SKILL.md", strings.Join(requiredSkillMentions, "\n")+"\n")
	writeTestAppFile(t, root, "PLAN.md", "See [docs](docs/index.md).\n")
	writeTestAppFile(t, root, "PLANS.md", validExecPlanStandardForTest())
	writeTestAppFile(t, root, "docs/index.md", "See [local](local-contract.md), [plans](plans/active.md), and [debt](tech-debt.md).\n")
	writeTestAppFile(t, root, "docs/local-contract.md", "Contract.\n")
	writeTestAppFile(t, root, "docs/environment.md", "Environment.\n")
	writeTestAppFile(t, root, "docs/environment.registry.json", `{"schema_version":"onlava.environment.registry.v1","variables":[{"name":"ONLAVA_TEST_","match":"prefix","scope":"test_only","direction":"test_input","category":"tests","stability":"test_only","secret":false,"allowed_in":["docs","tests"],"owner":"onlava runtime","rationale":"Test-only controls.","preferred_surface":"tests","docs":["docs/environment.md"]}]}`)
	writeTestAppFile(t, root, "docs/app-development-cookbook.md", "Cookbook.\n")
	writeTestAppFile(t, root, "docs/ui-agent-contract.md", "UI contract.\n")
	writeTestAppFile(t, root, "docs/harness-engineering.md", "Harness.\n")
	writeTestAppFile(t, root, "docs/plans/active.md", "Active.\n")
	writeTestAppFile(t, root, "docs/plans/completed.md", "Completed.\n")
	writeTestAppFile(t, root, "docs/tech-debt.md", "Debt.\n")
	for _, path := range []string{
		"docs/schemas/onlava.config.v1.schema.json",
		"docs/schemas/onlava.build.latest.v1.schema.json",
		"docs/schemas/onlava.docs.index.v1.schema.json",
		"docs/schemas/onlava.environment.registry.v1.schema.json",
		"docs/schemas/onlava.harness.artifact.v1.schema.json",
		"docs/schemas/onlava.harness.self.v1.schema.json",
		"docs/schemas/onlava.harness.toolchain.v1.schema.json",
		"docs/schemas/onlava.harness.changed_area.v1.schema.json",
		"docs/schemas/onlava.harness.drift.v1.schema.json",
		"docs/schemas/onlava.harness.test_timing.v1.schema.json",
		"docs/schemas/onlava.harness.fixture_matrix.v1.schema.json",
		"docs/schemas/onlava.harness.schema_validation.v1.schema.json",
		"docs/schemas/onlava.agent_context.v1.schema.json",
		"docs/schemas/onlava.harness.result.v1.schema.json",
		"docs/schemas/onlava.harness.ui.v1.schema.json",
		"docs/schemas/onlava.harness.ui.dom.v1.schema.json",
		"docs/schemas/onlava.check.result.v1.schema.json",
		"docs/schemas/onlava.doctor.result.v1.schema.json",
		"docs/schemas/onlava.gen.manifest.v1.schema.json",
		"docs/schemas/onlava.inspect.app.v1.schema.json",
		"docs/schemas/onlava.inspect.build.v1.schema.json",
		"docs/schemas/onlava.inspect.docs.v1.schema.json",
		"docs/schemas/onlava.inspect.endpoints.v1.schema.json",
		"docs/schemas/onlava.inspect.harness.v1.schema.json",
		"docs/schemas/onlava.inspect.metrics.v1.schema.json",
		"docs/schemas/onlava.inspect.paths.v1.schema.json",
		"docs/schemas/onlava.inspect.temporal.v1.schema.json",
		"docs/schemas/onlava.inspect.routes.v1.schema.json",
		"docs/schemas/onlava.inspect.services.v1.schema.json",
		"docs/schemas/onlava.inspect.traces.v1.schema.json",
		"docs/schemas/onlava.task.inspect.v1.schema.json",
		"docs/schemas/onlava.task.list.v1.schema.json",
		"docs/schemas/onlava.task.graph.v1.schema.json",
		"docs/schemas/onlava.traces.clear.v1.schema.json",
		"docs/schemas/onlava.dev.event.v1.schema.json",
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
