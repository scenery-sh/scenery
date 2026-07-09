package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
)

type harnessTimingCommandRunner func(context.Context, string, []string) ([]byte, error)

func confirmHarnessTimingOutliers(ctx context.Context, repoRoot string, report *harnessTestTimingReport, run harnessTimingCommandRunner) {
	if report == nil || run == nil {
		return
	}
	started := time.Now()
	confirmationRuns := report.Budgets.ConfirmationRuns
	if confirmationRuns <= 0 {
		confirmationRuns = harnessTimingConfirmationRuns
	}
	for i := range report.Packages {
		pkg := &report.Packages[i]
		if pkg.Seconds < pkg.BudgetSeconds {
			continue
		}
		command := []string{"go", "test", "-count=1", "-json", pkg.Package}
		output, err := run(ctx, repoRoot, command)
		if err != nil {
			report.Diagnostics = append(report.Diagnostics, timingConfirmationFailure("package "+pkg.Package, command, err))
			continue
		}
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

	for i := range report.ObservedSlowTests {
		observed := &report.ObservedSlowTests[i]
		command := []string{"go", "test", fmt.Sprintf("-count=%d", confirmationRuns), "-run", exactGoTestRunPattern(observed.Name), "-json", observed.Package}
		output, err := run(ctx, repoRoot, command)
		if err != nil {
			report.Diagnostics = append(report.Diagnostics, timingConfirmationFailure("test "+observed.Package+"."+observed.Name, command, err))
			continue
		}
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

func exactGoTestRunPattern(testName string) string {
	parts := strings.Split(testName, "/")
	for i, part := range parts {
		parts[i] = "^" + regexp.QuoteMeta(part) + "$"
	}
	return strings.Join(parts, "/")
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
