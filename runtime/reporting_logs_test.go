package runtime

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestReportingLogsPreserveContextAndBoundGroups(t *testing.T) {
	var output bytes.Buffer
	reporter := &devReporter{appID: "app", queue: make(chan []byte, 4)}
	logger := slog.New(&reportingHandler{base: newSceneryConsoleHandler(&output), reporter: reporter})
	logger = logger.With("component", "worker").WithGroup("job").With("id", 7).WithGroup("attempt")
	ctx := withState(context.Background(), &requestState{logsEnabled: true, trace: &traceSpan{traceID: "trace", spanID: "work"}})
	// A goroutine has no current invocation; the explicit context owns correlation.
	done := make(chan struct{})
	go func() {
		defer close(done)
		logger.InfoContext(ctx, "work completed", "number", 2, "token", map[string]any{"secret": "private-value"})
	}()
	<-done
	if len(reporter.queue) != 1 {
		t.Fatalf("reports = %d", len(reporter.queue))
	}
	log := decodeQueuedReport(t, <-reporter.queue).LogEvent
	if log.TraceID != "trace" || log.SpanID != "work" {
		t.Fatalf("log context = %+v", log)
	}
	for key, value := range map[string]any{"component": "worker", "job.id": "7", "job.attempt.number": "2", "job.attempt.token": "[redacted]"} {
		if log.Attrs[key] != value {
			t.Fatalf("attribute %s = %#v, want %#v", key, log.Attrs[key], value)
		}
	}
	if strings.Contains(output.String(), "private-value") || !strings.Contains(output.String(), "component=worker") || !strings.Contains(output.String(), "job.id=7") {
		t.Fatalf("console = %s", output.String())
	}
	output.Reset()
	logger.InfoContext(withState(ctx, &requestState{logsEnabled: false}), "filtered")
	if len(reporter.queue) != 0 || output.Len() != 0 {
		t.Fatal("context-filtered log was emitted")
	}
}
