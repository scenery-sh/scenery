package temporal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"strings"
	"time"

	temporalinterceptor "go.temporal.io/sdk/interceptor"
	temporallog "go.temporal.io/sdk/log"

	sceneryruntime "scenery.sh/runtime"
)

const sceneryTemporalTraceHeader = "scenery-temporal-trace"

var sceneryTemporalSpanContextKey = &struct{ name string }{"scenery-temporal-span"}

type sceneryTemporalTracer struct {
	temporalinterceptor.BaseTracer
	info sceneryruntime.TemporalRuntimeInfo
}

type sceneryTemporalSpanRef struct {
	traceID string
	spanID  string
}

type sceneryTemporalSpan struct {
	*sceneryTemporalSpanRef
	tracer       *sceneryTemporalTracer
	parentSpanID string
	operation    string
	name         string
	tags         map[string]string
	started      time.Time
}

func newSceneryTemporalTracer(info sceneryruntime.TemporalRuntimeInfo) *sceneryTemporalTracer {
	return &sceneryTemporalTracer{info: info}
}

func (t *sceneryTemporalTracer) Options() temporalinterceptor.TracerOptions {
	return temporalinterceptor.TracerOptions{
		SpanContextKey:          sceneryTemporalSpanContextKey,
		HeaderKey:               sceneryTemporalTraceHeader,
		AllowInvalidParentSpans: true,
	}
}

func (t *sceneryTemporalTracer) UnmarshalSpan(data map[string]string) (temporalinterceptor.TracerSpanRef, error) {
	traceID := strings.ToLower(strings.TrimSpace(data["trace_id"]))
	spanID := strings.ToLower(strings.TrimSpace(data["span_id"]))
	if !isTemporalTraceID(traceID) || !isTemporalSpanID(spanID) {
		return nil, nil
	}
	return &sceneryTemporalSpanRef{traceID: traceID, spanID: spanID}, nil
}

func (t *sceneryTemporalTracer) MarshalSpan(span temporalinterceptor.TracerSpan) (map[string]string, error) {
	ref := temporalSpanRef(span)
	if ref == nil || !isTemporalTraceID(ref.traceID) || !isTemporalSpanID(ref.spanID) {
		return nil, nil
	}
	return map[string]string{
		"trace_id": ref.traceID,
		"span_id":  ref.spanID,
	}, nil
}

func (t *sceneryTemporalTracer) SpanFromContext(ctx context.Context) temporalinterceptor.TracerSpan {
	if ctx == nil {
		return nil
	}
	span, _ := ctx.Value(sceneryTemporalSpanContextKey).(*sceneryTemporalSpan)
	return span
}

func (t *sceneryTemporalTracer) ContextWithSpan(ctx context.Context, span temporalinterceptor.TracerSpan) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if span == nil {
		return ctx
	}
	return context.WithValue(ctx, sceneryTemporalSpanContextKey, span)
}

func (t *sceneryTemporalTracer) StartSpan(options *temporalinterceptor.TracerStartSpanOptions) (temporalinterceptor.TracerSpan, error) {
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
	return &sceneryTemporalSpan{
		sceneryTemporalSpanRef: &sceneryTemporalSpanRef{
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

func (t *sceneryTemporalTracer) GetLogger(logger temporallog.Logger, ref temporalinterceptor.TracerSpanRef) temporallog.Logger {
	return logger
}

func (t *sceneryTemporalTracer) SpanName(options *temporalinterceptor.TracerStartSpanOptions) string {
	if options == nil {
		return "temporal.operation"
	}
	if strings.TrimSpace(options.Name) == "" {
		return "temporal." + strings.TrimSpace(options.Operation)
	}
	return "temporal." + strings.TrimSpace(options.Operation) + ":" + strings.TrimSpace(options.Name)
}

func (s *sceneryTemporalSpan) Finish(options *temporalinterceptor.TracerFinishSpanOptions) {
	if s == nil || s.tracer == nil {
		return
	}
	var err error
	if options != nil {
		err = options.Error
	}
	sceneryruntime.ReportTemporalTrace(sceneryruntime.TemporalTraceReport{
		TraceID:      s.traceID,
		SpanID:       s.spanID,
		ParentSpanID: s.parentSpanID,
		Type:         temporalTraceType(s.operation),
		Operation:    s.operation,
		Name:         s.name,
		StartedAt:    s.started,
		Err:          err,
	})
}

func temporalSpanRef(value any) *sceneryTemporalSpanRef {
	switch span := value.(type) {
	case *sceneryTemporalSpan:
		if span == nil {
			return nil
		}
		return span.sceneryTemporalSpanRef
	case *sceneryTemporalSpanRef:
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
	maps.Copy(out, tags)
	return out
}

func newTemporalTraceID(idempotencyKey, operation, name string) string {
	if strings.TrimSpace(idempotencyKey) != "" {
		return deterministicTemporalID(16, "trace", idempotencyKey, operation, name)
	}
	return newRandomHex(16)
}

func newTemporalSpanID(idempotencyKey, operation, name string) string {
	if strings.TrimSpace(idempotencyKey) != "" {
		return deterministicTemporalID(8, "span", idempotencyKey, operation, name)
	}
	return newRandomHex(8)
}

func deterministicTemporalID(size int, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:size])
}

func newRandomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
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
