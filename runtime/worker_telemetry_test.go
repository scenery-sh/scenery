package runtime

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"scenery.sh/internal/workerabi"
)

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
