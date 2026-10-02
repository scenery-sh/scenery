package runtime

import (
	"context"
	"errors"
	"testing"

	"scenery.sh/internal/devreport"
	"scenery.sh/runtime/shared"
)

func TestApplicationSpanRecordsNestedChildSpans(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		queue: make(chan []byte, 12),
	}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()

	state := &requestState{
		request: shared.Request{
			Service:  "maps",
			Endpoint: "GetEarth",
		},
		traceEnabled: true,
		trace: &traceSpan{
			traceID: "trace-1",
			spanID:  "request-1",
			isRoot:  true,
		},
	}

	generateCtx, generateSpan := StartSpan(withState(context.Background(), state), "GetEarth.generate")
	_, fetchSpan := StartSpan(generateCtx, "GetEarth.fetch_mesh")
	fetchSpan.End(errors.New("provider unavailable"))
	generateSpan.End(nil)

	generateStart := decodeQueuedReport(t, <-reporter.queue)
	fetchStart := decodeQueuedReport(t, <-reporter.queue)
	fetchEnd := decodeQueuedReport(t, <-reporter.queue)
	fetchSummary := decodeQueuedReport(t, <-reporter.queue)
	generateEnd := decodeQueuedReport(t, <-reporter.queue)
	generateSummary := decodeQueuedReport(t, <-reporter.queue)

	assertApplicationSpanStart(t, generateStart, "trace-1", "GetEarth.generate")
	assertApplicationSpanStart(t, fetchStart, "trace-1", "GetEarth.fetch_mesh")
	if got := fetchEnd.TraceEvent.Event["span_end"].(map[string]any)["status_code"]; got == "STATUS_CODE_OK" {
		t.Fatalf("fetch status = %q, want error status", got)
	}
	if !fetchSummary.TraceSummary.IsError {
		t.Fatal("fetch summary is not marked as an error")
	}
	if fetchSummary.TraceSummary.ParentSpanID == nil || *fetchSummary.TraceSummary.ParentSpanID != generateStart.TraceEvent.SpanID {
		t.Fatalf("fetch parent = %#v, want %q", fetchSummary.TraceSummary.ParentSpanID, generateStart.TraceEvent.SpanID)
	}
	if generateEnd.TraceEvent.Event["span_end"].(map[string]any)["status_code"] != "STATUS_CODE_OK" {
		t.Fatalf("generate end = %#v, want OK", generateEnd.TraceEvent.Event)
	}
	if got := generateSummary.TraceSummary.Type; got != "WORK" {
		t.Fatalf("generate type = %q, want WORK", got)
	}
	if generateSummary.TraceSummary.ParentSpanID == nil || *generateSummary.TraceSummary.ParentSpanID != "request-1" {
		t.Fatalf("generate parent = %#v, want request-1", generateSummary.TraceSummary.ParentSpanID)
	}
}

func TestApplicationSpanEndIsIdempotent(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		queue: make(chan []byte, 8),
	}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()

	state := &requestState{
		request:      shared.Request{Service: "maps", Endpoint: "GetEarth"},
		traceEnabled: true,
		trace:        &traceSpan{traceID: "trace-1", spanID: "request-1", isRoot: true},
	}
	_, span := StartSpan(withState(context.Background(), state), "GetEarth.save")
	span.End(nil)
	span.End(errors.New("late error"))

	for range 3 {
		<-reporter.queue
	}
	select {
	case queuedBody := <-reporter.queue:
		report := decodeQueuedReport(t, queuedBody)
		t.Fatalf("unexpected duplicate report: %#v", report)
	default:
	}
}

func TestApplicationSpanWithoutRequestIsNoop(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		queue: make(chan []byte, 4),
	}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()

	ctx, span := StartSpan(context.Background(), "background")
	if ctx == nil || span == nil {
		t.Fatal("StartSpan returned a nil context or span")
	}
	span.End(nil)

	select {
	case queuedBody := <-reporter.queue:
		report := decodeQueuedReport(t, queuedBody)
		t.Fatalf("unexpected report: %#v", report)
	default:
	}
}

func assertApplicationSpanStart(t *testing.T, report devreport.ReportEnvelope, traceID, operation string) {
	t.Helper()
	if report.Type != "trace-event" || report.TraceEvent == nil {
		t.Fatalf("start report = %#v, want trace event", report)
	}
	if report.TraceEvent.TraceID != traceID {
		t.Fatalf("trace id = %q, want %q", report.TraceEvent.TraceID, traceID)
	}
	start := report.TraceEvent.Event["span_start"].(map[string]any)
	work := start["work"].(map[string]any)
	if got := work["operation"]; got != operation {
		t.Fatalf("operation = %#v, want %q", got, operation)
	}
}
