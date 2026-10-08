package telemetryreport

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func supervisorTimingFixture(t *testing.T, events ...any) Report {
	t.Helper()
	home := t.TempDir()
	writeLines(t, filepath.Join(home, "agent", "dev", "timing.log"), events...)
	report, err := Build(Options{AgentHome: home, Until: time.Date(2026, 10, 8, 20, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := json.Marshal(report); err != nil {
		t.Fatalf("report cannot be serialized: %v", err)
	}
	return report
}

func supervisorTimingEvent(kind, operation, name string, duration float64, ok bool) any {
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	return supervisorEventLine(kind, at, map[string]any{
		"operation_id": operation, "name": name, "phase_id": name,
		"duration_ms": duration, "ok": ok, "reason": "source_rebuild", "started_at": at,
		"cache": "hit", "files_hashed": 3,
	})
}

func TestSupervisorInvalidDurationsPreserveOutcomes(t *testing.T) {
	t.Parallel()
	for _, duration := range []float64{-7, -.25, 0x1p63, 1e20, 1e308} {
		t.Run(fmt.Sprint(duration), func(t *testing.T) {
			report := supervisorTimingFixture(t,
				supervisorTimingEvent("build.step", "bad", "build.request", duration, true),
				supervisorTimingEvent("phase.finish", "", "startup", duration, true),
				supervisorTimingEvent("build.step", "zero", "build.request", 0, true),
			)
			b := report.Builds
			if b.Rebuilds.Count != 2 || b.Rebuilds.FailureCount != 0 || b.Rebuilds.PercentileSampleCount != 1 || ms(b.Rebuilds.P50MS) != 0 || ms(b.Rebuilds.P95MS) != 0 || b.Worktrees[0].Rebuilds.Count != 2 || b.Worktrees[0].Rebuilds.PercentileSampleCount != 1 || ms(b.Worktrees[0].Rebuilds.P50MS) != 0 {
				t.Fatalf("request outcomes/timing = %+v", b)
			}
			if len(b.StartupPhases) != 1 || b.StartupPhases[0].Count != 1 || b.StartupPhases[0].PercentileSampleCount != 0 || b.StartupPhases[0].P50MS != nil || b.StartupPhases[0].P95MS != nil {
				t.Fatalf("phase outcome/timing = %+v", b.StartupPhases)
			}
			if report.Sources.SupervisorInvalid != 2 || report.Sources.Supervisor[0].Invalid != 2 || report.Sources.Supervisor[0].Status != "partial" || len(b.CacheWork) != 1 || b.CacheWork[0].Samples != 2 || b.CacheWork[0].TimingSampleCount != 1 || b.CacheWork[0].AccumulatedMS == nil || *b.CacheWork[0].AccumulatedMS != 0 || b.CacheWork[0].FilesHashed != 6 {
				t.Fatalf("coverage/cache = %+v / %+v", report.Sources, b.CacheWork)
			}
		})
	}
	for _, duration := range []float64{0, .75, 12.75, math.Nextafter(0x1p63, 0)} {
		report := supervisorTimingFixture(t, supervisorTimingEvent("build.step", "valid", "build.request", duration, true))
		if report.Sources.SupervisorInvalid != 0 || report.Builds.Rebuilds.PercentileSampleCount != 1 || ms(report.Builds.Rebuilds.P50MS) != int64(duration) || (report.Builds.CacheWork[0].AccumulatedMS == nil || *report.Builds.CacheWork[0].AccumulatedMS != duration) {
			t.Fatalf("valid duration %g lost: %+v", duration, report)
		}
	}
}

func TestSupervisorStepTimingsKeepFractionalSums(t *testing.T) {
	t.Parallel()
	report := supervisorTimingFixture(t,
		supervisorTimingEvent("build.step", "fraction", "compile", .75, true),
		supervisorTimingEvent("build.step", "fraction", "compile", .75, true),
		supervisorTimingEvent("build.step", "fraction", "build.request", 12.75, true),
		supervisorTimingEvent("build.step", "negative", "bad-child", -.25, true),
		supervisorTimingEvent("build.step", "negative", "bad-child", 4, true),
		supervisorTimingEvent("build.step", "negative", "build.request", 20, true),
		supervisorTimingEvent("build.step", "overflow", "overflow-child", 5e18, true),
		supervisorTimingEvent("build.step", "overflow", "overflow-child", 5e18, true),
		supervisorTimingEvent("build.step", "overflow", "build.request", 20, true),
	)
	for _, step := range report.Builds.Steps {
		if step.Count != 1 || (step.Step == "compile" && (step.PercentileSampleCount != 1 || ms(step.P50MS) != 1)) || (step.Step != "compile" && (step.PercentileSampleCount != 0 || step.P50MS != nil || step.P95MS != nil)) {
			t.Fatalf("step timing = %+v", step)
		}
	}
	if len(report.Builds.Steps) != 3 || report.Sources.SupervisorInvalid != 2 || report.Sources.Supervisor[0].Invalid != 2 || report.Builds.Rebuilds.PercentileSampleCount != 3 {
		t.Fatalf("step coverage/request independence = %+v", report)
	}
	for _, cache := range report.Builds.CacheWork {
		if cache.Layer == "compile" && ((cache.AccumulatedMS == nil || *cache.AccumulatedMS != 1.5) || cache.Samples != 2 || cache.FilesHashed != 6) {
			t.Fatalf("fractional cache work = %+v", cache)
		}
	}
}

func TestSupervisorUntimedFailuresKeepJoinsAndTerminalCleanup(t *testing.T) {
	t.Parallel()
	var events []any
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	for index := range 5 {
		op := fmt.Sprintf("failed-%d", index)
		events = append(events, supervisorTimingEvent("build.step", op, "build.request", -1, false))
		failure := map[string]any{"error": "retained failure"}
		if index%2 == 0 {
			failure["operation_id"] = op
		}
		events = append(events, supervisorEventLine("build.error", at, failure))
	}
	// A malformed terminal timing must still clear that operation's old steps.
	events = append(events,
		supervisorTimingEvent("build.step", "reused", "compile", -1, true),
		supervisorTimingEvent("build.step", "reused", "build.request", -1, true),
		supervisorTimingEvent("build.step", "reused", "compile", 1.5, true),
		supervisorTimingEvent("build.step", "reused", "build.request", 10, true),
	)
	report := supervisorTimingFixture(t, events...)
	b := report.Builds
	if b.Rebuilds.Count != 7 || b.Rebuilds.FailureCount != 5 || b.Rebuilds.PercentileSampleCount != 1 || len(b.Streaks) != 1 || b.Streaks[0].Count != 5 || b.UnmatchedErrors != 0 || len(b.Failures) != 1 || b.Failures[0].Count != 5 || b.Failures[0].Name != "retained failure" {
		t.Fatalf("failed outcome joins/streaks = %+v", b)
	}
	if len(b.Steps) != 1 || b.Steps[0].Count != 2 || b.Steps[0].PercentileSampleCount != 1 || ms(b.Steps[0].P50MS) != 1 {
		t.Fatalf("terminal cleanup = %+v", b.Steps)
	}
}

func TestRebuildLatencyFindingUsesTimedSuccesses(t *testing.T) {
	t.Parallel()
	for _, samples := range []int{0, 1} {
		value := int64(12)
		timing := Timing{Count: 7, FailureCount: 5, PercentileSampleCount: samples}
		if samples > 0 {
			timing.P50MS, timing.P95MS = &value, &value
		}
		found := false
		for _, finding := range findings(Report{Builds: Builds{Rebuilds: timing}}) {
			if finding.Code == "builds.rebuild_latency" {
				found = true
				if !strings.Contains(finding.Message, "over 1 timed successful rebuild") {
					t.Fatalf("latency cohort = %s", finding.Message)
				}
			}
		}
		if found != (samples > 0) {
			t.Fatalf("latency finding for %d samples: %t", samples, found)
		}
	}
}

func TestSupervisorRawTimingTokensPreserveMetadata(t *testing.T) {
	t.Parallel()
	for _, token := range []string{"", "null", `"12"`, "true", "{}", "[]", "-1e-1000000", "1e1000000", "9223372036854775808"} {
		t.Run(token, func(t *testing.T) {
			rawEvent := func(kind, op, name string, ok bool) any {
				event := supervisorTimingEvent(kind, op, name, 0, ok).(map[string]any)
				data := event["data"].(map[string]any)["data"].(map[string]any)
				delete(data, "duration_ms")
				if token != "" {
					data["duration_ms"] = json.RawMessage(token)
				}
				return event
			}
			at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
			report := supervisorTimingFixture(t,
				supervisorTimingEvent("build.step", "bad", "compile", 1.5, true),
				rawEvent("build.step", "bad", "build.request", true),
				rawEvent("build.step", "failed", "build.request", false),
				supervisorEventLine("build.error", at, map[string]any{"operation_id": "failed", "error": "raw timing failure"}),
				rawEvent("phase.finish", "", "startup", true),
				supervisorTimingEvent("build.step", "zero", "build.request", 0, true),
			)
			b := report.Builds
			if b.Rebuilds.Count != 3 || b.Rebuilds.FailureCount != 1 || b.Rebuilds.PercentileSampleCount != 1 || ms(b.Rebuilds.P50MS) != 0 || len(b.Failures) != 1 || b.Failures[0].Name != "raw timing failure" || b.UnmatchedErrors != 0 {
				t.Fatalf("raw %q changed metadata: %+v", token, b)
			}
			if report.Sources.SupervisorInvalid != 3 || report.Sources.Supervisor[0].Invalid != 3 || len(b.Steps) != 1 || b.Steps[0].PercentileSampleCount != 1 || ms(b.Steps[0].P50MS) != 1 || len(b.StartupPhases) != 1 || b.StartupPhases[0].PercentileSampleCount != 0 {
				t.Fatalf("raw %q timing/coverage = %+v", token, report)
			}
		})
	}
	for _, token := range []string{"-0", "-0.0e1000000", "1e-1000000"} {
		event := supervisorTimingEvent("build.step", "zero", "build.request", 0, true).(map[string]any)
		event["data"].(map[string]any)["data"].(map[string]any)["duration_ms"] = json.RawMessage(token)
		report := supervisorTimingFixture(t, event)
		if report.Sources.SupervisorInvalid != 0 || report.Builds.Rebuilds.PercentileSampleCount != 1 || ms(report.Builds.Rebuilds.P50MS) != 0 {
			t.Fatalf("valid quantized zero token %q lost: %+v", token, report)
		}
	}
}

func TestSupervisorCacheRetainsUntimedWork(t *testing.T) {
	t.Parallel()
	bad := supervisorTimingEvent("build.step", "bad", "build.request", -.25, true)
	report := supervisorTimingFixture(t, bad)
	cache := report.Builds.CacheWork[0]
	if cache.Samples != 1 || cache.TimingSampleCount != 0 || cache.AccumulatedMS != nil || cache.FilesHashed != 3 {
		t.Fatalf("untimed work must remain visible without a false zero: %+v", cache)
	}
	report = supervisorTimingFixture(t, bad, supervisorTimingEvent("build.step", "zero", "build.request", 0, true))
	cache = report.Builds.CacheWork[0]
	if cache.Samples != 2 || cache.TimingSampleCount != 1 || cache.AccumulatedMS == nil || *cache.AccumulatedMS != 0 || cache.FilesHashed != 6 {
		t.Fatalf("valid zero and partial cache cohort = %+v", cache)
	}
}
