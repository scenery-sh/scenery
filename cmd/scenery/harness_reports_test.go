package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		SchemaVersion: "scenery.harness.self.v1",
		OK:            true,
		GeneratedAt:   "2026-05-29T00:00:00Z",
		Mode:          harnessSelfModeDefault,
		Repo: harnessSelfRepo{
			Root:       root,
			ModulePath: "scenery.sh",
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
		Artifacts: []harnessArtifact{{Name: "self-harness", Path: ".scenery/harness/self-latest.json", Exists: true}},
	}
	report := buildHarnessSchemaValidationReport(root, resp)
	if len(report.Validated) != 13 {
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
	writeTestAppFile(t, root, "docs/environment.md", "Environment.\n\n`SCENERY_APP_ID`\n")
	writeTestAppFile(t, root, "docs/environment.registry.json", `{
  "schema_version": "scenery.environment.registry.v1",
  "variables": [
    {
      "name": "SCENERY_APP_ID",
      "match": "exact",
      "scope": "runtime",
      "direction": "injected",
      "category": "app.identity",
      "stability": "injected",
      "secret": false,
      "allowed_in": ["code", "docs", "tests"],
      "owner": "scenery runtime",
      "rationale": "Injected app identity.",
      "preferred_surface": ".scenery.json",
      "docs": ["docs/environment.md"]
    }
  ]
}`)
	writeTestAppFile(t, root, "cmd/scenery/env.go", "package main\n\nconst _ = \"SCENERY_APP_ID\"\n")
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
  "schema_version": "scenery.environment.registry.v1",
  "variables": [
    {
      "name": "SCENERY_TEST_",
      "match": "prefix",
      "scope": "test_only",
      "direction": "test_input",
      "category": "tests",
      "stability": "test_only",
      "secret": false,
      "allowed_in": ["docs", "tests"],
      "owner": "scenery runtime",
      "rationale": "Test-only controls.",
      "preferred_surface": "tests",
      "docs": ["docs/environment.md"]
    }
  ]
}`)
	writeTestAppFile(t, root, "cmd/scenery/env.go", "package main\n\nconst _ = \"SCENERY_FAKE_NEW_ENV\"\nconst _ = \"SCENERY_TEST_ONLY_EXAMPLE\"\n")

	report, diagnostics := buildHarnessEnvVarReport(root, nil)
	if !hasErrorDiagnostics(diagnostics) {
		t.Fatalf("expected env diagnostics, got report %+v", report)
	}
	if !diagnosticsContain(diagnostics, "SCENERY_FAKE_NEW_ENV") {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	if !diagnosticsContain(diagnostics, "test-only environment variable used by production code") {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestBuildHarnessEnvVarReportIgnoresClaudeWorktreeCopies(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "docs/environment.registry.json", `{
  "schema_version": "scenery.environment.registry.v1",
  "variables": [
    {
      "name": "SCENERY_TEST_",
      "match": "prefix",
      "scope": "test_only",
      "direction": "test_input",
      "category": "tests",
      "stability": "test_only",
      "secret": false,
      "allowed_in": ["docs", "tests"],
      "owner": "scenery runtime",
      "rationale": "Test-only controls.",
      "preferred_surface": "tests",
      "docs": ["docs/environment.md"]
    }
  ]
}`)
	writeTestAppFile(t, root, ".claude/worktrees/scratch/cmd/scenery/env.go", "package main\n\nconst _ = \"SCENERY_FAKE_NEW_ENV\"\n")
	writeTestAppFile(t, root, ".claude/worktrees/scratch/docs/plans/0061-env-harness.md", "`SCENERY_TEST_ONLY_EXAMPLE` is a historical test-only sample.\n")

	report, diagnostics := buildHarnessEnvVarReport(root, nil)
	if hasErrorDiagnostics(diagnostics) {
		t.Fatalf("unexpected env diagnostics: %+v\nreport: %+v", diagnostics, report)
	}
	for _, variable := range report.Variables {
		if strings.HasPrefix(variable.Name, "SCENERY_FAKE_") || variable.Name == "SCENERY_TEST_ONLY_EXAMPLE" {
			t.Fatalf("local Claude worktree variable leaked into report: %+v", variable)
		}
	}
}

func TestBuildHarnessToolchainPreflightReportRedactsSecretEnv(t *testing.T) {
	t.Setenv("SCENERY_AUTH_JWT_SECRET", "example")
	t.Setenv("SCENERY_DEV_CACHE_DIR", "cache")

	oldProbe := harnessProbeTool
	harnessProbeTool = func(_ context.Context, name, scope string, required bool, _ []string) harnessToolchainTool {
		return harnessToolchainTool{Name: name, Scope: scope, Required: required, Present: true}
	}
	t.Cleanup(func() { harnessProbeTool = oldProbe })

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "docs/environment.registry.json", `{
  "schema_version": "scenery.environment.registry.v1",
  "variables": [
    {
      "name": "SCENERY_AUTH_JWT_SECRET",
      "match": "exact",
      "scope": "runtime",
      "direction": "user_input",
      "category": "auth",
      "stability": "secret",
      "secret": true,
      "allowed_in": ["code", "docs", "tests"],
      "owner": "scenery runtime",
      "rationale": "JWT signing secret.",
      "preferred_surface": "secret manager or local env",
      "docs": ["docs/environment.md"]
    },
    {
      "name": "SCENERY_DEV_CACHE_DIR",
      "match": "exact",
      "scope": "runtime",
      "direction": "user_input",
      "category": "dev",
      "stability": "dev_escape_hatch",
      "secret": false,
      "allowed_in": ["code", "docs", "tests"],
      "owner": "scenery runtime",
      "rationale": "Cache override.",
      "preferred_surface": ".scenery.json",
      "docs": ["docs/environment.md"]
    }
  ]
}`)

	report := buildHarnessToolchainPreflightReport(context.Background(), root)
	values := map[string]string{}
	for _, item := range report.Env {
		values[item.Name] = item.Value
	}
	if values["SCENERY_AUTH_JWT_SECRET"] != "<redacted>" {
		t.Fatalf("secret env was not redacted: %+v", report.Env)
	}
	if values["SCENERY_DEV_CACHE_DIR"] != "cache" {
		t.Fatalf("non-secret env was redacted or missing: %+v", report.Env)
	}
}

func TestDirectOSEnvUsagesCatchProductionCode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, "cmd/scenery/bad.go", "package main\n\nimport \"os\"\n\nvar _ = os.Getenv(\"SCENERY_BAD\")\n")
	writeTestAppFile(t, root, "cmd/scenery/bad_test.go", "package main\n\nimport \"os\"\n\nvar _ = os.Getenv(\"SCENERY_TEST_OK\")\n")
	writeTestAppFile(t, root, "internal/envpolicy/lookup.go", "package envpolicy\n\nimport \"os\"\n\nfunc Get(k string) string { return os.Getenv(k) }\n")

	got := directOSEnvUsages(root)
	if len(got) != 1 || got[0] != "cmd/scenery/bad.go" {
		t.Fatalf("directOSEnvUsages() = %+v", got)
	}
}

func TestBuildHarnessEmbedReportChecksBinaryFreshnessCoverage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, "go.mod", "module scenery.sh\n")
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
	t.Parallel()

	useFakeBuildGoRunner(t)

	root := t.TempDir()
	for _, subject := range []string{"app", "routes", "services", "endpoints"} {
		writeTestAppFile(t, root, "docs/schemas/scenery.inspect."+subject+".v1.schema.json", `{"type":"object","required":["schema_version"],"properties":{"schema_version":{"const":"scenery.inspect.`+subject+`.v1"}}}`)
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
