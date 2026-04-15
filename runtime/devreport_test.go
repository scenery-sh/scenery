package runtime

import (
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"

	"pulse.dev/internal/devdash"
)

func TestDevReporterDisablesOnConnectionRefused(t *testing.T) {
	reporter := &devReporter{
		appID: "app",
		url:   "http://127.0.0.1:9401/__pulse/report",
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
