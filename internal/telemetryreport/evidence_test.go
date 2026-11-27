package telemetryreport

import (
	"fmt"
	"testing"
	"time"
)

func TestMalformedBuildErrorKeepsSourceAndOutcomeEvidence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	for name, payload := range map[string]map[string]any{
		"numeric error":  map[string]any{"operation_id": "failed", "error": 17},
		"null error":     map[string]any{"operation_id": "failed", "error": nil},
		"null with code": map[string]any{"operation_id": "failed", "error": nil, "diagnostic": map[string]any{"code": "SCN8001"}},
		"missing error":  map[string]any{"operation_id": "failed"},
		"null data":      nil,
		"empty data":     map[string]any{},
	} {
		t.Run(name, func(t *testing.T) {
			for _, joined := range []bool{false, true} {
				t.Run(fmt.Sprint(joined), func(t *testing.T) {
					events := []any{
						supervisorTimingEvent("build.step", "failed", "build.request", 0, false),
						supervisorEventLine("build.error", at, payload),
					}
					if joined {
						events = append(events, supervisorEventLine("build.error", at, map[string]any{"operation_id": "failed", "error": "known cause"}))
					}
					events = append(events, supervisorTimingEvent("build.step", "success", "build.request", 0, true))
					r := supervisorTimingFixture(t, events...)
					s := r.Sources
					if len(s.Supervisor) != 1 || s.SupervisorInvalid != 1 || s.SupervisorLogsPartial != 0 || s.SupervisorLogsFailed != 0 {
						t.Fatalf("global source evidence = %+v", s)
					}
					file := s.Supervisor[0]
					if file.Invalid != 1 || file.Status != "partial" || file.SnapshotBytes == nil || file.ReadBytes != *file.SnapshotBytes || file.Records != len(events) {
						t.Fatalf("physical and invalid evidence = %+v", file)
					}
					b := r.Builds
					if b.Rebuilds.Count != 2 || b.Rebuilds.FailureCount != 1 || b.Rebuilds.PercentileSampleCount != 1 || b.UnmatchedErrors != 0 || len(b.RebuildFailures) != 1 || b.RebuildFailures[0].Count != 1 {
						t.Fatalf("known outcomes = %+v", b)
					}
					if joined && b.RebuildFailures[0].Name != "known cause" {
						t.Fatalf("valid subsequent error lost its operation join: %+v", b.RebuildFailures)
					}
				})
			}
		})
	}
	for name, payload := range map[string]map[string]any{
		"empty string":      {"error": ""},
		"legacy message":    {"error": "known cause"},
		"legacy diagnostic": {"diagnostic": map[string]any{"code": "SCN8001"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := supervisorTimingFixture(t,
				supervisorTimingEvent("build.step", "failed", "build.request", 0, false),
				supervisorEventLine("build.error", at, payload))
			if r.Sources.SupervisorInvalid != 0 || len(r.Sources.Supervisor) != 1 || r.Sources.Supervisor[0].Status != "complete" || r.Builds.Rebuilds.Count != 1 || r.Builds.Rebuilds.FailureCount != 1 || len(r.Builds.RebuildFailures) != 1 || r.Builds.RebuildFailures[0].Count != 1 || r.Builds.UnmatchedErrors != 0 {
				t.Fatalf("valid legacy/string evidence = %+v", r)
			}
			want := map[string]string{"empty string": "unknown", "legacy message": "known cause", "legacy diagnostic": "SCN8001 "}[name]
			if r.Builds.RebuildFailures[0].Name != want {
				t.Fatalf("valid cause = %+v want %q", r.Builds.RebuildFailures, want)
			}
		})
	}

}
