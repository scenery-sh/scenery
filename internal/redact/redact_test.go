package redact

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestValuePreservesErrorMessages proves logged errors keep their message:
// errors carry no exported fields, so the struct walk used to erase them to
// an empty map and every `slog.Warn(..., "err", err)` rendered `error=map[]`.
func TestValuePreservesErrorMessages(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("open store: %w", errors.New("connection refused"))
	got := Value(wrapped)
	text, ok := got.(string)
	if !ok || text != "open store: connection refused" {
		t.Fatalf("Value(error) = %#v, want the error message", got)
	}

	// Error text is still scrubbed like any other string.
	leaky := errors.New("connect failed: password=hunter2")
	text, ok = Value(leaky).(string)
	if !ok || strings.Contains(text, "hunter2") || !strings.Contains(text, Placeholder) {
		t.Fatalf("Value(sensitive error) = %#v, want redacted message", text)
	}

	// A nil error value stays nil instead of becoming a string.
	var nilErr error
	if got := Value(nilErr); got != nil {
		t.Fatalf("Value(nil error) = %#v, want nil", got)
	}

	// Errors nested in structures keep their message too.
	type report struct {
		Err error
	}
	nested, ok := Value(report{Err: errors.New("boom")}).(map[string]any)
	if !ok || nested["Err"] != "boom" {
		t.Fatalf("Value(struct{error}) = %#v, want nested message", nested)
	}
}

func TestStringRedactsTheCredentialsOfAURLInsideAMessage(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"open postgres://shop:hunter2@db.internal/shop: connection refused":   "open postgres://shop:[redacted]@db.internal/shop: connection refused",
		"dial https://svc:p%40ss@example.test:8443/x and http://plain.test/y": "dial https://svc:[redacted]@example.test:8443/x and http://plain.test/y",
		"listen tcp 203.0.113.7:59999: bind: can't assign requested address":  "listen tcp 203.0.113.7:59999: bind: can't assign requested address",
		"ratio 3:4 at user@example.test":                                      "ratio 3:4 at user@example.test",
	} {
		if got := String(input); got != want {
			t.Errorf("String(%q) = %q, want %q", input, got, want)
		}
	}
}
