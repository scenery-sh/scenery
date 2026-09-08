package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	if cached.DefaultTestClass != harnessTestClassFast || cached.TestTargetSeconds != 0.060 || cached.TestSeconds != 0.100 || cached.ConfirmationPercentile != 95 || cached.IntegrationExceptions == nil || len(cached.IntegrationExceptions) != 0 {
		t.Fatalf("cached fast-test policy = %+v", cached)
	}
	if cached.TestBinaryCount != 60 || cached.ColdPrepareSeconds != 30 {
		t.Fatalf("cached cold binary budgets = %+v", cached)
	}

	fresh := harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true)
	if fresh.Lane != "fresh" || fresh.TotalSeconds != 5 || fresh.TargetSeconds != 5 || fresh.Mode != "observe-total" {
		t.Fatalf("fresh budgets = %+v", fresh)
	}
	if fresh.ConfirmationRuns != 20 || fresh.ConfirmationScope != harnessConfirmationScopeRegressions {
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
	if audit.ConfirmationRuns != 20 || audit.ConfirmationScope != harnessConfirmationScopeAll {
		t.Fatalf("release audit confirmation budgets = %+v", audit)
	}
}

func TestSelectHarnessTimingConfirmationsDefersKnownOutliers(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		`{"Action":"pass","Package":"example.com/app","Test":"TestKnownSlow","Elapsed":0.08}`,
		`{"Action":"pass","Package":"example.com/app","Test":"TestWorse","Elapsed":0.09}`,
		`{"Action":"pass","Package":"example.com/app","Test":"TestNew","Elapsed":0.07}`,
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
			{Name: "TestKnownSlow", Package: "example.com/app", Seconds: 0.08},
			{Name: "TestWorse", Package: "example.com/app", Seconds: 0.06},
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
	if len(deferred) != 2 || deferred["example.com/app.TestKnownSlow"] != 0.08 || deferred["example.com/known."] != 13.0 {
		t.Fatalf("deferred confirmations = %+v", report.DeferredConfirmations)
	}
	if !hasDiagnosticContaining(report.Diagnostics, "skipped isolated confirmation for 2 known timing outlier(s)") {
		t.Fatalf("deferral diagnostic missing: %+v", report.Diagnostics)
	}

	var confirmed []string
	confirmHarnessTimingOutliers(context.Background(), "/repo", report, func(_ context.Context, _ string, command []string) ([]byte, error) {
		confirmed = append(confirmed, strings.Join(command, " "))
		if command[1] == "list" {
			return timingPackageList(), nil
		}
		if command[1] == "test" {
			return nil, nil
		}
		name := strings.TrimSuffix(strings.TrimPrefix(command[len(command)-1], "-test.run=^"), "$")
		return timingSampleEvents(name, 0.05), nil
	})
	for _, command := range confirmed {
		if strings.Contains(command, "example.com/known") {
			t.Fatalf("deferred package was still confirmed: %+v", confirmed)
		}
	}
	for _, observed := range report.ObservedSlowTests {
		if observed.IsolatedP95 == nil {
			t.Fatalf("selected root was not confirmed: %+v", observed)
		}
	}
}

func TestSelectHarnessTimingConfirmationsKeepsEverythingUnderAuditScope(t *testing.T) {
	t.Parallel()

	output := `{"Action":"pass","Package":"example.com/app","Test":"TestKnownSlow","Elapsed":0.08}`
	report := parseHarnessGoTestTimingWithBudgets([]byte(output), harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeRelease, true))
	baseline := &harnessTestTimingReport{
		ObservedSlowTests: []harnessTestTiming{{Name: "TestKnownSlow", Package: "example.com/app", Seconds: 0.08}},
	}
	selectHarnessTimingConfirmations(report, baseline)
	if len(report.ObservedSlowTests) != 1 || len(report.DeferredConfirmations) != 0 {
		t.Fatalf("audit scope narrowed confirmation: observed=%+v deferred=%+v", report.ObservedSlowTests, report.DeferredConfirmations)
	}
}

func TestSelectHarnessTimingConfirmationsWithoutBaselineKeepsEveryCandidate(t *testing.T) {
	t.Parallel()

	output := `{"Action":"pass","Package":"example.com/app","Test":"TestSlow","Elapsed":0.08}`
	report := parseHarnessGoTestTimingWithBudgets([]byte(output), harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	selectHarnessTimingConfirmations(report, nil)
	if len(report.ObservedSlowTests) != 1 || len(report.DeferredConfirmations) != 0 {
		t.Fatalf("missing baseline narrowed confirmation: observed=%+v deferred=%+v", report.ObservedSlowTests, report.DeferredConfirmations)
	}
}

func TestReadHarnessTimingBaselineRejectsStaleSchema(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, ".scenery", "harness", "test-timing-latest.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	report := harnessTestTimingReport{
		PayloadIdentity: cliPayloadIdentity{Kind: harnessTestTimingKind, SchemaRevision: "sha256:" + strings.Repeat("0", 64)},
		Budgets:         defaultHarnessTestTimingBudgets(),
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readHarnessTimingBaseline(root); got != nil {
		t.Fatalf("stale baseline was accepted: %+v", got)
	}

	report.PayloadIdentity = newCLIPayloadIdentity(harnessTestTimingKind)
	encoded, err = json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readHarnessTimingBaseline(root); got == nil {
		t.Fatal("current baseline was rejected")
	}
}

func TestSelectHarnessTimingConfirmationsNeverDefersNewBudgetCrossing(t *testing.T) {
	t.Parallel()

	current := `{"Action":"pass","Package":"example.com/app","Test":"TestCrossed","Elapsed":0.10}`
	report := parseHarnessGoTestTimingWithBudgets([]byte(current), harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	baseline := &harnessTestTimingReport{ObservedSlowTests: []harnessTestTiming{{
		Name: "TestCrossed", Package: "example.com/app", Class: harnessTestClassFast,
		Seconds: 0.09, TargetSeconds: 0.06, BudgetSeconds: 0.10,
	}}}
	selectHarnessTimingConfirmations(report, baseline)
	if len(report.ObservedSlowTests) != 1 || len(report.DeferredConfirmations) != 0 {
		t.Fatalf("new hard-budget crossing was deferred: observed=%+v deferred=%+v", report.ObservedSlowTests, report.DeferredConfirmations)
	}
}

func TestSelectHarnessTimingConfirmationsNeverDefersPriorConfirmedViolation(t *testing.T) {
	t.Parallel()

	current := `{"Action":"pass","Package":"example.com/app","Test":"TestPreviouslySlow","Elapsed":0.07}`
	report := parseHarnessGoTestTimingWithBudgets([]byte(current), harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	p95 := 0.10
	baseline := &harnessTestTimingReport{ObservedSlowTests: []harnessTestTiming{{
		Name: "TestPreviouslySlow", Package: "example.com/app", Class: harnessTestClassFast,
		Seconds: 0.07, TargetSeconds: 0.06, BudgetSeconds: 0.10, IsolatedP95: &p95,
	}}}
	selectHarnessTimingConfirmations(report, baseline)
	if len(report.ObservedSlowTests) != 1 || len(report.DeferredConfirmations) != 0 {
		t.Fatalf("prior confirmed violation was deferred: observed=%+v deferred=%+v", report.ObservedSlowTests, report.DeferredConfirmations)
	}
}

func TestParseHarnessTimingUsesExactTopLevelRootsAndAbsolutePolicy(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		`{"Action":"pass","Package":"example.com/app","Test":"TestRoot/subtest","Elapsed":0.30}`,
		`{"Action":"pass","Package":"example.com/app","Test":"TestRoot","Elapsed":0.08}`,
		`{"Action":"pass","Package":"example.com/app","Test":"TestBelowTarget","Elapsed":0.05}`,
		`{"Action":"pass","Package":"example.com/app","Test":"Testlowercase","Elapsed":0.20}`,
		`{"Action":"pass","Package":"example.com/app","Test":"BenchmarkThing","Elapsed":0.20}`,
		`{"Action":"pass","Package":"scenery.sh/internal/desktop","Test":"TestRunStreamsOutputAndPreservesExitCode","Elapsed":0.40}`,
	}, "\n")
	report := parseHarnessGoTestTimingWithBudgets([]byte(output), harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	if len(report.ObservedSlowTests) != 2 {
		t.Fatalf("observed candidates = %+v, want both exact top-level fast roots", report.ObservedSlowTests)
	}
	got := map[string]harnessTestTiming{}
	for _, timing := range report.ObservedSlowTests {
		got[timing.Name] = timing
	}
	for _, name := range []string{"TestRoot", "TestRunStreamsOutputAndPreservesExitCode"} {
		timing, ok := got[name]
		if !ok || timing.Class != harnessTestClassFast || timing.TargetSeconds != 0.06 || timing.BudgetSeconds != 0.10 {
			t.Fatalf("root timing %s = %+v", name, timing)
		}
	}
	if len(report.ObservedIntegrationTests) != 0 {
		t.Fatalf("integration timings = %+v, want none", report.ObservedIntegrationTests)
	}
}

func TestHarnessTimingIncludesParallelSubtestsWithoutQueueWait(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		`{"Time":"2026-08-24T12:00:00Z","Action":"run","Package":"example.com/app","Test":"TestRoot"}`,
		`{"Time":"2026-08-24T12:00:00.010Z","Action":"pause","Package":"example.com/app","Test":"TestRoot"}`,
		`{"Time":"2026-08-24T12:00:01Z","Action":"cont","Package":"example.com/app","Test":"TestRoot"}`,
		`{"Time":"2026-08-24T12:00:01.010Z","Action":"run","Package":"example.com/app","Test":"TestRoot/parallel"}`,
		`{"Time":"2026-08-24T12:00:01.080Z","Action":"pass","Package":"example.com/app","Test":"TestRoot/parallel","Elapsed":0.07}`,
		`{"Time":"2026-08-24T12:00:01.090Z","Action":"pass","Package":"example.com/app","Test":"TestRoot","Elapsed":0.01}`,
	}, "\n")

	samples := testElapsedSamplesFromGoTestJSON([]byte(output), "example.com/app", "TestRoot")
	if len(samples) != 1 || samples[0] < 0.099 || samples[0] > 0.101 {
		t.Fatalf("root samples = %#v, want active wall time 0.10s", samples)
	}
	report := parseHarnessGoTestTimingWithBudgets([]byte(output), harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	if len(report.ObservedSlowTests) != 1 || report.ObservedSlowTests[0].Name != "TestRoot" || report.ObservedSlowTests[0].Seconds != 0.10 {
		t.Fatalf("observed root timing = %+v", report.ObservedSlowTests)
	}
}

func TestExternalBoundaryObservationUsesFastGate(t *testing.T) {
	t.Parallel()

	output := []byte(`{"Action":"pass","Package":"scenery.sh/internal/desktop","Test":"TestRunStreamsOutputAndPreservesExitCode","Elapsed":3.0}`)
	report := parseHarnessGoTestTimingWithBudgets(output, harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeRelease, true))
	if len(report.ObservedIntegrationTests) != 0 || len(report.ObservedSlowTests) != 1 || len(report.SlowTests) != 0 {
		t.Fatalf("external-boundary root escaped the fast gate: %+v", report)
	}
	got := report.ObservedSlowTests[0]
	if got.Name != "TestRunStreamsOutputAndPreservesExitCode" || got.Class != harnessTestClassFast || got.TargetSeconds != 0.06 || got.BudgetSeconds != 0.10 {
		t.Fatalf("external-boundary root timing = %+v", got)
	}
}

func TestHarnessTimingIntegrationExceptionPolicyRemainsEmpty(t *testing.T) {
	t.Parallel()

	exceptions := harnessTimingIntegrationExceptions()
	if len(exceptions) != 0 {
		t.Fatalf("integration exceptions = %d, want 0", len(exceptions))
	}
	if err := validateHarnessTimingIntegrationExceptions(exceptions); err != nil {
		t.Fatal(err)
	}

	invalid := []harnessTestTimingException{{
		Package:        "example.com/pkg",
		Name:           "TestExternalBoundary",
		Class:          harnessTestClassIntegration,
		TargetSeconds:  harnessIntegrationTestTargetSeconds,
		BudgetSeconds:  harnessIntegrationTestBudgetSeconds,
		BoundaryReason: "process",
	}}
	if err := validateHarnessTimingIntegrationExceptions(invalid); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("non-empty exception policy error = %v", err)
	}
}

func TestHarnessTestBinaryTimingRanksBuildsByDuration(t *testing.T) {
	t.Parallel()

	timing := harnessTestBinaryTimingFromResult(testsuite.Result{
		ManifestHit:      false,
		TestPackageCount: 2,
		BuildParallelism: 4,
		Prepare: testsuite.PrepareTiming{
			Elapsed:     10 * time.Second,
			ListElapsed: 4 * time.Second,
			Builds: []testsuite.BinaryBuild{
				{Package: "scenery.sh/cmd/scenery", BuildID: "id-a", Elapsed: 3500 * time.Millisecond},
				{Package: "scenery.sh/internal/edge", BuildID: "id-b", Elapsed: 900 * time.Millisecond},
			},
		},
	})
	if timing.PrepareSeconds != 10 || timing.ListSeconds != 4 || timing.AggregateBuildSeconds != 4.4 || timing.BuildParallelism != 4 || timing.BuiltCount != 2 || timing.TestPackageCount != 2 {
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
	if got := strings.Join(harnessSelfGoTestCommandWithCacheMode(true), " "); got != "go run ./scripts/testsuite -p 6 -run .*" {
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
	slowSamples := 0
	var linkedBinary string
	links := 0
	run := func(_ context.Context, dir string, command []string) ([]byte, error) {
		joined := strings.Join(command, " ")
		commands = append(commands, joined)
		if command[1] == "tool" && (dir != "/repo/package" || command[6] != linkedBinary || !strings.Contains(joined, "-test.count=1 -test.parallel=1")) {
			t.Fatalf("sample does not use the linked package in isolation: dir=%s command=%v", dir, command)
		}
		switch {
		case joined == "go test -count=1 -p 1 -json example.com/app":
			return []byte(`{"Action":"pass","Package":"example.com/app","Elapsed":9.1}`), nil
		case joined == "go list -json example.com/app":
			return timingPackageList(), nil
		case strings.HasPrefix(joined, "go test -c -o "):
			linkedBinary = command[4]
			links++
			return nil, nil
		case strings.HasPrefix(joined, "go tool test2json -t -p example.com/app ") && strings.HasSuffix(joined, "-test.run=^TestSlow$"):
			slow := 0.07
			if slowSamples >= 18 {
				slow = 0.11
			}
			slowSamples++
			return timingSampleEvents("TestSlow", slow), nil
		case strings.HasPrefix(joined, "go tool test2json -t -p example.com/app ") && strings.HasSuffix(joined, "-test.run=^TestAlsoObserved$"):
			return timingSampleEvents("TestAlsoObserved", 0.05), nil
		default:
			return nil, fmt.Errorf("unexpected command %q", joined)
		}
	}
	confirmHarnessTimingOutliers(context.Background(), "/repo", report, run)

	if len(commands) != 43 || slowSamples != 20 || links != 1 {
		t.Fatalf("commands = %+v", commands)
	}
	if report.Packages[0].IsolatedSeconds == nil || *report.Packages[0].IsolatedSeconds != 9.1 {
		t.Fatalf("package confirmation = %+v", report.Packages[0])
	}
	if len(report.SlowTests) != 1 || report.SlowTests[0].IsolatedP95 == nil || *report.SlowTests[0].IsolatedP95 != 0.11 {
		t.Fatalf("confirmed tests = %+v", report.SlowTests)
	}
	if report.ObservedSlowTests[0].IsolatedP95 == nil || len(report.ObservedSlowTests[0].IsolatedSamples) != 20 {
		t.Fatalf("observed test confirmation evidence = %+v", report.ObservedSlowTests)
	}
	if hasDiagnosticContaining(report.Diagnostics, "package example.com/app took") {
		t.Fatalf("contended package warning was not cleared: %+v", report.Diagnostics)
	}
	if !hasDiagnosticContaining(report.Diagnostics, "fast test example.com/app.TestSlow took 0.110s p95 across 20 isolated serial samples") {
		t.Fatalf("confirmed test warning missing: %+v", report.Diagnostics)
	}
}

func TestNearestRankP95AllowsOneSpikeAndRejectsTwo(t *testing.T) {
	t.Parallel()

	oneSpike := make([]float64, 20)
	for i := range oneSpike {
		oneSpike[i] = 0.05
	}
	oneSpike[19] = 0.25
	if got := nearestRankPercentileSeconds(oneSpike, 0.95); got != 0.05 {
		t.Fatalf("one-spike p95 = %.3f, want 0.050", got)
	}

	twoSpikes := append([]float64(nil), oneSpike...)
	twoSpikes[18] = 0.10
	if got := nearestRankPercentileSeconds(twoSpikes, 0.95); got != 0.10 {
		t.Fatalf("two-spike p95 = %.3f, want inclusive 0.100 violation", got)
	}
}

func TestReleaseFreshFastTestBudgetViolationIsError(t *testing.T) {
	t.Parallel()

	report := parseHarnessGoTestTimingWithBudgets(
		[]byte(`{"Action":"pass","Package":"example.com/app","Test":"TestAtBudget","Elapsed":0.10}`),
		harnessSelfGoTestCommandWithCacheMode(true),
		time.Second,
		harnessTestTimingBudgetsForMode(harnessSelfModeRelease, true),
	)
	confirmHarnessTimingOutliers(context.Background(), "/repo", report, func(_ context.Context, _ string, command []string) ([]byte, error) {
		if command[1] == "list" {
			return timingPackageList(), nil
		}
		if command[1] == "test" {
			return nil, nil
		}
		if !strings.Contains(strings.Join(command, " "), "-test.count=1 ") {
			return nil, fmt.Errorf("unexpected command %q", strings.Join(command, " "))
		}
		return timingSampleEvents("TestAtBudget", 0.10), nil
	})
	if !hasErrorDiagnostics(report.Diagnostics) || len(report.SlowTests) != 1 {
		t.Fatalf("release fresh violation = tests:%+v diagnostics:%+v", report.SlowTests, report.Diagnostics)
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
	if hasDiagnosticContaining(report.Diagnostics, "cold test-binary preparation") {
		t.Fatalf("warm run compared partial linking against the cold prepare budget: %+v", report.Diagnostics)
	}
}

func TestColdPrepareSecondsBudgetAppliesOnlyOnFullColdPrepare(t *testing.T) {
	t.Parallel()
	partial := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	partial.TestBinaries = &harnessTestBinaryTiming{TestPackageCount: 60, BuiltCount: 5, PrepareSeconds: 200, AggregateBuildSeconds: 900, BuildParallelism: 4}
	applyHarnessColdBinaryBudgets(partial)
	if len(partial.Diagnostics) != 0 {
		t.Fatalf("partial rebuild tripped a cold budget: %+v", partial.Diagnostics)
	}

	cold := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	cold.TestBinaries = &harnessTestBinaryTiming{TestPackageCount: 60, BuiltCount: 60, PrepareSeconds: 30, AggregateBuildSeconds: 90, BuildParallelism: 4}
	applyHarnessColdBinaryBudgets(cold)
	if !hasDiagnosticContaining(cold.Diagnostics, "cold test-binary preparation took 30.000s wall time, over 30.000s budget (60 binaries at build-p=4)") {
		t.Fatalf("full-cold wall diagnostic missing: %+v", cold.Diagnostics)
	}

	held := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true))
	held.TestBinaries = &harnessTestBinaryTiming{TestPackageCount: 60, BuiltCount: 60, PrepareSeconds: 29.999, AggregateBuildSeconds: 900, BuildParallelism: 4}
	applyHarnessColdBinaryBudgets(held)
	if len(held.Diagnostics) != 0 {
		t.Fatalf("full cold under budget still warned: %+v", held.Diagnostics)
	}
}

func TestReleaseColdBinaryBudgetsAreEnforced(t *testing.T) {
	t.Parallel()
	report := parseHarnessGoTestTimingWithBudgets(nil, harnessSelfGoTestCommandWithCacheMode(true), time.Second, harnessTestTimingBudgetsForMode(harnessSelfModeRelease, true))
	report.TestBinaries = &harnessTestBinaryTiming{TestPackageCount: 61, BuiltCount: 61, PrepareSeconds: 30, AggregateBuildSeconds: 200, BuildParallelism: 4}
	applyHarnessColdBinaryBudgets(report)
	if !hasErrorDiagnostics(report.Diagnostics) {
		t.Fatalf("diagnostics = %+v, want enforced release errors", report.Diagnostics)
	}
	if !hasDiagnosticContaining(report.Diagnostics, "61 test binaries") || !hasDiagnosticContaining(report.Diagnostics, "cold test-binary preparation") {
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
