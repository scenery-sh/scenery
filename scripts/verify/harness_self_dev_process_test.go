package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestHarnessDevManagedProcessProbeStepReportsInjectedResult(t *testing.T) {
	t.Parallel()

	step := runHarnessDevManagedProcessProbeStepWithCheck(context.Background(), "/repo", func(_ context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
		if repoRoot != "/repo" {
			t.Fatalf("repo root = %q", repoRoot)
		}
		return map[string]any{"proof": "ok"}, nil, nil
	})
	if !step.OK || step.Name != harnessDevManagedProcessProbeName || step.Summary["proof"] != "ok" {
		t.Fatalf("successful step = %+v", step)
	}
	if !slices.Contains(harnessStepEffects(step), "external-binary") {
		t.Fatalf("step effects = %v, want external-binary", harnessStepEffects(step))
	}

	failed := runHarnessDevManagedProcessProbeStepWithCheck(context.Background(), "/repo", func(context.Context, string) (map[string]any, []checkDiagnostic, error) {
		return map[string]any{"proof": "failed"}, nil, errors.New("probe failed")
	})
	if failed.OK || failed.Error != "probe failed" || len(failed.Diagnostics) != 1 {
		t.Fatalf("failed step = %+v", failed)
	}
}
