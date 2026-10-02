package runtime

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestLoggingUsesContextIdentityBeforeGoroutineFallback(t *testing.T) {
	var output bytes.Buffer
	r := &devReporter{appID: "app", queue: make(chan []byte, 4)}
	h := &reportingHandler{base: newSceneryConsoleHandler(&output), reporter: r}
	restore := enterState(&requestState{logsEnabled: true, trace: &traceSpan{traceID: "outer", spanID: "outer"}})
	defer restore()
	ctx := withState(context.Background(), &requestState{logsEnabled: false})
	if err := h.Handle(ctx, slog.NewRecord(time.Now(), slog.LevelInfo, "suppressed", 0)); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 || len(r.queue) != 0 {
		t.Fatal("context log suppression ignored")
	}
	ctx = withState(context.Background(), &requestState{logsEnabled: true, trace: &traceSpan{traceID: "context", spanID: "child"}})
	if err := h.Handle(ctx, slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)); err != nil {
		t.Fatal(err)
	}
	if report := decodeQueuedReport(t, <-r.queue); report.LogEvent.TraceID != "context" || report.LogEvent.SpanID != "child" {
		t.Fatal("context trace identity lost")
	}
	if err := h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "fallback", 0)); err != nil {
		t.Fatal(err)
	}
	if report := decodeQueuedReport(t, <-r.queue); report.LogEvent.TraceID != "outer" {
		t.Fatal("context-free fallback lost")
	}
}
