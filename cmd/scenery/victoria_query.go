package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/devdash"
)

type victoriaJaegerResponse struct {
	Data []victoriaJaegerTrace `json:"data"`
}

const victoriaJaegerMaxTraceLimit = 1000

type victoriaJaegerTrace struct {
	TraceID   string                           `json:"traceID"`
	Spans     []victoriaJaegerSpan             `json:"spans"`
	Processes map[string]victoriaJaegerProcess `json:"processes"`
}

type victoriaJaegerProcess struct {
	ServiceName string              `json:"serviceName"`
	Tags        []victoriaJaegerTag `json:"tags"`
}

type victoriaJaegerSpan struct {
	TraceID       string                    `json:"traceID"`
	SpanID        string                    `json:"spanID"`
	OperationName string                    `json:"operationName"`
	References    []victoriaJaegerReference `json:"references"`
	StartTime     int64                     `json:"startTime"`
	Duration      int64                     `json:"duration"`
	ProcessID     string                    `json:"processID"`
	Tags          []victoriaJaegerTag       `json:"tags"`
	Logs          []victoriaJaegerLog       `json:"logs"`
}

type victoriaJaegerReference struct {
	RefType string `json:"refType"`
	TraceID string `json:"traceID"`
	SpanID  string `json:"spanID"`
}

type victoriaJaegerTag struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Value any    `json:"value"`
}

type victoriaJaegerLog struct {
	Timestamp int64               `json:"timestamp"`
	Fields    []victoriaJaegerTag `json:"fields"`
}

func defaultVictoriaQueryStack() *victoriaStack {
	if !victoriaEnabled() {
		return nil
	}
	stack := &victoriaStack{}
	for _, spec := range victoriaComponentSpecs() {
		baseURL := fmt.Sprintf("http://%s:%d", victoriaDefaultHost, spec.DefaultPort)
		stack.components = append(stack.components, &victoriaComponent{
			spec:        spec,
			baseURL:     baseURL,
			endpointURL: baseURL + spec.EndpointPath,
			external:    true,
		})
	}
	return stack
}

func (s *victoriaStack) QueryTraceSummaries(ctx context.Context, query devdash.TraceQuery) ([]*devdash.TraceSummary, error) {
	baseURL := s.BaseURL("traces")
	if baseURL == "" {
		return nil, errors.New("VictoriaTraces is unavailable")
	}
	if clearedAt := s.ClearedAt(query.AppID); query.Since.Before(clearedAt) {
		query.Since = clearedAt
	}
	if query.Limit <= 0 {
		query.Limit = 100
	}
	traces, err := queryVictoriaJaegerTraces(ctx, baseURL, query)
	if err != nil {
		if strings.Contains(err.Error(), "VictoriaTraces returned no traces") {
			return []*devdash.TraceSummary{}, nil
		}
		return nil, err
	}
	items := summariesFromVictoriaTraces(query.AppID, traces)
	items = filterVictoriaSummaries(items, query)
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].StartedAt.After(items[j].StartedAt)
	})
	if len(items) > query.Limit {
		items = items[:query.Limit]
	}
	return items, nil
}

func (s *victoriaStack) GetTraceSummaries(ctx context.Context, appID, traceID string) ([]*devdash.TraceSummary, error) {
	baseURL := s.BaseURL("traces")
	if baseURL == "" {
		return nil, errors.New("VictoriaTraces is unavailable")
	}
	traces, err := getVictoriaJaegerTrace(ctx, baseURL, traceID)
	if err != nil {
		return nil, err
	}
	items := summariesFromVictoriaTraces(appID, traces)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].IsRoot != items[j].IsRoot {
			return items[i].IsRoot
		}
		return items[i].StartedAt.Before(items[j].StartedAt)
	})
	return items, nil
}

func (s *victoriaStack) TraceEventsFor(ctx context.Context, appID, traceID, spanID string) ([]map[string]any, error) {
	baseURL := s.BaseURL("traces")
	if baseURL == "" {
		return nil, errors.New("VictoriaTraces is unavailable")
	}
	traces, err := getVictoriaJaegerTrace(ctx, baseURL, traceID)
	if err != nil {
		return nil, err
	}
	var compat []compatTraceEvent
	for _, trace := range traces {
		for _, span := range trace.Spans {
			if spanID != "" && span.SpanID != spanID {
				continue
			}
			summary := traceSummaryFromVictoriaSpan(appID, trace, span)
			compat = append(compat, compatFromVictoriaSpan(summary, span)...)
		}
	}
	if len(compat) == 0 {
		return nil, errors.New("trace not found in VictoriaTraces")
	}
	sortCompatTraceEvents(compat)
	return compatTracePayloads(compat), nil
}

func (s *victoriaStack) BaseURL(name string) string {
	if s == nil {
		return ""
	}
	for _, component := range s.components {
		if component.spec.Name == name {
			return component.baseURL
		}
	}
	return ""
}

func queryVictoriaJaegerTraces(ctx context.Context, baseURL string, query devdash.TraceQuery) ([]victoriaJaegerTrace, error) {
	values := url.Values{}
	if query.AppID != "" {
		values.Set("service", query.AppID)
	}
	values.Set("limit", strconv.Itoa(victoriaJaegerTraceLimit(query.Limit)))
	if !query.Since.IsZero() {
		values.Set("start", strconv.FormatInt(query.Since.UTC().UnixMicro(), 10))
		values.Set("end", strconv.FormatInt(time.Now().UTC().UnixMicro(), 10))
	}
	if query.TraceID != "" {
		return getVictoriaJaegerTrace(ctx, baseURL, query.TraceID)
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/select/jaeger/api/traces"
	if encoded := values.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	return fetchVictoriaJaeger(ctx, endpoint)
}

func victoriaJaegerTraceLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > victoriaJaegerMaxTraceLimit {
		return victoriaJaegerMaxTraceLimit
	}
	return limit
}

func getVictoriaJaegerTrace(ctx context.Context, baseURL, traceID string) ([]victoriaJaegerTrace, error) {
	if traceID == "" {
		return nil, errors.New("trace id is required")
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/select/jaeger/api/traces/" + url.PathEscape(traceID)
	return fetchVictoriaJaeger(ctx, endpoint)
}

func fetchVictoriaJaeger(ctx context.Context, endpoint string) ([]victoriaJaegerTrace, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := victoriaExportClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("VictoriaTraces query failed: %s", resp.Status)
	}
	var payload victoriaJaegerResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if len(payload.Data) == 0 {
		return nil, errors.New("VictoriaTraces returned no traces")
	}
	return payload.Data, nil
}

func summariesFromVictoriaTraces(appID string, traces []victoriaJaegerTrace) []*devdash.TraceSummary {
	var items []*devdash.TraceSummary
	for _, trace := range traces {
		for _, span := range trace.Spans {
			summary := traceSummaryFromVictoriaSpan(appID, trace, span)
			if summary != nil {
				items = append(items, summary)
			}
		}
	}
	return items
}

func traceSummaryFromVictoriaSpan(appID string, trace victoriaJaegerTrace, span victoriaJaegerSpan) *devdash.TraceSummary {
	traceID := firstNonEmpty(span.TraceID, trace.TraceID)
	if traceID == "" || span.SpanID == "" {
		return nil
	}
	tags := victoriaTagsMap(span.Tags)
	process := trace.Processes[span.ProcessID]
	serviceName := stringTag(tags, "scenery.service")
	if serviceName == "" {
		serviceName = process.ServiceName
	}
	endpointName := optionalVictoriaString(firstNonEmpty(stringTag(tags, "scenery.endpoint"), endpointFromOperation(span.OperationName)))
	summary := &devdash.TraceSummary{
		AppID:         appID,
		SessionID:     stringTag(tags, "scenery.session_id"),
		AppRootHash:   stringTag(tags, "scenery.app_root_hash"),
		Branch:        stringTag(tags, "scenery.branch"),
		Worktree:      stringTag(tags, "scenery.worktree"),
		TraceID:       traceID,
		SpanID:        span.SpanID,
		Type:          firstNonEmpty(stringTag(tags, "scenery.trace.type"), "REQUEST"),
		IsRoot:        len(span.References) == 0,
		IsError:       boolTag(tags, "scenery.is_error") || boolTag(tags, "error") || stringTag(tags, "otel.status_code") == "ERROR",
		StartedAt:     time.UnixMicro(span.StartTime).UTC(),
		DurationNanos: uint64(maxInt64(span.Duration, 0)) * uint64(time.Microsecond),
		ServiceName:   serviceName,
		EndpointName:  endpointName,
	}
	if messageID := stringTag(tags, "scenery.message_id"); messageID != "" {
		summary.MessageID = &messageID
	}
	for _, ref := range span.References {
		if ref.SpanID != "" {
			parent := ref.SpanID
			summary.ParentSpanID = &parent
			summary.IsRoot = false
			break
		}
	}
	return summary
}

func filterVictoriaSummaries(items []*devdash.TraceSummary, query devdash.TraceQuery) []*devdash.TraceSummary {
	out := items[:0]
	for _, item := range items {
		if query.TraceID != "" && item.TraceID != query.TraceID {
			continue
		}
		if query.SessionID != "" && item.SessionID != query.SessionID {
			continue
		}
		if query.ServiceName != "" && item.ServiceName != query.ServiceName {
			continue
		}
		if query.EndpointName != "" && (item.EndpointName == nil || *item.EndpointName != query.EndpointName) {
			continue
		}
		if query.Status == "ok" && item.IsError {
			continue
		}
		if query.Status == "error" && !item.IsError {
			continue
		}
		if !query.Since.IsZero() && item.StartedAt.Before(query.Since) {
			continue
		}
		if query.MinDurationNanos > 0 && item.DurationNanos < query.MinDurationNanos {
			continue
		}
		if !item.IsRoot && query.TraceID == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func compatFromVictoriaSpan(summary *devdash.TraceSummary, span victoriaJaegerSpan) []compatTraceEvent {
	events := buildCompatTraceEvents(summary, nil)
	for idx, logEvent := range span.Logs {
		payload := map[string]any{
			"trace_id":   compatTraceID(span.TraceID),
			"span_id":    compatHexIDString(span.SpanID),
			"event_id":   strconv.Itoa(idx + 2),
			"event_time": time.UnixMicro(logEvent.Timestamp).UTC().Format(time.RFC3339Nano),
			"span_event": map[string]any{
				"victoria": map[string]any{
					"fields": victoriaTagsMap(logEvent.Fields),
				},
			},
		}
		events = append(events, compatTraceEvent{
			id:      uint64(idx + 2),
			when:    time.UnixMicro(logEvent.Timestamp).UTC(),
			payload: payload,
		})
	}
	return events
}

func victoriaTagsMap(tags []victoriaJaegerTag) map[string]any {
	out := make(map[string]any, len(tags))
	for _, tag := range tags {
		out[tag.Key] = tag.Value
	}
	return out
}

func stringTag(tags map[string]any, key string) string {
	value, ok := tags[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func boolTag(tags map[string]any, key string) bool {
	value, ok := tags[key]
	if !ok {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	default:
		return false
	}
}

func endpointFromOperation(operation string) string {
	if idx := strings.LastIndex(operation, "."); idx >= 0 && idx+1 < len(operation) {
		return operation[idx+1:]
	}
	return ""
}

func optionalVictoriaString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func maxInt64(value, floor int64) int64 {
	if value < floor {
		return floor
	}
	return value
}
