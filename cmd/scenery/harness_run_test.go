package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/build"
)

func TestRunSceneryHarnessJSONSuccessWritesLatest(t *testing.T) {
	useFakeBuildGoRunner(t)

	root := filepath.Join(t.ArtifactDir(), "harnessapp")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
	writeHarnessTestApp(t, root, "harnessapp", "return nil")

	var out bytes.Buffer
	if err := runSceneryHarness(context.Background(), &out, []string{"--app-root", root, "--json", "--write"}); err != nil {
		t.Fatalf("runSceneryHarness returned error: %v\n%s", err, out.String())
	}

	var payload harnessResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "scenery.harness.result.v1" || !payload.OK {
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
	if err := runSceneryInspect([]string{"harness", "--app-root", root, "--json"}, &inspectOut); err != nil {
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

func TestRunSceneryHarnessJSONFailureIncludesNextAction(t *testing.T) {
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
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
	writeHarnessTestApp(t, root, "harnessfail", "return MissingSymbol")

	var out bytes.Buffer
	err := runSceneryHarness(context.Background(), &out, []string{"--app-root", root, "--json"})
	if _, ok := errors.AsType[*silentCLIError](err); !ok {
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

func TestRunHarnessPostgresBranchStep(t *testing.T) {
	prev := runHarnessPostgresBranchCheckFunc
	t.Cleanup(func() { runHarnessPostgresBranchCheckFunc = prev })
	runHarnessPostgresBranchCheckFunc = func(context.Context) (map[string]any, []checkDiagnostic, error) {
		return map[string]any{
			"branches":     2,
			"leases_after": 1,
		}, nil, nil
	}

	step := runHarnessPostgresBranchStep(context.Background(), t.TempDir())
	if !step.OK {
		t.Fatalf("Postgres branch step failed: error=%s diagnostics=%+v summary=%+v", step.Error, step.Diagnostics, step.Summary)
	}
	if got, _ := step.Summary["branches"].(int); got != 2 {
		t.Fatalf("branches summary = %v, want 2", step.Summary["branches"])
	}
	if got, _ := step.Summary["leases_after"].(int); got != 1 {
		t.Fatalf("leases_after summary = %v, want 1", step.Summary["leases_after"])
	}
}

func TestParseHarnessSelfArgsSupportsSummaryAndFullModes(t *testing.T) {
	t.Parallel()

	summary, err := parseHarnessSelfArgs([]string{"--summary", "--write"})
	if err != nil {
		t.Fatalf("summary parse: %v", err)
	}
	if !summary.JSON || summary.Output != harnessSelfOutputSummary {
		t.Fatalf("summary opts = %+v", summary)
	}
	full, err := parseHarnessSelfArgs([]string{"--json=full"})
	if err != nil {
		t.Fatalf("full parse: %v", err)
	}
	if !full.JSON || full.Output != harnessSelfOutputFull {
		t.Fatalf("full opts = %+v", full)
	}
}

func TestHarnessSelfSummaryStaysSmallAndOmitsArchiveFields(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	resp := harnessSelfResponse{
		SchemaVersion: "scenery.harness.self.v1",
		OK:            true,
		GeneratedAt:   "2026-06-08T00:00:00Z",
		Mode:          harnessSelfModeDefault,
		Repo: harnessSelfRepo{
			Root:       root,
			ModulePath: "scenery.sh",
			GoModPath:  filepath.Join(root, "go.mod"),
		},
		Knowledge: harnessKnowledge{
			Entrypoints: []harnessKnowledgeFile{{Path: "AGENTS.md", Exists: true}},
			Schemas:     []harnessKnowledgeFile{{Path: "docs/schemas/scenery.harness.self.v1.schema.json", Exists: true}},
		},
		ChangedArea: &harnessChangedAreaReport{
			SchemaVersion:       harnessChangedAreaSchema,
			IgnoredFiles:        []harnessChangedFile{{Path: ".scenery/harness/self-latest.json", Status: "untracked", Category: "local-artifact"}},
			RecommendedCommands: []string{},
		},
		Drift: &harnessDriftReport{
			SchemaVersion: harnessDriftSchema,
			Env: harnessEnvVarReport{Variables: []harnessEnvVarFinding{
				{Name: "SCENERY_ALPHA"}, {Name: "SCENERY_BETA"},
			}},
			CLI:    harnessCLIContractReport{Commands: []harnessCLIContractCommand{{Name: "harness self"}}},
			Embeds: harnessEmbedReport{Embeds: []harnessEmbedFinding{{File: "cmd/scenery/main.go"}}},
		},
		TestTiming: &harnessTestTimingReport{
			SchemaVersion: harnessTestTimingSchema,
			Command:       harnessSelfGoTestCommand(),
			TotalSeconds:  8,
			Budgets:       defaultHarnessTestTimingBudgets(),
		},
		Steps: []harnessStep{{
			Name:       "go tests",
			Command:    harnessSelfGoTestCommand(),
			OK:         true,
			DurationMS: 8000,
			Evidence: &harnessEvidence{
				SchemaVersion: harnessArtifactEvidenceSchema,
				DurationMS:    8000,
				StdoutTail:    strings.Repeat("pass\n", 1000),
				StderrTail:    strings.Repeat("warn\n", 1000),
			},
		}, {
			Name: "architecture checks",
			OK:   true,
			Summary: map[string]any{
				"large_files": 10,
			},
			Diagnostics: []checkDiagnostic{{
				Stage:    "architecture checks",
				Severity: "warning",
				File:     filepath.Join(root, "cmd/scenery/old.go"),
				Message:  "file has 1001 lines, over warning threshold 1000",
			}},
		}},
		Artifacts: []harnessArtifact{{Name: "self-harness", Path: ".scenery/harness/self-latest.json", SchemaVersion: "scenery.harness.self.v1", Exists: true}},
	}
	for i := 0; i < 20; i++ {
		resp.TestTiming.Packages = append(resp.TestTiming.Packages, harnessPackageTiming{Package: fmt.Sprintf("example.com/pkg%d", i), Seconds: float64(20 - i)})
		resp.TestTiming.SlowTests = append(resp.TestTiming.SlowTests, harnessTestTiming{Name: fmt.Sprintf("TestSlow%d", i), Package: "example.com/pkg", Seconds: float64(20 - i)})
	}

	summary := buildHarnessSelfSummary(resp)
	if summary.Status != "pass_with_debt" {
		t.Fatalf("status = %q, want pass_with_debt", summary.Status)
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 12000 {
		t.Fatalf("summary too large: got %d bytes", len(data))
	}
	for _, forbidden := range [][]byte{[]byte(`"variables"`), []byte(`"stdout_tail"`), []byte(`"stderr_tail"`)} {
		if bytes.Contains(data, forbidden) {
			t.Fatalf("summary contains forbidden field %s", forbidden)
		}
	}
	if len(summary.Reports.TestTiming.TopSlowTests) != 5 || len(summary.Reports.TestTiming.TopSlowPackages) != 5 {
		t.Fatalf("timing caps not applied: %+v", summary.Reports.TestTiming)
	}
}

func TestChangedAreaIgnoresLocalHarnessArtifacts(t *testing.T) {
	root := t.TempDir()
	oldCollect := harnessCollectChangedFiles
	oldList := harnessListGoPackages
	harnessCollectChangedFiles = func(context.Context, string) ([]harnessChangedFile, []checkDiagnostic) {
		return []harnessChangedFile{
			{Path: ".scenery/harness/self-latest.json", Status: "untracked"},
			{Path: "coverage/unit.harness.json", Status: "untracked"},
			{Path: "scenery-harness-self-20260608.json", Status: "untracked"},
		}, nil
	}
	harnessListGoPackages = func(context.Context, string) ([]harnessPackageInfo, error) { return nil, nil }
	t.Cleanup(func() {
		harnessCollectChangedFiles = oldCollect
		harnessListGoPackages = oldList
	})

	report := buildHarnessChangedAreaReport(context.Background(), root)
	if len(report.ChangedFiles) != 0 {
		t.Fatalf("changed files = %+v, want none", report.ChangedFiles)
	}
	if len(report.IgnoredFiles) != 3 {
		t.Fatalf("ignored files = %+v", report.IgnoredFiles)
	}
	if stringSliceContains(report.RecommendedCommands, "go test ./...") || stringSliceContains(report.RecommendedCommands, "scenery harness self --summary --write") {
		t.Fatalf("ignored-only changes recommended commands: %+v", report.RecommendedCommands)
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
	if isIgnoredHarnessLocalArtifact("docs/schemas/scenery.harness.self.v1.schema.json") {
		t.Fatal("schema source files must remain in changed-area analysis")
	}
}

func TestProbeHarnessToolParsesSceneryVersionJSON(t *testing.T) {

	dir := t.TempDir()
	path := filepath.Join(dir, "scenery")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"schema_version\":\"scenery.version.v1\",\"version\":\"v1.2.3\",\"commit\":\"abc\",\"built_at\":\"2026-06-08T00:00:00Z\",\"go_version\":\"go1.26.3\"}'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	tool := probeHarnessTool(context.Background(), "scenery", "required", true, []string{"version", "--json"})
	if tool.Version != "v1.2.3" || tool.Commit != "abc" || tool.GoVersion != "go1.26.3" {
		t.Fatalf("tool = %+v", tool)
	}
}

func TestInspectHarnessFocusedCommands(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	self := harnessSelfResponse{
		SchemaVersion: "scenery.harness.self.v1",
		OK:            true,
		GeneratedAt:   "2026-06-08T00:00:00Z",
		Mode:          harnessSelfModeDefault,
		Repo:          harnessSelfRepo{Root: root, ModulePath: "scenery.sh", GoModPath: filepath.Join(root, "go.mod")},
		Knowledge:     buildHarnessSelfKnowledge(root),
		Steps: []harnessStep{{
			Name: "go tests",
			OK:   true,
			Diagnostics: []checkDiagnostic{{
				Stage:    "go tests",
				Severity: "warning",
				Message:  "full Go suite took 8.000s",
			}},
		}},
		Artifacts: buildHarnessSelfArtifacts(root, true, harnessSelfResponse{TestTiming: &harnessTestTimingReport{}}),
	}
	if err := writeHarnessSelfResult(filepath.Join(root, ".scenery", "harness", "self-latest.json"), self); err != nil {
		t.Fatal(err)
	}
	timing := harnessTestTimingReport{
		SchemaVersion: harnessTestTimingSchema,
		Command:       harnessSelfGoTestCommand(),
		TotalSeconds:  8,
		Budgets:       defaultHarnessTestTimingBudgets(),
		Packages:      []harnessPackageTiming{{Package: "example.com/slow", Seconds: 3}},
		SlowTests:     []harnessTestTiming{{Name: "TestSlow", Package: "example.com/slow", Seconds: 1}},
	}
	if err := writeHarnessJSONFile(filepath.Join(root, ".scenery", "harness", "test-timing-latest.json"), timing); err != nil {
		t.Fatal(err)
	}

	var diagnosticsOut bytes.Buffer
	if err := runSceneryInspect([]string{"harness", "diagnostics", "--severity", "warning", "--repo-root", root, "--json"}, &diagnosticsOut); err != nil {
		t.Fatalf("diagnostics inspect: %v", err)
	}
	var diagnostics inspectHarnessDiagnosticsResponse
	if err := json.Unmarshal(diagnosticsOut.Bytes(), &diagnostics); err != nil {
		t.Fatal(err)
	}
	if len(diagnostics.Diagnostics) != 1 || diagnostics.Diagnostics[0].Severity != "warning" {
		t.Fatalf("diagnostics = %+v", diagnostics.Diagnostics)
	}

	var timingOut bytes.Buffer
	if err := runSceneryInspect([]string{"harness", "timing", "--top", "1", "--repo-root", root, "--json"}, &timingOut); err != nil {
		t.Fatalf("timing inspect: %v", err)
	}
	var timingResp inspectHarnessTimingResponse
	if err := json.Unmarshal(timingOut.Bytes(), &timingResp); err != nil {
		t.Fatal(err)
	}
	if len(timingResp.SlowTests) != 1 || len(timingResp.SlowPackages) != 1 {
		t.Fatalf("timing response = %+v", timingResp)
	}
}
