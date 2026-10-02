package runtime

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"scenery.sh/internal/devreport"
)

type reportEncodingProbe struct{ calls *atomic.Int64 }

func (p reportEncodingProbe) MarshalJSON() ([]byte, error) {
	p.calls.Add(1)
	return []byte(`"encoded"`), nil
}

func TestDevReporterRefusesFullQueuesBeforeEncoding(t *testing.T) {
	for _, mode := range []string{"slots", "bytes", "stopped", "available"} {
		t.Run(mode, func(t *testing.T) {
			r := &devReporter{queue: make(chan []byte, 1), stop: make(chan struct{})}
			var calls atomic.Int64
			switch mode {
			case "slots":
				r.queue <- []byte("queued")
			case "bytes":
				r.queuedBytes.Store(devreport.MaxQueuedBytes)
			case "stopped":
				close(r.stop)
			}
			r.enqueue(devreport.ReportEnvelope{Type: "log", LogEvent: &devreport.LogEvent{Attrs: map[string]any{"probe": reportEncodingProbe{&calls}}}})
			encoded, dropped := int64(0), uint64(1)
			switch mode {
			case "available":
				encoded, dropped = 1, 0
			case "stopped":
				dropped = 0
			}
			if calls.Load() != encoded || r.dropped.Load() != dropped {
				t.Fatalf("mode=%s encoded=%d dropped=%d", mode, calls.Load(), r.dropped.Load())
			}
		})
	}
}

func TestDevReporterBatchesReadyRecordsInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var received []devreport.ReportBatch
		r := &devReporter{appID: "app", url: "http://report.test/report", token: "secret", queue: make(chan []byte, 128), done: make(chan struct{}), stop: make(chan struct{})}
		r.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Authorization") != "Bearer secret" {
				t.Fatal("missing batch authentication")
			}
			var batch devreport.ReportBatch
			if err := json.NewDecoder(req.Body).Decode(&batch); err != nil {
				t.Fatal(err)
			}
			received = append(received, batch)
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}
		for index := range 70 {
			r.enqueue(devreport.ReportEnvelope{Type: "log", LogEvent: &devreport.LogEvent{Attrs: map[string]any{"index": index}}})
		}
		r.dropped.Store(3)
		go r.loop()
		synctest.Wait()
		r.stopLoop()
		<-r.done
		if len(received) != 2 || len(received[0].Reports) != 64 || len(received[1].Reports) != 6 || received[0].Dropped != 3 || r.queuedBytes.Load() != 0 {
			t.Fatalf("batch boundaries or reservation lifetime: %+v bytes=%d", received, r.queuedBytes.Load())
		}
		index := 0
		for _, batch := range received {
			for _, report := range batch.Reports {
				if report.LogEvent.Attrs["index"] != float64(index) {
					t.Fatal("reports reordered")
				}
				index++
			}
		}
	})
}

func decodeQueuedReport(t *testing.T, body []byte) devreport.ReportEnvelope {
	t.Helper()
	var report devreport.ReportEnvelope
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.TraceSummary != nil {
		report.TraceSummary.AppID = report.AppID
	}
	if report.TraceEvent != nil {
		report.TraceEvent.AppID = report.AppID
	}
	return report
}

func TestDevReporterBoundsEncodedQueueAndDetachesCallerData(t *testing.T) {
	r := &devReporter{appID: "app", queue: make(chan []byte, 1024)}
	attrs := map[string]any{"message": "original"}
	r.enqueue(devreport.ReportEnvelope{Type: "log", LogEvent: &devreport.LogEvent{Attrs: attrs}})
	attrs["message"] = "changed"
	if report := decodeQueuedReport(t, <-r.queue); report.LogEvent.Attrs["message"] != "original" {
		t.Fatal("queue retained mutable caller attributes")
	}
	// This test consumes the first record without the reporting loop.
	r.queuedBytes.Store(0)
	for range 100 {
		r.enqueue(devreport.ReportEnvelope{Type: "log", LogEvent: &devreport.LogEvent{Message: string(bytes.Repeat([]byte{'x'}, 60<<10))}})
	}
	if r.queuedBytes.Load() > devreport.MaxQueuedBytes || r.dropped.Load() == 0 {
		t.Fatal("encoded queue exceeded byte budget")
	}
	before := len(r.queue)
	r.enqueue(devreport.ReportEnvelope{Type: "log", LogEvent: &devreport.LogEvent{Message: string(bytes.Repeat([]byte{'x'}, devreport.MaxEnvelopeBytes))}})
	if len(r.queue) != before {
		t.Fatal("oversized record admitted")
	}
}
