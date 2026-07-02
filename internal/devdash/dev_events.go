package devdash

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const DevEventSchemaVersion = "scenery.dev.event.v1"

func DevEventFromOutput(appID, sessionID string, source DevSource, output []byte, createdAt time.Time) DevEvent {
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	source = normalizeDevSource(source)
	raw := strings.TrimRight(string(output), "\r\n")
	level, message, fields, parse := parseDevOutput(source, raw)
	return DevEvent{
		AppID:     appID,
		SessionID: sessionID,
		Source:    source,
		Level:     level,
		Message:   message,
		Fields:    fields,
		Raw:       raw,
		Parse:     parse,
		CreatedAt: createdAt.UTC(),
	}
}

func NewDevEvent(appID, sessionID string, source DevSource, level, message string, fields map[string]any, createdAt time.Time) DevEvent {
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	fieldsJSON := json.RawMessage(`{}`)
	if len(fields) > 0 {
		if data, err := json.Marshal(fields); err == nil {
			fieldsJSON = data
		}
	}
	source = normalizeDevSource(source)
	level = normalizeDevLevel(level, source.Stream)
	message = strings.TrimSpace(message)
	if message == "" {
		message = level
	}
	return DevEvent{
		AppID:     appID,
		SessionID: sessionID,
		Source:    source,
		Level:     level,
		Message:   message,
		Fields:    fieldsJSON,
		Parse:     DevEventParse{Format: "structured", OK: true},
		CreatedAt: createdAt.UTC(),
	}
}

func normalizeDevSource(source DevSource) DevSource {
	source.ID = strings.TrimSpace(source.ID)
	source.Kind = strings.TrimSpace(source.Kind)
	source.Name = strings.TrimSpace(source.Name)
	source.Role = strings.TrimSpace(source.Role)
	source.PID = strings.TrimSpace(source.PID)
	source.Stream = strings.TrimSpace(source.Stream)
	source.RestartID = strings.TrimSpace(source.RestartID)
	source.Status = strings.TrimSpace(source.Status)
	source.URL = strings.TrimSpace(source.URL)
	source.Reason = strings.TrimSpace(source.Reason)
	if source.ID == "" && source.PID != "" {
		source.ID = "process:" + source.PID
	}
	if source.ID == "" {
		source.ID = "supervisor"
	}
	if source.Kind == "" {
		source.Kind = "process"
	}
	if source.Name == "" {
		source.Name = source.ID
	}
	return source
}

func parseDevOutput(source DevSource, raw string) (string, string, json.RawMessage, DevEventParse) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return normalizeDevLevel("", source.Stream), "", json.RawMessage(`{}`), DevEventParse{Format: "raw", OK: false}
	}
	if fields, level, message, ok := parseDevJSONLog(raw); ok {
		return normalizeDevLevel(level, source.Stream), firstNonEmptyString(message, raw), fields, DevEventParse{Format: "json", OK: true}
	}
	if level, message, fields, ok := parseDevLevelLine(raw); ok {
		return normalizeDevLevel(level, source.Stream), firstNonEmptyString(message, raw), fields, DevEventParse{Format: "level-text", OK: true}
	}
	if fields, level, message, ok := parseDevKeyValueLog(raw); ok {
		return normalizeDevLevel(level, source.Stream), firstNonEmptyString(message, raw), fields, DevEventParse{Format: "keyvalue", OK: true}
	}
	return normalizeDevLevel(inferDevLevel(raw), source.Stream), raw, json.RawMessage(`{}`), DevEventParse{Format: "raw", OK: false}
}

func parseDevJSONLog(raw string) (json.RawMessage, string, string, bool) {
	if !strings.HasPrefix(raw, "{") {
		return nil, "", "", false
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return nil, "", "", false
	}
	level := stringValue(body, "level", "severity", "lvl")
	message := stringValue(body, "message", "msg", "body")
	for _, key := range []string{"time", "ts", "timestamp", "level", "severity", "lvl", "message", "msg", "body"} {
		delete(body, key)
	}
	fields, err := json.Marshal(body)
	if err != nil {
		fields = []byte(`{}`)
	}
	return fields, level, message, true
}

func parseDevKeyValueLog(raw string) (json.RawMessage, string, string, bool) {
	parts := splitDevLogFields(raw)
	if len(parts) < 2 {
		return nil, "", "", false
	}
	values := map[string]any{}
	messageParts := []string{}
	level := ""
	message := ""
	kvCount := 0
	for _, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(key) == "" {
			messageParts = append(messageParts, part)
			continue
		}
		kvCount++
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.ToLower(key) {
		case "level", "severity":
			level = value
		case "msg", "message":
			message = value
		case "time", "ts", "timestamp":
		default:
			values[key] = parseDevValue(value)
		}
	}
	if kvCount == 0 {
		return nil, "", "", false
	}
	if message == "" && len(messageParts) > 0 {
		message = strings.Join(messageParts, " ")
	}
	fields, err := json.Marshal(values)
	if err != nil {
		fields = []byte(`{}`)
	}
	return fields, level, message, true
}

func parseDevLevelLine(raw string) (string, string, json.RawMessage, bool) {
	parts := splitDevLogFields(raw)
	if len(parts) == 0 {
		return "", "", nil, false
	}
	levelIndex := -1
	level := ""
	for i, part := range parts {
		candidate := strings.Trim(part, "[]:")
		if normalized := normalizeExplicitDevLevel(candidate); normalized != "" {
			levelIndex = i
			level = normalized
			break
		}
		if i >= 2 {
			break
		}
	}
	if levelIndex < 0 {
		return "", "", nil, false
	}
	values := map[string]any{}
	messageParts := []string{}
	for _, part := range parts[levelIndex+1:] {
		key, value, ok := strings.Cut(part, "=")
		if ok && strings.TrimSpace(key) != "" {
			values[strings.TrimSpace(key)] = parseDevValue(strings.Trim(strings.TrimSpace(value), `"`))
			continue
		}
		messageParts = append(messageParts, part)
	}
	fields, err := json.Marshal(values)
	if err != nil {
		fields = []byte(`{}`)
	}
	return level, strings.Join(messageParts, " "), fields, true
}

func splitDevLogFields(raw string) []string {
	var out []string
	var b strings.Builder
	quote := rune(0)
	escaped := false
	for _, r := range raw {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && quote != 0:
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func stringValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return fmt.Sprint(value)
		}
	}
	return ""
}

func parseDevValue(value string) any {
	if value == "" {
		return ""
	}
	if b, err := strconv.ParseBool(value); err == nil {
		return b
	}
	if i, err := strconv.ParseInt(value, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return f
	}
	return value
}

func normalizeDevLevel(level, stream string) string {
	if normalized := normalizeExplicitDevLevel(level); normalized != "" {
		return normalized
	}
	if stream == "stderr" {
		return "error"
	}
	return "info"
}

func normalizeExplicitDevLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "dbg", "trace", "trc":
		return "debug"
	case "info", "inf", "information", "notice":
		return "info"
	case "warn", "wrn", "warning":
		return "warn"
	case "error", "err", "eror":
		return "error"
	case "fatal", "panic":
		return "fatal"
	default:
		return ""
	}
}

func inferDevLevel(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "panic") || strings.Contains(lower, "fatal"):
		return "fatal"
	case strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "exception"):
		return "error"
	case strings.Contains(lower, "warn"):
		return "warn"
	default:
		return ""
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
