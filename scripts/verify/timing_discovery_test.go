package main

import (
	"testing"
	"time"
)

func TestMandatoryTimingRejectsIncompleteDiscovery(t *testing.T) {
	t.Parallel()
	good := `{"Time":"2026-10-07T00:00:00Z","Action":"start","Package":"example"}
{"Time":"2026-10-07T00:00:00Z","Action":"run","Package":"example","Test":"TestOne"}
{"Time":"2026-10-07T00:00:00.01Z","Action":"pass","Package":"example","Test":"TestOne","Elapsed":0.01}
{"Time":"2026-10-07T00:00:00.02Z","Action":"pass","Package":"example","Elapsed":0.02}`
	budgets := defaultHarnessTestTimingBudgets()
	budgets.Mode = "enforce-total"
	for _, stream := range []string{good + "\n{truncated", good[:len(good)-90], `{"Action":"pass","Package":"example"}`, ""} {
		report := parseHarnessGoTestTimingWithBudgets([]byte(stream), []string{"go"}, time.Millisecond, budgets)
		qualifyTimingDiscovery(report)
		if report.Discovery.Complete || !hasErrorDiagnostics(report.Diagnostics) {
			t.Fatalf("incomplete stream satisfied mandatory audit: %+v", report)
		}
	}
	complete := parseHarnessGoTestTimingWithBudgets([]byte(good), []string{"go"}, time.Millisecond, budgets)
	qualifyTimingDiscovery(complete)
	if !complete.Discovery.Complete || hasErrorDiagnostics(complete.Diagnostics) {
		t.Fatalf("complete stream rejected: %+v", complete)
	}
	expected := 2
	complete.Discovery.ExpectedRoots = &expected
	qualifyTimingDiscovery(complete)
	if complete.Discovery.Complete || !hasErrorDiagnostics(complete.Diagnostics) {
		t.Fatal("missing discovered root was hidden")
	}
}

func TestGoCasesMarkCachedDurationsAsReplay(t *testing.T) {
	t.Parallel()
	stream := []byte(`{"Time":"2026-10-07T00:00:00Z","Action":"start","Package":"example"}
{"Time":"2026-10-07T00:00:00Z","Action":"run","Package":"example","Test":"TestOne"}
{"Time":"2026-10-07T00:00:00Z","Action":"pass","Package":"example","Test":"TestOne","Elapsed":0.04}
{"Time":"2026-10-07T00:00:00Z","Action":"output","Package":"example","Output":"ok example (cached)"}
{"Time":"2026-10-07T00:00:00Z","Action":"pass","Package":"example"}`)
	report := parseHarnessGoTestTimingWithBudgets(stream, []string{"go"}, time.Millisecond, defaultHarnessTestTimingBudgets())
	result := normalizeGoCases(stream, report, nil)
	if len(result.Cases) != 1 || !result.Cases[0].Replayed || result.Completeness.Executed != 0 || result.Cases[0].FirstAttemptOutcome != "incomplete" {
		t.Fatalf("cached output presented as execution: %+v", result)
	}
}
