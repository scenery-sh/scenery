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
	"scenery.sh/internal/machine"
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
	// generation is the application generation the span's request was pinned
	// to by the process host; zero outside a process-linked runtime.
	generation uint64
}

type devReporter struct {
	appID       string
	sessionID   string
	appRootHash string
	branch      string
	worktree    string
	url         string
	token       string

	client      *http.Client
	queue       chan []byte
	admissionMu sync.Mutex
	queuedBytes atomic.Int64
	dropped     atomic.Uint64
	done        chan struct{}
	stop        chan struct{}

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
	attrs    []slog.Attr
	groups   []string
}

var reporterMu sync.RWMutex
var globalReporter *devReporter

// reportInternalFailures makes a report token this process mints useful. The
// answer that carries the token withholds the cause, so the cause is logged
// beside the token, where the operator of a deployed process finds it, and
// sent to the development supervisor when one is attached, where
// `scenery inspect report` resolves it.
func reportInternalFailures() {
	machine.SetInternalFailureSink(func(failure machine.InternalFailure) {
		cause := redact.String(failure.Cause)
		slog.Error("internal failure", "report_token", failure.ReportToken, "code", failure.Code, "cause", cause)
		if reporter := activeReporter(); reporter != nil {
			reporter.enqueue(devreport.ReportEnvelope{Type: "internal-failure", InternalFailure: &devreport.InternalFailure{
				ReportToken: failure.ReportToken, Code: failure.Code, Cause: cause, Timestamp: time.Now().UTC(),
			}})
		}
	})
}

func startDevelopmentReporting(cfg AppConfig) func() {
	_ = cfg
	reportInternalFailures()
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
		queue:       make(chan []byte, 1024),
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

// loop sends the reports already queued together, without delaying an isolated
// report to fill a batch. Byte reservations include the one in-flight batch.
func (r *devReporter) loop() {
	defer close(r.done)
	defer func() {
		r.admissionMu.Lock()
		defer r.admissionMu.Unlock()
		r.disabled.Store(true)
		for {
			select {
			case body := <-r.queue:
				r.queuedBytes.Add(-int64(len(body)))
			default:
				return
			}
		}
	}()
	var pending []byte
	defer func() { r.queuedBytes.Add(-int64(len(pending))) }()
	for {
		var first []byte
		if pending != nil {
			first, pending = pending, nil
		} else {
			select {
			case <-r.stop:
				select {
				case first = <-r.queue:
				default:
					return
				}
			case first = <-r.queue:
			}
		}
		batch := [][]byte{first}
		size := len(first) + devreport.BatchWrapperBytes
	drain:
		for len(batch) < devreport.MaxBatchReports {
			select {
			case next := <-r.queue:
				if size+len(next)+1 > devreport.MaxBatchBytes {
					pending = next
					break drain
				}
				batch = append(batch, next)
				size += len(next) + 1
			default:
				break drain
			}
		}
		r.backoffBeforePost()
		dropped := r.dropped.Swap(0)
		err := r.post(batch, dropped)
		for _, body := range batch {
			r.queuedBytes.Add(-int64(len(body)))
		}
		if err != nil {
			r.dropped.Add(dropped + uint64(len(batch)))
			failures := r.failures.Add(1)
			select {
			case <-r.stop:
				return
			default:
			}
			if shouldDisableDevReporting(err) {
				r.disabled.Store(true)
				return
			}
			if failures == 1 {
				_, _ = fmt.Fprintf(osStderr(), "scenery: dev report failed, retrying with backoff: %v\n", err)
			}
			continue
		}
		r.failures.Store(0)
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

func (r *devReporter) post(batch [][]byte, dropped uint64) error {
	// Each record was encoded once at admission, before it entered the byte
	// budget. RawMessage preserves that encoding in the current wire batch.
	reports := make([]json.RawMessage, len(batch))
	for index, body := range batch {
		reports[index] = body
	}
	body, err := json.Marshal(struct {
		Reports []json.RawMessage `json:"reports"`
		Dropped uint64            `json:"dropped,omitempty"`
	}{reports, dropped})
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}

func (r *devReporter) enqueue(env devreport.ReportEnvelope) {
	if r == nil || r.disabled.Load() {
		return
	}
	select {
	case <-r.stop:
		return
	default:
	}
	// Reporting is lossy under overload. Refuse already-full queues before
	// touching caller payloads or allocating their encoding. Admission below
	// still enforces the exact budget when concurrent producers race this check.
	if len(r.queue) == cap(r.queue) || r.queuedBytes.Load() >= devreport.MaxQueuedBytes {
		r.dropped.Add(1)
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
		if env.TraceSummary.Generation != 0 && env.TraceSummary.BuildInputDigest == "" {
			env.TraceSummary.BuildInputDigest = CurrentLinkedContractBundle().BuildInputDigest
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
	body, err := json.Marshal(env)
	if err != nil || len(body) > devreport.MaxEnvelopeBytes {
		r.dropped.Add(1)
		return
	}
	r.admissionMu.Lock()
	defer r.admissionMu.Unlock()
	if r.disabled.Load() {
		return
	}
	select {
	case <-r.stop:
		return
	default:
	}

	for {
		used := r.queuedBytes.Load()
		if used+int64(len(body)) > devreport.MaxQueuedBytes {
			r.dropped.Add(1)
			return
		}
		if r.queuedBytes.CompareAndSwap(used, used+int64(len(body))) {
			break
		}
	}
	select {
	case r.queue <- body:
	default:
		r.queuedBytes.Add(-int64(len(body)))
		r.dropped.Add(1)
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
	r.admissionMu.Lock()
	defer r.admissionMu.Unlock()
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
	result := map[string]any{
		"msg":  redact.String(diagnosticError(err).Error()),
		"code": traceErrorCode(err),
	}
	if wrapped, ok := errs.As(err); ok {
		if delivery, ok := wrapped.Meta["delivery"].(string); ok {
			result["delivery"] = delivery
		}
	}
	return result
}

func (r *devReporter) nextEventID() uint64 {
	return r.eventSeq.Add(1)
}

func (h *reportingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *reportingHandler) Handle(ctx context.Context, record slog.Record) error {
	state := logState(ctx)
	if state != nil && !state.logsEnabled {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, resolvedLogStateKey{}, resolvedLogState{state})
	if err := h.base.Handle(ctx, record); err != nil {
		return err
	}
	if h.reporter == nil {
		return nil
	}
	attrs := make(map[string]any)
	add := func(attr slog.Attr) {
		value := attr.Value
		attrs[attr.Key] = value.Any()
		if value.Kind() != slog.KindAny {
			attrs[attr.Key] = consoleValueString(value)
		}
	}
	for _, attr := range h.attrs {
		add(attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		for _, bound := range bindLogAttrs(h.groups, []slog.Attr{attr}) {
			add(bound)
		}
		return true
	})
	event := &devreport.LogEvent{
		AppID: h.reporter.appID, Level: record.Level.String(), Message: record.Message,
		Attrs: attrs, Timestamp: record.Time,
	}
	if state != nil {
		event.InvocationCorrelation = invocationCorrelation(state)
		event.Generation, event.Service, event.Operation = state.processGeneration, state.request.Service, state.request.Endpoint
		if state.trace != nil {
			event.TraceID, event.SpanID, event.ParentSpanID = state.trace.traceID, state.trace.spanID, state.trace.parentSpanID
		}
	}
	h.reporter.enqueue(devreport.ReportEnvelope{Type: "log", AppID: h.reporter.appID, LogEvent: event})
	return nil
}

func (h *reportingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.base = h.base.WithAttrs(attrs)
	next.attrs = append(append([]slog.Attr(nil), h.attrs...), bindLogAttrs(h.groups, attrs)...)
	return &next
}

func (h *reportingHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	next.base = h.base.WithGroup(name)
	next.groups = append(append([]string(nil), h.groups...), name)
	return &next
}

type tracedRoundTripper struct {
	base     http.RoundTripper
	reporter *devReporter
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

// diagnosticError reports an implementation failure by its cause rather than
// by the neutral message the caller receives.
func diagnosticError(err error) error {
	var transport *ContractTransportError
	if errors.As(err, &transport) && transport.Outcome == "system.internal" && transport.Cause != nil {
		return transport.Cause
	}
	return err
}
