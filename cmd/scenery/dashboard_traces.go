package main

import (
	"context"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/devdash"
)

type compatTraceEvent struct {
	id      uint64
	when    time.Time
	payload map[string]any
}

func (s *dashboardServer) traceEventsFor(ctx context.Context, appID, sessionID, traceID string) ([]map[string]any, error) {
	if victoria := s.dashboardVictoria(); victoria != nil {
		if events, err := victoria.TraceEventsFor(ctx, appID, traceID, ""); err == nil && len(events) > 0 {
			return events, nil
		}
	}
	summaries, err := s.dashboardStore().GetTraceSummariesForSession(ctx, appID, sessionID, traceID)
	if err != nil {
		return nil, err
	}

	var compat []compatTraceEvent
	for _, summary := range summaries {
		rawEvents, err := s.dashboardStore().GetTraceEventsForSession(ctx, appID, sessionID, traceID, summary.SpanID)
		if err != nil {
			return nil, err
		}
		compat = append(compat, buildCompatTraceEvents(summary, rawEvents)...)
	}

	sortCompatTraceEvents(compat)
	return compatTracePayloads(compat), nil
}

func (s *dashboardServer) traceEventsForSpan(ctx context.Context, appID, sessionID, traceID, spanID string) ([]map[string]any, error) {
	if victoria := s.dashboardVictoria(); victoria != nil {
		if events, err := victoria.TraceEventsFor(ctx, appID, traceID, spanID); err == nil && len(events) > 0 {
			return events, nil
		}
	}
	rawEvents, err := s.dashboardStore().GetTraceEventsForSession(ctx, appID, sessionID, traceID, spanID)
	if err != nil {
		return nil, err
	}

	var summary *devdash.TraceSummary
	summaries, err := s.dashboardStore().GetTraceSummariesForSession(ctx, appID, sessionID, traceID)
	if err != nil {
		return nil, err
	}
	for _, candidate := range summaries {
		if candidate.SpanID == spanID {
			summary = candidate
			break
		}
	}

	compat := buildCompatTraceEvents(summary, rawEvents)
	sortCompatTraceEvents(compat)
	return compatTracePayloads(compat), nil
}

func (s *dashboardServer) listTraceSummaries(ctx context.Context, appID, sessionID string, limit int, messageID string) ([]*devdash.TraceSummary, error) {
	if victoria := s.dashboardVictoria(); victoria != nil {
		items, err := victoria.QueryTraceSummaries(ctx, devdash.TraceQuery{
			AppID:     appID,
			SessionID: sessionID,
			Limit:     limit,
		})
		if err == nil && len(items) > 0 {
			if messageID != "" {
				items = filterTraceSummariesByMessageID(items, messageID)
			}
			return items, nil
		}
	}
	return s.dashboardStore().ListTraceSummariesForSession(ctx, appID, sessionID, limit, messageID)
}

func (s *dashboardServer) getTraceSummaries(ctx context.Context, appID, sessionID, traceID string) ([]*devdash.TraceSummary, error) {
	if victoria := s.dashboardVictoria(); victoria != nil {
		items, err := victoria.GetTraceSummaries(ctx, appID, traceID)
		if err == nil && len(items) > 0 {
			if sessionID == "" {
				return items, nil
			}
			filtered := items[:0]
			for _, item := range items {
				if item.SessionID == sessionID {
					filtered = append(filtered, item)
				}
			}
			if len(filtered) > 0 {
				return filtered, nil
			}
		}
	}
	return s.dashboardStore().GetTraceSummariesForSession(ctx, appID, sessionID, traceID)
}

func filterTraceSummariesByMessageID(items []*devdash.TraceSummary, messageID string) []*devdash.TraceSummary {
	if messageID == "" {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if item.MessageID != nil && strings.Contains(*item.MessageID, messageID) {
			out = append(out, item)
		}
	}
	return out
}

func buildCompatTraceEvents(summary *devdash.TraceSummary, events []*devdash.TraceEvent) []compatTraceEvent {
	out := make([]compatTraceEvent, 0, len(events)+2)
	var (
		hasStart bool
		hasEnd   bool
		minID    uint64
		maxID    uint64
	)
	for i, event := range events {
		if event == nil {
			continue
		}
		if i == 0 || event.EventID < minID {
			minID = event.EventID
		}
		if event.EventID > maxID {
			maxID = event.EventID
		}
		if _, ok := event.Event["span_start"]; ok {
			hasStart = true
		}
		if _, ok := event.Event["span_end"]; ok {
			hasEnd = true
		}
		out = append(out, compatTraceEvent{
			id:      event.EventID,
			when:    event.EventTime.UTC(),
			payload: compatTraceEventPayload(summary, event),
		})
	}

	if summary == nil {
		return out
	}

	if !hasStart {
		startID := uint64(1)
		if minID > 1 {
			startID = minID - 1
		}
		out = append(out, synthCompatSpanStart(summary, startID))
		if maxID < startID {
			maxID = startID
		}
	}
	if !hasEnd {
		endID := maxID + 1
		if endID == 0 {
			endID = maxID
		}
		out = append(out, synthCompatSpanEnd(summary, endID))
	}

	return out
}

func sortCompatTraceEvents(events []compatTraceEvent) {
	sort.Slice(events, func(i, j int) bool {
		switch {
		case events[i].id < events[j].id:
			return true
		case events[i].id > events[j].id:
			return false
		default:
			return events[i].when.Before(events[j].when)
		}
	})
}

func compatTracePayloads(events []compatTraceEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		out = append(out, event.payload)
	}
	return out
}

func compatTraceEventPayload(summary *devdash.TraceSummary, event *devdash.TraceEvent) map[string]any {
	out := map[string]any{
		"trace_id":   compatTraceID(event.TraceID),
		"span_id":    compatHexIDString(event.SpanID),
		"event_id":   strconv.FormatUint(event.EventID, 10),
		"event_time": event.EventTime.UTC().Format(time.RFC3339Nano),
	}
	for key, value := range event.Event {
		out[key] = compatTraceValue(key, value)
	}
	if summary != nil {
		if start, ok := out["span_start"].(map[string]any); ok {
			if summary.ParentSpanID != nil {
				if _, exists := start["parent_span_id"]; !exists {
					start["parent_span_id"] = compatHexIDString(*summary.ParentSpanID)
				}
			}
			if summary.CallerEventID != nil {
				if _, exists := start["caller_event_id"]; !exists {
					start["caller_event_id"] = strconv.FormatUint(*summary.CallerEventID, 10)
				}
			}
		}
	}
	return out
}

func compatTraceValue(key string, value any) any {
	switch value := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for nestedKey, nestedValue := range value {
			out[nestedKey] = compatTraceValue(nestedKey, nestedValue)
		}
		return out
	case []any:
		out := make([]any, 0, len(value))
		for _, item := range value {
			out = append(out, compatTraceValue("", item))
		}
		return out
	case []string:
		out := make([]any, 0, len(value))
		for _, item := range value {
			out = append(out, item)
		}
		return out
	case string:
		switch key {
		case "trace_id", "parent_trace_id":
			return compatTraceID(value)
		case "span_id", "parent_span_id":
			return compatHexIDString(value)
		case "caller_event_id", "correlation_event_id", "duration_nanos":
			return compatUintString(value)
		default:
			return value
		}
	case uint64:
		if compatUintField(key) {
			return strconv.FormatUint(value, 10)
		}
		return value
	case int:
		if compatUintField(key) && value >= 0 {
			return strconv.FormatInt(int64(value), 10)
		}
		return value
	case int64:
		if compatUintField(key) && value >= 0 {
			return strconv.FormatInt(value, 10)
		}
		return value
	case float64:
		if compatUintField(key) && value >= 0 {
			return strconv.FormatUint(uint64(value), 10)
		}
		return value
	default:
		return value
	}
}

func compatUintField(key string) bool {
	switch key {
	case "span_id", "parent_span_id", "caller_event_id", "correlation_event_id", "event_id", "duration_nanos":
		return true
	default:
		return false
	}
}

func compatTraceID(raw string) map[string]string {
	value, ok := parseCompatHexOrDecimal(raw)
	if !ok {
		return map[string]string{"high": "0", "low": "0"}
	}
	lowMask := new(big.Int).SetUint64(^uint64(0))
	low := new(big.Int).And(new(big.Int).Set(value), lowMask)
	high := new(big.Int).Rsh(new(big.Int).Set(value), 64)
	return map[string]string{
		"high": high.String(),
		"low":  low.String(),
	}
}

func compatUintString(raw string) string {
	value, ok := parseCompatDecimalOrHex(raw)
	if !ok {
		return "0"
	}
	return value.String()
}

func compatHexIDString(raw string) string {
	value, ok := parseCompatHexOrDecimal(raw)
	if !ok {
		return "0"
	}
	return value.String()
}

func parseCompatDecimalOrHex(raw string) (*big.Int, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "0x")
	raw = strings.TrimPrefix(raw, "0X")
	if raw == "" {
		return nil, false
	}

	value := new(big.Int)
	if isDecimalString(raw) {
		if _, ok := value.SetString(raw, 10); ok {
			return value, true
		}
	}
	if _, ok := value.SetString(raw, 16); ok {
		return value, true
	}
	return nil, false
}

func parseCompatHexOrDecimal(raw string) (*big.Int, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "0x")
	raw = strings.TrimPrefix(raw, "0X")
	if raw == "" {
		return nil, false
	}

	value := new(big.Int)
	if _, ok := value.SetString(raw, 16); ok {
		return value, true
	}
	if _, ok := value.SetString(raw, 10); ok {
		return value, true
	}
	return nil, false
}

func isDecimalString(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func synthCompatSpanStart(summary *devdash.TraceSummary, eventID uint64) compatTraceEvent {
	service := summary.ServiceName
	endpoint := derefString(summary.EndpointName)
	spanStart := map[string]any{}
	if summary.ParentSpanID != nil {
		spanStart["parent_span_id"] = compatHexIDString(*summary.ParentSpanID)
	}
	if summary.CallerEventID != nil {
		spanStart["caller_event_id"] = strconv.FormatUint(*summary.CallerEventID, 10)
	}
	if strings.EqualFold(summary.Type, "AUTH") {
		spanStart["auth"] = map[string]any{
			"service_name":  service,
			"endpoint_name": endpoint,
		}
	} else {
		spanStart["request"] = map[string]any{
			"service_name":  service,
			"endpoint_name": endpoint,
			"http_method":   synthRequestMethod(endpoint),
			"path":          synthRequestPath(service, endpoint),
			"path_params":   []any{},
			"mocked":        false,
		}
	}
	return compatTraceEvent{
		id:   eventID,
		when: summary.StartedAt.UTC(),
		payload: map[string]any{
			"trace_id":   compatTraceID(summary.TraceID),
			"span_id":    compatHexIDString(summary.SpanID),
			"event_id":   strconv.FormatUint(eventID, 10),
			"event_time": summary.StartedAt.UTC().Format(time.RFC3339Nano),
			"span_start": spanStart,
		},
	}
}

func synthCompatSpanEnd(summary *devdash.TraceSummary, eventID uint64) compatTraceEvent {
	service := summary.ServiceName
	endpoint := derefString(summary.EndpointName)
	eventTime := summary.StartedAt.UTC()
	if summary.DurationNanos > 0 {
		eventTime = eventTime.Add(time.Duration(summary.DurationNanos))
	}
	spanEnd := map[string]any{
		"duration_nanos": strconv.FormatUint(summary.DurationNanos, 10),
	}
	if strings.EqualFold(summary.Type, "AUTH") {
		spanEnd["auth"] = map[string]any{
			"service_name":  service,
			"endpoint_name": endpoint,
		}
	} else {
		httpStatus := 200
		if summary.IsError {
			httpStatus = 500
		}
		spanEnd["request"] = map[string]any{
			"service_name":     service,
			"endpoint_name":    endpoint,
			"http_status_code": httpStatus,
		}
	}
	if summary.IsError {
		spanEnd["error"] = map[string]any{
			"msg": "operation failed",
		}
	}
	return compatTraceEvent{
		id:   eventID,
		when: eventTime,
		payload: map[string]any{
			"trace_id":   compatTraceID(summary.TraceID),
			"span_id":    compatHexIDString(summary.SpanID),
			"event_id":   strconv.FormatUint(eventID, 10),
			"event_time": eventTime.Format(time.RFC3339Nano),
			"span_end":   spanEnd,
		},
	}
}

func synthRequestMethod(endpoint string) string {
	if strings.EqualFold(endpoint, "init") {
		return "INIT"
	}
	return "GET"
}

func synthRequestPath(service, endpoint string) string {
	service = strings.TrimSpace(service)
	endpoint = strings.TrimSpace(endpoint)
	switch {
	case service == "" && endpoint == "":
		return "/"
	case endpoint == "":
		return "/" + service
	default:
		return "/" + service + "." + endpoint
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
