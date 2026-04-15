package runtime

import (
	"context"
	"strings"
	"time"

	"pulse.dev/internal/devdash"
	"pulse.dev/runtime/shared"
)

type dbQueryTraceKey struct{}

type dbQueryTrace struct {
	reporter  *devReporter
	span      *traceSpan
	started   time.Time
	query     string
	operation string
	argsCount int
}

const maxDBQueryLength = 2048

// TraceDBQueryStart starts a child trace for a database query and returns
// a context carrying the query trace metadata for TraceDBQueryEnd.
func TraceDBQueryStart(ctx context.Context, query string, argsCount int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	state := stateFromContext(ctx)
	if state == nil {
		state = currentState()
	}
	if state == nil || state.trace == nil {
		return ctx
	}

	reporter := activeReporter()
	if reporter == nil {
		return ctx
	}

	started := time.Now().UTC()
	query = normalizeDBQuery(query)
	operation := dbQueryOperation(query)
	trace := &dbQueryTrace{
		reporter: reporter,
		span: &traceSpan{
			traceID:      state.trace.traceID,
			spanID:       newSpanID(),
			parentSpanID: state.trace.spanID,
			spanType:     "DB",
			service:      state.request.Service,
			endpoint:     operation,
			started:      started,
			requestType:  shared.InternalCall,
		},
		started:   started,
		query:     query,
		operation: operation,
		argsCount: max(argsCount, 0),
	}

	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devdash.TraceEvent{
			TraceID:   trace.span.traceID,
			SpanID:    trace.span.spanID,
			EventID:   reporter.nextEventID(),
			EventTime: started,
			Event: map[string]any{
				"span_start": map[string]any{
					"db": map[string]any{
						"service_name":  state.request.Service,
						"endpoint_name": state.request.Endpoint,
						"operation":     operation,
						"query":         query,
						"args_count":    trace.argsCount,
					},
				},
			},
		},
	})

	return context.WithValue(ctx, dbQueryTraceKey{}, trace)
}

// TraceDBQueryEnd finishes a database query trace started by TraceDBQueryStart.
func TraceDBQueryEnd(ctx context.Context, commandTag string, rowsAffected int64, err error) {
	if ctx == nil {
		return
	}

	trace, _ := ctx.Value(dbQueryTraceKey{}).(*dbQueryTrace)
	if trace == nil || trace.reporter == nil || trace.span == nil {
		return
	}

	duration := time.Since(trace.started)
	commandTag = strings.TrimSpace(commandTag)
	dbInfo := map[string]any{
		"operation": trace.operation,
		"query":     trace.query,
	}
	if commandTag != "" {
		dbInfo["command_tag"] = commandTag
	}
	if rowsAffected >= 0 {
		dbInfo["rows_affected"] = rowsAffected
	}

	trace.reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-event",
		AppID: trace.reporter.appID,
		TraceEvent: &devdash.TraceEvent{
			TraceID:   trace.span.traceID,
			SpanID:    trace.span.spanID,
			EventID:   trace.reporter.nextEventID(),
			EventTime: time.Now().UTC(),
			Event: map[string]any{
				"span_end": map[string]any{
					"duration_nanos": uint64(duration),
					"status_code":    statusCodeName(err),
					"db":             dbInfo,
					"error":          traceError(err),
				},
			},
		},
	})

	trace.reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-summary",
		AppID: trace.reporter.appID,
		TraceSummary: &devdash.TraceSummary{
			AppID:         trace.reporter.appID,
			TraceID:       trace.span.traceID,
			SpanID:        trace.span.spanID,
			Type:          trace.span.spanType,
			IsRoot:        false,
			IsError:       err != nil,
			StartedAt:     trace.started,
			DurationNanos: uint64(duration),
			ServiceName:   trace.span.service,
			EndpointName:  optionalString(trace.span.endpoint),
			ParentSpanID:  optionalString(trace.span.parentSpanID),
		},
	})
}

func normalizeDBQuery(query string) string {
	query = strings.Join(strings.Fields(strings.TrimSpace(query)), " ")
	if query == "" {
		return "unknown"
	}
	if len(query) > maxDBQueryLength {
		return query[:maxDBQueryLength] + "..."
	}
	return query
}

func dbQueryOperation(query string) string {
	if query == "" {
		return "QUERY"
	}
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return "QUERY"
	}
	return strings.ToUpper(fields[0])
}
