package rlog

import (
	"context"
	"fmt"
	"log/slog"
)

type Ctx struct {
	attrs []slog.Attr
}

func Debug(msg string, keysAndValues ...any) {
	logWith(slog.Default(), slog.LevelDebug, msg, normalizeAttrs(keysAndValues...)...)
}

func Info(msg string, keysAndValues ...any) {
	logWith(slog.Default(), slog.LevelInfo, msg, normalizeAttrs(keysAndValues...)...)
}

func Warn(msg string, keysAndValues ...any) {
	logWith(slog.Default(), slog.LevelWarn, msg, normalizeAttrs(keysAndValues...)...)
}

func Error(msg string, keysAndValues ...any) {
	logWith(slog.Default(), slog.LevelError, msg, normalizeAttrs(keysAndValues...)...)
}

func With(keysAndValues ...any) Ctx {
	return Ctx{attrs: normalizeAttrs(keysAndValues...)}
}

func (c Ctx) Debug(msg string, keysAndValues ...any) {
	logWith(slog.Default(), slog.LevelDebug, msg, appendAttrs(c.attrs, normalizeAttrs(keysAndValues...)...)...)
}

func (c Ctx) Info(msg string, keysAndValues ...any) {
	logWith(slog.Default(), slog.LevelInfo, msg, appendAttrs(c.attrs, normalizeAttrs(keysAndValues...)...)...)
}

func (c Ctx) Warn(msg string, keysAndValues ...any) {
	logWith(slog.Default(), slog.LevelWarn, msg, appendAttrs(c.attrs, normalizeAttrs(keysAndValues...)...)...)
}

func (c Ctx) Error(msg string, keysAndValues ...any) {
	logWith(slog.Default(), slog.LevelError, msg, appendAttrs(c.attrs, normalizeAttrs(keysAndValues...)...)...)
}

func (c Ctx) With(keysAndValues ...any) Ctx {
	return Ctx{attrs: appendAttrs(c.attrs, normalizeAttrs(keysAndValues...)...)}
}

func logWith(logger *slog.Logger, level slog.Level, msg string, attrs ...slog.Attr) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.LogAttrs(context.Background(), level, msg, attrs...)
}

func normalizeAttrs(keysAndValues ...any) []slog.Attr {
	if len(keysAndValues) == 0 {
		return nil
	}
	attrs := make([]slog.Attr, 0, len(keysAndValues))
	for i := 0; i < len(keysAndValues); i++ {
		if attr, ok := keysAndValues[i].(slog.Attr); ok {
			attrs = append(attrs, attr)
			continue
		}

		key := fmt.Sprint(keysAndValues[i])
		var value any
		if i+1 < len(keysAndValues) {
			value = keysAndValues[i+1]
			i++
		}
		attrs = append(attrs, slog.Any(key, value))
	}
	return attrs
}

func appendAttrs(existing []slog.Attr, extra ...slog.Attr) []slog.Attr {
	if len(existing) == 0 {
		return append([]slog.Attr(nil), extra...)
	}
	if len(extra) == 0 {
		return append([]slog.Attr(nil), existing...)
	}
	out := make([]slog.Attr, 0, len(existing)+len(extra))
	out = append(out, existing...)
	out = append(out, extra...)
	return out
}
