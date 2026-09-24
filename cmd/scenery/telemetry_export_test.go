package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
)

// exportTestVictoria serves only the export endpoints of the observability
// backend.
type exportTestVictoria struct{ base string }

func (v exportTestVictoria) QueryTraceSummaries(context.Context, devdash.TraceQuery) ([]*devdash.TraceSummary, error) {
	return nil, nil
}
func (v exportTestVictoria) ListDevEvents(context.Context, devdash.DevEventQuery) ([]devdash.DevEvent, error) {
	return nil, nil
}
func (v exportTestVictoria) MarkCleared(string, time.Time) {}
func (v exportTestVictoria) URLs() map[string]string       { return nil }
func (v exportTestVictoria) Endpoint(signal string) string { return v.base + "/" + signal }

type exportTestController struct {
	runtimeRPCTestController
	victoria dashboardVictoria
}

func (c exportTestController) dashboardAuthorizeReport(*http.Request, devdash.ReportEnvelope) dashboardReportAuth {
	return dashboardReportAuth{Authorized: true}
}
func (c exportTestController) dashboardVictoria() dashboardVictoria { return c.victoria }

// otlpTopLevelEntries counts the top-level field-1 entries of an OTLP export
// request: one per resource.
func otlpTopLevelEntries(t *testing.T, payload []byte) int {
	t.Helper()
	entries := 0
	for len(payload) > 0 {
		if payload[0] != 0x0a {
			t.Fatalf("unexpected field key %#x", payload[0])
		}
		length, shift, index := 0, 0, 1
		for ; payload[index]&0x80 != 0; index++ {
			length |= int(payload[index]&0x7f) << shift
			shift += 7
		}
		length |= int(payload[index]) << shift
		payload = payload[index+1+length:]
		entries++
	}
	return entries
}

// A report larger than the intake limit is refused before it is decoded and
// counted as dropped; the status of the runtime reports the count.
func TestReportIntakeRefusesAnOversizedReport(t *testing.T) {
	t.Parallel()

	server := newDashboardServerWithController(exportTestController{runtimeRPCTestController: runtimeRPCTestController{status: devdash.AppStatus{
		AppID: "demo", Observability: &devdash.ObservabilityState{Enabled: true},
	}}}, t.TempDir(), "127.0.0.1:0", nil)
	t.Cleanup(func() { _ = server.Close() })
	body, err := json.Marshal(devdash.ReportEnvelope{Type: "log", LogEvent: &devdash.LogEvent{Message: strings.Repeat("x", dashboardReportMaxBytes)}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	server.handleReport(rec, httptest.NewRequest(http.MethodPost, devdash.ReportPath, bytes.NewReader(body)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized report status = %d", rec.Code)
	}
	result, err := server.dispatchRPC(context.Background(), "status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if export := result.(runtimeStatus).Observability.Export; export != (runtimeTelemetryExport{Dropped: 1}) {
		t.Fatalf("status export = %+v", export)
	}
}

// A backend that does not answer holds at most the workers; intake never
// waits for it, reports beyond the bounded queue are dropped and counted, and
// what the backend refuses is counted as failed.
func TestTelemetryExporterStaysBoundedBehindASlowBackend(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	exported := 0
	exporter := newTelemetryExporter(func(batch []telemetryExportJob) int {
		<-release
		mu.Lock()
		exported += len(batch)
		mu.Unlock()
		return len(batch)
	})
	before := runtime.NumGoroutine()
	started := time.Now()
	const reports = 3 * telemetryExportQueueLimit
	for range reports {
		exporter.enqueue(telemetryExportJob{log: &devdash.LogEvent{Message: "m"}})
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("intake waited %s for a stalled backend", elapsed)
	}
	// Goroutines of earlier tests may still come and go; one per report
	// would be thousands.
	if goroutines := runtime.NumGoroutine() - before; goroutines > telemetryExportWorkers+16 {
		t.Fatalf("%d goroutines behind a stalled backend", goroutines)
	}
	dropped := exporter.counts().Dropped
	held := reports - int(dropped)
	if dropped == 0 || held > telemetryExportQueueLimit+telemetryExportWorkers*telemetryExportBatchLimit {
		t.Fatalf("dropped %d, held %d; the queue holds at most %d", dropped, held, telemetryExportQueueLimit+telemetryExportWorkers*telemetryExportBatchLimit)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		done := exported == held
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	exporter.close()
	if counts := exporter.counts(); counts.Failed != uint64(held) || counts.Dropped != dropped {
		t.Fatalf("counts = %+v, want %d failed", counts, held)
	}
}

// Reports queued together leave in one request per signal whose payload holds
// every report, and a refused request counts each of its reports as failed.
func TestTelemetryExportBatchesReportsPerSignal(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	requests := map[string][]int{}
	refuse := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		payload, _ := io.ReadAll(req.Body)
		mu.Lock()
		requests[req.URL.Path] = append(requests[req.URL.Path], otlpTopLevelEntries(t, payload))
		failing := refuse
		mu.Unlock()
		if failing {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(backend.Close)
	server := newDashboardServerWithController(exportTestController{victoria: exportTestVictoria{base: backend.URL}}, t.TempDir(), "127.0.0.1:0", nil)
	t.Cleanup(func() { _ = server.Close() })
	now := time.Now()
	batch := []telemetryExportJob{
		{log: &devdash.LogEvent{AppID: "demo", Level: "info", Message: "a", Timestamp: now}},
		{log: &devdash.LogEvent{AppID: "demo", Level: "warn", Message: "b", Timestamp: now}},
		{log: &devdash.LogEvent{AppID: "demo", Level: "error", Message: "c", Timestamp: now}},
		{summary: &devdash.TraceSummary{AppID: "demo", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef", StartedAt: now, DurationNanos: 5}},
	}
	if failed := server.exportTelemetryBatch(batch); failed != 0 {
		t.Fatalf("failed = %d", failed)
	}
	mu.Lock()
	if logs, traces, metrics := requests["/logs"], requests["/traces"], requests["/metrics"]; len(logs) != 1 || logs[0] != 3 || len(traces) != 1 || traces[0] != 1 || len(metrics) != 1 {
		t.Fatalf("requests = %v", requests)
	}
	refuse = true
	mu.Unlock()
	if failed := server.exportTelemetryBatch(batch); failed != 5 {
		t.Fatalf("failed = %d, want one per report and signal", failed)
	}
}
