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
)

func TestRunSceneryHarnessJSONSuccessWritesLatest(t *testing.T) {
	useFakeBuildGoRunner(t)

	root := filepath.Join(t.ArtifactDir(), "harnessapp")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
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
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("SCENERY_DEV_CACHE_DIR", cacheRoot)
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

func TestSummarizeGoTestFailures(t *testing.T) {
	t.Parallel()

	output := []byte(strings.Join([]string{
		`{"Action":"output","Package":"scenery.sh/storage","Test":"TestLease","Output":"=== RUN   TestLease\n"}`,
		`{"Action":"output","Package":"scenery.sh/storage","Test":"TestLease","Output":"storage_test.go:12: expected lease\n"}`,
		`{"Action":"fail","Package":"scenery.sh/storage","Test":"TestLease","Elapsed":0.01}`,
		`{"Action":"output","Package":"scenery.sh/cmd/scenery","Output":"cmd/scenery/main.go:12:2: missing module\n"}`,
		`{"Action":"fail","Package":"scenery.sh/cmd/scenery","Elapsed":0.01}`,
	}, "\n"))

	summary := summarizeGoTestFailures(output)
	if !strings.Contains(summary, "scenery.sh/storage TestLease") {
		t.Fatalf("summary missing test failure: %q", summary)
	}
	if !strings.Contains(summary, "expected lease") {
		t.Fatalf("summary missing test output: %q", summary)
	}
	if !strings.Contains(summary, "scenery.sh/cmd/scenery") || !strings.Contains(summary, "missing module") {
		t.Fatalf("summary missing package failure: %q", summary)
	}
}

func TestParseHarnessSelfArgsSupportsSummaryAndFullModes(t *testing.T) {
	t.Parallel()

	summary, err := parseHarnessSelfArgs([]string{"--summary", "--write"})
	if err != nil {
		t.Fatalf("summary parse: %v", err)
	}
	if summary.JSON || summary.Output != harnessSelfOutputSummary {
		t.Fatalf("summary opts = %+v", summary)
	}
	full, err := parseHarnessSelfArgs([]string{"-o", "json"})
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
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.harness.self"),
		OK:                 true,
		GeneratedAt:        "2026-06-08T00:00:00Z",
		Mode:               harnessSelfModeDefault,
		Repo: harnessSelfRepo{
			Root:       root,
			ModulePath: "scenery.sh",
			GoModPath:  filepath.Join(root, "go.mod"),
		},
		Knowledge: harnessKnowledge{
			Entrypoints: []harnessKnowledgeFile{{Path: "AGENTS.md", Exists: true}},
			Schemas:     []harnessKnowledgeFile{{Path: "docs/schemas/scenery.harness.self.schema.json", Exists: true}},
		},
		ChangedArea: &harnessChangedAreaReport{
			cliPayloadIdentity:  newCLIPayloadIdentity(harnessChangedAreaKind),
			IgnoredFiles:        []harnessChangedFile{{Path: ".scenery/harness/self-latest.json", Status: "untracked", Category: "local-artifact"}},
			RecommendedCommands: []string{},
		},
		Drift: &harnessDriftReport{
			cliPayloadIdentity: newCLIPayloadIdentity(harnessDriftKind),
			Env: harnessEnvVarReport{Variables: []harnessEnvVarFinding{
				{Name: "SCENERY_ALPHA"}, {Name: "SCENERY_BETA"},
			}},
			CLI:    harnessCLIContractReport{Commands: []harnessCLIContractCommand{{Name: "harness self"}}},
			Embeds: harnessEmbedReport{Embeds: []harnessEmbedFinding{{File: "cmd/scenery/main.go"}}},
		},
		TestTiming: &harnessTestTimingReport{
			cliPayloadIdentity: newCLIPayloadIdentity(harnessTestTimingKind),
			Command:            harnessSelfGoTestCommand(),
			TotalSeconds:       8,
			Budgets:            defaultHarnessTestTimingBudgets(),
		},
		Steps: []harnessStep{{
			Name:       "go tests",
			Command:    harnessSelfGoTestCommand(),
			OK:         true,
			DurationMS: 8000,
			Evidence: &harnessEvidence{
				cliPayloadIdentity: newCLIPayloadIdentity(harnessArtifactEvidenceKind),
				DurationMS:         8000,
				StdoutTail:         strings.Repeat("pass\n", 1000),
				StderrTail:         strings.Repeat("warn\n", 1000),
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
		Artifacts: []harnessArtifact{newHarnessArtifact("self-harness", ".scenery/harness/self-latest.json", "scenery.harness.self", true)},
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
	if isIgnoredHarnessLocalArtifact("docs/schemas/scenery.harness.self.schema.json") {
		t.Fatal("schema source files must remain in changed-area analysis")
	}
}

func TestProbeHarnessToolParsesSceneryVersionJSON(t *testing.T) {

	dir := t.TempDir()
	path := filepath.Join(dir, "scenery")
	versionIdentity := newCLIPayloadIdentity("scenery.version")
	envelope := newCLIEnvelope(true, map[string]any{"kind": versionIdentity.Kind, "schema_revision": versionIdentity.SchemaRevision, "version": "v1.2.3", "commit": "abc", "built_at": "2026-06-08T00:00:00Z", "go_version": "go1.26.3"}, nil)
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %q\n", string(encoded))
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	tool := probeHarnessTool(context.Background(), "scenery", "required", true, []string{"version", "-o", "json"})
	if tool.Version != "v1.2.3" || tool.Commit != "abc" || tool.GoVersion != "go1.26.3" {
		t.Fatalf("tool = %+v", tool)
	}
}

func TestInspectHarnessFocusedCommands(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	self := harnessSelfResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.harness.self"),
		OK:                 true,
		GeneratedAt:        "2026-06-08T00:00:00Z",
		Mode:               harnessSelfModeDefault,
		Repo:               harnessSelfRepo{Root: root, ModulePath: "scenery.sh", GoModPath: filepath.Join(root, "go.mod")},
		Knowledge:          buildHarnessSelfKnowledge(root),
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
		cliPayloadIdentity: newCLIPayloadIdentity(harnessTestTimingKind),
		Command:            harnessSelfGoTestCommand(),
		TotalSeconds:       8,
		Budgets:            defaultHarnessTestTimingBudgets(),
		Packages:           []harnessPackageTiming{{Package: "example.com/slow", Seconds: 3}},
		SlowTests:          []harnessTestTiming{{Name: "TestSlow", Package: "example.com/slow", Seconds: 1}},
	}
	if err := writeHarnessJSONFile(filepath.Join(root, ".scenery", "harness", "test-timing-latest.json"), timing); err != nil {
		t.Fatal(err)
	}

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
