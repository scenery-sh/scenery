package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"scenery.sh/internal/devdash"
)

// Report intake hands accepted telemetry to the observability backend through
// a bounded queue served by a few workers. A slow or absent backend therefore
// costs dropped telemetry, which is counted, and never unbounded goroutines,
// memory, or a delayed answer to the reporting process or to runtime control.
const (
	telemetryExportQueueLimit = 1024
	telemetryExportWorkers    = 2
	// telemetryExportBatchLimit bounds the reports one export request
	// carries: a worker sends what queued behind a report with it.
	telemetryExportBatchLimit = 64
	telemetryExportTimeout    = 2 * time.Second
)

// telemetryExportJob is one accepted report: a log event, or a trace summary
// with its buffered events.
type telemetryExportJob struct {
	log     *devdash.LogEvent
	summary *devdash.TraceSummary
	events  []*devdash.TraceEvent
}

type telemetryExporter struct {
	jobs    chan telemetryExportJob
	start   sync.Once
	stop    chan struct{}
	halt    sync.Once
	workers sync.WaitGroup
	// export sends one batch and returns how many of its reports failed.
	export  func([]telemetryExportJob) int
	dropped atomic.Uint64
	failed  atomic.Uint64
}

func newTelemetryExporter(export func([]telemetryExportJob) int) *telemetryExporter {
	return &telemetryExporter{jobs: make(chan telemetryExportJob, telemetryExportQueueLimit), stop: make(chan struct{}), export: export}
}

// enqueue queues a report without waiting; a full queue drops it.
func (e *telemetryExporter) enqueue(job telemetryExportJob) {
	if e == nil {
		return
	}
	e.start.Do(func() {
		for range telemetryExportWorkers {
			e.workers.Go(e.work)
		}
	})
	select {
	case <-e.stop:
		e.dropped.Add(1)
		return
	default:
	}
	select {
	case e.jobs <- job:
	default:
		e.dropped.Add(1)
	}
}

func (e *telemetryExporter) work() {
	for {
		var job telemetryExportJob
		select {
		case <-e.stop:
			return
		case job = <-e.jobs:
		}
		batch := []telemetryExportJob{job}
	drain:
		for len(batch) < telemetryExportBatchLimit {
			select {
			case next := <-e.jobs:
				batch = append(batch, next)
			default:
				break drain
			}
		}
		if failed := e.export(batch); failed > 0 {
			e.failed.Add(uint64(failed))
		}
	}
}

// drop counts a report refused before it reached the queue.
func (e *telemetryExporter) drop() {
	if e != nil {
		e.dropped.Add(1)
	}
}

// counts reports what did not reach the observability backend: reports
// dropped because they were too large or the queue was full, and exports that
// failed, one for each report and signal.
func (e *telemetryExporter) counts() runtimeTelemetryExport {
	if e == nil {
		return runtimeTelemetryExport{}
	}
	return runtimeTelemetryExport{Dropped: e.dropped.Load(), Failed: e.failed.Load()}
}

// close stops the workers once their current batch is sent; queued reports
// are abandoned with the process.
func (e *telemetryExporter) close() {
	if e == nil {
		return
	}
	// No worker starts after close.
	e.start.Do(func() {})
	e.halt.Do(func() { close(e.stop) })
	e.workers.Wait()
}

// exportTelemetryBatch sends a batch to the observability backend: one OTLP
// request per signal, whose payload concatenates the batch's encoded
// requests, which protobuf reads as one request holding all their resources.
// A log event goes to the logExporter hook instead when one is set.
func (s *dashboardServer) exportTelemetryBatch(batch []telemetryExportJob) int {
	payloads := map[string][]byte{}
	reports := map[string]int{}
	add := func(signal string, payload []byte) {
		payloads[signal] = append(payloads[signal], payload...)
		reports[signal]++
	}
	for _, job := range batch {
		switch {
		case job.log != nil && s.logExporter != nil:
			s.logExporter(job.log)
		case job.log != nil:
			add("logs", buildOTLPLogProto(job.log))
		case job.summary != nil:
			add("traces", buildOTLPTraceProto(job.summary, job.events))
			add("metrics", buildOTLPMetricProto(job.summary))
		}
	}
	if len(payloads) == 0 {
		return 0
	}
	victoria := s.dashboardVictoria()
	if victoria == nil {
		// Observability is off: nothing was due to be exported.
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), telemetryExportTimeout)
	defer cancel()
	failed := 0
	for signal, payload := range payloads {
		endpoint := victoria.Endpoint(signal)
		if endpoint == "" {
			continue
		}
		if err := postVictoriaProtobuf(ctx, endpoint, payload); err != nil {
			failed += reports[signal]
		}
	}
	return failed
}
