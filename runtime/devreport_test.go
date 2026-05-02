package runtime

import (
	"context"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"

	"onlava.com/internal/devdash"
	"onlava.com/runtime/shared"
)

func TestDevReporterDisablesOnConnectionRefused(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		url:   "http://127.0.0.1:9401/__onlava/report",
		token: "token",
		client: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
			}),
		},
		queue: make(chan devdash.ReportEnvelope, 4),
		done:  make(chan struct{}),
		stop:  make(chan struct{}),
	}

	go reporter.loop()
	reporter.enqueue(devdash.ReportEnvelope{Type: "trace-event"})

	select {
	case <-reporter.done:
	case <-time.After(2 * time.Second):
		t.Fatal("reporter loop did not stop after connection refused")
	}

	if !reporter.disabled.Load() {
		t.Fatal("reporter should be disabled after connection refused")
	}

	reporter.enqueue(devdash.ReportEnvelope{Type: "trace-event"})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestTracedRoundTripperRedactsSensitiveURLAndError(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		queue: make(chan devdash.ReportEnvelope, 4),
	}
	restoreReporter := setTestReporter(reporter)
	defer restoreReporter()

	state := &requestState{
		request: shared.Request{
			Service:  "users",
			Endpoint: "ExchangeSession",
		},
		traceEnabled: true,
		trace: &traceSpan{
			traceID: "trace-1",
			spanID:  "span-1",
			isRoot:  true,
		},
	}
	restoreState := enterState(state)
	defer restoreState()

	transport := &tracedRoundTripper{
		reporter: reporter,
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, syscall.ECONNREFUSED
		}),
	}

	req, err := http.NewRequestWithContext(withState(context.Background(), state), http.MethodGet, "https://user:pass@example.com/path?token=abc&x=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = transport.RoundTrip(req)

	start := <-reporter.queue
	end := <-reporter.queue
	startPayload := start.TraceEvent.Event["span_event"].(map[string]any)["http_call_start"].(map[string]any)
	if got := startPayload["url"]; got != "https://user:%5Bredacted%5D@example.com/path?token=%5Bredacted%5D&x=1" {
		t.Fatalf("redacted url = %#v", got)
	}
	endPayload := end.TraceEvent.Event["span_event"].(map[string]any)["http_call_end"].(map[string]any)
	errPayload := endPayload["err"].(map[string]any)
	if got := errPayload["msg"]; got != "connection refused" {
		t.Fatalf("error msg = %#v", got)
	}
}
