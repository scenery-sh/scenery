package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestHarnessDesktopProcessProbeStepReportsInjectedResult(t *testing.T) {
	t.Parallel()

	step := runHarnessDesktopProcessProbeStepWithCheck(context.Background(), "/repo", func(_ context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
		if repoRoot != "/repo" {
			t.Fatalf("repo root = %q", repoRoot)
		}
		return map[string]any{"proof": "ok"}, nil, nil
	})
	if !step.OK || step.Name != harnessDesktopProcessProbeName || step.Summary["proof"] != "ok" {
		t.Fatalf("successful step = %+v", step)
	}
	for _, effect := range []string{"agent-socket", "external-binary", "filesystem-write", "loopback-network", "ports", "tempdir"} {
		if !slices.Contains(harnessStepEffects(step), effect) {
			t.Fatalf("step effects %v missing %q", harnessStepEffects(step), effect)
		}
	}

	failed := runHarnessDesktopProcessProbeStepWithCheck(context.Background(), "/repo", func(context.Context, string) (map[string]any, []checkDiagnostic, error) {
		return map[string]any{"proof": "failed"}, nil, errors.New("probe failed")
	})
	if failed.OK || failed.Error != "probe failed" || len(failed.Diagnostics) != 1 {
		t.Fatalf("failed step = %+v", failed)
	}
}
