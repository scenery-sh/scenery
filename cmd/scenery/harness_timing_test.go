package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/testsuite"
)

func TestHarnessTimingBudgetsUseSeparateLanes(t *testing.T) {
	t.Parallel()

	cached := harnessTestTimingBudgetsForMode(harnessSelfModeDefault, false)
	if cached.Lane != "cached" || cached.TotalSeconds != 5 || cached.TargetSeconds != 5 || cached.Mode != "observe-total" {
		t.Fatalf("cached budgets = %+v", cached)
	}
	if cached.PackageSeconds != 10 || cached.PackageOverrides["scenery.sh/cmd/scenery"] != 15 || cached.ConfirmationRuns != 0 {
		t.Fatalf("cached package/test confirmation budgets = %+v", cached)
	}
	if cached.TestBinaryCount != 60 || cached.ColdBuildSeconds != 180 {
		t.Fatalf("cached cold binary budgets = %+v", cached)
	}

	fresh := harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true)
	if fresh.Lane != "fresh" || fresh.TotalSeconds != 5 || fresh.TargetSeconds != 5 || fresh.Mode != "observe-total" {
		t.Fatalf("fresh budgets = %+v", fresh)
	}
	if fresh.ConfirmationRuns != 3 || fresh.ConfirmationScope != harnessConfirmationScopeRegressions {
		t.Fatalf("fresh confirmation budgets = %+v", fresh)
	}

	release := harnessTestTimingBudgetsForMode(harnessSelfModeRelease, false)
	if release.Lane != "release" || release.TotalSeconds != 30 || release.TargetSeconds != 5 || release.Mode != "enforce-total" {
		t.Fatalf("release budgets = %+v", release)
	}
	if release.ConfirmationRuns != 0 || release.ConfirmationScope != "" {
		t.Fatalf("release confirmation budgets = %+v", release)
	}

	audit := harnessTestTimingBudgetsForMode(harnessSelfModeRelease, true)
	if audit.ConfirmationRuns != 3 || audit.ConfirmationScope != harnessConfirmationScopeAll {
		t.Fatalf("release audit confirmation budgets = %+v", audit)
	}
}

func TestSelectHarnessTimingConfirmationsDefersKnownOutliers(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		`{"Action":"pass","Package":"example.com/app","Test":"TestKnownSlow","Elapsed":0.8}`,
		`{"Action":"pass","Package":"example.com/app","Test":"TestWorse","Elapsed":2.0}`,
		`{"Action":"pass","Package":"example.com/app","Test":"TestNew","Elapsed":0.7}`,
		`{"Action":"pass","Package":"example.com/app","Elapsed":13.2}`,
		`{"Action":"pass","Package":"example.com/known","Elapsed":13.1}`,
	}, "\n")
	budgets := harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true)
	report := parseHarnessGoTestTimingWithBudgets([]byte(output), harnessSelfGoTestCommandWithCacheMode(true), 13*time.Second, budgets)

	baseline := &harnessTestTimingReport{
		Packages: []harnessPackageTiming{
			{Package: "example.com/known", Seconds: 13.0, BudgetSeconds: 10},
		},
		ObservedSlowTests: []harnessTestTiming{
			{Name: "TestKnownSlow", Package: "example.com/app", Seconds: 0.8},
			{Name: "TestWorse", Package: "example.com/app", Seconds: 0.8},
		},
	}
	selectHarnessTimingConfirmations(report, baseline)

	observed := map[string]bool{}
	for _, test := range report.ObservedSlowTests {
		observed[test.Name] = true
	}
	if !observed["TestNew"] || !observed["TestWorse"] || observed["TestKnownSlow"] {
		t.Fatalf("observed slow tests after selection = %+v", report.ObservedSlowTests)
	}
	deferred := map[string]float64{}
	for _, entry := range report.DeferredConfirmations {
		deferred[entry.Package+"."+entry.Name] = entry.BaselineSeconds
	}
	if len(deferred) != 2 || deferred["example.com/app.TestKnownSlow"] != 0.8 || deferred["example.com/known."] != 13.0 {
		t.Fatalf("deferred confirmations = %+v", report.DeferredConfirmations)
	}
	if !hasDiagnosticContaining(report.Diagnostics, "skipped isolated confirmation for 2 known timing outlier(s)") {
		t.Fatalf("deferral diagnostic missing: %+v", report.Diagnostics)
	}

	var confirmed []string
	confirmHarnessTimingOutliers(context.Background(), "/repo", report, func(_ context.Context, _ string, command []string) ([]byte, error) {
		confirmed = append(confirmed, strings.Join(command, " "))
		return []byte(`{"Action":"pass","Package":"example.com/app","Elapsed":0.1}`), nil
	})
	for _, command := range confirmed {
		if strings.Contains(command, "example.com/known") {
			t.Fatalf("deferred package was still confirmed: %+v", confirmed)
		}
	}
}

func TestSelectHarnessTimingConfirmationsKeepsEverythingUnderAuditScope(t *testing.T) {
	t.Parallel()

	output := `{"Action":"pass","Package":"example.com/app","Test":"TestKnownSlow","Elapsed":0.8}`
	report := parseHarnessGoTestTimingWithBudgets([]byte(output), harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeRelease, true))
	baseline := &harnessTestTimingReport{
		ObservedSlowTests: []harnessTestTiming{{Name: "TestKnownSlow", Package: "example.com/app", Seconds: 0.8}},
	}
	selectHarnessTimingConfirmations(report, baseline)
	if len(report.ObservedSlowTests) != 1 || len(report.DeferredConfirmations) != 0 {
		t.Fatalf("audit scope narrowed confirmation: observed=%+v deferred=%+v", report.ObservedSlowTests, report.DeferredConfirmations)
	}
}

func TestSelectHarnessTimingConfirmationsWithoutBaselineKeepsEveryCandidate(t *testing.T) {
	t.Parallel()

	output := `{"Action":"pass","Package":"example.com/app","Test":"TestSlow","Elapsed":0.8}`
	report := parseHarnessGoTestTimingWithBudgets([]byte(output), harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	selectHarnessTimingConfirmations(report, nil)
	if len(report.ObservedSlowTests) != 1 || len(report.DeferredConfirmations) != 0 {
		t.Fatalf("missing baseline narrowed confirmation: observed=%+v deferred=%+v", report.ObservedSlowTests, report.DeferredConfirmations)
	}
}

func TestHarnessTestBinaryTimingRanksBuildsByDuration(t *testing.T) {
	t.Parallel()

	timing := harnessTestBinaryTimingFromResult(testsuite.Result{
		ManifestHit:      false,
		TestPackageCount: 2,
		Prepare: testsuite.PrepareTiming{
			Elapsed:     10 * time.Second,
			ListElapsed: 4 * time.Second,
			Builds: []testsuite.BinaryBuild{
				{Package: "scenery.sh/cmd/scenery", BuildID: "id-a", Elapsed: 3500 * time.Millisecond},
				{Package: "scenery.sh/internal/edge", BuildID: "id-b", Elapsed: 900 * time.Millisecond},
			},
		},
	})
	if timing.PrepareSeconds != 10 || timing.ListSeconds != 4 || timing.BuildSeconds != 4.4 || timing.BuiltCount != 2 || timing.TestPackageCount != 2 {
		t.Fatalf("test binary timing = %+v", timing)
	}
	if timing.Builds[0].Package != "scenery.sh/cmd/scenery" || timing.Builds[0].BuildID != "id-a" || timing.Builds[0].Seconds != 3.5 {
		t.Fatalf("slowest build = %+v", timing.Builds)
	}
}

func TestHarnessSelfGoTestCommandUsesResultCacheUnlessFresh(t *testing.T) {
	t.Parallel()
	if got := strings.Join(harnessSelfGoTestCommand(), " "); got != "go test -json ./..." {
		t.Fatalf("cached command = %q", got)
	}
	if got := strings.Join(harnessSelfGoTestCommandWithCacheMode(true), " "); got != "go run ./scripts/testsuite -p 3 -run .*" {
		t.Fatalf("fresh command = %q", got)
	}
}

func TestConfirmHarnessTimingOutliersUsesIsolatedEvidence(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		`{"Action":"pass","Package":"example.com/app","Test":"TestSlow","Elapsed":0.8}`,
		`{"Action":"pass","Package":"example.com/app","Test":"TestAlsoObserved","Elapsed":0.7}`,
		`{"Action":"pass","Package":"example.com/app","Elapsed":13.2}`,
	}, "\n")
	report := parseHarnessGoTestTimingWithBudgets([]byte(output), harnessSelfGoTestCommandWithCacheMode(true), 13*time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	if len(report.ObservedSlowTests) != 2 || len(report.SlowTests) != 0 {
		t.Fatalf("pre-confirmation tests = observed:%+v confirmed:%+v", report.ObservedSlowTests, report.SlowTests)
	}

	var commands []string
	run := func(_ context.Context, _ string, command []string) ([]byte, error) {
		joined := strings.Join(command, " ")
		commands = append(commands, joined)
		switch joined {
		case "go test -count=1 -p 1 -json example.com/app":
			return []byte(`{"Action":"pass","Package":"example.com/app","Elapsed":9.1}`), nil
		case "go test -count=3 -parallel=1 -run ^(TestAlsoObserved|TestSlow)$ -json example.com/app":
			return []byte(strings.Join([]string{
				`{"Action":"pass","Package":"example.com/app","Test":"TestSlow","Elapsed":0.7}`,
				`{"Action":"pass","Package":"example.com/app","Test":"TestSlow","Elapsed":0.9}`,
				`{"Action":"pass","Package":"example.com/app","Test":"TestSlow","Elapsed":0.6}`,
				`{"Action":"pass","Package":"example.com/app","Test":"TestAlsoObserved","Elapsed":0.1}`,
				`{"Action":"pass","Package":"example.com/app","Test":"TestAlsoObserved","Elapsed":0.1}`,
				`{"Action":"pass","Package":"example.com/app","Test":"TestAlsoObserved","Elapsed":0.1}`,
			}, "\n")), nil
		default:
			return nil, fmt.Errorf("unexpected command %q", joined)
		}
	}
	confirmHarnessTimingOutliers(context.Background(), "/repo", report, run)

	if len(commands) != 2 {
		t.Fatalf("commands = %+v", commands)
	}
	if report.Packages[0].IsolatedSeconds == nil || *report.Packages[0].IsolatedSeconds != 9.1 {
		t.Fatalf("package confirmation = %+v", report.Packages[0])
	}
	if len(report.SlowTests) != 1 || report.SlowTests[0].IsolatedMedian == nil || *report.SlowTests[0].IsolatedMedian != 0.7 {
		t.Fatalf("confirmed tests = %+v", report.SlowTests)
	}
	if report.ObservedSlowTests[0].IsolatedMedian == nil || len(report.ObservedSlowTests[0].IsolatedSamples) != 3 {
		t.Fatalf("observed test confirmation evidence = %+v", report.ObservedSlowTests)
	}
	if hasDiagnosticContaining(report.Diagnostics, "package example.com/app took") {
		t.Fatalf("contended package warning was not cleared: %+v", report.Diagnostics)
	}
	if !hasDiagnosticContaining(report.Diagnostics, "test example.com/app.TestSlow took 0.700s median in isolation") {
		t.Fatalf("confirmed test warning missing: %+v", report.Diagnostics)
	}
}

func TestConfirmHarnessTimingOutliersWarnsOnlyForConfirmedPackage(t *testing.T) {
	t.Parallel()

	output := []byte(`{"Action":"pass","Package":"example.com/app","Elapsed":13.2}`)
	report := parseHarnessGoTestTimingWithBudgets(output, harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	confirmHarnessTimingOutliers(context.Background(), "/repo", report, func(_ context.Context, _ string, command []string) ([]byte, error) {
		return []byte(`{"Action":"pass","Package":"example.com/app","Elapsed":12.5}`), nil
	})
	if !hasDiagnosticContaining(report.Diagnostics, "package example.com/app took 12.500s in isolation") {
		t.Fatalf("confirmed package warning missing: %+v", report.Diagnostics)
	}
}

func TestCommandPackageUsesExplicitTimingBudget(t *testing.T) {
	t.Parallel()

	// 12.0s is over the 10s default package budget but under the command
	// package's explicit 15s budget, so only the override can keep it silent.
	output := []byte(`{"Action":"pass","Package":"scenery.sh/cmd/scenery","Elapsed":12.0}`)
	report := parseHarnessGoTestTimingWithBudgets(output, harnessSelfGoTestCommand(), time.Second, defaultHarnessTestTimingBudgets())
	called := false
	confirmHarnessTimingOutliers(context.Background(), "/repo", report, func(context.Context, string, []string) ([]byte, error) {
		called = true
		return nil, nil
	})
	if called || len(report.Packages) != 1 || report.Packages[0].BudgetSeconds != 15 {
		t.Fatalf("command package budget = %+v, called = %v", report.Packages, called)
	}
}

func TestReleaseTimingBudgetIsEnforced(t *testing.T) {
	t.Parallel()
	report := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommand(), 31*time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeRelease, false))
	if !hasErrorDiagnostics(report.Diagnostics) {
		t.Fatalf("diagnostics = %+v, want enforced release error", report.Diagnostics)
	}
}

func TestColdBinaryCountBudgetAppliesOnWarmFreshRuns(t *testing.T) {
	t.Parallel()
	report := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	report.TestBinaries = &harnessTestBinaryTiming{ManifestHit: true, TestPackageCount: 61, BuiltCount: 0}
	applyHarnessColdBinaryBudgets(report)
	if !hasDiagnosticContaining(report.Diagnostics, "fresh lane has 61 test binaries, over the 60 binary-count budget") {
		t.Fatalf("binary-count diagnostic missing: %+v", report.Diagnostics)
	}
	if hasDiagnosticContaining(report.Diagnostics, "link-CPU") {
		t.Fatalf("warm run compared partial linking against the cold CPU budget: %+v", report.Diagnostics)
	}
}

func TestColdBuildSecondsBudgetAppliesOnlyOnFullColdPrepare(t *testing.T) {
	t.Parallel()
	partial := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	partial.TestBinaries = &harnessTestBinaryTiming{TestPackageCount: 60, BuiltCount: 5, BuildSeconds: 200}
	applyHarnessColdBinaryBudgets(partial)
	if len(partial.Diagnostics) != 0 {
		t.Fatalf("partial rebuild tripped a cold budget: %+v", partial.Diagnostics)
	}

	cold := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	cold.TestBinaries = &harnessTestBinaryTiming{TestPackageCount: 60, BuiltCount: 60, BuildSeconds: 180}
	applyHarnessColdBinaryBudgets(cold)
	if !hasDiagnosticContaining(cold.Diagnostics, "cold test-binary linking took 180.000s, over 180.000s link-CPU budget (60 binaries)") {
		t.Fatalf("full-cold CPU diagnostic missing: %+v", cold.Diagnostics)
	}

	held := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	held.TestBinaries = &harnessTestBinaryTiming{TestPackageCount: 60, BuiltCount: 60, BuildSeconds: 179.999}
	applyHarnessColdBinaryBudgets(held)
	if len(held.Diagnostics) != 0 {
		t.Fatalf("full cold under budget still warned: %+v", held.Diagnostics)
	}
}

func TestReleaseColdBinaryBudgetsAreEnforced(t *testing.T) {
	t.Parallel()
	report := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeRelease, true))
	report.TestBinaries = &harnessTestBinaryTiming{TestPackageCount: 61, BuiltCount: 61, BuildSeconds: 200}
	applyHarnessColdBinaryBudgets(report)
	if !hasErrorDiagnostics(report.Diagnostics) {
		t.Fatalf("diagnostics = %+v, want enforced release errors", report.Diagnostics)
	}
	if !hasDiagnosticContaining(report.Diagnostics, "61 test binaries") || !hasDiagnosticContaining(report.Diagnostics, "link-CPU") {
		t.Fatalf("release cold diagnostics = %+v", report.Diagnostics)
	}
}

func hasDiagnosticContaining(diagnostics []checkDiagnostic, substring string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, substring) {
			return true
		}
	}
	return false
}
