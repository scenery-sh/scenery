package main

import (
	"context"
	"errors"
	"testing"
)

func TestHarnessStandardAuthFailureEvidence(t *testing.T) {
	t.Parallel()
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
