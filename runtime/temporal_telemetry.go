package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/internal/devdash"
	temporalinterceptor "go.temporal.io/sdk/interceptor"
	temporallog "go.temporal.io/sdk/log"
)

const onlavaTemporalTraceHeader = "onlava-temporal-trace"

var onlavaTemporalSpanContextKey = &struct{ name string }{"onlava-temporal-span"}

type onlavaTemporalTracer struct {
	temporalinterceptor.BaseTracer
	info TemporalRuntimeInfo
}

type onlavaTemporalSpanRef struct {
	traceID string
	spanID  string
}

type onlavaTemporalSpan struct {
	*onlavaTemporalSpanRef
	tracer       *onlavaTemporalTracer
	parentSpanID string
	operation    string
	name         string
	tags         map[string]string
	started      time.Time
}

func newOnlavaTemporalTracer(info TemporalRuntimeInfo) *onlavaTemporalTracer {
	return &onlavaTemporalTracer{info: info}
}

func (t *onlavaTemporalTracer) Options() temporalinterceptor.TracerOptions {
	return temporalinterceptor.TracerOptions{
		SpanContextKey:          onlavaTemporalSpanContextKey,
		HeaderKey:               onlavaTemporalTraceHeader,
		AllowInvalidParentSpans: true,
	}
}

func (t *onlavaTemporalTracer) UnmarshalSpan(data map[string]string) (temporalinterceptor.TracerSpanRef, error) {
	traceID := strings.ToLower(strings.TrimSpace(data["trace_id"]))
	spanID := strings.ToLower(strings.TrimSpace(data["span_id"]))
	if !isTemporalTraceID(traceID) || !isTemporalSpanID(spanID) {
		return nil, nil
	}
	return &onlavaTemporalSpanRef{traceID: traceID, spanID: spanID}, nil
}

func (t *onlavaTemporalTracer) MarshalSpan(span temporalinterceptor.TracerSpan) (map[string]string, error) {
	ref := temporalSpanRef(span)
	if ref == nil || !isTemporalTraceID(ref.traceID) || !isTemporalSpanID(ref.spanID) {
		return nil, nil
	}
	return map[string]string{
		"trace_id": ref.traceID,
		"span_id":  ref.spanID,
	}, nil
}

func (t *onlavaTemporalTracer) SpanFromContext(ctx context.Context) temporalinterceptor.TracerSpan {
	if ctx == nil {
		return nil
	}
	span, _ := ctx.Value(onlavaTemporalSpanContextKey).(*onlavaTemporalSpan)
	return span
}

func (t *onlavaTemporalTracer) ContextWithSpan(ctx context.Context, span temporalinterceptor.TracerSpan) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if span == nil {
		return ctx
	}
	return context.WithValue(ctx, onlavaTemporalSpanContextKey, span)
}

func (t *onlavaTemporalTracer) StartSpan(options *temporalinterceptor.TracerStartSpanOptions) (temporalinterceptor.TracerSpan, error) {
	if options == nil {
		options = &temporalinterceptor.TracerStartSpanOptions{}
	}
	parent := temporalSpanRef(options.Parent)
	traceID := ""
	parentSpanID := ""
	if parent != nil {
		traceID = parent.traceID
		parentSpanID = parent.spanID
	}
	if !isTemporalTraceID(traceID) {
		traceID = newTemporalTraceID(options.IdempotencyKey, options.Operation, options.Name)
	}
	spanID := newTemporalSpanID(options.IdempotencyKey, options.Operation, options.Name)
	started := options.Time
	if started.IsZero() {
		started = time.Now()
	}
	return &onlavaTemporalSpan{
		onlavaTemporalSpanRef: &onlavaTemporalSpanRef{
			traceID: traceID,
			spanID:  spanID,
		},
		tracer:       t,
		parentSpanID: parentSpanID,
		operation:    strings.TrimSpace(options.Operation),
		name:         strings.TrimSpace(options.Name),
		tags:         cloneTemporalTags(options.Tags),
		started:      started.UTC(),
	}, nil
}

func (t *onlavaTemporalTracer) GetLogger(logger temporallog.Logger, ref temporalinterceptor.TracerSpanRef) temporallog.Logger {
	return logger
}

func (t *onlavaTemporalTracer) SpanName(options *temporalinterceptor.TracerStartSpanOptions) string {
	if options == nil {
		return "temporal.operation"
	}
	if strings.TrimSpace(options.Name) == "" {
		return "temporal." + strings.TrimSpace(options.Operation)
	}
	return "temporal." + strings.TrimSpace(options.Operation) + ":" + strings.TrimSpace(options.Name)
}

func (s *onlavaTemporalSpan) Finish(options *temporalinterceptor.TracerFinishSpanOptions) {
	if s == nil || s.tracer == nil {
		return
	}
	reporter := activeReporter()
	if reporter == nil {
		return
	}
	finished := time.Now().UTC()
	started := s.started
	if started.IsZero() || started.After(finished) {
		started = finished
	}
	duration := finished.Sub(started)
	var err error
	if options != nil {
		err = options.Error
	}
	endpointName := optionalString(s.name)
	summary := &devdash.TraceSummary{
		AppID:         reporter.appID,
		TraceID:       s.traceID,
		SpanID:        s.spanID,
		Type:          temporalTraceType(s.operation),
		IsRoot:        s.parentSpanID == "",
		IsError:       err != nil,
		StartedAt:     started,
		DurationNanos: uint64(duration),
		ServiceName:   "temporal",
		EndpointName:  endpointName,
	}
	if s.parentSpanID != "" {
		summary.ParentSpanID = optionalString(s.parentSpanID)
	}
	reporter.enqueue(devdash.ReportEnvelope{
		Type:         "trace-summary",
		AppID:        reporter.appID,
		TraceSummary: summary,
	})
	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "log",
		AppID: reporter.appID,
		LogEvent: &devdash.LogEvent{
			AppID:     reporter.appID,
			TraceID:   s.traceID,
			SpanID:    s.spanID,
			Level:     temporalLogLevel(err),
			Message:   "temporal operation completed",
			Timestamp: finished,
			Attrs: map[string]any{
				"temporal":           true,
				"temporal_operation": s.operation,
				"temporal_name":      s.name,
				"temporal_error":     errString(err),
			},
		},
	})
}

func temporalSpanRef(value any) *onlavaTemporalSpanRef {
	switch span := value.(type) {
	case *onlavaTemporalSpan:
		if span == nil {
			return nil
		}
		return span.onlavaTemporalSpanRef
	case *onlavaTemporalSpanRef:
		return span
	default:
		return nil
	}
}

func cloneTemporalTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}

func newTemporalTraceID(idempotencyKey, operation, name string) string {
	if strings.TrimSpace(idempotencyKey) != "" {
		return deterministicTemporalID(16, "trace", idempotencyKey, operation, name)
	}
	return newTraceID()
}

func newTemporalSpanID(idempotencyKey, operation, name string) string {
	if strings.TrimSpace(idempotencyKey) != "" {
		return deterministicTemporalID(8, "span", idempotencyKey, operation, name)
	}
	return newSpanID()
}

func deterministicTemporalID(size int, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:size])
}

func temporalTraceType(operation string) string {
	switch strings.TrimSpace(operation) {
	case "StartWorkflow", "SignalWorkflow", "SignalWithStartWorkflow", "QueryWorkflow", "UpdateWorkflow", "UpdateWithStartWorkflow", "CreateSchedule":
		return "TEMPORAL_CLIENT"
	case "RunWorkflow", "HandleSignal", "HandleQuery", "ValidateUpdate", "HandleUpdate":
		return "TEMPORAL_WORKFLOW"
	case "StartActivity":
		return "TEMPORAL_ACTIVITY_SCHEDULE"
	case "RunActivity":
		return "TEMPORAL_ACTIVITY"
	case "StartChildWorkflow":
		return "TEMPORAL_CHILD_WORKFLOW"
	case "StartNexusOperation", "RunCancelNexusOperationHandler", "RunStartNexusOperationHandler":
		return "TEMPORAL_NEXUS"
	default:
		return "TEMPORAL_OPERATION"
	}
}

func temporalLogLevel(err error) string {
	if err != nil {
		return "error"
	}
	return "info"
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func isTemporalTraceID(value string) bool {
	return isTemporalHexID(value, 32)
}

func isTemporalSpanID(value string) bool {
	return isTemporalHexID(value, 16)
}

func isTemporalHexID(value string, size int) bool {
	if len(value) != size || strings.Count(value, "0") == size {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}
