package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeBuildPreparedExecutableUsesCandidateEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenery-app-full-digest")
	if err := os.WriteFile(path, []byte("artifact"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := nativeBuildPreparedExecutable([]harnessEditLatencyPhase{{Name: "candidate.prepare", OK: true, WrittenPaths: []string{path}}})
	want, canonicalErr := filepath.EvalSymlinks(path)
	if err != nil || canonicalErr != nil || got != want {
		t.Fatalf("prepared executable = %q: %v", got, err)
	}
}

func TestNativeBuildCompilerSummaryIncludesRetainedStockControl(t *testing.T) {
	samples := []nativeBuildDriverSample{
		{Backend: "bare_stock", AccountableBuildMS: 28, FirstVerifiedResponseMS: 38, AcceptedEditMS: 43, OK: true},
		{Backend: "stock", AccountableBuildMS: 30, FirstVerifiedResponseMS: 40, AcceptedEditMS: 45, OK: true},
		{Backend: "retained_stock", AccountableBuildMS: 25, FirstVerifiedResponseMS: 35, AcceptedEditMS: 40, OK: true},
		{Backend: "compiler", AccountableBuildMS: 20, FirstVerifiedResponseMS: 30, AcceptedEditMS: 35, OK: true},
	}
	summary := nativeBuildCohortSummary(samples)
	if summary["backend_count"] != 4 || summary["complete_pairs"] != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	matrix := nativeBuildExecutionMatrix(summary, nativeBuildCompilerSpec)
	for _, index := range []int{0, 1, 3, 4} {
		if matrix[index]["status"] != "performed" {
			t.Fatalf("matrix[%d]=%+v", index, matrix[index])
		}
	}
}

func TestNativeBuildProductResultRequiresProductRetainedCompilerEvidence(t *testing.T) {
	phases := []harnessEditLatencyPhase{
		{Name: "build.backend", Reason: "retained_compiler", OK: true},
		{Name: "go.command", Cache: "retained_recipe", Reason: "build", DurationMS: 50, Actions: 3, PackagesRebuilt: []string{"example/app"}, OK: true},
		{Name: "go.compile", DurationMS: 20, OK: true},
		{Name: "go.link", DurationMS: 25, OK: true},
	}
	result, err := nativeBuildProductResult(phases, "owner", 2)
	if err != nil || result.TransactionMS != 50 || result.ToolInvocations != 3 || result.Owner != "owner" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := nativeBuildProductResult(phases[1:], "owner", 2); err == nil || !strings.Contains(err.Error(), "product candidate") {
		t.Fatalf("missing product backend accepted: %v", err)
	}
}

func TestNativeBuildComparisonUsesExplicitBaseline(t *testing.T) {
	summary := nativeBuildCohortSummary([]nativeBuildDriverSample{
		{Backend: "stock", AccountableBuildMS: 100, FirstVerifiedResponseMS: 110, AcceptedEditMS: 120, OK: true},
		{Backend: "retained_stock", AccountableBuildMS: 60, FirstVerifiedResponseMS: 70, AcceptedEditMS: 80, OK: true},
		{Backend: "compiler", AccountableBuildMS: 70, FirstVerifiedResponseMS: 80, AcceptedEditMS: 90, OK: true},
	})
	deltas := nativeBuildComparisonDeltas(summary, "retained_stock", "compiler")
	build := deltas["accountable_build"].(map[string]any)["p50_ms"].(map[string]any)
	if build["baseline_ms"] != float64(60) || build["candidate_ms"] != float64(70) || build["absolute_reduction_ms"] != float64(-10) {
		t.Fatalf("unfair comparison: %+v", build)
	}
}

func TestNativeBuildSchedulingPoliciesCountEveryMeasuredRequest(t *testing.T) {
	counts := nativeBuildSchedulingPolicies([]nativeBuildDriverSample{
		{SchedulingPolicy: "darwin_background_cleared"},
		{SchedulingPolicy: "darwin_background_absent"},
		{SchedulingPolicy: "darwin_background_absent"},
		{},
	})
	if counts["darwin_background_cleared"] != 1 || counts["darwin_background_absent"] != 2 || counts["not_reported"] != 1 || len(counts) != 3 {
		t.Fatalf("scheduling policy counts = %#v", counts)
	}
}
