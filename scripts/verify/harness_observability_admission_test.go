package main

import (
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
)

func TestBackendAdmissionCounterRequiresKnownValue(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name, body string
		want       uint64
		reject     bool
	}{
		{"known zero", harnessAdmissionCounter + " 0\n", 0, false},
		{"known value", "# TYPE vt_rows_dropped_total counter\n" + harnessAdmissionCounter + " 7\n", 7, false},
		{"absent", "vt_rows_dropped_total{reason=\"debug\"} 0\n", 0, true},
		{"duplicate", strings.Repeat(harnessAdmissionCounter+" 1\n", 2), 0, true},
		{"negative", harnessAdmissionCounter + " -1\n", 0, true},
		{"nan", harnessAdmissionCounter + " NaN\n", 0, true},
		{"fraction", harnessAdmissionCounter + " 1.5\n", 0, true},
		{"overflow", harnessAdmissionCounter + " 18446744073709551616\n", 0, true},
		{"missing value", harnessAdmissionCounter, 0, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			got, err := parseHarnessAdmissionCounter(fixture.body)
			if (err != nil) != fixture.reject || got != fixture.want {
				t.Fatalf("counter=%d err=%v want=%d reject=%t", got, err, fixture.want, fixture.reject)
			}
		})
	}
}

func TestBackendAdmissionDeltaRejectsResetAndChangedOwner(t *testing.T) {
	t.Parallel()
	before := harnessAdmissionSnapshot{Owner: localagent.Owner{PID: 7, StartedAt: "start", Exe: "/owned/traces", CmdlineHash: "fingerprint"}, Counter: 2}
	for _, fixture := range []struct {
		name   string
		change func(*harnessAdmissionSnapshot)
		reject bool
	}{
		{"one rejection", func(s *harnessAdmissionSnapshot) { s.Counter++ }, false},
		{"reset", func(s *harnessAdmissionSnapshot) { s.Counter = 1 }, true},
		{"changed pid", func(s *harnessAdmissionSnapshot) { s.Owner.PID++ }, true},
		{"reused pid", func(s *harnessAdmissionSnapshot) { s.Owner.StartedAt = "new" }, true},
		{"same executable basename", func(s *harnessAdmissionSnapshot) { s.Owner.Exe = "/other/traces" }, true},
		{"command changed", func(s *harnessAdmissionSnapshot) { s.Owner.CmdlineHash = "new" }, true},
		{"observed start absent", func(s *harnessAdmissionSnapshot) { s.Owner.StartedAt = "" }, true},
		{"observed executable absent", func(s *harnessAdmissionSnapshot) { s.Owner.Exe = "" }, true},
		{"observed command absent", func(s *harnessAdmissionSnapshot) { s.Owner.CmdlineHash = "" }, true},
		{"unknown owner", func(s *harnessAdmissionSnapshot) { s.Owner = localagent.Owner{} }, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			after := before
			fixture.change(&after)
			got, err := harnessAdmissionDelta(before, after)
			if (err != nil) != fixture.reject || (!fixture.reject && got != 1) {
				t.Fatalf("delta=%d err=%v reject=%t", got, err, fixture.reject)
			}
		})
	}
	unknown := before
	unknown.Owner.StartedAt = ""
	if _, err := harnessAdmissionDelta(unknown, before); err == nil {
		t.Fatal("missing initial identity accepted")
	}
}

func TestBackendAdmissionEventsRequireExactInventory(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name   string
		change func([]devdash.TraceDetailEvent) []devdash.TraceDetailEvent
		reject bool
	}{
		{"valid", func(s []devdash.TraceDetailEvent) []devdash.TraceDetailEvent { return s }, false},
		{"missing", func(s []devdash.TraceDetailEvent) []devdash.TraceDetailEvent { return s[:1] }, true},
		{"duplicate", func(s []devdash.TraceDetailEvent) []devdash.TraceDetailEvent { s[1] = s[0]; return s }, true},
		{"out of range", func(s []devdash.TraceDetailEvent) []devdash.TraceDetailEvent {
			s[0].Data["admission"].(map[string]any)["ordinal"] = float64(3)
			return s
		}, true},
		{"fractional identity", func(s []devdash.TraceDetailEvent) []devdash.TraceDetailEvent {
			s[0].Data["admission"].(map[string]any)["ordinal"] = 1.5
			return s
		}, true},
		{"name", func(s []devdash.TraceDetailEvent) []devdash.TraceDetailEvent { s[0].Name = "other"; return s }, true},
		{"timestamp", func(s []devdash.TraceDetailEvent) []devdash.TraceDetailEvent {
			s[0].Time = s[0].Time.Add(time.Microsecond)
			return s
		}, true},
		{"data", func(s []devdash.TraceDetailEvent) []devdash.TraceDetailEvent { s[0].Data = nil; return s }, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var events []devdash.TraceDetailEvent
			for i := range 2 {
				at := time.Unix(1812450000, int64(i)*1000).UTC()
				events = append(events, devdash.TraceDetailEvent{Time: at, Name: "scenery.admission", Data: map[string]any{"admission": map[string]any{"kind": "control", "ordinal": float64(i + 1), "time": at.Format(time.RFC3339Nano)}}})
			}
			if err := verifyHarnessAdmissionEvents(fixture.change(events), 2); (err != nil) != fixture.reject {
				t.Fatalf("inventory err=%v reject=%t", err, fixture.reject)
			}
		})
	}
}
