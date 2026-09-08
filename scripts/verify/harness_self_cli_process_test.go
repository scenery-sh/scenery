package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestHarnessCLIProcessProbeStepReportsInjectedResult(t *testing.T) {
	t.Parallel()

	step := runHarnessCLIProcessProbeStepWithCheck(context.Background(), "/repo", func(_ context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
		if repoRoot != "/repo" {
			t.Fatalf("repo root = %q", repoRoot)
		}
		return map[string]any{"proof": "ok"}, nil, nil
	})
	if !step.OK || step.Name != harnessCLIProcessProbeName || step.Summary["proof"] != "ok" {
		t.Fatalf("successful step = %+v", step)
	}
	for _, effect := range []string{"external-binary", "filesystem-write", "tempdir"} {
		if !slices.Contains(harnessStepEffects(step), effect) {
			t.Fatalf("step effects %v missing %q", harnessStepEffects(step), effect)
		}
	}

	failed := runHarnessCLIProcessProbeStepWithCheck(context.Background(), "/repo", func(context.Context, string) (map[string]any, []checkDiagnostic, error) {
		return nil, nil, errors.New("probe failed")
	})
	if failed.OK || failed.Error != "probe failed" || len(failed.Diagnostics) != 1 {
		t.Fatalf("failed step = %+v", failed)
	}
}
