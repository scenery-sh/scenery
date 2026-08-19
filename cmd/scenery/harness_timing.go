package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/testsuite"
)

type harnessTimingCommandRunner func(context.Context, string, []string) ([]byte, error)

// A candidate is re-confirmed only when it is materially worse than the last
// recorded run: both thresholds must be crossed so that neither ordinary
// scheduling jitter on a fast test nor a small absolute drift on a slow one
// buys a full multi-run confirmation pass.
const (
	harnessTimingRegressionRatio   = 1.25
	harnessTimingRegressionSeconds = 0.5
)

func harnessTestBinaryTimingFromResult(result testsuite.Result) *harnessTestBinaryTiming {
	timing := &harnessTestBinaryTiming{
		ManifestHit:      result.ManifestHit,
		PrepareSeconds:   roundSeconds(result.Prepare.Elapsed.Seconds()),
		ListSeconds:      roundSeconds(result.Prepare.ListElapsed.Seconds()),
		BuildSeconds:     roundSeconds(result.Prepare.BuildElapsed().Seconds()),
		BuiltCount:       len(result.Prepare.Builds),
		TestPackageCount: result.TestPackageCount,
	}
	for _, build := range result.Prepare.Builds {
		timing.Builds = append(timing.Builds, harnessTestBinaryBuild{
			Package: build.Package,
			BuildID: build.BuildID,
			Seconds: roundSeconds(build.Elapsed.Seconds()),
		})
	}
	return timing
}

// readHarnessTimingBaseline loads the previous run's report. A missing or
// unreadable artifact is not an error: without a baseline every candidate is
// new, which is the conservative direction.
func readHarnessTimingBaseline(repoRoot string) *harnessTestTimingReport {
	report, err := readHarnessJSON[harnessTestTimingReport](filepath.Join(repoRoot, ".scenery", "harness", "test-timing-latest.json"))
	if err != nil {
		return nil
	}
	return &report
}

// selectHarnessTimingConfirmations narrows the confirmation pass to candidates
// that are new or materially worse than the baseline, and records every skip
// in the report. Under the "all" scope it keeps every candidate.
func selectHarnessTimingConfirmations(report *harnessTestTimingReport, baseline *harnessTestTimingReport) {
	if report == nil || report.Budgets.ConfirmationScope == harnessConfirmationScopeAll {
		return
	}
	baselinePackages := map[string]float64{}
	baselineTests := map[string]float64{}
	if baseline != nil {
		for _, pkg := range baseline.Packages {
			if pkg.BudgetSeconds > 0 && harnessPackageTimingEffectiveSeconds(pkg) >= pkg.BudgetSeconds {
				baselinePackages[pkg.Package] = harnessPackageTimingEffectiveSeconds(pkg)
			}
		}
		for _, test := range baseline.ObservedSlowTests {
			baselineTests[test.Package+"."+test.Name] = harnessTestTimingEffectiveSeconds(test)
		}
	}

	for i := range report.Packages {
		pkg := &report.Packages[i]
		if pkg.BudgetSeconds <= 0 || pkg.Seconds < pkg.BudgetSeconds {
			continue
		}
		known, ok := baselinePackages[pkg.Package]
		if !ok || harnessTimingIsRegression(pkg.Seconds, known) {
			continue
		}
		report.DeferredConfirmations = append(report.DeferredConfirmations, harnessTimingDeferral{
			Package:         pkg.Package,
			Seconds:         pkg.Seconds,
			BaselineSeconds: known,
			Reason:          "package was already over budget at this level in the recorded baseline",
		})
	}

	kept := report.ObservedSlowTests[:0]
	for _, test := range report.ObservedSlowTests {
		known, ok := baselineTests[test.Package+"."+test.Name]
		if !ok || harnessTimingIsRegression(test.Seconds, known) {
			kept = append(kept, test)
			continue
		}
		report.DeferredConfirmations = append(report.DeferredConfirmations, harnessTimingDeferral{
			Package:         test.Package,
			Name:            test.Name,
			Seconds:         test.Seconds,
			BaselineSeconds: known,
			Reason:          "test was already over budget at this level in the recorded baseline",
		})
	}
	report.ObservedSlowTests = kept

	if len(report.DeferredConfirmations) == 0 {
		return
	}
	sort.Slice(report.DeferredConfirmations, func(i, j int) bool {
		if report.DeferredConfirmations[i].Seconds == report.DeferredConfirmations[j].Seconds {
			return report.DeferredConfirmations[i].Package+"."+report.DeferredConfirmations[i].Name <
				report.DeferredConfirmations[j].Package+"."+report.DeferredConfirmations[j].Name
		}
		return report.DeferredConfirmations[i].Seconds > report.DeferredConfirmations[j].Seconds
	})
	report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
		Stage:           "go tests",
		Severity:        "info",
		Message:         fmt.Sprintf("skipped isolated confirmation for %d known timing outlier(s) at their recorded level", len(report.DeferredConfirmations)),
		SuggestedAction: "Run `.scenery/harness/bin/scenery harness self --release --fresh-tests -o json --write` for a full timing audit, or read `deferred_confirmations` in `.scenery/harness/test-timing-latest.json`.",
	})
}

// applyHarnessColdBinaryBudgets records the cold-link regression signal.
// Binary count is a structural cost and is checked on every fresh run.
// Aggregate link CPU is only comparable on a full cold prepare, when every
// listed test package was linked in this run.
func applyHarnessColdBinaryBudgets(report *harnessTestTimingReport) {
	if report == nil || report.TestBinaries == nil {
		return
	}
	timing := report.TestBinaries
	budgets := report.Budgets
	severity := "warning"
	suggestion := "Review `.scenery/harness/test-timing-latest.json` test_binaries; cold binary budgets are advisory in default self-harness mode."
	if budgets.Mode == "enforce-total" {
		severity = "error"
		suggestion = "Shrink test-binary closures or raise the recorded cold budget in defaultHarnessTestTimingBudgets if the growth is intentional."
	}
	if budgets.TestBinaryCount > 0 && timing.TestPackageCount > budgets.TestBinaryCount {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "go tests",
			Severity:        severity,
			Message:         fmt.Sprintf("fresh lane has %d test binaries, over the %d binary-count budget", timing.TestPackageCount, budgets.TestBinaryCount),
			SuggestedAction: suggestion,
		})
	}
	fullCold := timing.TestPackageCount > 0 && timing.BuiltCount == timing.TestPackageCount
	if fullCold && budgets.ColdBuildSeconds > 0 && timing.BuildSeconds >= budgets.ColdBuildSeconds {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "go tests",
			Severity:        severity,
			Message:         fmt.Sprintf("cold test-binary linking took %.3fs, over %.3fs link-CPU budget (%d binaries)", timing.BuildSeconds, budgets.ColdBuildSeconds, timing.BuiltCount),
			SuggestedAction: suggestion,
		})
	}
}

func harnessTimingIsRegression(seconds, baselineSeconds float64) bool {
	if baselineSeconds <= 0 {
		return true
	}
	return seconds >= baselineSeconds*harnessTimingRegressionRatio && seconds >= baselineSeconds+harnessTimingRegressionSeconds
}

func harnessPackageTimingEffectiveSeconds(timing harnessPackageTiming) float64 {
	if timing.IsolatedSeconds != nil {
		return *timing.IsolatedSeconds
	}
	return timing.Seconds
}

func confirmHarnessTimingOutliers(ctx context.Context, repoRoot string, report *harnessTestTimingReport, run harnessTimingCommandRunner) {
	if report == nil || run == nil {
		return
	}
	started := time.Now()
	confirmationRuns := report.Budgets.ConfirmationRuns
	if confirmationRuns <= 0 {
		return
	}
	deferredPackages := map[string]bool{}
	for _, deferral := range report.DeferredConfirmations {
		if deferral.Name == "" {
			deferredPackages[deferral.Package] = true
		}
	}
	var packageIndices []int
	command := []string{"go", "test", "-count=1", "-p", "1", "-json"}
	for i, pkg := range report.Packages {
		if pkg.Seconds >= pkg.BudgetSeconds && !deferredPackages[pkg.Package] {
			packageIndices = append(packageIndices, i)
			command = append(command, pkg.Package)
		}
	}
	if len(packageIndices) > 0 {
		output, err := run(ctx, repoRoot, command)
		if err != nil {
			report.Diagnostics = append(report.Diagnostics, timingConfirmationFailure("packages", command, err))
		} else {
			for _, index := range packageIndices {
				pkg := &report.Packages[index]
				seconds, ok := packageElapsedFromGoTestJSON(output, pkg.Package)
				if !ok {
					report.Diagnostics = append(report.Diagnostics, timingConfirmationFailure("package "+pkg.Package, command, errors.New("package timing missing from JSON output")))
					continue
				}
				seconds = roundSeconds(seconds)
				pkg.IsolatedSeconds = &seconds
				if seconds >= pkg.BudgetSeconds {
					report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
						Stage:           "go tests",
						Severity:        "warning",
						Message:         fmt.Sprintf("package %s took %.3fs in isolation, over %.3fs budget (%.3fs in full suite)", pkg.Package, seconds, pkg.BudgetSeconds, pkg.Seconds),
						SuggestedAction: "Inspect `.scenery/harness/test-timing-latest.json` and reduce repeated process startup or slow fixture setup.",
					})
				}
			}
		}
	}

	for _, group := range harnessTestConfirmationGroups(report.ObservedSlowTests) {
		command := []string{"go", "test", fmt.Sprintf("-count=%d", confirmationRuns), "-parallel=1", "-run", harnessTestGroupRunPattern(report.ObservedSlowTests, group.Indices), "-json", group.Package}
		output, err := run(ctx, repoRoot, command)
		if err != nil {
			report.Diagnostics = append(report.Diagnostics, timingConfirmationFailure("tests in package "+group.Package, command, err))
			continue
		}
		for _, index := range group.Indices {
			observed := &report.ObservedSlowTests[index]
			samples := testElapsedSamplesFromGoTestJSON(output, observed.Package, observed.Name)
			if len(samples) != confirmationRuns {
				report.Diagnostics = append(report.Diagnostics, timingConfirmationFailure(
					"test "+observed.Package+"."+observed.Name,
					command,
					fmt.Errorf("got %d timing samples, want %d", len(samples), confirmationRuns),
				))
				continue
			}
			for i := range samples {
				samples[i] = roundSeconds(samples[i])
			}
			median := roundSeconds(medianSeconds(samples))
			observed.IsolatedSamples = samples
			observed.IsolatedMedian = &median
			if median < observed.BudgetSeconds {
				continue
			}
			confirmed := *observed
			report.SlowTests = append(report.SlowTests, confirmed)
			for i := range report.Packages {
				if report.Packages[i].Package == confirmed.Package {
					report.Packages[i].Tests = append(report.Packages[i].Tests, confirmed)
					break
				}
			}
			report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
				Stage:           "go tests",
				Severity:        "warning",
				Message:         fmt.Sprintf("test %s.%s took %.3fs median in isolation, over %.3fs budget (%.3fs in full suite)", confirmed.Package, confirmed.Name, median, confirmed.BudgetSeconds, confirmed.Seconds),
				SuggestedAction: "Reduce repeated setup or process startup in the confirmed slow test without weakening its assertion boundary.",
			})
		}
	}
	for i := range report.Packages {
		sort.Slice(report.Packages[i].Tests, func(a, b int) bool {
			return harnessTestTimingEffectiveSeconds(report.Packages[i].Tests[a]) > harnessTestTimingEffectiveSeconds(report.Packages[i].Tests[b])
		})
	}
	sort.Slice(report.SlowTests, func(i, j int) bool {
		left := harnessTestTimingEffectiveSeconds(report.SlowTests[i])
		right := harnessTestTimingEffectiveSeconds(report.SlowTests[j])
		if left == right {
			return report.SlowTests[i].Package+"."+report.SlowTests[i].Name < report.SlowTests[j].Package+"."+report.SlowTests[j].Name
		}
		return left > right
	})
	report.ConfirmationSeconds = roundSeconds(time.Since(started).Seconds())
}

func runHarnessTimingConfirmationCommand(ctx context.Context, repoRoot string, command []string) ([]byte, error) {
	path, err := exec.LookPath(command[0])
	if err != nil {
		return nil, err
	}
	cmd := commandTreeContext(ctx, path, command[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = envWithOverrides(envpolicy.Environ(), harnessSelfGoTestEnv()...)
	return cmd.CombinedOutput()
}

func packageElapsedFromGoTestJSON(output []byte, packageName string) (float64, bool) {
	var seconds float64
	found := false
	scanGoTestJSONEvents(output, func(event goTestJSONEvent) {
		if event.Package == packageName && event.Test == "" && (event.Action == "pass" || event.Action == "fail") && event.Elapsed > 0 {
			seconds = event.Elapsed
			found = true
		}
	})
	return seconds, found
}

func testElapsedSamplesFromGoTestJSON(output []byte, packageName, testName string) []float64 {
	var samples []float64
	scanGoTestJSONEvents(output, func(event goTestJSONEvent) {
		if event.Package == packageName && event.Test == testName && (event.Action == "pass" || event.Action == "fail") {
			samples = append(samples, event.Elapsed)
		}
	})
	return samples
}

func scanGoTestJSONEvents(output []byte, visit func(goTestJSONEvent)) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		var event goTestJSONEvent
		if err := json.Unmarshal(bytes.TrimSpace(scanner.Bytes()), &event); err == nil {
			visit(event)
		}
	}
}

type harnessTestConfirmationGroup struct {
	Package string
	Indices []int
}

func harnessTestConfirmationGroups(timings []harnessTestTiming) []harnessTestConfirmationGroup {
	byPackage := map[string][]int{}
	for i, timing := range timings {
		byPackage[timing.Package] = append(byPackage[timing.Package], i)
	}
	packages := make([]string, 0, len(byPackage))
	for packageName := range byPackage {
		packages = append(packages, packageName)
	}
	sort.Strings(packages)
	groups := make([]harnessTestConfirmationGroup, 0, len(packages))
	for _, packageName := range packages {
		groups = append(groups, harnessTestConfirmationGroup{Package: packageName, Indices: byPackage[packageName]})
	}
	return groups
}

func harnessTestGroupRunPattern(timings []harnessTestTiming, indices []int) string {
	names := map[string]bool{}
	for _, index := range indices {
		name := strings.SplitN(timings[index].Name, "/", 2)[0]
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, regexp.QuoteMeta(name))
	}
	sort.Strings(sorted)
	if len(sorted) == 1 {
		return "^" + sorted[0] + "$"
	}
	return "^(" + strings.Join(sorted, "|") + ")$"
}

func medianSeconds(values []float64) float64 {
	values = append([]float64{}, values...)
	sort.Float64s(values)
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return (values[middle-1] + values[middle]) / 2
}

func harnessTestTimingEffectiveSeconds(timing harnessTestTiming) float64 {
	if timing.IsolatedMedian != nil {
		return *timing.IsolatedMedian
	}
	return timing.Seconds
}

func timingConfirmationFailure(subject string, command []string, err error) checkDiagnostic {
	return checkDiagnostic{
		Stage:           "go tests",
		Severity:        "warning",
		Message:         fmt.Sprintf("could not confirm timing for %s: %v", subject, err),
		SuggestedAction: "Rerun `" + strings.Join(command, " ") + "` and inspect its JSON output.",
	}
}
