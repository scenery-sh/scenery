package runtime

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/workerabi"
	"scenery.sh/runtime/shared"
)

func TestWorkerTelemetryKeepsScopedIdentityAndElapsedTime(t *testing.T) {
	reporter := &devReporter{appID: "app", queue: make(chan []byte, 8)}
	defer setTestReporter(reporter)()
	state := &requestState{
		traceEnabled: true, processGeneration: 7, workerModule: "pricing", workerRuntime: "rust",
		request: shared.Request{InvocationID: "call"},
		trace:   &traceSpan{traceID: strings.Repeat("a", 32), spanID: strings.Repeat("b", 16)},
	}
	request := httptest.NewRequestWithContext(withState(t.Context(), state), http.MethodPost, "/telemetry",
		strings.NewReader(`[{"kind":"span","name":"quote","elapsed_ns":10000,"attributes":{"invocation_id":"overridden"}}]`))
	response := httptest.NewRecorder()
	serveWorkerTelemetry(WorkerServicePlan{Name: "quotes"}, response, request)
	if response.Code != http.StatusOK || reporter.queuedBytes.Load() == 0 || len(reporter.queue) != 3 {
		t.Fatalf("accepted batch: %d, reports queued: %d", response.Code, len(reporter.queue))
	}
	start := decodeQueuedReport(t, <-reporter.queue).TraceEvent
	end := decodeQueuedReport(t, <-reporter.queue).TraceEvent
	summary := decodeQueuedReport(t, <-reporter.queue).TraceSummary
	attributes := start.Event["span_start"].(map[string]any)["worker"].(map[string]any)
	if summary.InvocationID != "call" || attributes["invocation_id"] != "call" || summary.Runtime != "rust" ||
		summary.Module != "pricing" || summary.ServiceName != "quotes" || summary.Generation != 7 ||
		summary.TraceID != state.trace.traceID || summary.ParentSpanID == nil || *summary.ParentSpanID != state.trace.spanID ||
		summary.DurationNanos != 10000 || end.EventTime.Sub(start.EventTime) != 10*time.Microsecond {
		t.Fatalf("telemetry: summary=%+v attributes=%+v", summary, attributes)
	}
}

func TestGoWorkerLogAndSQLShareHostDispatchSpan(t *testing.T) {
	reporter := &devReporter{appID: "app", queue: make(chan []byte, 8)}
	defer setTestReporter(reporter)()
	traceID, spanID, parent := strings.Repeat("a", 32), strings.Repeat("b", 16), strings.Repeat("c", 16)
	ctx, _, err := goWorkerContext(t.Context(), workerabi.Invocation{
		Service: "data/service/catalog", Operation: "data/operation/find", Mode: "durable",
		Context: workerabi.InvocationContext{
			InvocationID: "job", ExecutionID: "job", Generation: 5,
			Traceparent: "00-" + traceID + "-" + spanID + "-01",
			Metadata:    []byte(`{"logs_enabled":true,"module":"data","service_name":"catalog","parent_span_id":"` + parent + `"}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(&reportingHandler{base: slog.NewJSONHandler(io.Discard, nil), reporter: reporter})
	log.InfoContext(ctx, "querying catalog")
	TraceDBQueryEnd(TraceDBQueryStart(ctx, "SELECT 1", 0), "", -1, context.Canceled)
	if len(reporter.queue) != 4 {
		t.Fatalf("expected log and three SQL records, got %d", len(reporter.queue))
	}
	event := decodeQueuedReport(t, <-reporter.queue).LogEvent
	<-reporter.queue
	<-reporter.queue
	summary := decodeQueuedReport(t, <-reporter.queue).TraceSummary
	if event.InvocationID != "job" || event.ExecutionID != "job" || event.TraceID != traceID || event.SpanID != spanID ||
		event.ParentSpanID != parent || event.Generation != 5 || event.Service != "catalog" ||
		summary.InvocationCorrelation != event.InvocationCorrelation || summary.TraceID != event.TraceID ||
		summary.Generation != event.Generation || summary.ParentSpanID == nil || *summary.ParentSpanID != event.SpanID ||
		summary.ErrorCode != "canceled" {
		t.Fatalf("log=%+v query=%+v", event, summary)
	}
}
