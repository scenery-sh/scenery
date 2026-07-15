package victoria

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/devdash"
)

const (
	victoriaDevEventKindField     = "scenery_dev_kind"
	victoriaDevEventRevisionField = "scenery_dev_schema_revision"
	victoriaDevEventAppField      = "scenery_app_id"
	victoriaDevEventSession       = "scenery_session_id"
	victoriaDevEventCreatedAt     = "created_at"
	victoriaDevEventMessage       = "message"
)

const victoriaDevEventQueryWindow = "30d"

// ExportDevEvent writes one dev event into VictoriaLogs.
func (s *Stack) ExportDevEvent(ctx context.Context, event devdash.DevEvent) error {
	baseURL := s.BaseURL("logs")
	if baseURL == "" {
		return errors.New("VictoriaLogs is unavailable")
	}
	body, err := json.Marshal(DevEventRecord(event))
	if err != nil {
		return err
	}
	body = append(body, '\n')
	values := url.Values{}
	values.Set("_stream_fields", strings.Join([]string{victoriaDevEventKindField, victoriaDevEventRevisionField, victoriaDevEventAppField, victoriaDevEventSession, "source_id"}, ","))
	values.Set("_msg_field", victoriaDevEventMessage)
	endpoint := strings.TrimRight(baseURL, "/") + "/insert/jsonline?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/stream+json")
	resp, err := exportClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("VictoriaLogs dev event export failed: %s", resp.Status)
	}
	return nil
}

// DevEventRecord maps a dev event to the VictoriaLogs jsonline row shape.
func DevEventRecord(event devdash.DevEvent) map[string]any {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	fields := event.Fields
	if len(fields) == 0 || !json.Valid(fields) {
		fields = json.RawMessage(`{}`)
	}
	message := strings.TrimSpace(event.Message)
	if message == "" {
		message = strings.TrimSpace(event.Raw)
	}
	if message == "" {
		message = event.Level
	}
	return map[string]any{
		victoriaDevEventKindField:     devdash.DevEventKind,
		victoriaDevEventRevisionField: devdash.DevEventSchemaRevision,
		victoriaDevEventAppField:      event.AppID,
		victoriaDevEventSession:       event.SessionID,
		"id":                          event.ID,
		victoriaDevEventCreatedAt:     event.CreatedAt.UTC().Format(time.RFC3339Nano),
		"source_id":                   event.Source.ID,
		"source_kind":                 event.Source.Kind,
		"source_name":                 event.Source.Name,
		"source_role":                 event.Source.Role,
		"source_pid":                  event.Source.PID,
		"source_stream":               event.Source.Stream,
		"source_restart_id":           event.Source.RestartID,
		"source_status":               event.Source.Status,
		"source_url":                  event.Source.URL,
		"source_reason":               event.Source.Reason,
		"level":                       event.Level,
		victoriaDevEventMessage:       message,
		"fields_json":                 string(fields),
		"raw":                         event.Raw,
		"parse_format":                event.Parse.Format,
		"parse_ok":                    event.Parse.OK,
	}
}

// ListDevEvents queries dev events from VictoriaLogs.
func (s *Stack) ListDevEvents(ctx context.Context, query devdash.DevEventQuery) ([]devdash.DevEvent, error) {
	baseURL := s.BaseURL("logs")
	if baseURL == "" {
		return nil, errors.New("VictoriaLogs is unavailable")
	}
	if query.Limit <= 0 {
		query.Limit = 200
	}
	rows, err := queryVictoriaDevEvents(ctx, baseURL, query)
	if err != nil {
		return nil, err
	}
	items := make([]devdash.DevEvent, 0, len(rows))
	for _, row := range rows {
		if event, ok := devEventFromVictoriaRecord(row); ok {
			items = append(items, event)
		}
	}
	items = filterVictoriaDevEvents(items, query)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ID != items[j].ID {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	if len(items) > query.Limit {
		if query.AfterID > 0 {
			items = items[:query.Limit]
		} else {
			items = items[len(items)-query.Limit:]
		}
	}
	return items, nil
}

// ListDevSources aggregates the distinct dev event sources for an app session.
func (s *Stack) ListDevSources(ctx context.Context, appID, sessionID string) ([]devdash.DevSource, error) {
	items, err := s.ListDevEvents(ctx, devdash.DevEventQuery{
		AppID:     appID,
		SessionID: sessionID,
		Limit:     10000,
	})
	if err != nil {
		return nil, err
	}
	byID := make(map[string]devdash.DevSource)
	for _, item := range items {
		source := item.Source
		if source.ID == "" {
			continue
		}
		byID[source.ID] = mergeDevSource(byID[source.ID], source)
	}
	sources := make([]devdash.DevSource, 0, len(byID))
	for _, source := range byID {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].ID < sources[j].ID
	})
	return sources, nil
}

func mergeDevSource(base, next devdash.DevSource) devdash.DevSource {
	if base.ID == "" {
		return next
	}
	if next.Kind != "" {
		base.Kind = next.Kind
	}
	if next.Name != "" {
		base.Name = next.Name
	}
	if next.Role != "" {
		base.Role = next.Role
	}
	if next.PID != "" {
		base.PID = next.PID
	}
	if next.Stream != "" {
		base.Stream = next.Stream
	}
	if next.RestartID != "" {
		base.RestartID = next.RestartID
	}
	if next.Status != "" {
		base.Status = next.Status
	}
	if next.URL != "" {
		base.URL = next.URL
	}
	if next.Reason != "" {
		base.Reason = next.Reason
	}
	return base
}

func queryVictoriaDevEvents(ctx context.Context, baseURL string, query devdash.DevEventQuery) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	values := url.Values{}
	values.Set("query", victoriaDevEventsLogSQL(query))
	values.Set("limit", strconv.Itoa(victoriaDevEventFetchLimit(query)))
	values.Set("start", victoriaDevEventQueryWindow)
	values.Set("end", "now")

	endpoint := strings.TrimRight(baseURL, "/") + "/select/logsql/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := exportClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		detail := strings.TrimSpace(string(data))
		if detail != "" {
			return nil, fmt.Errorf("VictoriaLogs dev event query failed: %s: %s", resp.Status, detail)
		}
		return nil, fmt.Errorf("VictoriaLogs dev event query failed: %s", resp.Status)
	}
	return decodeVictoriaDevEventRows(resp.Body)
}

func victoriaDevEventsLogSQL(query devdash.DevEventQuery) string {
	stream := map[string]string{
		victoriaDevEventKindField:     devdash.DevEventKind,
		victoriaDevEventRevisionField: devdash.DevEventSchemaRevision,
	}
	if query.AppID != "" {
		stream[victoriaDevEventAppField] = query.AppID
	}
	if query.SessionID != "" {
		stream[victoriaDevEventSession] = query.SessionID
	}
	if query.SourceID != "" {
		stream["source_id"] = query.SourceID
	}
	keys := make([]string, 0, len(stream))
	for key := range stream {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(stream[key]))
	}
	b.WriteByte('}')
	return b.String()
}

func victoriaDevEventFetchLimit(query devdash.DevEventQuery) int {
	limit := query.Limit
	if limit <= 0 {
		limit = 200
	}
	if query.AfterID > 0 && limit < 1000 {
		return 1000
	}
	if query.Kind != "" || query.Level != "" || (query.Stream != "" && query.Stream != "all") || strings.TrimSpace(query.Grep) != "" || !query.Since.IsZero() {
		expanded := limit * 20
		if expanded < 1000 {
			expanded = 1000
		}
		if expanded > 10000 {
			expanded = 10000
		}
		return expanded
	}
	return limit
}

func decodeVictoriaDevEventRows(r io.Reader) ([]map[string]any, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var rows []map[string]any
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row map[string]any
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, scanner.Err()
}

func devEventFromVictoriaRecord(row map[string]any) (devdash.DevEvent, bool) {
	if victoriaRecordString(row, victoriaDevEventKindField) != devdash.DevEventKind || victoriaRecordString(row, victoriaDevEventRevisionField) != devdash.DevEventSchemaRevision {
		return devdash.DevEvent{}, false
	}
	fields := victoriaRecordString(row, "fields_json")
	if fields == "" || !json.Valid([]byte(fields)) {
		fields = "{}"
	}
	event := devdash.DevEvent{
		ID:        victoriaRecordInt64(row, "id"),
		AppID:     victoriaRecordString(row, victoriaDevEventAppField),
		SessionID: victoriaRecordString(row, victoriaDevEventSession),
		Source: devdash.DevSource{
			ID:        victoriaRecordString(row, "source_id"),
			Kind:      victoriaRecordString(row, "source_kind"),
			Name:      victoriaRecordString(row, "source_name"),
			Role:      victoriaRecordString(row, "source_role"),
			PID:       victoriaRecordString(row, "source_pid"),
			Stream:    victoriaRecordString(row, "source_stream"),
			RestartID: victoriaRecordString(row, "source_restart_id"),
			Status:    victoriaRecordString(row, "source_status"),
			URL:       victoriaRecordString(row, "source_url"),
			Reason:    victoriaRecordString(row, "source_reason"),
		},
		Level:     victoriaRecordString(row, "level"),
		Message:   firstNonEmpty(victoriaRecordString(row, victoriaDevEventMessage), victoriaRecordString(row, "_msg")),
		Fields:    json.RawMessage(fields),
		Raw:       victoriaRecordString(row, "raw"),
		Parse:     devdash.DevEventParse{Format: victoriaRecordString(row, "parse_format"), OK: victoriaRecordBool(row, "parse_ok")},
		CreatedAt: victoriaRecordTime(row, victoriaDevEventCreatedAt, "_time"),
	}
	return event, true
}

func filterVictoriaDevEvents(items []devdash.DevEvent, query devdash.DevEventQuery) []devdash.DevEvent {
	grep := strings.ToLower(strings.TrimSpace(query.Grep))
	out := items[:0]
	for _, item := range items {
		if query.AppID != "" && item.AppID != query.AppID {
			continue
		}
		if query.SessionID != "" && item.SessionID != query.SessionID {
			continue
		}
		if query.AfterID > 0 && item.ID <= query.AfterID {
			continue
		}
		if query.SourceID != "" && item.Source.ID != query.SourceID {
			continue
		}
		if query.Kind != "" && item.Source.Kind != query.Kind {
			continue
		}
		if query.Level != "" && item.Level != query.Level {
			continue
		}
		if query.Stream != "" && query.Stream != "all" && item.Source.Stream != query.Stream {
			continue
		}
		if !query.Since.IsZero() && item.CreatedAt.Before(query.Since) {
			continue
		}
		if grep != "" {
			haystack := strings.ToLower(item.Message + "\x00" + item.Raw + "\x00" + string(item.Fields))
			if !strings.Contains(haystack, grep) {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

func victoriaRecordString(row map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := row[key]
		if !ok || value == nil {
			continue
		}
		switch v := value.(type) {
		case string:
			return v
		case json.Number:
			return v.String()
		case float64:
			if v == float64(int64(v)) {
				return strconv.FormatInt(int64(v), 10)
			}
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		}
	}
	return ""
}

func victoriaRecordInt64(row map[string]any, key string) int64 {
	value, ok := row[key]
	if !ok || value == nil {
		return 0
	}
	switch v := value.(type) {
	case int64:
		return v
	case json.Number:
		n, _ := v.Int64()
		return n
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	}
	return 0
}

func victoriaRecordBool(row map[string]any, key string) bool {
	value, ok := row[key]
	if !ok || value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true") || strings.TrimSpace(v) == "1"
	case json.Number:
		return v.String() != "0"
	case float64:
		return v != 0
	}
	return false
}

func victoriaRecordTime(row map[string]any, keys ...string) time.Time {
	for _, key := range keys {
		raw := victoriaRecordString(row, key)
		if raw == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t
		}
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			switch {
			case n > 1e18:
				return time.Unix(0, n).UTC()
			case n > 1e15:
				return time.UnixMicro(n).UTC()
			case n > 1e12:
				return time.UnixMilli(n).UTC()
			default:
				return time.Unix(n, 0).UTC()
			}
		}
	}
	return time.Time{}
}
