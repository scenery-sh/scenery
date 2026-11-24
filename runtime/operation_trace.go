package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/devreport"
)

// beginOperationTrace gives a framework-owned execution its own identity while
// preserving the invocation token, authentication and generation admission.
func beginOperationTrace(ctx context.Context, kind, service, operation string, attributes map[string]any) (context.Context, func(error)) {
	traceCtx, end := beginOperationTraceAt(ctx, kind, service, operation, attributes, time.Now().UTC())
	return traceCtx, func(err error) { end(time.Now().UTC(), err) }
}

func beginOperationTraceAt(ctx context.Context, kind, service, operation string, attributes map[string]any, started time.Time) (context.Context, func(time.Time, error)) {
	if ctx == nil {
		ctx = context.Background()
	}
	state := stateFromContext(ctx)
	if state == nil {
		state = currentState()
	}
	if state == nil || !state.traceEnabled {
		return ctx, func(time.Time, error) {}
	}
	if service == "" {
		service = state.request.Service
	}
	clone := *state
	span := &traceSpan{traceID: newTraceID(), spanID: newSpanID(), spanType: kind, service: service, endpoint: operation, started: started, isRoot: true, requestType: state.request.Type, generation: state.processGeneration}
	if traceID, _, ok := parseTraceParent("00-" + state.request.TraceID + "-1111111111111111-01"); ok {
		span.traceID = traceID
	}
	if state.trace != nil {
		span.traceID, span.parentSpanID, span.isRoot = state.trace.traceID, state.trace.spanID, false
	} else if traceID, parentID, ok := parseTraceParent(state.request.Headers.Get("traceparent")); ok {
		span.traceID, span.parentSpanID = traceID, parentID
	}
	clone.trace = span
	clone.request.TraceID, clone.request.Service, clone.request.Endpoint = span.traceID, service, operation
	clone.request.Started, clone.started = started, started
	reporter := activeReporter()
	key := strings.ToLower(kind)
	data := make(map[string]any, len(attributes)+5)
	for k, v := range attributes {
		data[k] = v
	}
	data["service_name"], data["operation"] = service, operation
	data["invocation_id"] = state.request.InvocationID
	if state.workerRuntime != "" {
		data["runtime"], data["module"] = state.workerRuntime, state.workerModule
	}
	if reporter != nil {
		emitOperationEvent(reporter, span, started, map[string]any{"span_start": map[string]any{key: data}})
	}
	var once sync.Once
	return withState(ctx, &clone), func(ended time.Time, err error) {
		once.Do(func() {
			if reporter == nil {
				return
			}
			duration := uint64(max(0, ended.Sub(started)))
			emitOperationEvent(reporter, span, ended, map[string]any{"span_end": map[string]any{"duration_nanos": duration, "status_code": statusCodeName(err), key: data, "error": traceError(err)}})
			reporter.enqueue(devreport.ReportEnvelope{Type: "trace-summary", AppID: reporter.appID, TraceSummary: &devreport.TraceSummary{AppID: reporter.appID, TraceID: span.traceID, SpanID: span.spanID, Type: kind, InvocationCorrelation: invocationCorrelation(state), ErrorCode: traceErrorCode(err), IsRoot: span.isRoot, IsError: err != nil, StartedAt: started, DurationNanos: duration, ServiceName: service, EndpointName: optionalString(operation), ParentSpanID: optionalString(span.parentSpanID), Generation: span.generation}})
		})
	}
}

func emitOperationEvent(reporter *devReporter, span *traceSpan, at time.Time, data map[string]any) {
	reporter.enqueue(devreport.ReportEnvelope{Type: "trace-event", AppID: reporter.appID, TraceEvent: &devreport.TraceEvent{TraceID: span.traceID, SpanID: span.spanID, EventID: reporter.nextEventID(), EventTime: at, Event: data}})
}

// finishOperation also records panics without exposing the panic payload or
// changing the recovery policy of the surrounding execution adapter.
func finishOperation(end func(error), err *error) {
	if value := recover(); value != nil {
		end(errors.New("operation panicked"))
		panic(value)
	}
	end(*err)
}

func traceParentForContext(ctx context.Context) string {
	state := stateFromContext(ctx)
	if state == nil {
		state = currentState()
	}
	if state == nil || state.trace == nil {
		return ""
	}
	return "00-" + state.trace.traceID + "-" + state.trace.spanID + "-01"
}

// Only the current fixed-length W3C format is accepted. Malformed or zero
// identifiers never become backend trace IDs.
func parseTraceParent(value string) (string, string, bool) {
	if len(value) != 55 || value[:3] != "00-" || value[35] != '-' || value[52] != '-' {
		return "", "", false
	}
	traceID, spanID := value[3:35], value[36:52]
	for _, part := range []string{traceID, spanID, value[53:]} {
		for _, c := range part {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return "", "", false
			}
		}
	}
	if traceID == strings.Repeat("0", 32) || spanID == strings.Repeat("0", 16) {
		return "", "", false
	}
	return traceID, spanID, true
}

// Generated policy metadata identifies the semantic operation independently of
// its HTTP, internal or MCP transport.
func operationTraceIdentity(policy *ContractHTTPPolicy, fallback string, attributes map[string]any) (string, map[string]any) {
	if attributes == nil {
		attributes = map[string]any{}
	}
	if policy != nil {
		if policy.ServiceName != "" {
			fallback = policy.ServiceName
		}
		if policy.OperationAddress != "" {
			attributes["operation_address"] = policy.OperationAddress
		}
		if policy.BindingAddress != "" {
			attributes["binding_address"] = policy.BindingAddress
		}
	}
	return fallback, attributes
}

func invocationCorrelation(state *requestState) devreport.InvocationCorrelation {
	return devreport.InvocationCorrelation{InvocationID: state.request.InvocationID, ExecutionID: state.request.ExecutionID, Module: state.workerModule, Runtime: state.workerRuntime}
}

type traceCodedError string

func (e traceCodedError) Error() string { return string(e) }

func traceErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	}
	if code, ok := errors.AsType[traceCodedError](err); ok {
		return string(code)
	}
	if failure, ok := errors.AsType[*ContractTransportError](err); ok {
		return failure.Outcome
	}
	return string(errs.Code(err))
}
