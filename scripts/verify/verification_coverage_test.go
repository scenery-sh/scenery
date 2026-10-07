package main

import (
	"testing"
	"time"

	"scenery.sh/internal/harnessreport"
)

func TestVerificationLatestOutcomeCannotBeHiddenByOlderSuccess(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC)
	current := harnessSelfResponse{Run: &harnessreport.ValidationRun{ID: "current", InputsStable: true, ArchivePath: ".scenery/harness/runs/current"}, GeneratedAt: now.Format(time.RFC3339Nano)}
	prior := current
	prior.Run = &harnessreport.ValidationRun{ID: "prior", InputsStable: true, ArchivePath: ".scenery/harness/runs/prior"}
	prior.Steps = []harnessStep{{Name: "table", OK: true}}
	for _, skipped := range []any{[]string{"table"}, []any{"table"}} {
		current.Steps = []harnessStep{{Name: "prerequisite", Error: "Bun unavailable", Summary: map[string]any{"skipped_lanes": skipped}}}
		lane := selectVerificationLane(current, []harnessSelfResponse{current, prior}, "table", "table", now)
		if lane.Outcome != "blocked" || !lane.Selected || lane.Reason != "Bun unavailable" {
			t.Fatalf("blocked lane was hidden: %+v", lane)
		}
	}
	current.Steps = nil
	failed := prior
	failed.Steps = []harnessStep{{Name: "table", Error: "case failed"}}
	lane := selectVerificationLane(current, []harnessSelfResponse{current, failed, prior}, "table", "table", now)
	if lane.Outcome != "failed" || lane.RunID != "prior" || lane.Selected {
		t.Fatalf("latest historical failure was hidden: %+v", lane)
	}
	current.Steps = []harnessStep{{Name: "go tests", OK: true}}
	current.TestTiming = &harnessTestTimingReport{}
	lane = selectVerificationLane(current, []harnessSelfResponse{current}, "go", "go tests", now)
	if lane.Outcome != "incomplete" {
		t.Fatalf("incomplete Go discovery was accepted: %+v", lane)
	}
}
