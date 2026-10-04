package runtime

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"scenery.sh/errs"
)

func BenchmarkConsoleRequestLog(b *testing.B) {
	record := slog.NewRecord(time.Unix(0, 0), levelTrace, "request completed", 0)
	record.AddAttrs(
		slog.Any("code", errs.OK),
		slog.Int64("duration_ms", 231),
		slog.String("endpoint", "Config"),
		slog.String("service", "tenants"),
		slog.String("trace_id", "trace-123"),
	)
	b.Run("serial", func(b *testing.B) {
		handler := newSceneryConsoleHandler(io.Discard)
		b.ReportAllocs()
		for b.Loop() {
			if err := handler.Handle(context.Background(), record); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("parallel", func(b *testing.B) {
		handler := newSceneryConsoleHandler(io.Discard)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if err := handler.Handle(context.Background(), record); err != nil {
					b.Error(err)
					return
				}
			}
		})
	})
}
