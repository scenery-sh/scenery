package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func timingSampleEvents(name string, seconds float64) []byte {
	return []byte(fmt.Sprintf("{\"Action\":\"run\",\"Package\":\"example.com/app\",\"Test\":%q}\n{\"Action\":\"pass\",\"Package\":\"example.com/app\",\"Test\":%q,\"Elapsed\":%g}\n{\"Action\":\"pass\",\"Package\":\"example.com/app\",\"Elapsed\":1}\n", name, name, seconds))
}

func timingPackageList() []byte {
	return []byte(`{"ImportPath":"example.com/app","Dir":"/repo/package"}`)
}

func TestIsolatedHarnessTimingSampleRejectsUnusableEvidence(t *testing.T) {
	t.Parallel()
	valid := timingSampleEvents("TestRoot", 0.05)
	for _, output := range [][]byte{
		nil, []byte("broken"), valid[:len(valid)/2], append(append([]byte{}, valid...), valid...),
		[]byte(strings.ReplaceAll(string(valid), `"run"`, `"output"`)),
		[]byte(strings.ReplaceAll(string(valid), `"pass"`, `"skip"`)),
		append(append([]byte{}, valid...), []byte("{\"Action\":\"output\",\"Package\":\"example.com/app\",\"Output\":\"ok (cached)\"}\n")...),
	} {
		if _, err := isolatedHarnessTimingSample(output, "example.com/app", "TestRoot"); err == nil {
			t.Fatalf("accepted unusable evidence %q", output)
		}
	}
	if seconds, err := isolatedHarnessTimingSample(valid, "example.com/app", "TestRoot"); err != nil || seconds != 0.05 {
		t.Fatalf("valid sample = %v, %v", seconds, err)
	}
}

func TestReleaseTimingConfirmationFailsClosed(t *testing.T) {
	t.Parallel()
	for _, lane := range []string{harnessSelfModeDefault, harnessSelfModeRelease} {
		report := &harnessTestTimingReport{
			Budgets:           harnessTestTimingBudgetsForMode(lane, true),
			ObservedSlowTests: []harnessTestTiming{{Package: "example.com/app", Name: "TestRoot", Seconds: 0.07, BudgetSeconds: 0.1}},
		}
		for _, failure := range []string{"list", "malformed", "identity", "relative", "build", "sample"} {
			copy := *report
			copy.ObservedSlowTests = append([]harnessTestTiming{}, report.ObservedSlowTests...)
			calls := 0
			confirmHarnessTimingOutliers(context.Background(), "/repo", &copy, func(_ context.Context, _ string, command []string) ([]byte, error) {
				if command[1] == "list" {
					if failure == "list" {
						return nil, errors.New("list failed")
					}
					if failure == "identity" {
						return []byte(`{"ImportPath":"other","Dir":"/repo/package"}`), nil
					}
					if failure == "malformed" {
						return []byte("broken"), nil
					}
					if failure == "relative" {
						return []byte(`{"ImportPath":"example.com/app","Dir":"relative"}`), nil
					}
					return timingPackageList(), nil
				}
				if command[1] == "test" {
					if failure == "build" {
						return nil, errors.New("link failed")
					}
					return nil, nil
				}
				calls++
				if calls == 20 {
					return nil, nil
				}
				return timingSampleEvents("TestRoot", 0.05), nil
			})
			wantCalls := 0
			if failure == "sample" {
				wantCalls = 20
			}
			if calls != wantCalls || copy.ObservedSlowTests[0].IsolatedP95 != nil || len(copy.Diagnostics) != 1 || hasErrorDiagnostics(copy.Diagnostics) != (lane == harnessSelfModeRelease) {
				t.Fatalf("confirmation lane=%s failure=%s calls=%d report=%+v", lane, failure, calls, copy)
			}
			if action := copy.Diagnostics[0].SuggestedAction; !strings.Contains(action, "go test") || strings.Contains(action, "scenery-timing-confirm-") {
				t.Fatalf("confirmation must offer a durable reproduction command: %s", action)
			}
		}
	}
}
