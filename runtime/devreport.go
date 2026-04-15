package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"pulse.dev/errs"
	"pulse.dev/internal/devdash"
	"pulse.dev/runtime/shared"
)

type traceSpan struct {
	traceID      string
	spanID       string
	parentSpanID string
	spanType     string
	service      string
	endpoint     string
	started      time.Time
	requestType  shared.RequestType
	isRoot       bool
}

type devReporter struct {
	appID string
	url   string
	token string

	client *http.Client
	queue  chan devdash.ReportEnvelope
	done   chan struct{}
	stop   chan struct{}

	eventSeq atomic.Uint64
	disabled atomic.Bool

	prevLogger           *slog.Logger
	prevDefaultTransport http.RoundTripper
	prevClientTransport  http.RoundTripper
}

type reportingHandler struct {
	base     slog.Handler
	reporter *devReporter
}

var reporterMu sync.RWMutex
var globalReporter *devReporter

func startDevelopmentReporting(cfg AppConfig) func() {
	_ = cfg
	url := stringsTrim(osGetenv("PULSE_DEV_REPORT_URL"))
	token := stringsTrim(osGetenv("PULSE_DEV_REPORT_TOKEN"))
	appID := stringsTrim(osGetenv("PULSE_APP_ID"))
	if url == "" || token == "" || appID == "" {
		return func() {}
	}

	reporter := &devReporter{
		appID:  appID,
		url:    url,
		token:  token,
		client: &http.Client{Timeout: 2 * time.Second},
		queue:  make(chan devdash.ReportEnvelope, 1024),
		done:   make(chan struct{}),
		stop:   make(chan struct{}),
	}

	reporter.prevLogger = slog.Default()
	baseHandler := reportingBaseHandler(reporter.prevLogger.Handler())
	slog.SetDefault(slog.New(&reportingHandler{
		base:     baseHandler,
		reporter: reporter,
	}))

	reporter.prevDefaultTransport = http.DefaultTransport
	reporter.prevClientTransport = http.DefaultClient.Transport
	http.DefaultTransport = &tracedRoundTripper{
		base:     reporter.prevDefaultTransport,
		reporter: reporter,
	}
	http.DefaultClient.Transport = http.DefaultTransport

	reporterMu.Lock()
	globalReporter = reporter
	reporterMu.Unlock()

	go reporter.loop()
	return func() {
		reporterMu.Lock()
		if globalReporter == reporter {
			globalReporter = nil
		}
		reporterMu.Unlock()
		reporter.disabled.Store(true)
		slog.SetDefault(reporter.prevLogger)
		http.DefaultTransport = reporter.prevDefaultTransport
		http.DefaultClient.Transport = reporter.prevClientTransport
		reporter.stopLoop()
		<-reporter.done
	}
}

func reportingBaseHandler(prev slog.Handler) slog.Handler {
	_ = prev
	return newPulseConsoleHandler(osStderr())
}

func activeReporter() *devReporter {
	reporterMu.RLock()
	defer reporterMu.RUnlock()
	return globalReporter
}

func (r *devReporter) loop() {
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return
		case env := <-r.queue:
			if r.disabled.Load() {
				continue
			}
			if err := r.post(env); err != nil {
				if shouldDisableDevReporting(err) {
					r.disabled.Store(true)
					return
				}
				fmt.Fprintf(osStderr(), "pulse: dev report failed: %v\n", err)
			}
		}
	}
}

func (r *devReporter) post(env devdash.ReportEnvelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, r.url, bytesReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}

func (r *devReporter) enqueue(env devdash.ReportEnvelope) {
	if r == nil || r.disabled.Load() {
		return
	}
	select {
	case <-r.stop:
		return
	case r.queue <- env:
	default:
		// Keep the app responsive if the dashboard falls behind.
	}
}

func (r *devReporter) stopLoop() {
	if r == nil {
		return
	}
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
}

func shouldDisableDevReporting(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && (errno == syscall.ECONNREFUSED || errno == syscall.EPIPE || errno == syscall.ECONNRESET) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	return false
}

func startRequestTrace(state *requestState) {
	if state == nil || state.trace != nil {
		return
	}
	reporter := activeReporter()
	if reporter == nil {
		return
	}
	span := &traceSpan{
		traceID:     newTraceID(),
		spanID:      newSpanID(),
		spanType:    spanTypeForRequest(state.request.Type),
		service:     state.request.Service,
		endpoint:    state.request.Endpoint,
		started:     state.request.Started,
		requestType: state.request.Type,
		isRoot:      true,
	}
	if span.started.IsZero() {
		span.started = time.Now().UTC()
		state.request.Started = span.started
	}
	state.trace = span
	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devdash.TraceEvent{
			TraceID:   span.traceID,
			SpanID:    span.spanID,
			EventID:   reporter.nextEventID(),
			EventTime: span.started,
			Event: map[string]any{
				"span_start": map[string]any{
					"request": map[string]any{
						"service_name":    state.request.Service,
						"endpoint_name":   state.request.Endpoint,
						"http_method":     state.request.Method,
						"path":            state.request.Path,
						"path_params":     tracePathParams(state.request.PathParams),
						"request_headers": flattenHeaders(state.request.Headers),
						"uid":             state.auth.UID,
						"mocked":          false,
					},
				},
			},
		},
	})
}

func finishRequestTrace(state *requestState, httpStatus int, payload any, err error) {
	if state == nil || state.trace == nil {
		return
	}
	reporter := activeReporter()
	if reporter == nil {
		return
	}
	span := state.trace
	duration := time.Since(span.started)
	if span.isRoot && err != nil {
		logRequestFailure(state, err)
	}
	if span.isRoot {
		logRequestCompleted(state, duration, err)
	}
	endpointName := optionalString(span.endpoint)
	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devdash.TraceEvent{
			TraceID:   span.traceID,
			SpanID:    span.spanID,
			EventID:   reporter.nextEventID(),
			EventTime: time.Now().UTC(),
			Event: map[string]any{
				"span_end": map[string]any{
					"duration_nanos": uint64(duration),
					"status_code":    statusCodeName(err),
					"request": map[string]any{
						"service_name":     span.service,
						"endpoint_name":    span.endpoint,
						"http_status_code": httpStatus,
						"uid":              state.auth.UID,
					},
					"error": traceError(err),
				},
			},
		},
	})
	summary := &devdash.TraceSummary{
		AppID:         reporter.appID,
		TraceID:       span.traceID,
		SpanID:        span.spanID,
		Type:          span.spanType,
		IsRoot:        span.isRoot,
		IsError:       err != nil,
		StartedAt:     span.started,
		DurationNanos: uint64(duration),
		ServiceName:   span.service,
		EndpointName:  endpointName,
	}
	if span.parentSpanID != "" {
		summary.ParentSpanID = &span.parentSpanID
	}
	reporter.enqueue(devdash.ReportEnvelope{
		Type:         "trace-summary",
		AppID:        reporter.appID,
		TraceSummary: summary,
	})
	state.trace = nil
}

func traceAuthCall(ctx context.Context, handler *AuthHandler, invoke func(context.Context) (AuthInfo, error)) (AuthInfo, error) {
	parent := stateFromContext(ctx)
	if parent == nil || parent.trace == nil {
		return invoke(ctx)
	}
	reporter := activeReporter()
	if reporter == nil {
		return invoke(ctx)
	}
	child := &traceSpan{
		traceID:      parent.trace.traceID,
		spanID:       newSpanID(),
		parentSpanID: parent.trace.spanID,
		spanType:     "AUTH",
		service:      handler.Service,
		endpoint:     parent.request.Endpoint,
		started:      time.Now().UTC(),
		requestType:  shared.InternalCall,
	}
	clone := *parent
	clone.trace = child
	callCtx := withState(ctx, &clone)
	restore := enterState(&clone)
	defer restore()
	logAuthHandlerStart(&clone, handler)

	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devdash.TraceEvent{
			TraceID:   child.traceID,
			SpanID:    child.spanID,
			EventID:   reporter.nextEventID(),
			EventTime: child.started,
			Event: map[string]any{
				"span_start": map[string]any{
					"auth": map[string]any{
						"service_name":  handler.Service,
						"endpoint_name": parent.request.Endpoint,
					},
				},
			},
		},
	})

	info, err := invoke(callCtx)
	logAuthHandlerCompleted(&clone, handler, info, err, time.Since(child.started))
	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devdash.TraceEvent{
			TraceID:   child.traceID,
			SpanID:    child.spanID,
			EventID:   reporter.nextEventID(),
			EventTime: time.Now().UTC(),
			Event: map[string]any{
				"span_end": map[string]any{
					"duration_nanos": uint64(time.Since(child.started)),
					"status_code":    statusCodeName(err),
					"auth": map[string]any{
						"service_name":  handler.Service,
						"endpoint_name": parent.request.Endpoint,
						"uid":           info.UID,
					},
					"error": traceError(err),
				},
			},
		},
	})
	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-summary",
		AppID: reporter.appID,
		TraceSummary: &devdash.TraceSummary{
			AppID:         reporter.appID,
			TraceID:       child.traceID,
			SpanID:        child.spanID,
			Type:          child.spanType,
			IsRoot:        false,
			IsError:       err != nil,
			StartedAt:     child.started,
			DurationNanos: uint64(time.Since(child.started)),
			ServiceName:   child.service,
			EndpointName:  optionalString(child.endpoint),
			ParentSpanID:  optionalString(child.parentSpanID),
		},
	})
	return info, err
}

func startInternalCallTrace(parent *requestState, child *requestState) {
	if parent == nil || parent.trace == nil || child == nil {
		return
	}
	child.trace = &traceSpan{
		traceID:      parent.trace.traceID,
		spanID:       newSpanID(),
		parentSpanID: parent.trace.spanID,
		spanType:     "REQUEST",
		service:      child.request.Service,
		endpoint:     child.request.Endpoint,
		started:      child.request.Started,
		requestType:  child.request.Type,
	}
	reporter := activeReporter()
	if reporter == nil {
		return
	}
	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devdash.TraceEvent{
			TraceID:   child.trace.traceID,
			SpanID:    child.trace.spanID,
			EventID:   reporter.nextEventID(),
			EventTime: child.trace.started,
			Event: map[string]any{
				"span_start": map[string]any{
					"request": map[string]any{
						"service_name":  child.request.Service,
						"endpoint_name": child.request.Endpoint,
						"http_method":   child.request.Method,
						"path":          child.request.Path,
						"path_params":   tracePathParams(child.request.PathParams),
						"uid":           child.auth.UID,
						"mocked":        false,
					},
				},
			},
		},
	})
}

func recordMiddlewareEvent(name, phase string, err error) {
	state := currentState()
	if state == nil || state.trace == nil {
		return
	}
	reporter := activeReporter()
	if reporter == nil {
		return
	}
	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devdash.TraceEvent{
			TraceID:   state.trace.traceID,
			SpanID:    state.trace.spanID,
			EventID:   reporter.nextEventID(),
			EventTime: time.Now().UTC(),
			Event: map[string]any{
				"span_event": map[string]any{
					"log_message": map[string]any{
						"level": "INFO",
						"msg":   "middleware " + phase,
						"fields": []map[string]any{
							traceField("middleware", name),
							traceField("phase", phase),
							traceField("error", errorString(err)),
						},
					},
				},
			},
		},
	})
}

func recordServiceInit(service string, duration time.Duration, err error) {
	reporter := activeReporter()
	if reporter == nil {
		return
	}
	traceID := newTraceID()
	spanID := newSpanID()
	now := time.Now().UTC()
	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devdash.TraceEvent{
			TraceID:   traceID,
			SpanID:    spanID,
			EventID:   reporter.nextEventID(),
			EventTime: now.Add(-duration),
			Event: map[string]any{
				"span_event": map[string]any{
					"service_init_start": map[string]any{
						"service": service,
					},
				},
			},
		},
	})
	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devdash.TraceEvent{
			TraceID:   traceID,
			SpanID:    spanID,
			EventID:   reporter.nextEventID(),
			EventTime: now,
			Event: map[string]any{
				"span_event": map[string]any{
					"service_init_end": map[string]any{
						"error": traceError(err),
					},
				},
			},
		},
	})
	reporter.enqueue(devdash.ReportEnvelope{
		Type:  "trace-summary",
		AppID: reporter.appID,
		TraceSummary: &devdash.TraceSummary{
			AppID:         reporter.appID,
			TraceID:       traceID,
			SpanID:        spanID,
			Type:          "REQUEST",
			IsRoot:        true,
			IsError:       err != nil,
			StartedAt:     now.Add(-duration),
			DurationNanos: uint64(duration),
			ServiceName:   service,
			EndpointName:  optionalString("init"),
		},
	})
}

func newTraceID() string {
	return newRandomHex(16)
}

func newSpanID() string {
	return newRandomHex(8)
}

func newRandomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func spanTypeForRequest(requestType shared.RequestType) string {
	switch requestType {
	case shared.RawAPICall, shared.APICall, shared.InternalCall:
		return "REQUEST"
	default:
		return "REQUEST"
	}
}

func statusCodeName(err error) string {
	switch errs.Code(err) {
	case errs.OK:
		return "STATUS_CODE_OK"
	case errs.Canceled:
		return "STATUS_CODE_CANCELED"
	case errs.InvalidArgument:
		return "STATUS_CODE_INVALID_ARGUMENT"
	case errs.DeadlineExceeded:
		return "STATUS_CODE_DEADLINE_EXCEEDED"
	case errs.NotFound:
		return "STATUS_CODE_NOT_FOUND"
	case errs.AlreadyExists:
		return "STATUS_CODE_ALREADY_EXISTS"
	case errs.PermissionDenied:
		return "STATUS_CODE_PERMISSION_DENIED"
	case errs.ResourceExhausted:
		return "STATUS_CODE_RESOURCE_EXHAUSTED"
	case errs.FailedPrecondition:
		return "STATUS_CODE_FAILED_PRECONDITION"
	case errs.Aborted, errs.Conflict:
		return "STATUS_CODE_ABORTED"
	case errs.OutOfRange:
		return "STATUS_CODE_OUT_OF_RANGE"
	case errs.Unimplemented:
		return "STATUS_CODE_UNIMPLEMENTED"
	case errs.Unavailable:
		return "STATUS_CODE_UNAVAILABLE"
	case errs.DataLoss:
		return "STATUS_CODE_DATA_LOSS"
	case errs.Unauthenticated:
		return "STATUS_CODE_UNAUTHENTICATED"
	default:
		return "STATUS_CODE_INTERNAL"
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func traceError(err error) any {
	if err == nil {
		return nil
	}
	return map[string]any{
		"msg": err.Error(),
	}
}

func tracePathParams(params shared.PathParams) []string {
	items := make([]string, 0, len(params))
	for _, param := range params {
		items = append(items, param.Value)
	}
	return items
}

func flattenHeaders(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		out[key] = stringsJoin(values, ", ")
	}
	return out
}

func traceField(key string, value any) map[string]any {
	switch value := value.(type) {
	case string:
		return map[string]any{"key": key, "str": value}
	case bool:
		return map[string]any{"key": key, "bool": value}
	case int:
		return map[string]any{"key": key, "int": value}
	case uint64:
		return map[string]any{"key": key, "uint": value}
	default:
		if value == nil {
			return map[string]any{"key": key, "str": ""}
		}
		return map[string]any{"key": key, "str": fmt.Sprint(value)}
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (r *devReporter) nextEventID() uint64 {
	return r.eventSeq.Add(1)
}

func (h *reportingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *reportingHandler) Handle(ctx context.Context, record slog.Record) error {
	if err := h.base.Handle(ctx, record); err != nil {
		return err
	}
	if h.reporter == nil {
		return nil
	}
	attrs := make(map[string]any)
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	traceID := ""
	spanID := ""
	if state := currentState(); state != nil && state.trace != nil {
		traceID = state.trace.traceID
		spanID = state.trace.spanID
	}
	h.reporter.enqueue(devdash.ReportEnvelope{
		Type:  "log",
		AppID: h.reporter.appID,
		LogEvent: &devdash.LogEvent{
			AppID:     h.reporter.appID,
			TraceID:   traceID,
			SpanID:    spanID,
			Level:     record.Level.String(),
			Message:   record.Message,
			Attrs:     attrs,
			Timestamp: record.Time,
		},
	})
	return nil
}

func (h *reportingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &reportingHandler{base: h.base.WithAttrs(attrs), reporter: h.reporter}
}

func (h *reportingHandler) WithGroup(name string) slog.Handler {
	return &reportingHandler{base: h.base.WithGroup(name), reporter: h.reporter}
}

type tracedRoundTripper struct {
	base     http.RoundTripper
	reporter *devReporter
}

func (t *tracedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	state := currentState()
	var traceID, spanID string
	var start time.Time
	if state != nil && state.trace != nil && t.reporter != nil {
		traceID = state.trace.traceID
		spanID = state.trace.spanID
		start = time.Now().UTC()
		t.reporter.enqueue(devdash.ReportEnvelope{
			Type:  "trace-event",
			AppID: t.reporter.appID,
			TraceEvent: &devdash.TraceEvent{
				TraceID:   traceID,
				SpanID:    spanID,
				EventID:   t.reporter.nextEventID(),
				EventTime: start,
				Event: map[string]any{
					"span_event": map[string]any{
						"http_call_start": map[string]any{
							"method": req.Method,
							"url":    req.URL.String(),
						},
					},
				},
			},
		})
	}
	resp, err := base.RoundTrip(req)
	if traceID != "" && t.reporter != nil {
		end := map[string]any{}
		if resp != nil {
			end["status_code"] = resp.StatusCode
		}
		if err != nil {
			end["err"] = map[string]any{"msg": err.Error()}
		}
		t.reporter.enqueue(devdash.ReportEnvelope{
			Type:  "trace-event",
			AppID: t.reporter.appID,
			TraceEvent: &devdash.TraceEvent{
				TraceID:   traceID,
				SpanID:    spanID,
				EventID:   t.reporter.nextEventID(),
				EventTime: time.Now().UTC(),
				Event: map[string]any{
					"span_event": map[string]any{
						"http_call_end": end,
					},
				},
			},
		})
	}
	return resp, err
}

// Small wrappers keep this file testable without making os/strings/bytes direct globals in tests.
var (
	bytesReader = func(data []byte) io.Reader { return bytes.NewReader(data) }
	osGetenv    = os.Getenv
	osStderr    = func() io.Writer { return os.Stderr }
	stringsJoin = strings.Join
	stringsTrim = strings.TrimSpace
)
