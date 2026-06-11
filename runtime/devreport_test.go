package runtime

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"scenery.sh/internal/devreport"
	"scenery.sh/runtime/shared"
)

func TestDevReporterDisablesOnConnectionRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reporter := &devReporter{
			appID: "app",
			url:   "http://127.0.0.1:9401/__scenery/report",
			token: "token",
			client: &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
				}),
			},
			queue: make(chan devreport.ReportEnvelope, 4),
			done:  make(chan struct{}),
			stop:  make(chan struct{}),
		}
		var stopOnce sync.Once
		t.Cleanup(func() { stopOnce.Do(func() { close(reporter.stop) }) })

		go reporter.loop()
		reporter.enqueue(devreport.ReportEnvelope{Type: "trace-event"})
		synctest.Wait()

		select {
		case <-reporter.done:
		default:
			t.Fatal("reporter loop did not stop after connection refused")
		}

		if !reporter.disabled.Load() {
			t.Fatal("reporter should be disabled after connection refused")
		}

		reporter.enqueue(devreport.ReportEnvelope{Type: "trace-event"})
	})
}

func TestDevReporterAddsSessionIdentity(t *testing.T) {
	reporter := &devReporter{
		appID:       "app",
		sessionID:   "session-a",
		appRootHash: "root123",
		branch:      "feature/a",
		worktree:    "onlv-a",
		queue:       make(chan devreport.ReportEnvelope, 4),
		stop:        make(chan struct{}),
	}
	reporter.enqueue(devreport.ReportEnvelope{
		Type: "trace-summary",
		TraceSummary: &devreport.TraceSummary{
			TraceID: "trace-1",
			SpanID:  "span-1",
		},
	})

	report := <-reporter.queue
	if report.AppID != "app" || report.SessionID != "session-a" || report.AppRootHash != "root123" || report.Branch != "feature/a" || report.Worktree != "onlv-a" {
		t.Fatalf("envelope identity = %+v", report)
	}
	if report.ReporterPID <= 0 {
		t.Fatalf("reporter pid = %d, want current process pid", report.ReporterPID)
	}
	if report.TraceSummary.AppID != "app" || report.TraceSummary.SessionID != "session-a" || report.TraceSummary.AppRootHash != "root123" || report.TraceSummary.Branch != "feature/a" || report.TraceSummary.Worktree != "onlv-a" {
		t.Fatalf("summary identity = %+v", report.TraceSummary)
	}
}

func TestDevReportBackoffDelay(t *testing.T) {
	tests := []struct {
		failures uint64
		want     time.Duration
	}{
		{failures: 0, want: 0},
		{failures: 1, want: 100 * time.Millisecond},
		{failures: 2, want: 200 * time.Millisecond},
		{failures: 5, want: 1600 * time.Millisecond},
		{failures: 10, want: 2 * time.Second},
	}
	for _, tt := range tests {
		if got := devReportBackoffDelay(tt.failures); got != tt.want {
			t.Fatalf("devReportBackoffDelay(%d) = %v, want %v", tt.failures, got, tt.want)
		}
	}
}

func TestDevReporterBacksOffAfterFailedPost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		reporter := &devReporter{
			appID: "app",
			url:   "http://dashboard.test/__scenery/report",
			token: "token",
			client: &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls.Add(1)
					return &http.Response{
						StatusCode: http.StatusUnauthorized,
						Status:     "401 Unauthorized",
						Body:       io.NopCloser(strings.NewReader("unauthorized")),
					}, nil
				}),
			},
			queue: make(chan devreport.ReportEnvelope, 4),
			done:  make(chan struct{}),
			stop:  make(chan struct{}),
		}
		var stopOnce sync.Once
		t.Cleanup(func() { stopOnce.Do(func() { close(reporter.stop) }) })

		go reporter.loop()
		reporter.enqueue(devreport.ReportEnvelope{Type: "trace-event"})
		reporter.enqueue(devreport.ReportEnvelope{Type: "trace-event"})
		synctest.Wait()

		if got := calls.Load(); got != 1 {
			t.Fatalf("post calls before backoff = %d, want 1", got)
		}
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		stopOnce.Do(func() { close(reporter.stop) })
		<-reporter.done

		if got := calls.Load(); got != 2 {
			t.Fatalf("post calls = %d, want 2", got)
		}
		if reporter.failures.Load() != 2 {
			t.Fatalf("failures = %d, want 2", reporter.failures.Load())
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestTracedRoundTripperRedactsSensitiveURLAndError(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		queue: make(chan devreport.ReportEnvelope, 4),
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
