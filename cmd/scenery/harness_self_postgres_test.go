package main

import (
	"context"
	"slices"
	"testing"
)

func TestHarnessPostgresProbeStepModePassThrough(t *testing.T) {
	original := runHarnessPostgresProbeCheckFunc
	defer func() { runHarnessPostgresProbeCheckFunc = original }()

	var gotFull []bool
	runHarnessPostgresProbeCheckFunc = func(ctx context.Context, repoRoot string, full bool) (map[string]any, []checkDiagnostic, error) {
		gotFull = append(gotFull, full)
		proof := "smoke"
		if full {
			proof = "full"
		}
		return map[string]any{"postgres_probe": "ran", "proof": proof}, nil, nil
	}

	smoke := runHarnessPostgresProbeStep(context.Background(), t.TempDir(), false)
	if !smoke.OK {
		t.Fatalf("smoke step not OK: %+v", smoke)
	}
	if slices.Contains(smoke.Command, "--full") {
		t.Fatalf("smoke step command must not advertise --full: %v", smoke.Command)
	}
	if smoke.Summary["proof"] != "smoke" {
		t.Fatalf("smoke step summary proof = %v", smoke.Summary["proof"])
	}

	full := runHarnessPostgresProbeStep(context.Background(), t.TempDir(), true)
	if !full.OK {
		t.Fatalf("full step not OK: %+v", full)
	}
	if !slices.Contains(full.Command, "--full") {
		t.Fatalf("full step command must advertise --full: %v", full.Command)
	}
	if full.Summary["proof"] != "full" {
		t.Fatalf("full step summary proof = %v", full.Summary["proof"])
	}

	if want := []bool{false, true}; !slices.Equal(gotFull, want) {
		t.Fatalf("probe check full flags = %v, want %v", gotFull, want)
	}
}
