package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/envpolicy"
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

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`,
		"scenery.agent_context.schema.json",
		"scenery.approval-token.schema.json",
		"scenery.approval-trust.schema.json",
		"scenery.assistant.init.schema.json",
		"scenery.assistant.sync.schema.json",
		"scenery.build.desktop.schema.json",
		"scenery.build.result.schema.json",
		"scenery.deploy.registry.schema.json",
		"scenery.deploy.status.schema.json",
		"scenery.doctor.result.schema.json",
		"scenery.environment.registry.schema.json",
		"scenery.harness.artifact.schema.json",
		"scenery.harness.changed_area.schema.json",
		"scenery.harness.schema_validation.schema.json",
		"scenery.harness.self.summary.schema.json",
		"scenery.harness.self.schema.json",
		"scenery.harness.test_timing.schema.json",
		"scenery.help.schema.json",
		"scenery.inspect.docs.schema.json",
		"scenery.inspect.harness.schema.json",
		"scenery.snapshot.load.schema.json",
		"scenery.snapshot.manifest.schema.json",
		"scenery.snapshot.save.schema.json",
		"scenery.snapshot.verify.schema.json",
		"scenery.storage.object.schema.json",
		"scenery.storage.list.schema.json",
		"scenery.storage.delete.schema.json",
		"scenery.storage.cleanup.schema.json",
		"scenery.storage.inspect.schema.json",
		"scenery.telemetry.schema.json",
		"scenery.version.schema.json",
		"scenery.worktree.upgrade.schema.json",
	)
	resp := harnessSelfResponse{
		PayloadIdentity: newCLIPayloadIdentity("scenery.harness.self"),
		OK:              true,
		GeneratedAt:     "2026-05-29T00:00:00Z",
		Mode:            harnessSelfModeDefault,
		Repo: harnessSelfRepo{
			Root:       root,
			ModulePath: "scenery.sh",
			GoModPath:  filepath.Join(root, "go.mod"),
		},
		Knowledge: buildHarnessSelfKnowledge(root),
		ChangedArea: &harnessChangedAreaReport{
			PayloadIdentity: newCLIPayloadIdentity(harnessChangedAreaKind),
		},
		TestTiming: &harnessTestTimingReport{
			PayloadIdentity: newCLIPayloadIdentity(harnessTestTimingKind),
			Command:         harnessSelfGoTestCommand(),
			Budgets:         defaultHarnessTestTimingBudgets(),
		},
		Steps:     []harnessStep{{Name: "test", Command: []string{"true"}, OK: true}},
		Artifacts: []harnessArtifact{{Name: "self-harness", Path: ".scenery/harness/self-latest.json", Exists: true}},
	}
	reads := 0
	report := buildHarnessSchemaValidationReportWithReader(root, resp, func(target any, args ...string) error {
		reads++
		return json.Unmarshal([]byte(`{}`), target)
	})
	if reads != 4 {
		t.Fatalf("product schema reads = %d, want 4", reads)
	}
	if len(report.Validated) != 33 {
		t.Fatalf("validated = %+v", report.Validated)
	}
	if hasErrorDiagnostics(report.Diagnostics) {
		t.Fatalf("schema diagnostics = %+v", report.Diagnostics)
	}
}

func TestWriteHarnessSelfRepoWritesOnlyRequestedSchemas(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"type":"object"}`, "scenery.help.schema.json")
	matches, err := filepath.Glob(filepath.Join(root, "docs", "schemas", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("fixture schemas = %v, want docs index plus requested help schema", matches)
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
	if report.Kind != harnessToolchainKind || report.SchemaRevision != newCLIPayloadIdentity(harnessToolchainKind).SchemaRevision {
		t.Fatalf("identity = %q %q", report.Kind, report.SchemaRevision)
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
  "kind": "`+envpolicy.Kind+`",
  "schema_revision": "`+envpolicy.SchemaRevision+`",
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
	writeTestAppFile(t, root, "internal/build/source.go", "package build\n\nconst _ = `.env .DS_Store __MACOSX node_modules coverage`\n")

	report := buildHarnessDriftReportWithReaders(context.Background(), root, func(_ string, diagnostics []checkDiagnostic) (harnessCLIContractReport, []checkDiagnostic) {
		return harnessCLIContractReport{Commands: []harnessCLIContractCommand{{Name: "version", Usage: true, Smoke: true}}}, diagnostics
	}, func(_ context.Context, _ string, diagnostics []checkDiagnostic) (harnessArtifactHygieneReport, []checkDiagnostic) {
		return harnessArtifactHygieneReport{}, diagnostics
	})
	if report.Kind != harnessDriftKind || report.SchemaRevision != newCLIPayloadIdentity(harnessDriftKind).SchemaRevision {
		t.Fatalf("identity = %q %q", report.Kind, report.SchemaRevision)
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
  "kind": "`+envpolicy.Kind+`",
  "schema_revision": "`+envpolicy.SchemaRevision+`",
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

func diagnosticsContain(diagnostics []checkDiagnostic, needle string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, needle) {
			return true
		}
	}
	return false
}

func TestBuildHarnessEnvVarReportIgnoresClaudeWorktreeCopies(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "docs/environment.registry.json", `{
  "kind": "`+envpolicy.Kind+`",
  "schema_revision": "`+envpolicy.SchemaRevision+`",
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
	t.Setenv("JWT_SECRET", "example")
	t.Setenv("SCENERY_DEV_CACHE_DIR", "cache")

	oldProbe := harnessProbeTool
	harnessProbeTool = func(_ context.Context, name, scope string, required bool, _ []string) harnessToolchainTool {
		return harnessToolchainTool{Name: name, Scope: scope, Required: required, Present: true}
	}
	t.Cleanup(func() { harnessProbeTool = oldProbe })

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeTestAppFile(t, root, "docs/environment.registry.json", `{
  "kind": "`+envpolicy.Kind+`",
  "schema_revision": "`+envpolicy.SchemaRevision+`",
  "variables": [
    {
      "name": "JWT_SECRET",
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
	if values["JWT_SECRET"] != "<redacted>" {
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
	writeTestAppFile(t, root, "cmd/scenery/dashboard_static/embed.go", "package dashboardstatic\n\nimport \"embed\"\n\n//go:embed dist\nvar _ embed.FS\n")
	writeTestAppFile(t, root, "cmd/scenery/dashboard_static/dist/index.html", "<!doctype html>\n")

	report, diagnostics := buildHarnessEmbedReport(root, nil)
	if hasErrorDiagnostics(diagnostics) {
		t.Fatalf("embed diagnostics = %+v", diagnostics)
	}
	if len(report.Embeds) != 2 {
		t.Fatalf("embeds = %+v", report.Embeds)
	}
	for _, embed := range report.Embeds {
		if !embed.CoveredByBinaryFreshness {
			t.Fatalf("embed not covered: %+v", embed)
		}
	}
}

func TestBuildHarnessFixtureMatrixReport(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, subject := range []string{"app", "routes", "services", "endpoints"} {
		identity := newCLIPayloadIdentity("scenery.inspect." + subject)
		writeTestAppFile(t, root, "docs/schemas/scenery.inspect."+subject+".schema.json", `{"type":"object","required":["kind","schema_revision"],"properties":{"kind":{"const":"`+identity.Kind+`"},"schema_revision":{"const":"`+identity.SchemaRevision+`"}}}`)
	}
	writeTestAppFile(t, root, "testdata/apps/basic/.scenery.json", `{}`)

	var commands []string
	report := buildHarnessFixtureMatrixReportWithRunner(context.Background(), root, func(_ context.Context, _ string, output io.Writer, args ...string) error {
		commands = append(commands, strings.Join(args, " "))
		payload := map[string]any{}
		if args[0] == "inspect" {
			identity := newCLIPayloadIdentity("scenery.inspect." + args[1])
			payload["kind"], payload["schema_revision"] = identity.Kind, identity.SchemaRevision
		}
		return json.NewEncoder(output).Encode(newCLIEnvelope(true, payload, nil))
	})
	if len(commands) != 6 || !strings.HasPrefix(commands[0], "generate --target contracts ") || !strings.HasPrefix(commands[1], "check ") {
		t.Fatalf("fixture command boundary = %v", commands)
	}
	if report.Kind != harnessFixtureMatrixKind || report.SchemaRevision != newCLIPayloadIdentity(harnessFixtureMatrixKind).SchemaRevision {
		t.Fatalf("identity = %q %q", report.Kind, report.SchemaRevision)
	}
	if len(report.Fixtures) != 1 {
		t.Fatalf("fixtures = %+v", report.Fixtures)
	}
	if !report.Fixtures[0].Check || !report.Fixtures[0].Inspect["app"] || !report.Fixtures[0].Inspect["routes"] || !report.Fixtures[0].Inspect["services"] || !report.Fixtures[0].Inspect["endpoints"] {
		t.Fatalf("fixture result = %+v", report.Fixtures[0])
	}
}

func TestCheckDeclaredGoToolchainAvailable(t *testing.T) {
	t.Parallel()

	// Test declaration and failure classification in process. The real toolchain
	// invocation remains a mandatory verifier preflight boundary.
	run := func(_ context.Context, version string) ([]byte, error) {
		if version == "go1.24.99" {
			return []byte("toolchain unavailable"), fmt.Errorf("exit 1")
		}
		if !strings.HasPrefix(version, "go1.") {
			t.Fatalf("unexpected toolchain %q", version)
		}
		return []byte("/toolchain"), nil
	}
	if diagnostics := checkDeclaredGoToolchainAvailableWithRunner(context.Background(), repoRootForTest(t), run); len(diagnostics) != 0 {
		t.Fatalf("declared toolchain reported unavailable on a healthy checkout: %+v", diagnostics)
	}

	// A declared toolchain absent from the module cache must fail preflight
	// with the restore command, since GOPROXY=off forbids fetching it. The
	// version is deliberately one that was never released.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/preflight\n\ngo 1.24.99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diagnostics := checkDeclaredGoToolchainAvailableWithRunner(context.Background(), root, run)
	if len(diagnostics) != 1 || diagnostics[0].Severity != "error" {
		t.Fatalf("missing toolchain diagnostics = %+v", diagnostics)
	}
	if !strings.Contains(diagnostics[0].Message, "go1.24.99") || !strings.Contains(diagnostics[0].SuggestedAction, "GOTOOLCHAIN=go1.24.99 go version") {
		t.Fatalf("diagnostic lacks version or restore command: %+v", diagnostics[0])
	}

	// A repo without go.mod stays silent rather than failing preflight.
	if diagnostics := checkDeclaredGoToolchainAvailableWithRunner(context.Background(), t.TempDir(), run); len(diagnostics) != 0 {
		t.Fatalf("missing go.mod produced diagnostics: %+v", diagnostics)
	}
}
