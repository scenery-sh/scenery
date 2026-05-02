package runtime

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pbrazdil/onlava/errs"
	"github.com/pbrazdil/onlava/internal/redact"
	"github.com/pbrazdil/onlava/internal/stdlog"
	"github.com/pbrazdil/onlava/internal/termstyle"
)

const levelTrace = slog.Level(-8)

func init() {
	stdlog.Install(osStderr())
	log.SetFlags(log.LstdFlags)
	// Install the onlava console logger before generated package init code runs.
	slog.SetDefault(slog.New(newOnlavaConsoleHandler(osStderr())))
}

type consoleAttr struct {
	key   string
	value string
}

type onlavaConsoleHandler struct {
	out      io.Writer
	minLevel slog.Level
	palette  termstyle.Palette
	mu       *sync.Mutex
	attrs    []slog.Attr
	groups   []string
}

func newOnlavaConsoleHandler(out io.Writer) slog.Handler {
	return &onlavaConsoleHandler{
		out:      out,
		minLevel: levelTrace,
		palette:  termstyle.New(out),
		mu:       &sync.Mutex{},
	}
}

func (h *onlavaConsoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

func (h *onlavaConsoleHandler) Handle(_ context.Context, record slog.Record) error {
	if state := currentState(); state != nil && !state.logsEnabled {
		return nil
	}
	attrs := h.collectAttrs(record)
	line := h.formatRecord(record, attrs)
	if line == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, line)
	return err
}

func (h *onlavaConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &next
}

func (h *onlavaConsoleHandler) WithGroup(name string) slog.Handler {
	if strings.TrimSpace(name) == "" {
		return h
	}
	next := *h
	next.groups = append(append([]string(nil), h.groups...), name)
	return &next
}

func (h *onlavaConsoleHandler) collectAttrs(record slog.Record) []consoleAttr {
	attrs := make([]consoleAttr, 0, len(h.attrs)+record.NumAttrs())
	for _, attr := range h.attrs {
		h.appendAttr(&attrs, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		h.appendAttr(&attrs, h.groups, attr)
		return true
	})
	return attrs
}

func (h *onlavaConsoleHandler) appendAttr(dst *[]consoleAttr, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		groupName := strings.TrimSpace(attr.Key)
		nextGroups := groups
		if groupName != "" {
			nextGroups = append(append([]string(nil), groups...), groupName)
		}
		for _, item := range attr.Value.Group() {
			h.appendAttr(dst, nextGroups, item)
		}
		return
	}
	key := strings.TrimSpace(attr.Key)
	if key == "" {
		return
	}
	if len(groups) > 0 {
		key = strings.Join(append(append([]string(nil), groups...), key), ".")
	}
	if key == "err" {
		key = "error"
	}
	value := redactedSlogValue(key, attr.Value)
	*dst = append(*dst, consoleAttr{
		key:   key,
		value: consoleValueString(value),
	})
}

func (h *onlavaConsoleHandler) formatRecord(record slog.Record, attrs []consoleAttr) string {
	if record.Message == "onlava secrets missing" {
		return h.formatSecretsWarning(attrs)
	}
	level := h.levelLabel(record.Level)
	message := strings.TrimSpace(strings.TrimPrefix(record.Message, "onlava "))
	if message == "" {
		message = record.Message
	}
	var b strings.Builder
	b.WriteString(record.Time.Local().Format("3:04PM"))
	b.WriteByte(' ')
	b.WriteString(level)
	if message != "" {
		b.WriteByte(' ')
		b.WriteString(message)
	}
	for _, attr := range attrs {
		if attr.value == "" {
			continue
		}
		b.WriteByte(' ')
		b.WriteString(attr.key)
		b.WriteByte('=')
		b.WriteString(attr.value)
	}
	b.WriteByte('\n')
	return b.String()
}

func (h *onlavaConsoleHandler) formatSecretsWarning(attrs []consoleAttr) string {
	var fields []string
	for _, attr := range attrs {
		if attr.key == "fields" {
			fields = splitConsoleList(attr.value)
			break
		}
	}
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(h.palette.Yellow("warning:"))
	b.WriteString(" secrets not defined: ")
	b.WriteString(strings.Join(fields, ", "))
	b.WriteByte('\n')
	b.WriteString(h.palette.Cyan("note:"))
	b.WriteString(" undefined secrets are left empty for local development only.")
	b.WriteByte('\n')
	b.WriteString(h.palette.Dim("see "))
	b.WriteString(h.palette.Dim("https://github.com/pbrazdil/onlava/docs/primitives/secrets"))
	b.WriteString(h.palette.Dim(" for more information"))
	b.WriteByte('\n')
	return b.String()
}

func (h *onlavaConsoleHandler) levelLabel(level slog.Level) string {
	switch {
	case level <= levelTrace:
		return h.palette.Blue("TRC")
	case level < slog.LevelInfo:
		return h.palette.Dim("DBG")
	case level < slog.LevelWarn:
		return h.palette.Yellow("INF")
	case level < slog.LevelError:
		return h.palette.Yellow("WRN")
	default:
		return h.palette.Red("ERR")
	}
}

func consoleValueString(value slog.Value) string {
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindBool:
		if value.Bool() {
			return "true"
		}
		return "false"
	case slog.KindInt64:
		return fmt.Sprintf("%d", value.Int64())
	case slog.KindUint64:
		return fmt.Sprintf("%d", value.Uint64())
	case slog.KindFloat64:
		return fmt.Sprintf("%g", value.Float64())
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().Format(time.RFC3339Nano)
	case slog.KindAny:
		switch v := value.Any().(type) {
		case nil:
			return ""
		case error:
			return v.Error()
		case fmt.Stringer:
			return v.String()
		case []string:
			return strings.Join(v, ",")
		case []any:
			items := make([]string, 0, len(v))
			for _, item := range v {
				items = append(items, fmt.Sprint(item))
			}
			return strings.Join(items, ",")
		default:
			return fmt.Sprint(v)
		}
	default:
		return value.String()
	}
}

func redactedSlogValue(key string, value slog.Value) slog.Value {
	switch value.Kind() {
	case slog.KindGroup:
		items := value.Group()
		redacted := make([]any, 0, len(items))
		for _, item := range items {
			redacted = append(redacted, slog.Attr{
				Key:   item.Key,
				Value: redactedSlogValue(item.Key, item.Value),
			})
		}
		return slog.AnyValue(redacted)
	case slog.KindAny:
		return slog.AnyValue(redact.Value(value.Any()))
	default:
		if redact.SensitiveKey(key) {
			return slog.StringValue(redact.Placeholder)
		}
		return value
	}
}

func splitConsoleList(value string) []string {
	if value == "" {
		return nil
	}
	items := strings.Split(value, ",")
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	slices.Sort(out)
	return out
}

func logTrace(ctx context.Context, msg string, args ...any) {
	slog.Log(ctx, levelTrace, msg, args...)
}

func logRequestStart(state *requestState) {
	if state == nil || !state.logsEnabled || state.trace == nil || !state.trace.isRoot || state.startLogged {
		return
	}
	state.startLogged = true
	args := []any{
		"endpoint", state.request.Endpoint,
		"service", state.request.Service,
		"trace_id", state.trace.traceID,
	}
	if state.auth.UID != "" {
		args = append(args, "uid", state.auth.UID)
	}
	logTrace(context.Background(), "starting request", args...)
}

func logRequestFailure(state *requestState, err error) {
	if state == nil || !state.logsEnabled || state.trace == nil || !state.trace.isRoot || err == nil {
		return
	}
	args := []any{
		"error", err.Error(),
		"code", errs.Code(err),
		"endpoint", state.request.Endpoint,
		"service", state.request.Service,
		"trace_id", state.trace.traceID,
	}
	if state.auth.UID != "" {
		args = append(args, "uid", state.auth.UID)
	}
	slog.Error("request failed", args...)
}

func logRequestCompleted(state *requestState, duration time.Duration, err error) {
	if state == nil || !state.logsEnabled || state.trace == nil || !state.trace.isRoot {
		return
	}
	args := []any{
		"code", errs.Code(err),
		"duration_ms", duration.Milliseconds(),
		"endpoint", state.request.Endpoint,
		"service", state.request.Service,
		"trace_id", state.trace.traceID,
	}
	if state.auth.UID != "" {
		args = append(args, "uid", state.auth.UID)
	}
	logTrace(context.Background(), "request completed", args...)
}

func logAuthHandlerStart(state *requestState, handler *AuthHandler) {
	if state == nil || !state.logsEnabled || state.trace == nil || handler == nil || !logsEnabledForAuthHandler(handler) {
		return
	}
	logTrace(context.Background(), "running auth handler",
		"endpoint", handler.Name,
		"service", handler.Service,
		"trace_id", state.trace.traceID,
	)
}

func logAuthHandlerCompleted(state *requestState, handler *AuthHandler, info AuthInfo, err error, duration time.Duration) {
	if state == nil || !state.logsEnabled || state.trace == nil || handler == nil || !logsEnabledForAuthHandler(handler) {
		return
	}
	args := []any{
		"endpoint", handler.Name,
		"service", handler.Service,
		"trace_id", state.trace.traceID,
		"code", errs.Code(err),
		"duration_ms", duration.Milliseconds(),
	}
	if info.UID != "" {
		args = append(args, "uid", info.UID)
	}
	logTrace(context.Background(), "auth handler completed", args...)
}
