package main

import (
	"context"
	"errors"
	"testing"
)

func TestHarnessStandardAuthCasesAndFailureEvidence(t *testing.T) {
	t.Parallel()
	ids, roots := map[string]bool{}, map[string]bool{}
	for _, scenario := range harnessStandardAuthCases {
		if scenario.ID == "" || !isExactTopLevelGoTestRoot(scenario.Root) || ids[scenario.ID] || roots[scenario.Root] {
			t.Fatalf("invalid or duplicate auth scenario: %+v", scenario)
		}
		ids[scenario.ID], roots[scenario.Root] = true, true
	}
	if len(ids) != 15 || !ids["refresh-replay"] || !ids["token-rotation"] || !ids["user-lifecycle"] {
		t.Fatalf("auth assertion inventory changed: %v", ids)
	}
	for _, report := range []harnessAuthCaseReport{
		{Case: "schema", OK: true, Assertions: []string{"initialized", "schema exists"}},
		{Case: "wrong", OK: true, Assertions: []string{"initialized", "schema exists"}},
		{Case: "schema", OK: false, Error: "database unavailable"},
		{Case: "schema", OK: true, Assertions: []string{"initialized"}},
		{Case: "schema", OK: true, Assertions: []string{"initialized", "schema exists"}, Error: "cleanup failed"},
	} {
		err := validateHarnessAuthCase("schema", report)
		want := report.Case == "schema" && report.OK && len(report.Assertions) == 2 && report.Error == ""
		if (err == nil) != want {
			t.Fatalf("auth report acceptance=%v report=%+v", err, report)
		}
	}
	for _, failure := range []error{nil, errors.New("owned database cleanup failed")} {
		step := runHarnessStandardAuthStepWithCheck(t.Context(), "/repo", func(_ context.Context, root string) (map[string]any, error) {
			if root != "/repo" {
				t.Fatal(root)
			}
			return map[string]any{"cases": []map[string]any{{"id": "refresh-replay", "ok": true}}}, failure
		})
		if step.OK != (failure == nil) || hasErrorDiagnostics(step.Diagnostics) != (failure != nil) || step.Summary["cases"] == nil || step.Name != harnessStandardAuthName {
			t.Fatalf("auth failure evidence changed: %+v", step)
		}
	}
}
