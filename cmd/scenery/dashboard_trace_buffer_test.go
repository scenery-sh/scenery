package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
)

// Evicted trace-event reports contribute once to the public loss count;
// events handed to their summary are still available for export.
func TestTraceBufferEvictionsReachRuntimeStatus(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"capacity", "age on add", "age on drain"} {
		t.Run(kind, func(t *testing.T) {
			buffer := newDashboardTraceEventBuffer()
			buffer.maxSize = 2
			server := &dashboardServer{
				traces:    buffer,
				telemetry: newTelemetryExporter(nil),
				controller: runtimeRPCTestController{status: devdash.AppStatus{
					AppID: "proof", Observability: &devdash.ObservabilityState{Enabled: true},
				}},
			}
			server.telemetry.dropped.Store(7)
			server.telemetry.failed.Store(3)
			for id := uint64(1); id <= 2; id++ {
				server.bufferTraceEvent(&devdash.TraceEvent{AppID: "proof", SessionID: "session", TraceID: "trace", SpanID: "span", EventID: id})
			}
			key := dashboardTraceEventKey{appID: "proof", sessionID: "session", traceID: "trace", spanID: "span"}
			buffer.events[key][0].addedAt = time.Now().Add(-time.Second)
			if kind != "capacity" {
				buffer.maxSize = 10
				items := buffer.events[key]
				items[0].addedAt = time.Now().Add(-2 * dashboardTraceEventBufferTTL)
			}
			if kind != "age on drain" {
				server.bufferTraceEvent(&devdash.TraceEvent{AppID: "proof", SessionID: "session", TraceID: "trace", SpanID: "other", EventID: 3})
			}
			summary := &devdash.TraceSummary{AppID: "proof", SessionID: "session", TraceID: "trace", SpanID: "span"}
			events := server.drainBufferedTraceEvents(summary)
			if len(events) != 1 || events[0].EventID != 2 {
				t.Fatalf("surviving events = %+v, want event 2", events)
			}
			if again := server.drainBufferedTraceEvents(summary); len(again) != 0 {
				t.Fatalf("second drain = %+v", again)
			}
			if kind != "age on drain" {
				summary.SpanID = "other"
				if other := server.drainBufferedTraceEvents(summary); len(other) != 1 || other[0].EventID != 3 {
					t.Fatalf("unrelated span events = %+v", other)
				}
			}
			if buffer.total != 0 {
				t.Fatalf("buffered after drain = %d", buffer.total)
			}
			result, err := server.dispatchRPC(context.Background(), "status", nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := result.(runtimeStatus).Observability.Export; got != (runtimeTelemetryExport{Dropped: 7, Failed: 3, TraceBufferDroppedEvents: 1}) {
				t.Fatalf("runtime loss evidence = %+v, want one additional discarded event", got)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var payload any
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.dev-runtime.status.schema.json"), payload); len(diagnostics) != 0 {
				t.Fatalf("status schema diagnostics = %+v", diagnostics)
			}
		})
	}
}

func TestTraceBufferExpiryCountsOnlyRemovedEvents(t *testing.T) {
	t.Parallel()
	buffer := newDashboardTraceEventBuffer()
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-buffer.maxAge)
	key := dashboardTraceEventKey{appID: "proof", traceID: "trace", spanID: "span"}
	buffer.events[key] = []bufferedDashboardTraceEvent{
		{event: devdash.TraceEvent{EventID: 1}, addedAt: cutoff.Add(-time.Nanosecond)},
		{event: devdash.TraceEvent{EventID: 2}, addedAt: cutoff},
		{event: devdash.TraceEvent{EventID: 3}, addedAt: cutoff.Add(time.Nanosecond)},
	}
	buffer.total = 3
	buffer.pruneLocked(now)
	buffer.pruneLocked(now)
	if buffer.total != 2 || buffer.droppedCount() != 1 || buffer.events[key][0].event.EventID != 2 {
		t.Fatalf("TTL boundary total=%d dropped=%d events=%+v", buffer.total, buffer.droppedCount(), buffer.events[key])
	}
	buffer.pruneLocked(now.Add(time.Nanosecond))
	if buffer.total != 1 || buffer.droppedCount() != 2 || buffer.events[key][0].event.EventID != 3 {
		t.Fatalf("advanced cutoff total=%d dropped=%d events=%+v", buffer.total, buffer.droppedCount(), buffer.events[key])
	}
}

func TestTraceBufferLossCountsOccurrencesWithoutRecounting(t *testing.T) {
	t.Parallel()
	buffer := newDashboardTraceEventBuffer()
	buffer.maxSize = 2
	key := dashboardTraceEventKey{appID: "one", sessionID: "session", traceID: "trace", spanID: "span"}
	old := time.Now().Add(-2 * buffer.maxAge)
	buffer.events[key] = []bufferedDashboardTraceEvent{
		{event: devdash.TraceEvent{EventID: 1}, addedAt: old},
		{event: devdash.TraceEvent{EventID: 1}, addedAt: old},
	}
	live := key
	live.appID = "two"
	buffer.events[live] = []bufferedDashboardTraceEvent{{event: devdash.TraceEvent{EventID: 2}, addedAt: time.Now().Add(-time.Second)}}
	buffer.total = 3
	if buffer.droppedCount() != 0 || buffer.total != 3 {
		t.Fatal("observing loss evidence pruned the buffer")
	}
	buffer.add(&devdash.TraceEvent{AppID: "three", TraceID: "trace", SpanID: "span", EventID: 3})
	if buffer.total != 2 || buffer.droppedCount() != 2 {
		t.Fatalf("TTL plus capacity recounted removals: total=%d drops=%d", buffer.total, buffer.droppedCount())
	}
	buffer.add(&devdash.TraceEvent{AppID: "four", TraceID: "trace", SpanID: "span", EventID: 4})
	if buffer.total != 2 || buffer.droppedCount() != 3 || len(buffer.drain(live)) != 0 {
		t.Fatalf("capacity loss after TTL: total=%d drops=%d", buffer.total, buffer.droppedCount())
	}
}

func TestTraceBufferDrainRequiresCompleteIdentity(t *testing.T) {
	t.Parallel()
	buffer := newDashboardTraceEventBuffer()
	keys := []dashboardTraceEventKey{
		{appID: "app", sessionID: "session", traceID: "trace", spanID: "span"},
		{appID: "other", sessionID: "session", traceID: "trace", spanID: "span"},
		{appID: "app", sessionID: "other", traceID: "trace", spanID: "span"},
		{appID: "app", sessionID: "session", traceID: "other", spanID: "span"},
		{appID: "app", sessionID: "session", traceID: "trace", spanID: "other"},
	}
	for index, key := range keys {
		buffer.add(&devdash.TraceEvent{AppID: key.appID, SessionID: key.sessionID, TraceID: key.traceID, SpanID: key.spanID, EventID: uint64(index + 1)})
	}
	for index, key := range keys {
		events := buffer.drain(key)
		if len(events) != 1 || events[0].EventID != uint64(index+1) || len(buffer.drain(key)) != 0 {
			t.Fatalf("drain crossed identity %+v: %+v", key, events)
		}
	}
	if buffer.total != 0 || buffer.droppedCount() != 0 {
		t.Fatalf("drain counted loss: total=%d drops=%d", buffer.total, buffer.droppedCount())
	}
}
