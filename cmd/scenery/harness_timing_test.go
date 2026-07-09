package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestHarnessTimingBudgetsUseSeparateLanes(t *testing.T) {
	t.Parallel()

	cached := harnessTestTimingBudgetsForMode(harnessSelfModeDefault, false)
	if cached.Lane != "cached" || cached.TotalSeconds != 12 || cached.TargetSeconds != 7 || cached.Mode != "observe-total" {
		t.Fatalf("cached budgets = %+v", cached)
	}
	if cached.PackageOverrides["scenery.sh/cmd/scenery"] != 10 || cached.ConfirmationRuns != 3 {
		t.Fatalf("cached package/test confirmation budgets = %+v", cached)
	}

	fresh := harnessTestTimingBudgetsForMode(harnessSelfModeDefault, true)
	if fresh.Lane != "fresh" || fresh.TotalSeconds != 18 || fresh.TargetSeconds != 7 || fresh.Mode != "observe-total" {
		t.Fatalf("fresh budgets = %+v", fresh)
	}

	release := harnessTestTimingBudgetsForMode(harnessSelfModeRelease, false)
	if release.Lane != "release" || release.TotalSeconds != 30 || release.TargetSeconds != 7 || release.Mode != "enforce-total" {
		t.Fatalf("release budgets = %+v", release)
	}
}

func TestHarnessSelfGoTestCommandUsesMeasuredPackageParallelism(t *testing.T) {
	t.Parallel()
	if got := strings.Join(harnessSelfGoTestCommand(), " "); got != "go test -p 8 -json ./..." {
		t.Fatalf("cached command = %q", got)
	}
	if got := strings.Join(harnessSelfGoTestCommandWithCacheMode(true), " "); got != "go test -count=1 -p 8 -json ./..." {
		t.Fatalf("fresh command = %q", got)
	}
}

func TestConfirmHarnessTimingOutliersUsesIsolatedEvidence(t *testing.T) {
	output := strings.Join([]string{
		`{"Action":"pass","Package":"example.com/app","Test":"TestSlow","Elapsed":0.8}`,
		`{"Action":"pass","Package":"example.com/app","Test":"TestAlsoObserved","Elapsed":0.7}`,
		`{"Action":"pass","Package":"example.com/app","Elapsed":3.2}`,
	}, "\n")
	report := parseHarnessGoTestTimingWithBudgets([]byte(output), harnessSelfGoTestCommand(), 13*time.Second, defaultHarnessTestTimingBudgets())
	if len(report.ObservedSlowTests) != 2 || len(report.SlowTests) != 0 {
		t.Fatalf("pre-confirmation tests = observed:%+v confirmed:%+v", report.ObservedSlowTests, report.SlowTests)
	}

	var commands []string
	run := func(_ context.Context, _ string, command []string) ([]byte, error) {
		joined := strings.Join(command, " ")
		commands = append(commands, joined)
		switch joined {
		case "go test -count=1 -p 1 -json example.com/app":
			return []byte(`{"Action":"pass","Package":"example.com/app","Elapsed":1.1}`), nil
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
	if report.Packages[0].IsolatedSeconds == nil || *report.Packages[0].IsolatedSeconds != 1.1 {
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
	output := []byte(`{"Action":"pass","Package":"example.com/app","Elapsed":3.2}`)
	report := parseHarnessGoTestTimingWithBudgets(output, harnessSelfGoTestCommand(), time.Second, defaultHarnessTestTimingBudgets())
	confirmHarnessTimingOutliers(context.Background(), "/repo", report, func(_ context.Context, _ string, command []string) ([]byte, error) {
		return []byte(`{"Action":"pass","Package":"example.com/app","Elapsed":2.5}`), nil
	})
	if !hasDiagnosticContaining(report.Diagnostics, "package example.com/app took 2.500s in isolation") {
		t.Fatalf("confirmed package warning missing: %+v", report.Diagnostics)
	}
}

func TestCommandPackageUsesExplicitTimingBudget(t *testing.T) {
	output := []byte(`{"Action":"pass","Package":"scenery.sh/cmd/scenery","Elapsed":9}`)
	report := parseHarnessGoTestTimingWithBudgets(output, harnessSelfGoTestCommand(), time.Second, defaultHarnessTestTimingBudgets())
	called := false
	confirmHarnessTimingOutliers(context.Background(), "/repo", report, func(context.Context, string, []string) ([]byte, error) {
		called = true
		return nil, nil
	})
	if called || len(report.Packages) != 1 || report.Packages[0].BudgetSeconds != 10 {
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

func hasDiagnosticContaining(diagnostics []checkDiagnostic, substring string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, substring) {
			return true
		}
	}
	return false
}
