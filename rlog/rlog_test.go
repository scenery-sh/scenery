package rlog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestInfoLogsWithKeyValues(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	Info("hello", "service", "pulse", "count", 3)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal log entry: %v", err)
	}
	if entry["msg"] != "hello" {
		t.Fatalf("msg = %v, want %q", entry["msg"], "hello")
	}
	if entry["service"] != "pulse" {
		t.Fatalf("service = %v, want %q", entry["service"], "pulse")
	}
	if entry["count"] != float64(3) {
		t.Fatalf("count = %v, want %v", entry["count"], 3)
	}
}

func TestWithCarriesContext(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	ctx := With("component", "health").With("request_id", "abc")
	ctx.Warn("degraded", "status", 503)

	line := buf.String()
	for _, part := range []string{"component=health", "request_id=abc", "status=503", "level=WARN", "msg=degraded"} {
		if !strings.Contains(line, part) {
			t.Fatalf("log output missing %q: %s", part, line)
		}
	}
}

func TestNormalizeAttrsHandlesOddAndAttrInput(t *testing.T) {
	attrs := normalizeAttrs("foo", "bar", slog.String("a", "b"), "lonely")
	if len(attrs) != 3 {
		t.Fatalf("len(attrs) = %d, want 3", len(attrs))
	}
	if attrs[0].Key != "foo" || attrs[0].Value.String() != "bar" {
		t.Fatalf("attrs[0] = %#v, want foo=bar", attrs[0])
	}
	if attrs[1].Key != "a" || attrs[1].Value.String() != "b" {
		t.Fatalf("attrs[1] = %#v, want a=b", attrs[1])
	}
	if attrs[2].Key != "lonely" || attrs[2].Value.Any() != nil {
		t.Fatalf("attrs[2] = %#v, want lonely=<nil>", attrs[2])
	}
}
