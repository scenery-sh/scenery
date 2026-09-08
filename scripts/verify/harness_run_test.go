package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRunHarnessParallelDevStep(t *testing.T) {
	prev := runHarnessParallelDevCheckFunc
	t.Cleanup(func() { runHarnessParallelDevCheckFunc = prev })
	runHarnessParallelDevCheckFunc = func(context.Context, string) (map[string]any, []checkDiagnostic, error) {
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
		PayloadIdentity: newCLIPayloadIdentity("scenery.harness.self"),
		OK:              true,
		GeneratedAt:     "2026-06-08T00:00:00Z",
		Mode:            harnessSelfModeDefault,
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
			PayloadIdentity:     newCLIPayloadIdentity(harnessChangedAreaKind),
			IgnoredFiles:        []harnessChangedFile{{Path: ".scenery/harness/self-latest.json", Status: "untracked", Category: "local-artifact"}},
			RecommendedCommands: []string{},
		},
		Drift: &harnessDriftReport{
			PayloadIdentity: newCLIPayloadIdentity(harnessDriftKind),
			Env: harnessEnvVarReport{Variables: []harnessEnvVarFinding{
				{Name: "SCENERY_ALPHA"}, {Name: "SCENERY_BETA"},
			}},
			CLI:    harnessCLIContractReport{Commands: []harnessCLIContractCommand{{Name: "harness self"}}},
			Embeds: harnessEmbedReport{Embeds: []harnessEmbedFinding{{File: "cmd/scenery/main.go"}}},
		},
		TestTiming: &harnessTestTimingReport{
			PayloadIdentity: newCLIPayloadIdentity(harnessTestTimingKind),
			Command:         harnessSelfGoTestCommand(),
			TotalSeconds:    8,
			Budgets:         defaultHarnessTestTimingBudgets(),
		},
		Steps: []harnessStep{{
			Name:       "go tests",
			Command:    harnessSelfGoTestCommand(),
			OK:         true,
			DurationMS: 8000,
			Evidence: &harnessEvidence{
				PayloadIdentity: newCLIPayloadIdentity(harnessArtifactEvidenceKind),
				DurationMS:      8000,
				StdoutTail:      strings.Repeat("pass\n", 1000),
				StderrTail:      strings.Repeat("warn\n", 1000),
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
		resp.TestTiming.SlowTests = append(resp.TestTiming.SlowTests, harnessTestTiming{
			Name: "TestSlow" + fmt.Sprint(i), Package: "example.com/pkg", Class: harnessTestClassFast,
			Seconds: float64(20 - i), TargetSeconds: harnessFastTestTargetSeconds, BudgetSeconds: harnessFastTestBudgetSeconds,
		})
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

func TestHarnessToolOutputParsesSceneryVersionJSONInProcess(t *testing.T) {
	t.Parallel()

	versionIdentity := newCLIPayloadIdentity("scenery.version")
	envelope := newCLIEnvelope(true, map[string]any{"kind": versionIdentity.Kind, "schema_revision": versionIdentity.SchemaRevision, "version": "v1.2.3", "commit": "abc", "built_at": "2026-06-08T00:00:00Z", "go_version": "go1.26.3"}, nil)
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	tool := applyHarnessToolOutput(harnessToolchainTool{Name: "scenery", Present: true}, "scenery", []string{"version", "-o", "json"}, encoded)
	if tool.Version != "v1.2.3" || tool.Commit != "abc" || tool.GoVersion != "go1.26.3" {
		t.Fatalf("tool = %+v", tool)
	}
	fallback := applyHarnessToolOutput(harnessToolchainTool{Name: "go", Present: true}, "go", []string{"version"}, []byte("go version go1.27 darwin/arm64\nignored"))
	if fallback.Version != "go version go1.27 darwin/arm64" {
		t.Fatalf("fallback tool = %+v", fallback)
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
	if slices.Contains(report.RecommendedCommands, "go test ./...") || slices.Contains(report.RecommendedCommands, "go run ./scripts/verify --summary --write") {
		t.Fatalf("ignored-only changes recommended commands: %+v", report.RecommendedCommands)
	}
}
