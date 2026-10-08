package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"scenery.sh/internal/devdash"
	obs "scenery.sh/internal/observability"
)

// Exercise capacity with authenticated intake and TTL with a natural SDK span.
// The real wait and backend/client round trips belong only to this named probe.
func proveHarnessTraceBufferLoss(ctx context.Context, p *worktreeRuntimeProbe, root string, rpc *runtimeBoundsConnection, rpcURL, appID, api string, selectEvents *atomic.Int64, hostPID, buildDigest string, artifacts harnessArtifactContext) (map[string]any, error) {
	const capacity = 4096
	type counters struct {
		Dropped uint64 `json:"dropped"`
		Failed  uint64 `json:"failed"`
		Events  uint64 `json:"trace_buffer_dropped_events"`
	}
	read := func() (counters, error) {
		response, err := rpc.call(10, "status", map[string]any{}, 5*time.Second)
		if err != nil || response.Error != nil {
			return counters{}, fmt.Errorf("trace buffer status: %v %+v", err, response.Error)
		}
		var status struct {
			Observability struct {
				Export map[string]json.RawMessage `json:"export"`
			} `json:"observability"`
		}
		if err := json.Unmarshal(response.Result, &status); err != nil {
			return counters{}, err
		}
		if _, present := status.Observability.Export["trace_buffer_dropped_events"]; !present {
			return counters{}, errors.New("served status lacks trace-event loss evidence")
		}
		data, err := json.Marshal(status.Observability.Export)
		if err != nil {
			return counters{}, err
		}
		var result counters
		if err := json.Unmarshal(data, &result); err != nil {
			return counters{}, err
		}
		return result, nil
	}
	emit := func(count int64) (string, error) {
		selectEvents.Store(count)
		defer selectEvents.Store(0)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/books", nil)
		if err != nil {
			return "", err
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return "", err
		}
		defer func() { _ = response.Body.Close() }()
		_, readErr := io.Copy(io.Discard, response.Body)
		if readErr != nil || response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("trace buffer fixture: HTTP %d: %v", response.StatusCode, readErr)
		}
		if response.Header.Get("X-Scenery-Process-ID") != hostPID || response.Header.Get("X-Scenery-Build-Input-Digest") != buildDigest {
			return "", errors.New("trace buffer proof changed serving identity")
		}
		return response.Header.Get("X-Trace-Id"), nil
	}
	detail := func(traceID string, matches func(devdash.TraceDetail) bool) (devdash.TraceDetail, error) {
		readback, stop := context.WithTimeout(ctx, 45*time.Second)
		defer stop()
		var observed devdash.TraceDetail
		var lastErr error
		err := waitForHarnessCondition(readback, func() bool {
			response, err := rpc.call(11, "traces/get", map[string]any{"app_id": appID, "trace_id": traceID}, 5*time.Second)
			if err != nil || response.Error != nil {
				lastErr = fmt.Errorf("trace detail: %v %+v", err, response.Error)
				return false
			}
			var candidate devdash.TraceDetail
			if err := json.Unmarshal(response.Result, &candidate); err != nil {
				lastErr = err
				return false
			}
			observed = candidate
			return matches(observed)
		})
		if err != nil {
			return observed, fmt.Errorf("trace buffer readback %s (%d spans): %w: %v", traceID, len(observed.Spans), err, lastErr)
		}
		return observed, nil
	}
	completedRequest := func(traceID string) error {
		_, err := detail(traceID, func(value devdash.TraceDetail) bool {
			spans := make([]*devdash.TraceSummary, 0, len(value.Spans))
			for i := range value.Spans {
				spans = append(spans, &value.Spans[i].TraceSummary)
			}
			return verifyObservabilitySpans(spans) == nil
		})
		return err
	}
	before, err := read()
	if err != nil {
		return nil, err
	}
	immediateID, err := emit(0)
	if err != nil {
		return nil, err
	}
	if err := completedRequest(immediateID); err != nil {
		return nil, fmt.Errorf("immediate request: %w", err)
	}
	immediate, err := read()
	if err != nil {
		return nil, err
	}
	if immediate != before {
		return nil, fmt.Errorf("immediate operation added loss: before=%+v after=%+v", before, immediate)
	}
	capacityID, err := emit(capacity + 1)
	if err != nil {
		return nil, err
	}
	if err := completedRequest(capacityID); err != nil {
		return nil, fmt.Errorf("capacity request: %w", err)
	}
	retained, err := detail(strings.Repeat("f", 32), func(value devdash.TraceDetail) bool {
		if len(value.Spans) != (capacity+1+127)/128 {
			return false
		}
		for _, span := range value.Spans {
			if len(span.Events) == 0 || span.IsError {
				return false
			}
		}
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("capacity survivor export: %w", err)
	}
	afterCapacity, err := read()
	if err != nil {
		return nil, err
	}
	if afterCapacity.Events <= before.Events || afterCapacity.Dropped != before.Dropped || afterCapacity.Failed != before.Failed {
		return nil, fmt.Errorf("trace capacity loss units: before=%+v after=%+v", before, afterCapacity)
	}
	heldID, err := emit(1)
	if err != nil {
		return nil, err
	}
	var held devdash.TraceSpanDetail
	_, err = detail(heldID, func(value devdash.TraceDetail) bool {
		for _, span := range value.Spans {
			if span.Type == "WORK" && span.EndpointName != nil && *span.EndpointName == "trace-buffer-long-lived" {
				held = span
				return true
			}
		}
		return false
	})
	if err != nil {
		return nil, fmt.Errorf("long-lived summary export: %w", err)
	}
	if held.DurationNanos < uint64(30*time.Second) || held.IsError || held.ParentSpanID == nil || len(held.Events) != 1 || held.Events[0].Data["span_end"] == nil || held.Events[0].Data["span_start"] != nil {
		return nil, fmt.Errorf("long-lived span lost summary/end evidence: %+v", held)
	}
	spanJSON, err := json.Marshal(held)
	if err != nil {
		return nil, err
	}
	spanArtifact, err := artifacts.Write("trace buffer natural TTL", "observability-trace-buffer-ttl.json", "", spanJSON)
	if err != nil {
		return nil, err
	}
	afterAge, err := read()
	if err != nil {
		return nil, err
	}
	if afterAge.Events != afterCapacity.Events+1 || afterAge.Dropped != before.Dropped || afterAge.Failed != before.Failed {
		return nil, fmt.Errorf("natural TTL loss inventory: capacity=%+v age=%+v", afterCapacity, afterAge)
	}
	// VictoriaMetrics normally hides fresh samples for 30 seconds.
	readback, stop := context.WithTimeout(ctx, 45*time.Second)
	defer stop()
	var durationSeconds float64
	var metricErr error
	if err := waitForHarnessCondition(readback, func() bool {
		output, err := runHarnessAppCLIWithEnv(readback, p.repo, root, p.env, "metrics", "query", "--promql", `scenery_request_duration_seconds{scenery_trace_type="WORK",scenery_endpoint="trace-buffer-long-lived"}`, "--instant", "-o", "json")
		if err != nil {
			metricErr = err
			return false
		}
		var result obs.MetricsQueryResult
		if err := decodeCLIJSON(output, &result); err != nil {
			metricErr = err
			return false
		}
		if !result.Scope.Enforced {
			metricErr = errors.New("duration metric query did not enforce runtime scope")
			return false
		}
		for _, series := range result.Series {
			if series.Value != nil {
				seconds, err := strconv.ParseFloat(series.Value.Value, 64)
				if err == nil && seconds >= 30 {
					durationSeconds = seconds
					return true
				}
			}
		}
		metricErr = fmt.Errorf("duration sample >= 30 seconds missing (%d series)", len(result.Series))
		return false
	}); err != nil {
		return nil, fmt.Errorf("long-lived duration metric: %w: %v", err, metricErr)
	}
	clientPath := filepath.Join(root, "trace-buffer-client.ts")
	if err := os.WriteFile(clientPath, []byte(observabilityStatusClientProbe), 0600); err != nil {
		return nil, err
	}
	output, err := p.run(root, "bun", "run", clientPath, rpcURL, strconv.FormatUint(afterAge.Events, 10))
	if err != nil {
		return nil, err
	}
	var client map[string]any
	if err := json.Unmarshal([]byte(output), &client); err != nil {
		return nil, fmt.Errorf("trace buffer client proof: %w", err)
	}
	retainedEvents := 0
	for _, span := range retained.Spans {
		retainedEvents += len(span.Events)
	}
	return map[string]any{"submitted_capacity_events": capacity + 1, "capacity_phase_evictions": afterCapacity.Events - before.Events, "capacity_retained_events": retainedEvents, "natural_ttl_evictions": afterAge.Events - afterCapacity.Events, "long_lived_trace_id": heldID, "long_lived_span": spanArtifact, "surviving_events": len(held.Events), "duration_metric_seconds": durationSeconds, "immediate_operation_no_loss": true, "existing_loss_counters_preserved": true, "generated_status_client": client, "host_pid": hostPID, "build_input_digest": buildDigest}, nil
}

const observabilityStatusClientProbe = `
import { DevRuntimeClient } from "./client/generated/dev-runtime.ts";
const client = new DevRuntimeClient({url:process.argv[2]});
try {
 const status = await client.status({signal:AbortSignal.timeout(5000)});
 if(status.observability?.export.trace_buffer_dropped_events!==Number(process.argv[3])) throw new Error("generated status client lost buffer eviction count");
 console.log(JSON.stringify({kind:status.kind,schema_revision:status.schema_revision,trace_buffer_dropped_events:status.observability.export.trace_buffer_dropped_events}));
} finally { client.dispose(); }
`
