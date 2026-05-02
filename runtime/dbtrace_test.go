package runtime

import (
	"context"
	"testing"

	"onlava.com/internal/devdash"
	"onlava.com/runtime/shared"
)

func TestTraceDBQueryRecordsChildSpan(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		queue: make(chan devdash.ReportEnvelope, 8),
	}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()

	state := &requestState{
		request: shared.Request{
			Service:  "tenants",
			Endpoint: "Config",
		},
		traceEnabled: true,
		trace: &traceSpan{
			traceID: "trace-1",
			spanID:  "parent-1",
			isRoot:  true,
		},
	}

	ctx := TraceDBQueryStart(withState(context.Background(), state), " \n SELECT  *  FROM tenants WHERE id = $1 \n", 1)
	TraceDBQueryEnd(ctx, "SELECT 1", 1, nil)

	start := <-reporter.queue
	end := <-reporter.queue
	summary := <-reporter.queue

	if start.Type != "trace-event" || start.TraceEvent == nil {
		t.Fatalf("start report = %#v, want trace event", start)
	}
	if got := start.TraceEvent.TraceID; got != "trace-1" {
		t.Fatalf("start trace id = %q, want %q", got, "trace-1")
	}
	startPayload, _ := start.TraceEvent.Event["span_start"].(map[string]any)
	dbStart, _ := startPayload["db"].(map[string]any)
	if got := dbStart["operation"]; got != "SELECT" {
		t.Fatalf("start operation = %#v, want %q", got, "SELECT")
	}
	if got := dbStart["query"]; got != "SELECT * FROM tenants WHERE id = $1" {
		t.Fatalf("start query = %#v", got)
	}
	if got := dbStart["args_count"]; got != 1 {
		t.Fatalf("start args_count = %#v, want 1", got)
	}

	if end.Type != "trace-event" || end.TraceEvent == nil {
		t.Fatalf("end report = %#v, want trace event", end)
	}
	endPayload, _ := end.TraceEvent.Event["span_end"].(map[string]any)
	dbEnd, _ := endPayload["db"].(map[string]any)
	if got := dbEnd["command_tag"]; got != "SELECT 1" {
		t.Fatalf("end command_tag = %#v, want %q", got, "SELECT 1")
	}
	if got := dbEnd["rows_affected"]; got != int64(1) {
		t.Fatalf("end rows_affected = %#v, want 1", got)
	}

	if summary.Type != "trace-summary" || summary.TraceSummary == nil {
		t.Fatalf("summary report = %#v, want trace summary", summary)
	}
	if got := summary.TraceSummary.Type; got != "DB" {
		t.Fatalf("summary type = %q, want %q", got, "DB")
	}
	if summary.TraceSummary.ParentSpanID == nil || *summary.TraceSummary.ParentSpanID != "parent-1" {
		t.Fatalf("summary parent span id = %#v, want %q", summary.TraceSummary.ParentSpanID, "parent-1")
	}
	if got := summary.TraceSummary.ServiceName; got != "tenants" {
		t.Fatalf("summary service = %q, want %q", got, "tenants")
	}
	if summary.TraceSummary.EndpointName == nil || *summary.TraceSummary.EndpointName != "SELECT" {
		t.Fatalf("summary endpoint = %#v, want %q", summary.TraceSummary.EndpointName, "SELECT")
	}
}

func TestTraceDBQueryWithoutRequestIsNoop(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		queue: make(chan devdash.ReportEnvelope, 4),
	}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()

	ctx := TraceDBQueryStart(context.Background(), "SELECT 1", 0)
	TraceDBQueryEnd(ctx, "SELECT 1", 1, nil)

	select {
	case report := <-reporter.queue:
		t.Fatalf("unexpected report: %#v", report)
	default:
	}
}

func TestTraceDBQueryRedactsInlineLiterals(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		queue: make(chan devdash.ReportEnvelope, 4),
	}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()

	state := &requestState{
		request: shared.Request{
			Service:  "users",
			Endpoint: "Lookup",
		},
		traceEnabled: true,
		trace: &traceSpan{
			traceID: "trace-2",
			spanID:  "parent-2",
			isRoot:  true,
		},
	}

	ctx := TraceDBQueryStart(withState(context.Background(), state), `SELECT * FROM users WHERE email = 'secret@example.com' AND age = 42`, 0)
	TraceDBQueryEnd(ctx, "", -1, nil)

	start := <-reporter.queue
	startPayload, _ := start.TraceEvent.Event["span_start"].(map[string]any)
	dbStart, _ := startPayload["db"].(map[string]any)
	if got := dbStart["query"]; got != "SELECT * FROM users WHERE email = ? AND age = ?" {
		t.Fatalf("redacted query = %#v", got)
	}
}

func TestTraceDBQueryUsesSQLCQueryNameAsOperation(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		queue: make(chan devdash.ReportEnvelope, 4),
	}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()

	state := &requestState{
		request: shared.Request{
			Service:  "jobs",
			Endpoint: "LatestOffers",
		},
		traceEnabled: true,
		trace: &traceSpan{
			traceID: "trace-3",
			spanID:  "parent-3",
			isRoot:  true,
		},
	}

	ctx := TraceDBQueryStart(withState(context.Background(), state), "-- name: ListLatestJobListings :many\nSELECT * FROM job_listings", 0)
	TraceDBQueryEnd(ctx, "SELECT 30", 30, nil)

	start := <-reporter.queue
	<-reporter.queue
	summary := <-reporter.queue

	startPayload, _ := start.TraceEvent.Event["span_start"].(map[string]any)
	dbStart, _ := startPayload["db"].(map[string]any)
	if got := dbStart["operation"]; got != "ListLatestJobListings" {
		t.Fatalf("start operation = %#v, want %q", got, "ListLatestJobListings")
	}
	if summary.TraceSummary.EndpointName == nil || *summary.TraceSummary.EndpointName != "ListLatestJobListings" {
		t.Fatalf("summary endpoint = %#v, want %q", summary.TraceSummary.EndpointName, "ListLatestJobListings")
	}
}

func setTestReporter(reporter *devReporter) func() {
	reporterMu.Lock()
	prev := globalReporter
	globalReporter = reporter
	reporterMu.Unlock()
	return func() {
		reporterMu.Lock()
		globalReporter = prev
		reporterMu.Unlock()
	}
}
