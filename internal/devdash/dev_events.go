package devdash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const DevEventSchemaVersion = "onlava.dev.event.v1"

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

func (s *Store) UpsertDevSource(ctx context.Context, appID, sessionID string, source DevSource) error {
	if s == nil || s.db == nil {
		return errors.New("devdash store is nil")
	}
	source = normalizeDevSource(source)
	if appID == "" || source.ID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		insert into dev_sources (app_id, session_id, source_id, kind, name, role, pid, status, restart_id, url, reason, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(app_id, session_id, source_id) do update set
			kind=excluded.kind,
			name=excluded.name,
			role=excluded.role,
			pid=excluded.pid,
			status=excluded.status,
			restart_id=excluded.restart_id,
			url=excluded.url,
			reason=excluded.reason,
			updated_at=excluded.updated_at
	`, appID, sessionID, source.ID, source.Kind, source.Name, source.Role, source.PID, source.Status, source.RestartID, source.URL, source.Reason, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) WriteDevEvent(ctx context.Context, event DevEvent) error {
	_, err := s.WriteDevEventReturningID(ctx, event)
	return err
}

func (s *Store) WriteDevEventReturningID(ctx context.Context, event DevEvent) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("devdash store is nil")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.Source = normalizeDevSource(event.Source)
	event.Level = normalizeDevLevel(event.Level, event.Source.Stream)
	event.Message = strings.TrimSpace(event.Message)
	if event.Message == "" {
		event.Message = strings.TrimSpace(event.Raw)
	}
	if event.Message == "" {
		event.Message = event.Level
	}
	if len(event.Fields) == 0 || !json.Valid(event.Fields) {
		event.Fields = json.RawMessage(`{}`)
	}
	if event.Parse.Format == "" {
		event.Parse.Format = "raw"
	}
	if err := s.UpsertDevSource(ctx, event.AppID, event.SessionID, event.Source); err != nil {
		return 0, err
	}
	parseOK := 0
	if event.Parse.OK {
		parseOK = 1
	}
	res, err := s.db.ExecContext(ctx, `
		insert into dev_events (
			app_id, session_id, source_id, source_kind, source_name, source_role, source_pid, source_stream,
			source_restart_id, source_status, source_url, source_reason, level, message, fields_json, raw_output,
			parse_format, parse_ok, created_at
		)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.AppID, event.SessionID, event.Source.ID, event.Source.Kind, event.Source.Name, event.Source.Role, event.Source.PID, event.Source.Stream,
		event.Source.RestartID, event.Source.Status, event.Source.URL, event.Source.Reason, event.Level, event.Message, string(event.Fields), event.Raw,
		event.Parse.Format, parseOK, event.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	if id, err := res.LastInsertId(); err == nil {
		event.ID = id
	}
	return event.ID, nil
}

func (s *Store) ListDevSources(ctx context.Context, appID, sessionID string) ([]DevSource, error) {
	query := `
		select source_id, kind, name, role, pid, status, restart_id, url, reason
		from dev_sources
		where app_id = ?
	`
	args := []any{appID}
	if sessionID != "" {
		query += ` and session_id = ?`
		args = append(args, sessionID)
	}
	query += ` order by source_id asc`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []DevSource
	for rows.Next() {
		var source DevSource
		if err := rows.Scan(&source.ID, &source.Kind, &source.Name, &source.Role, &source.PID, &source.Status, &source.RestartID, &source.URL, &source.Reason); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (s *Store) ListDevEvents(ctx context.Context, query DevEventQuery) ([]DevEvent, error) {
	if query.Limit <= 0 {
		query.Limit = 200
	}
	stmt := `
		select id, app_id, session_id, source_id, source_kind, source_name, source_role, source_pid, source_stream,
			source_restart_id, source_status, source_url, source_reason, level, message, fields_json, raw_output,
			parse_format, parse_ok, created_at
		from dev_events
		where app_id = ?
	`
	args := []any{query.AppID}
	if query.SessionID != "" {
		stmt += ` and session_id = ?`
		args = append(args, query.SessionID)
	}
	if query.AfterID > 0 {
		stmt += ` and id > ?`
		args = append(args, query.AfterID)
	}
	if query.SourceID != "" {
		stmt += ` and source_id = ?`
		args = append(args, query.SourceID)
	}
	if query.Kind != "" {
		stmt += ` and source_kind = ?`
		args = append(args, query.Kind)
	}
	if query.Level != "" {
		stmt += ` and level = ?`
		args = append(args, query.Level)
	}
	if query.Stream != "" && query.Stream != "all" {
		stmt += ` and source_stream = ?`
		args = append(args, query.Stream)
	}
	if !query.Since.IsZero() {
		stmt += ` and created_at >= ?`
		args = append(args, query.Since.UTC().Format(time.RFC3339Nano))
	}
	if strings.TrimSpace(query.Grep) != "" {
		pattern := "%" + strings.ToLower(strings.TrimSpace(query.Grep)) + "%"
		stmt += ` and (lower(message) like ? or lower(raw_output) like ? or lower(fields_json) like ?)`
		args = append(args, pattern, pattern, pattern)
	}
	if query.AfterID > 0 {
		stmt += ` order by id asc limit ?`
		args = append(args, query.Limit)
		return s.scanDevEvents(ctx, stmt, args...)
	}
	stmt += ` order by id desc limit ?`
	args = append(args, query.Limit)
	items, err := s.scanDevEvents(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, nil
}

func (s *Store) scanDevEvents(ctx context.Context, stmt string, args ...any) ([]DevEvent, error) {
	rows, err := s.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []DevEvent
	for rows.Next() {
		var item DevEvent
		var fields, createdAt string
		var parseOK int
		if err := rows.Scan(
			&item.ID, &item.AppID, &item.SessionID, &item.Source.ID, &item.Source.Kind, &item.Source.Name, &item.Source.Role,
			&item.Source.PID, &item.Source.Stream, &item.Source.RestartID, &item.Source.Status, &item.Source.URL, &item.Source.Reason,
			&item.Level, &item.Message, &fields, &item.Raw, &item.Parse.Format, &parseOK, &createdAt,
		); err != nil {
			return nil, err
		}
		item.Parse.OK = parseOK != 0
		if fields == "" {
			fields = "{}"
		}
		item.Fields = json.RawMessage(fields)
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		items = append(items, item)
	}
	return items, rows.Err()
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
	case "debug", "trace":
		return "debug"
	case "info", "information", "notice":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error", "err":
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
