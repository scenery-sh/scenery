package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestHarnessNativeContractApplicationProbeStepReportsInjectedResult(t *testing.T) {
	t.Parallel()

	step := runHarnessNativeContractApplicationProbeStepWithCheck(context.Background(), "/repo", func(_ context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
		if repoRoot != "/repo" {
			t.Fatalf("repo root = %q", repoRoot)
		}
		return map[string]any{"proof": "ok"}, nil, nil
	})
	if !step.OK || step.Name != harnessNativeContractApplicationProbeName || step.Summary["proof"] != "ok" {
		t.Fatalf("successful step = %+v", step)
	}
	for _, effect := range []string{"external-binary", "loopback-network", "node-runtime", "tempdir"} {
		if !slices.Contains(harnessStepEffects(step), effect) {
			t.Fatalf("step effects %v missing %q", harnessStepEffects(step), effect)
		}
	}

	failed := runHarnessNativeContractApplicationProbeStepWithCheck(context.Background(), "/repo", func(context.Context, string) (map[string]any, []checkDiagnostic, error) {
		return map[string]any{"proof": "failed"}, nil, errors.New("probe failed")
	})
	if failed.OK || failed.Error != "probe failed" || len(failed.Diagnostics) != 1 {
		t.Fatalf("failed step = %+v", failed)
	}
}
