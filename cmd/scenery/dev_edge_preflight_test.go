package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultConfiguredEdgeRouteProbeRetriesTransientFailures(t *testing.T) {
	withEdgeProbeRetryTiming(t, 200*time.Millisecond, 5*time.Millisecond)

	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := defaultConfiguredEdgeRouteProbe(context.Background(), server.URL); err != nil {
		t.Fatalf("probe should succeed after transient failures: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("probe attempts = %d, want 3", got)
	}
}

func TestDefaultConfiguredEdgeRouteProbeGivesUpAfterWindow(t *testing.T) {
	withEdgeProbeRetryTiming(t, 40*time.Millisecond, 5*time.Millisecond)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	start := time.Now()
	err := defaultConfiguredEdgeRouteProbe(context.Background(), server.URL)
	if err == nil {
		t.Fatal("probe should fail when every attempt fails")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("probe error = %v, want last HTTP 500", err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("probe gave up too early: %v", elapsed)
	}
}

func withEdgeProbeRetryTiming(t *testing.T, window, interval time.Duration) {
	t.Helper()
	oldWindow := edgeProbeRetryWindow
	oldInterval := edgeProbeRetryInterval
	edgeProbeRetryWindow = window
	edgeProbeRetryInterval = interval
	t.Cleanup(func() {
		edgeProbeRetryWindow = oldWindow
		edgeProbeRetryInterval = oldInterval
	})
}
