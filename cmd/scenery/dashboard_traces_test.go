package main

import (
	"testing"
	"time"

	"scenery.sh/internal/devdash"
)

func TestDashboardTraceEventsForServiceInitSynthesizesSpanBoundaries(t *testing.T) {
	t.Parallel()

	traceID := "6bd7469c20430af3eb2cc9896851d723"
	spanID := "4babf8489f07f89b"
	startedAt := time.Date(2026, time.April, 14, 14, 33, 56, 725492916, time.UTC)

	summary := &devdash.TraceSummary{
		AppID:         "app-test",
		TraceID:       traceID,
		SpanID:        spanID,
		Type:          "REQUEST",
		IsRoot:        true,
		StartedAt:     startedAt,
		DurationNanos: 84,
		ServiceName:   "console",
		EndpointName:  ptrString("init"),
	}

	events := []*devdash.TraceEvent{
		{
			AppID:     "app-test",
			TraceID:   traceID,
			SpanID:    spanID,
			EventID:   7,
			EventTime: startedAt,
			Event: map[string]any{
				"span_event": map[string]any{
					"service_init_start": map[string]any{
						"service": "console",
					},
				},
			},
		},
		{
			AppID:     "app-test",
			TraceID:   traceID,
			SpanID:    spanID,
			EventID:   8,
			EventTime: startedAt.Add(84 * time.Nanosecond),
			Event: map[string]any{
				"span_event": map[string]any{
					"service_init_end": map[string]any{
						"error": nil,
					},
				},
			},
		},
	}

	compat := buildCompatTraceEvents(summary, events)
	sortCompatTraceEvents(compat)
	payloads := compatTracePayloads(compat)
	if len(payloads) != 4 {
		t.Fatalf("expected 4 events, got %d", len(payloads))
	}

	first := payloads[0]
	if _, ok := first["event"]; ok {
		t.Fatal("expected compatibility event payload without nested event envelope")
	}
	if _, ok := first["span_start"]; !ok {
		t.Fatalf("expected synthesized span_start, got %#v", first)
	}
	if first["span_id"] != compatUintString(spanID) {
		t.Fatalf("unexpected span_id: %v", first["span_id"])
	}
	spanStart := first["span_start"].(map[string]any)
	request := spanStart["request"].(map[string]any)
	if request["http_method"] != "INIT" {
		t.Fatalf("unexpected synthesized method: %v", request["http_method"])
	}
	if request["path"] != "/console.init" {
		t.Fatalf("unexpected synthesized path: %v", request["path"])
	}

	second := payloads[1]
	spanEvent := second["span_event"].(map[string]any)
	if _, ok := spanEvent["service_init_start"]; !ok {
		t.Fatalf("expected preserved service_init_start event, got %#v", second)
	}

	last := payloads[len(payloads)-1]
	if _, ok := last["span_end"]; !ok {
		t.Fatalf("expected synthesized span_end, got %#v", last)
	}
}

func TestDashboardTraceEventsForSpanFlattensRequestEvents(t *testing.T) {
	t.Parallel()

	traceID := "00000000000000010000000000000002"
	spanID := "0000000000000003"
	parentSpanID := "0000000000000001"
	startedAt := time.Date(2026, time.April, 14, 15, 0, 0, 0, time.UTC)

	summary := &devdash.TraceSummary{
		AppID:         "app-test",
		TraceID:       traceID,
		SpanID:        spanID,
		Type:          "REQUEST",
		IsRoot:        false,
		StartedAt:     startedAt,
		DurationNanos: uint64(5 * time.Millisecond),
		ServiceName:   "tenants",
		EndpointName:  ptrString("Config"),
		ParentSpanID:  &parentSpanID,
	}

	events := []*devdash.TraceEvent{
		{
			AppID:     "app-test",
			TraceID:   traceID,
			SpanID:    spanID,
			EventID:   11,
			EventTime: startedAt,
			Event: map[string]any{
				"span_start": map[string]any{
					"request": map[string]any{
						"service_name":  "tenants",
						"endpoint_name": "Config",
						"http_method":   "GET",
						"path":          "/tenants/config",
					},
				},
			},
		},
		{
			AppID:     "app-test",
			TraceID:   traceID,
			SpanID:    spanID,
			EventID:   12,
			EventTime: startedAt.Add(5 * time.Millisecond),
			Event: map[string]any{
				"span_end": map[string]any{
					"duration_nanos": uint64(5 * time.Millisecond),
					"request": map[string]any{
						"service_name":     "tenants",
						"endpoint_name":    "Config",
						"http_status_code": 200,
					},
				},
			},
		},
	}

	compat := buildCompatTraceEvents(summary, events)
	sortCompatTraceEvents(compat)
	payloads := compatTracePayloads(compat)
	if len(payloads) != 2 {
		t.Fatalf("expected 2 events, got %d", len(payloads))
	}

	first := payloads[0]
	if _, ok := first["event"]; ok {
		t.Fatal("expected flattened compatibility payload")
	}
	traceValue, ok := first["trace_id"].(map[string]string)
	if !ok {
		t.Fatalf("expected trace_id object, got %#v", first["trace_id"])
	}
	if traceValue["high"] != "1" || traceValue["low"] != "2" {
		t.Fatalf("unexpected trace_id conversion: %#v", traceValue)
	}
	if first["span_id"] != "3" {
		t.Fatalf("unexpected span_id conversion: %v", first["span_id"])
	}
	spanStart := first["span_start"].(map[string]any)
	if spanStart["parent_span_id"] != "1" {
		t.Fatalf("expected injected parent_span_id, got %v", spanStart["parent_span_id"])
	}
}

func ptrString(value string) *string {
	return &value
}
