package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"scenery.sh/internal/appsdk"
	"scenery.sh/internal/devreport"
	"scenery.sh/runtime/shared"
)

type sqlValueError struct{}

func (sqlValueError) Error() string    { return `invalid input syntax for integer: "private-argument"` }
func (sqlValueError) SQLState() string { return "22P02" }

func TestDBTraceErrorsDoNotEchoQueryArguments(t *testing.T) {
	for _, err := range []error{sqlValueError{}, errors.New("cannot encode private-argument"), context.Canceled, context.DeadlineExceeded} {
		data, marshalErr := json.Marshal(dbTraceError(err))
		if marshalErr != nil || strings.Contains(string(data), "private-argument") {
			t.Fatalf("unsafe DB error: %s (%v)", data, marshalErr)
		}
	}
	data, _ := json.Marshal(dbTraceError(sqlValueError{}))
	if !strings.Contains(string(data), "22P02") {
		t.Fatal("database error lost SQLSTATE")
	}
}

func TestSQLTraceBridgePreservesApplicationParentAndFailure(t *testing.T) {
	reporter := &devReporter{appID: "app", queue: make(chan devreport.ReportEnvelope, 8)}
	restore := setTestReporter(reporter)
	defer restore()
	ctx := withState(context.Background(), &requestState{traceEnabled: true, trace: &traceSpan{traceID: "trace", spanID: "request"}})
	ctx, end := (applicationSpanStarter{}).StartApplicationSpan(ctx, "database work")
	work := stateFromContext(ctx).trace
	host := appsdk.CurrentHost()
	query := host.TraceDBQueryStart(ctx, "SELECT 1/0", 0)
	host.TraceDBQueryEnd(query, "", -1, errors.New("division by zero"))
	end(nil)
	var found bool
	for len(reporter.queue) > 0 {
		report := <-reporter.queue
		if summary := report.TraceSummary; summary != nil && summary.Type == "DB" {
			found = true
			if !summary.IsError || summary.ParentSpanID == nil || *summary.ParentSpanID != work.spanID || summary.TraceID != work.traceID {
				t.Fatalf("SQL child = %+v", summary)
			}
		}
	}
	if !found {
		t.Fatal("runtime host did not emit SQL span")
	}
}

func TestNormalizeDBQueryProtectsPostgresLiterals(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{`SELECT 'private', E'private\'value', $$private$$, $tag$private ' value$tag$`, `SELECT ?, E?, ?, ?`},
		{`SELECT 'path\', 'private'`, `SELECT ?, ?`},
		{"-- private\nSELECT /* outer /* private */ private */ 1.2e-3, $12 FROM \"users2\"", `SELECT ?, $12 FROM "users2"`},
		{"-- name: FindUser :one\nSELECT 'private' -- private", `-- name: FindUser SELECT ?`},
		{`SELECT 'unterminated-private`, `SELECT ?`},
	} {
		if got := normalizeDBQuery(tc.query); got != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
	if got := normalizeDBQuery(strings.Repeat("č", maxDBQueryLength)); !utf8.ValidString(got) || len(got) > maxDBQueryLength+3 {
		t.Fatal("invalid truncated SQL")
	}
}

func TestTraceDBQueryRecordsChildSpan(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		queue: make(chan devreport.ReportEnvelope, 8),
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
		queue: make(chan devreport.ReportEnvelope, 4),
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
		queue: make(chan devreport.ReportEnvelope, 4),
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
		queue: make(chan devreport.ReportEnvelope, 4),
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
