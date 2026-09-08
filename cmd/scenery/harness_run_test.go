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

func TestHarnessCommandRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"self", "-o", "json"}, {"--not-a-flag", "-o", "json"}} {
		var output bytes.Buffer
		err := runSceneryHarness(t.Context(), &output, args)
		if err == nil || cliExitCode(err) != 2 || !strings.HasPrefix(err.Error(), "invalid_request:") {
			t.Fatalf("harness %v error = %v, exit = %d", args, err, cliExitCode(err))
		}
	}
	if _, ok := findHelpCommand([]string{"harness", "self"}); ok {
		t.Fatal("help advertises a repository-verification product command")
	}
}

func TestRunSceneryHarnessJSONSuccessWritesLatest(t *testing.T) {
	useFakeBuildGoRunner(t)

	root := filepath.Join(t.ArtifactDir(), "harnessapp")
	_ = isolateCommandCacheRootAt(t, filepath.Join(t.TempDir(), "cache"))
	writeHarnessTestApp(t, root, "harnessapp", "return nil")

	var out bytes.Buffer
	if err := runSceneryHarness(context.Background(), &out, []string{"--app-root", root, "-o", "json", "--write"}); err != nil {
		t.Fatalf("runSceneryHarness returned error: %v\n%s", err, out.String())
	}

	var payload harnessResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if payload.Kind != "scenery.harness.result" || payload.SchemaRevision != newCLIPayloadIdentity("scenery.harness.result").SchemaRevision || !payload.OK {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.App.Name != "harnessapp" || payload.App.ModulePath != "example.com/harnessapp" {
		t.Fatalf("app = %+v", payload.App)
	}
	if len(payload.Steps) != 9 {
		t.Fatalf("steps = %d, want 9", len(payload.Steps))
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
	if err := runSceneryInspect([]string{"harness", "--app-root", root, "-o", "json"}, &inspectOut); err != nil {
		t.Fatalf("inspect harness: %v\n%s", err, inspectOut.String())
	}
	var inspectPayload inspectHarnessResponse
	if err := decodeCLIJSON(inspectOut.Bytes(), &inspectPayload); err != nil {
		t.Fatalf("decode inspect harness: %v\n%s", err, inspectOut.String())
	}
	if inspectPayload.Kind != inspectHarnessKind || inspectPayload.SchemaRevision != newCLIPayloadIdentity(inspectHarnessKind).SchemaRevision || len(inspectPayload.Evidence) == 0 {
		t.Fatalf("inspect harness payload = %+v", inspectPayload)
	}
}

func TestRunSceneryHarnessJSONFailureIncludesNextAction(t *testing.T) {
	root := t.TempDir()
	_ = isolateCommandCacheRootAt(t, filepath.Join(t.TempDir(), "cache"))
	writeHarnessTestApp(t, root, "harnessfail", "return nil")
	writeTestAppFile(t, root, "invalid.scn", "unsupported \"fixture\" {}\n")

	var out bytes.Buffer
	err := runSceneryHarness(context.Background(), &out, []string{"--app-root", root, "-o", "json"})
	if _, ok := errors.AsType[*silentCLIError](err); !ok {
		t.Fatalf("expected silentCLIError, got %v\n%s", err, out.String())
	}

	var payload harnessResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if payload.OK {
		t.Fatalf("payload ok = true, want false")
	}
	if len(payload.NextActions) == 0 {
		t.Fatalf("expected next actions: %+v", payload)
	}
	if !strings.Contains(strings.Join(payload.NextActions, "\n"), "unknown top-level block") {
		t.Fatalf("next actions = %+v", payload.NextActions)
	}
}

func TestChangedAreaSelectsDeterministicValidationByPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		paths            []string
		packageDirs      []string
		wantClasses      []string
		wantCommands     []string
		forbidCommands   []string
		wantRelevantDocs []string
	}{
		{
			name:           "documentation only",
			paths:          []string{"AGENTS.md", "PLANS.md", "ui/AGENTS.md", "ui/components/AGENTS.md"},
			wantClasses:    []string{harnessValidationDocumentation},
			wantCommands:   []string{harnessValidationQuickCommand},
			forbidCommands: []string{"go test ./...", harnessValidationFullCommand},
		},
		{
			name:           "one go package",
			paths:          []string{"errs/example.go"},
			packageDirs:    []string{"errs"},
			wantClasses:    []string{harnessValidationGoPackage},
			wantCommands:   []string{"go test ./errs", "go test ./..."},
			forbidCommands: []string{harnessValidationQuickCommand, harnessValidationFullCommand},
		},
		{
			name:             "cli json contract",
			paths:            []string{"cmd/scenery/help.go", "ui/components/AGENTS.md"},
			packageDirs:      []string{"cmd/scenery"},
			wantClasses:      []string{harnessValidationCLIJSONContract, harnessValidationGoPackage},
			wantCommands:     []string{"go test ./cmd/scenery", "go test ./...", harnessValidationQuickCommand},
			forbidCommands:   []string{harnessValidationFullCommand},
			wantRelevantDocs: []string{"docs/local-contract.md"},
		},
		{
			name:         "compiler",
			paths:        []string{"internal/compiler/compiler.go"},
			packageDirs:  []string{"internal/compiler"},
			wantClasses:  []string{harnessValidationCompilerGenerator, harnessValidationGoPackage},
			wantCommands: append([]string{"go test ./internal/compiler", "go test ./..."}, harnessFixtureRegenerationCommands...),
			forbidCommands: []string{
				harnessValidationQuickCommand,
				harnessValidationFullCommand,
			},
		},
		{
			name:        "ui catalog",
			paths:       []string{"ui/src/button.tsx"},
			wantClasses: []string{harnessValidationUICatalog},
			wantCommands: append([]string{
				"apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json",
				"go test ./internal/generate",
			}, harnessFixtureRegenerationCommands...),
			forbidCommands: []string{harnessValidationFullCommand},
		},
		{
			name:        "dashboard",
			paths:       []string{"apps/console/src/App.tsx"},
			wantClasses: []string{harnessValidationDashboard},
			wantCommands: []string{
				"cd apps/console && bun run lint && bun run typecheck && bun run build",
				harnessValidationUICommand,
			},
			forbidCommands: []string{harnessValidationFullCommand},
		},
		{
			name:           "release sensitive runtime",
			paths:          []string{"runtime/server.go"},
			packageDirs:    []string{"runtime"},
			wantClasses:    []string{harnessValidationGoPackage, harnessValidationReleaseRuntime},
			wantCommands:   []string{"go test ./runtime", "go test ./...", harnessValidationFullCommand},
			forbidCommands: []string{harnessValidationQuickCommand},
		},
		{
			name:           "repository fallback",
			paths:          []string{"Makefile"},
			wantClasses:    []string{harnessValidationRepositoryFallback},
			wantCommands:   []string{"go test ./..."},
			forbidCommands: []string{harnessValidationQuickCommand, harnessValidationFullCommand},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			var packages []harnessPackageInfo
			for _, rel := range tt.packageDirs {
				packages = append(packages, harnessPackageInfo{
					ImportPath: "scenery.sh/" + rel,
					Dir:        filepath.Join(root, filepath.FromSlash(rel)),
					RelDir:     rel,
				})
			}
			changes := make([]harnessChangedFile, 0, len(tt.paths))
			for _, path := range tt.paths {
				changes = append(changes, harnessChangedFile{Path: path, Status: "modified"})
			}
			report := &harnessChangedAreaReport{}
			populateHarnessChangedAreaReport(root, report, changes, packages, nil)

			if strings.Join(report.ValidationClasses, "\n") != strings.Join(tt.wantClasses, "\n") {
				t.Fatalf("validation classes = %v, want %v", report.ValidationClasses, tt.wantClasses)
			}
			for _, command := range tt.wantCommands {
				if !stringSliceContains(report.RecommendedCommands, command) {
					t.Errorf("recommended commands %v missing %q", report.RecommendedCommands, command)
				}
			}
			for _, command := range tt.forbidCommands {
				if stringSliceContains(report.RecommendedCommands, command) {
					t.Errorf("recommended commands %v unexpectedly contain %q", report.RecommendedCommands, command)
				}
			}
			for _, path := range tt.wantRelevantDocs {
				if !stringSliceContains(report.RelevantDocs, path) {
					t.Errorf("relevant docs %v missing %q", report.RelevantDocs, path)
				}
			}
		})
	}
}

func TestHarnessLocalArtifactIgnoreDoesNotHideSchemas(t *testing.T) {
	t.Parallel()

	if !isIgnoredHarnessLocalArtifact("coverage/unit.harness.json") {
		t.Fatal("coverage harness report should be ignored")
	}
	if !isIgnoredHarnessLocalArtifact(".claude/worktrees/example/docs/plans/0061-env-harness.md") {
		t.Fatal("local Claude worktree artifacts should be ignored")
	}
	if isIgnoredHarnessLocalArtifact("docs/schemas/scenery.harness.self.schema.json") {
		t.Fatal("schema source files must remain in changed-area analysis")
	}
}

func TestInspectHarnessFocusedCommands(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	self := harnessSelfResponse{
		PayloadIdentity: newCLIPayloadIdentity("scenery.harness.self"),
		OK:              true,
		GeneratedAt:     "2026-06-08T00:00:00Z",
		Mode:            "default",
		Repo:            harnessSelfRepo{Root: root, ModulePath: "scenery.sh", GoModPath: filepath.Join(root, "go.mod")},
		Knowledge:       harnessKnowledge{},
		Steps: []harnessStep{{
			Name: "go tests",
			OK:   true,
			Diagnostics: []checkDiagnostic{{
				Stage:    "go tests",
				Severity: "warning",
				Message:  "full Go suite took 8.000s",
			}},
		}},
		Artifacts: []harnessArtifact{{Name: "latest-self-harness", Path: ".scenery/harness/self-latest.json", Kind: "scenery.harness.self", Exists: true}},
	}
	writeHarnessReportFixture(t, root, "self-latest.json", self)
	timing := harnessTestTimingReport{
		PayloadIdentity: newCLIPayloadIdentity(harnessTestTimingKind),
		Command:         []string{"go", "test", "-json", "./..."},
		TotalSeconds:    8,
		Budgets:         harnessTestTimingBudgets{},
		Packages:        []harnessPackageTiming{{Package: "example.com/slow", Seconds: 3}},
		SlowTests: []harnessTestTiming{{
			Name: "TestSlow", Package: "example.com/slow", Class: "fast",
			Seconds: 1, TargetSeconds: 0.06, BudgetSeconds: 0.1,
		}},
	}
	writeHarnessReportFixture(t, root, "test-timing-latest.json", timing)

	var diagnosticsOut bytes.Buffer
	if err := runSceneryInspect([]string{"harness", "diagnostics", "--severity", "warning", "--repo-root", root, "-o", "json"}, &diagnosticsOut); err != nil {
		t.Fatalf("diagnostics inspect: %v", err)
	}
	var diagnostics inspectHarnessDiagnosticsResponse
	if err := decodeCLIJSON(diagnosticsOut.Bytes(), &diagnostics); err != nil {
		t.Fatal(err)
	}
	if len(diagnostics.Diagnostics) != 1 || diagnostics.Diagnostics[0].Severity != "warning" {
		t.Fatalf("diagnostics = %+v", diagnostics.Diagnostics)
	}

	var timingOut bytes.Buffer
	if err := runSceneryInspect([]string{"harness", "timing", "--top", "1", "--repo-root", root, "-o", "json"}, &timingOut); err != nil {
		t.Fatalf("timing inspect: %v", err)
	}
	var timingResp inspectHarnessTimingResponse
	if err := decodeCLIJSON(timingOut.Bytes(), &timingResp); err != nil {
		t.Fatal(err)
	}
	if len(timingResp.SlowTests) != 1 || len(timingResp.SlowPackages) != 1 {
		t.Fatalf("timing response = %+v", timingResp)
	}
}

func writeHarnessReportFixture(t *testing.T, root, name string, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, filepath.Join(".scenery", "harness", name), string(data))
}

func TestInspectHarnessFocusedMissingArtifact(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)

	var timingOut bytes.Buffer
	err := runSceneryInspect([]string{"harness", "timing", "--top", "1", "--repo-root", root, "-o", "json"}, &timingOut)
	if err == nil {
		t.Fatal("expected missing test-timing artifact error")
	}
	if !strings.HasPrefix(err.Error(), "failed_precondition:") || !strings.Contains(err.Error(), "test-timing") {
		t.Fatalf("timing error = %v", err)
	}
	if code := cliExitCode(err); code != 3 {
		t.Fatalf("timing exit code = %d, want 3", code)
	}

	var artifactOut bytes.Buffer
	err = runSceneryInspect([]string{"harness", "artifact", "nope", "--repo-root", root, "-o", "json"}, &artifactOut)
	if err == nil {
		t.Fatal("expected unknown artifact error")
	}
	if !strings.HasPrefix(err.Error(), "invalid_request:") {
		t.Fatalf("artifact error = %v", err)
	}
	if code := cliExitCode(err); code != 2 {
		t.Fatalf("artifact exit code = %d, want 2", code)
	}
}
