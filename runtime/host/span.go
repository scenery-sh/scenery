package host

import (
	"context"
	"strings"
	"time"

	"scenery.sh/internal/devreport"
	"scenery.sh/internal/runtimeapp"
	"scenery.sh/runtime/shared"
)

const maxApplicationSpanNameLength = 128

// Span is an application-owned child span. End is safe to call more than once.
type Span = runtimeapp.Span

// StartSpan starts a child span beneath the current request or application span.
// The returned context must be passed to nested work so further spans and
// automatically traced database and HTTP activity use this span as their parent.
func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	if ctx == nil {
		ctx = context.Background()
	}

	state := stateFromContext(ctx)
	if state == nil {
		state = currentState()
	}
	if state == nil || state.trace == nil || !state.traceEnabled {
		return ctx, &Span{}
	}

	reporter := activeReporter()
	if reporter == nil {
		return ctx, &Span{}
	}

	name = normalizeApplicationSpanName(name)
	started := time.Now().UTC()
	child := &traceSpan{
		traceID:      state.trace.traceID,
		spanID:       newSpanID(),
		parentSpanID: state.trace.spanID,
		spanType:     "WORK",
		service:      state.request.Service,
		endpoint:     name,
		started:      started,
		requestType:  shared.InternalCall,
	}
	clone := *state
	clone.trace = child

	reporter.enqueue(devreport.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devreport.TraceEvent{
			TraceID:   child.traceID,
			SpanID:    child.spanID,
			EventID:   reporter.nextEventID(),
			EventTime: started,
			Event: map[string]any{
				"span_start": map[string]any{
					"work": map[string]any{
						"service_name": state.request.Service,
						"operation":    name,
					},
				},
			},
		},
	})

	return withState(ctx, &clone), runtimeapp.NewSpan(func(err error) {
		endApplicationSpan(reporter, child, err)
	})
}

func endApplicationSpan(reporter *devReporter, span *traceSpan, err error) {
	finished := time.Now().UTC()
	duration := finished.Sub(span.started)
	reporter.enqueue(devreport.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devreport.TraceEvent{
			TraceID:   span.traceID,
			SpanID:    span.spanID,
			EventID:   reporter.nextEventID(),
			EventTime: finished,
			Event: map[string]any{
				"span_end": map[string]any{
					"duration_nanos": uint64(duration),
					"status_code":    statusCodeName(err),
					"work": map[string]any{
						"operation": span.endpoint,
					},
					"error": traceError(err),
				},
			},
		},
	})
	reporter.enqueue(devreport.ReportEnvelope{
		Type:  "trace-summary",
		AppID: reporter.appID,
		TraceSummary: &devreport.TraceSummary{
			AppID:         reporter.appID,
			TraceID:       span.traceID,
			SpanID:        span.spanID,
			Type:          span.spanType,
			IsRoot:        false,
			IsError:       err != nil,
			StartedAt:     span.started,
			DurationNanos: uint64(duration),
			ServiceName:   span.service,
			EndpointName:  optionalString(span.endpoint),
			ParentSpanID:  optionalString(span.parentSpanID),
		},
	})
}

func normalizeApplicationSpanName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unnamed"
	}
	if len(name) > maxApplicationSpanNameLength {
		return name[:maxApplicationSpanNameLength]
	}
	return name
}
