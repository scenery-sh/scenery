package main

import (
	"context"
	"os/exec"
	"testing"
)

func TestHarnessConsoleDepsStepMissingBun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	step, ready := runHarnessConsoleDepsStep(context.Background(), t.TempDir(), harnessArtifactContext{})
	if ready {
		t.Fatal("console deps step must not report ready without bun in PATH")
	}
	if step.OK {
		t.Fatal("console deps step must fail without bun in PATH")
	}
	if step.Name != "console dependencies" {
		t.Fatalf("step name = %q", step.Name)
	}
	if len(step.Diagnostics) != 1 || step.Diagnostics[0].Severity != "error" {
		t.Fatalf("expected one error diagnostic, got %+v", step.Diagnostics)
	}
	skipped, ok := step.Summary["skipped_lanes"].([]string)
	if !ok || len(skipped) != len(harnessConsoleLaneNames) {
		t.Fatalf("skipped_lanes = %v", step.Summary["skipped_lanes"])
	}
}

func TestHarnessConsoleDepsStepInstallFailure(t *testing.T) {
	// An empty directory has no package.json, so a real bun install fails and
	// the step must gate the dependent lanes with an actionable diagnostic.
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun unavailable")
	}
	step, ready := runHarnessConsoleDepsStep(context.Background(), t.TempDir(), harnessArtifactContext{})
	if ready || step.OK {
		t.Fatalf("console deps step must fail in a directory without package.json: %+v", step)
	}
	if len(step.Diagnostics) == 0 {
		t.Fatal("expected an actionable diagnostic on install failure")
	}
	if _, ok := step.Summary["skipped_lanes"]; !ok {
		t.Fatalf("install failure summary must record skipped lanes: %v", step.Summary)
	}
}
