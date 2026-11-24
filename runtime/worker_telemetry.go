package runtime

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"scenery.sh/internal/runtimeapi"
)

// WorkerTelemetryRecord is a span or log sent in a scoped batch.
type WorkerTelemetryRecord struct {
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Message    string         `json:"message,omitempty"`
	ElapsedNS  int64          `json:"elapsed_ns,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

func serveWorkerTelemetry(plan WorkerServicePlan, w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var batch []WorkerTelemetryRecord
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&batch); err != nil || len(batch) == 0 || len(batch) > 64 || decoder.Decode(new(json.RawMessage)) != io.EOF {
		http.Error(w, "invalid telemetry batch", http.StatusBadRequest)
		return
	}
	for _, record := range batch {
		if record.Kind != "span" && record.Kind != "log" || strings.TrimSpace(record.Name) == "" || len(record.Name) > 128 ||
			len(record.Message) > 2048 || len(record.Attributes) > 32 || record.ElapsedNS < 0 || time.Duration(record.ElapsedNS) > time.Hour {
			http.Error(w, "invalid telemetry record", http.StatusBadRequest)
			return
		}
	}
	at := time.Now().UTC()
	ctx := request.Context()
	invocation, _ := runtimeapi.InvocationFromContext(ctx)
	state := stateFromContext(ctx)
	for _, record := range batch {
		if record.Kind == "log" {
			if state != nil && state.logsEnabled {
				logTrace(ctx, record.Message, "name", record.Name, "service", plan.Name,
					"invocation_id", invocation.ID(), "trace_id", invocation.TraceID(), "attributes", record.Attributes)
			}
			continue
		}
		started := at.Add(-time.Duration(record.ElapsedNS))
		if state != nil && !state.started.IsZero() && started.Before(state.started) {
			started = state.started
		}
		_, end := beginOperationTraceAt(ctx, "WORKER", plan.Name, record.Name, record.Attributes, started)
		end(at, nil)
	}
	writeProcessHostJSON(w, http.StatusOK, struct {
		Accepted int `json:"accepted"`
	}{Accepted: len(batch)})
}
