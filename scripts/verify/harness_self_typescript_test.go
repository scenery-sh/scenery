package main

import (
	"context"
	"os/exec"
	"testing"
)

func TestHarnessTypeScriptDepsStepMissingBun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	step, ready := runHarnessTypeScriptDepsStep(context.Background(), t.TempDir(), harnessArtifactContext{})
	if ready {
		t.Fatal("typescript deps step must not report ready without bun in PATH")
	}
	if step.OK {
		t.Fatal("typescript deps step must fail without bun in PATH")
	}
	if step.Name != "typescript dependencies" {
		t.Fatalf("step name = %q", step.Name)
	}
	if len(step.Diagnostics) != 1 || step.Diagnostics[0].Severity != "error" {
		t.Fatalf("expected one error diagnostic, got %+v", step.Diagnostics)
	}
	skipped, ok := step.Summary["skipped_lanes"].([]string)
	if !ok || len(skipped) != len(harnessTypeScriptLaneNames) {
		t.Fatalf("skipped_lanes = %v", step.Summary["skipped_lanes"])
	}
}

func TestHarnessTypeScriptDepsStepInstallFailure(t *testing.T) {
	t.Parallel()

	// An empty directory has no package.json, so a real bun install fails and
	// the step must gate the dependent lanes with an actionable diagnostic.
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun unavailable")
	}
	step, ready := runHarnessTypeScriptDepsStep(context.Background(), t.TempDir(), harnessArtifactContext{})
	if ready || step.OK {
		t.Fatalf("typescript deps step must fail in a directory without package.json: %+v", step)
	}
	if len(step.Diagnostics) == 0 {
		t.Fatal("expected an actionable diagnostic on install failure")
	}
	if _, ok := step.Summary["skipped_lanes"]; !ok {
		t.Fatalf("install failure summary must record skipped lanes: %v", step.Summary)
	}
}
