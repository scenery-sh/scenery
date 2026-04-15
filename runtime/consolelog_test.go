package runtime

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"pulse.dev/errs"
)

func TestPulseConsoleHandlerFormatsTraceRecords(t *testing.T) {
	var out bytes.Buffer
	handler := newPulseConsoleHandler(&out)
	record := slog.NewRecord(time.Date(2026, time.April, 14, 15, 13, 0, 0, time.Local), levelTrace, "request completed", 0)
	record.AddAttrs(
		slog.Any("code", errs.OK),
		slog.Int64("duration_ms", 231),
		slog.String("endpoint", "Config"),
		slog.String("service", "tenants"),
		slog.String("trace_id", "trace-123"),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"3:13PM TRC request completed",
		"code=ok",
		"duration_ms=231",
		"endpoint=Config",
		"service=tenants",
		"trace_id=trace-123",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q does not contain %q", got, want)
		}
	}
}

func TestPulseConsoleHandlerFormatsSecretsWarning(t *testing.T) {
	var out bytes.Buffer
	handler := newPulseConsoleHandler(&out)
	record := slog.NewRecord(time.Now(), slog.LevelWarn, "pulse secrets missing", 0)
	record.AddAttrs(slog.Any("fields", []string{"DatabaseURL", "ResendAPIKey"}))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"warning: secrets not defined: DatabaseURL, ResendAPIKey",
		"note: undefined secrets are left empty for local development only.",
		"https://pulse.dev/docs/primitives/secrets",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q does not contain %q", got, want)
		}
	}
}

func TestPulseConsoleHandlerColorsTraceWhenForced(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")

	var out bytes.Buffer
	handler := newPulseConsoleHandler(&out)
	record := slog.NewRecord(time.Date(2026, time.April, 14, 15, 13, 0, 0, time.Local), levelTrace, "request completed", 0)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "\x1b[34mTRC\x1b[0m") {
		t.Fatalf("output %q does not contain blue TRC label", got)
	}
}
