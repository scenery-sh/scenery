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
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/devreport"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/redact"
	"scenery.sh/runtime/shared"
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
	appID       string
	sessionID   string
	appRootHash string
	branch      string
	worktree    string
	url         string
	token       string

	client *http.Client
	queue  chan devreport.ReportEnvelope
	done   chan struct{}
	stop   chan struct{}

	eventSeq atomic.Uint64
	disabled atomic.Bool
	failures atomic.Uint64

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
	url := stringsTrim(osGetenv("SCENERY_DEV_REPORT_URL"))
	token := stringsTrim(osGetenv("SCENERY_DEV_REPORT_TOKEN"))
	appID := stringsTrim(osGetenv("SCENERY_APP_ID"))
	sessionID := stringsTrim(osGetenv("SCENERY_SESSION_ID"))
	appRootHash := stringsTrim(osGetenv("SCENERY_APP_ROOT_HASH"))
	branch := stringsTrim(osGetenv("SCENERY_BRANCH"))
	worktree := stringsTrim(osGetenv("SCENERY_WORKTREE"))
	if url == "" || token == "" || appID == "" {
		return func() {}
	}

	reporter := &devReporter{
		appID:       appID,
		sessionID:   sessionID,
		appRootHash: appRootHash,
		branch:      branch,
		worktree:    worktree,
		url:         url,
		token:       token,
		client:      &http.Client{Timeout: 2 * time.Second},
		queue:       make(chan devreport.ReportEnvelope, 1024),
		done:        make(chan struct{}),
		stop:        make(chan struct{}),
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
	return newSceneryConsoleHandler(osStderr())
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
			r.backoffBeforePost()
			if err := r.post(env); err != nil {
				failures := r.failures.Add(1)
				if shouldDisableDevReporting(err) {
					r.disabled.Store(true)
					return
				}
				if failures == 1 {
					fmt.Fprintf(osStderr(), "scenery: dev report failed, retrying with backoff: %v\n", err)
				}
				continue
			}
			r.failures.Store(0)
		}
	}
}

func (r *devReporter) backoffBeforePost() {
	failures := r.failures.Load()
	if failures == 0 {
		return
	}
	delay := devReportBackoffDelay(failures)
	if delay <= 0 {
		return
	}
	select {
	case <-r.stop:
	case <-time.After(delay):
	}
}

func devReportBackoffDelay(failures uint64) time.Duration {
	if failures == 0 {
		return 0
	}
	delay := time.Duration(1<<(minUint64(failures, 6)-1)) * 100 * time.Millisecond
	if delay > 2*time.Second {
		return 2 * time.Second
	}
	return delay
}

func (r *devReporter) post(env devreport.ReportEnvelope) error {
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

func (r *devReporter) enqueue(env devreport.ReportEnvelope) {
	if r == nil || r.disabled.Load() {
		return
	}
	if env.AppID == "" {
		env.AppID = r.appID
	}
	if env.SessionID == "" {
		env.SessionID = r.sessionID
	}
	if env.AppRootHash == "" {
		env.AppRootHash = r.appRootHash
	}
	if env.Branch == "" {
		env.Branch = r.branch
	}
	if env.Worktree == "" {
		env.Worktree = r.worktree
	}
	if env.ReporterPID == 0 {
		env.ReporterPID = os.Getpid()
	}
	if env.TraceSummary != nil {
		if env.TraceSummary.AppID == "" {
			env.TraceSummary.AppID = env.AppID
		}
		if env.TraceSummary.SessionID == "" {
			env.TraceSummary.SessionID = env.SessionID
		}
		if env.TraceSummary.AppRootHash == "" {
			env.TraceSummary.AppRootHash = env.AppRootHash
		}
		if env.TraceSummary.Branch == "" {
			env.TraceSummary.Branch = env.Branch
		}
		if env.TraceSummary.Worktree == "" {
			env.TraceSummary.Worktree = env.Worktree
		}
	}
	if env.TraceEvent != nil {
		if env.TraceEvent.AppID == "" {
			env.TraceEvent.AppID = env.AppID
		}
		if env.TraceEvent.SessionID == "" {
			env.TraceEvent.SessionID = env.SessionID
		}
		if env.TraceEvent.AppRootHash == "" {
			env.TraceEvent.AppRootHash = env.AppRootHash
		}
		if env.TraceEvent.Branch == "" {
			env.TraceEvent.Branch = env.Branch
		}
		if env.TraceEvent.Worktree == "" {
			env.TraceEvent.Worktree = env.Worktree
		}
	}
	if env.LogEvent != nil {
		if env.LogEvent.AppID == "" {
			env.LogEvent.AppID = env.AppID
		}
		if env.LogEvent.SessionID == "" {
			env.LogEvent.SessionID = env.SessionID
		}
		if env.LogEvent.AppRootHash == "" {
			env.LogEvent.AppRootHash = env.AppRootHash
		}
		if env.LogEvent.Branch == "" {
			env.LogEvent.Branch = env.Branch
		}
		if env.LogEvent.Worktree == "" {
			env.LogEvent.Worktree = env.Worktree
		}
	}
	select {
	case <-r.stop:
		return
	case r.queue <- env:
	default:
		// Keep the app responsive if the dashboard falls behind.
	}
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
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
	if errno, ok := errors.AsType[syscall.Errno](err); ok && (errno == syscall.ECONNREFUSED || errno == syscall.EPIPE || errno == syscall.ECONNRESET) {
		return true
	}
	_, ok := errors.AsType[*net.OpError](err)
	return ok
}

func startRequestTrace(state *requestState) {
	if state == nil || state.trace != nil || (!state.logsEnabled && !state.traceEnabled) {
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
	state.request.TraceID = span.traceID
	reporter := activeReporter()
	if reporter == nil || !state.traceEnabled {
		return
	}
	reporter.enqueue(devreport.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devreport.TraceEvent{
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
	span := state.trace
	duration := time.Since(span.started)
	if span.isRoot && err != nil {
		logRequestFailure(state, err)
	}
	if span.isRoot {
		logRequestCompleted(state, duration, err)
	}
	reporter := activeReporter()
	if reporter == nil || !state.traceEnabled {
		state.trace = nil
		return
	}
	endpointName := optionalString(span.endpoint)
	reporter.enqueue(devreport.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devreport.TraceEvent{
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
	summary := &devreport.TraceSummary{
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
	reporter.enqueue(devreport.ReportEnvelope{
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
	authLogsEnabled := logsEnabledForAuthHandler(handler)
	authTraceEnabled := traceEnabledForAuthHandler(handler)
	if !authLogsEnabled && !authTraceEnabled {
		return invoke(ctx)
	}
	child := &traceSpan{
		traceID:      parent.trace.traceID,
		spanID:       newSpanID(),
		parentSpanID: parent.trace.spanID,
		spanType:     "AUTH",
		service:      handler.Service,
		endpoint:     handler.Name,
		started:      time.Now().UTC(),
		requestType:  shared.InternalCall,
	}
	clone := *parent
	clone.trace = child
	clone.logsEnabled = authLogsEnabled
	clone.traceEnabled = authTraceEnabled
	callCtx := withState(ctx, &clone)
	restore := enterState(&clone)
	defer restore()
	logAuthHandlerStart(&clone, handler)

	reporter := activeReporter()
	if reporter != nil && authTraceEnabled {
		reporter.enqueue(devreport.ReportEnvelope{
			Type:  "trace-event",
			AppID: reporter.appID,
			TraceEvent: &devreport.TraceEvent{
				TraceID:   child.traceID,
				SpanID:    child.spanID,
				EventID:   reporter.nextEventID(),
				EventTime: child.started,
				Event: map[string]any{
					"span_start": map[string]any{
						"auth": map[string]any{
							"service_name":  handler.Service,
							"endpoint_name": handler.Name,
						},
					},
				},
			},
		})
	}

	info, err := invoke(callCtx)
	logAuthHandlerCompleted(&clone, handler, info, err, time.Since(child.started))
	if reporter != nil && authTraceEnabled {
		reporter.enqueue(devreport.ReportEnvelope{
			Type:  "trace-event",
			AppID: reporter.appID,
			TraceEvent: &devreport.TraceEvent{
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
							"endpoint_name": handler.Name,
							"uid":           info.UID,
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
	}
	return info, err
}

func startInternalCallTrace(parent *requestState, child *requestState) {
	if parent == nil || parent.trace == nil || child == nil || (!parent.logsEnabled && !parent.traceEnabled) || (!child.logsEnabled && !child.traceEnabled) {
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
	if reporter == nil || !child.traceEnabled {
		return
	}
	reporter.enqueue(devreport.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devreport.TraceEvent{
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
	if state == nil || state.trace == nil || !state.traceEnabled {
		return
	}
	reporter := activeReporter()
	if reporter == nil {
		return
	}
	reporter.enqueue(devreport.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devreport.TraceEvent{
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
	reporter.enqueue(devreport.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devreport.TraceEvent{
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
	reporter.enqueue(devreport.ReportEnvelope{
		Type:  "trace-event",
		AppID: reporter.appID,
		TraceEvent: &devreport.TraceEvent{
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
	reporter.enqueue(devreport.ReportEnvelope{
		Type:  "trace-summary",
		AppID: reporter.appID,
		TraceSummary: &devreport.TraceSummary{
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
	err = contractDiagnosticError(err)
	return map[string]any{
		"msg": redact.String(err.Error()),
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
	return redact.Headers(headers)
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
	if state := currentState(); state != nil && !state.logsEnabled {
		return nil
	}
	if err := h.base.Handle(ctx, record); err != nil {
		return err
	}
	if h.reporter == nil {
		return nil
	}
	attrs := make(map[string]any)
	record.Attrs(func(attr slog.Attr) bool {
		value := redactedSlogValue(attr.Key, attr.Value.Resolve())
		attrs[attr.Key] = value.Any()
		if value.Kind() != slog.KindAny {
			attrs[attr.Key] = consoleValueString(value)
		}
		return true
	})
	traceID := ""
	spanID := ""
	if state := currentState(); state != nil && state.trace != nil {
		traceID = state.trace.traceID
		spanID = state.trace.spanID
	}
	h.reporter.enqueue(devreport.ReportEnvelope{
		Type:  "log",
		AppID: h.reporter.appID,
		LogEvent: &devreport.LogEvent{
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
	if state != nil && state.trace != nil && state.traceEnabled && t.reporter != nil {
		traceID = state.trace.traceID
		spanID = state.trace.spanID
		start = time.Now().UTC()
		t.reporter.enqueue(devreport.ReportEnvelope{
			Type:  "trace-event",
			AppID: t.reporter.appID,
			TraceEvent: &devreport.TraceEvent{
				TraceID:   traceID,
				SpanID:    spanID,
				EventID:   t.reporter.nextEventID(),
				EventTime: start,
				Event: map[string]any{
					"span_event": map[string]any{
						"http_call_start": map[string]any{
							"method": req.Method,
							"url":    redactURL(req.URL),
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
			end["err"] = map[string]any{"msg": redact.String(err.Error())}
		}
		t.reporter.enqueue(devreport.ReportEnvelope{
			Type:  "trace-event",
			AppID: t.reporter.appID,
			TraceEvent: &devreport.TraceEvent{
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

func redactURL(value *url.URL) string {
	if value == nil {
		return ""
	}
	if redacted, ok := redact.URL(value.String()); ok {
		return redacted
	}
	return value.String()
}

// Small wrappers keep this file testable without making os/strings/bytes direct globals in tests.
var (
	bytesReader = func(data []byte) io.Reader { return bytes.NewReader(data) }
	osGetenv    = envpolicy.Get
	osStderr    = func() io.Writer { return os.Stderr }
	stringsTrim = strings.TrimSpace
)
