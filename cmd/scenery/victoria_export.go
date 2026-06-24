package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/devdash"
)

var victoriaExportClient = &http.Client{Timeout: time.Second}

const sceneryRequestDurationMetricName = "scenery_request_duration_seconds"

func (s *dashboardServer) exportVictoriaTraceSummaryWithEvents(ctx context.Context, summary *devdash.TraceSummary, events []*devdash.TraceEvent) {
	victoria := s.dashboardVictoria()
	if s == nil || victoria == nil || summary == nil {
		return
	}
	traceEndpoint := victoria.Endpoint("traces")
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if traceEndpoint != "" {
		_ = postVictoriaProtobuf(ctx, traceEndpoint, buildOTLPTraceProto(summary, events))
	}
	if metricsEndpoint := victoria.Endpoint("metrics"); metricsEndpoint != "" {
		_ = postVictoriaProtobuf(ctx, metricsEndpoint, buildOTLPMetricProto(summary))
	}
}

func (s *dashboardServer) exportVictoriaLogEvent(event *devdash.LogEvent) {
	victoria := s.dashboardVictoria()
	if s == nil || victoria == nil || event == nil {
		return
	}
	endpoint := victoria.Endpoint("logs")
	if endpoint == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = postVictoriaProtobuf(ctx, endpoint, buildOTLPLogProto(event))
}

func postVictoriaProtobuf(ctx context.Context, endpoint string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := victoriaExportClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return errors.New(resp.Status)
	}
	return nil
}

func buildOTLPTracePayload(summary *devdash.TraceSummary, events []*devdash.TraceEvent) map[string]any {
	start := unixNanoString(summary.StartedAt)
	end := unixNanoString(summary.StartedAt.Add(time.Duration(summary.DurationNanos)))
	span := map[string]any{
		"traceId":           validOTLPTraceID(summary.TraceID),
		"spanId":            validOTLPSpanID(summary.SpanID),
		"name":              traceSpanName(summary),
		"kind":              "SPAN_KIND_SERVER",
		"startTimeUnixNano": start,
		"endTimeUnixNano":   end,
		"attributes":        traceSummaryAttributes(summary),
	}
	if otlpEvents := traceEventsToOTLP(events); len(otlpEvents) > 0 {
		span["events"] = otlpEvents
	}
	if summary.ParentSpanID != nil {
		if parent := validOTLPSpanID(*summary.ParentSpanID); parent != "" {
			span["parentSpanId"] = parent
		}
	}
	if summary.IsError {
		span["status"] = map[string]any{"code": "STATUS_CODE_ERROR"}
	}
	return map[string]any{
		"resourceSpans": []any{
			map[string]any{
				"resource": resourceAttributes(summary.AppID),
				"scopeSpans": []any{
					map[string]any{
						"scope": map[string]any{
							"name":    "scenery",
							"version": sceneryVersion,
						},
						"spans": []any{span},
					},
				},
			},
		},
	}
}

func traceEventsToOTLP(events []*devdash.TraceEvent) []any {
	out := make([]any, 0, len(events))
	for _, event := range events {
		if event == nil || event.EventTime.IsZero() {
			continue
		}
		name := traceEventName(event)
		out = append(out, map[string]any{
			"timeUnixNano": unixNanoString(event.EventTime),
			"name":         name,
			"attributes":   attrsToJSON(traceEventAttributePairs(event)),
		})
	}
	return out
}

func traceEventName(event *devdash.TraceEvent) string {
	for key := range event.Event {
		switch key {
		case "span_start", "span_end":
			continue
		default:
			return "scenery." + key
		}
	}
	return "scenery.event"
}

func buildOTLPLogPayload(event *devdash.LogEvent) map[string]any {
	record := map[string]any{
		"timeUnixNano": unixNanoString(event.Timestamp),
		"severityText": strings.ToUpper(event.Level),
		"body":         otlpValue(event.Message),
		"attributes":   logAttributes(event),
	}
	if traceID := validOTLPTraceID(event.TraceID); traceID != "" {
		record["traceId"] = traceID
	}
	if spanID := validOTLPSpanID(event.SpanID); spanID != "" {
		record["spanId"] = spanID
	}
	return map[string]any{
		"resourceLogs": []any{
			map[string]any{
				"resource": resourceAttributes(event.AppID),
				"scopeLogs": []any{
					map[string]any{
						"scope": map[string]any{
							"name":    "scenery",
							"version": sceneryVersion,
						},
						"logRecords": []any{record},
					},
				},
			},
		},
	}
}

func buildOTLPTraceProto(summary *devdash.TraceSummary, events []*devdash.TraceEvent) []byte {
	start := uint64(summary.StartedAt.UTC().UnixNano())
	end := uint64(summary.StartedAt.Add(time.Duration(summary.DurationNanos)).UTC().UnixNano())
	span := protoBytes(1, mustHexBytes(validOTLPTraceID(summary.TraceID)))
	span = append(span, protoBytes(2, mustHexBytes(validOTLPSpanID(summary.SpanID)))...)
	if summary.ParentSpanID != nil {
		if parent := mustHexBytes(validOTLPSpanID(*summary.ParentSpanID)); len(parent) > 0 {
			span = append(span, protoBytes(4, parent)...)
		}
	}
	span = append(span, protoString(5, traceSpanName(summary))...)
	span = append(span, protoVarint(6, 2)...) // SPAN_KIND_SERVER
	span = append(span, protoFixed64(7, start)...)
	span = append(span, protoFixed64(8, end)...)
	for _, attr := range traceSummaryAttributePairs(summary) {
		span = append(span, protoMessage(9, protoKeyValue(attr.Key, attr.Value))...)
	}
	for _, event := range events {
		if event == nil || event.EventTime.IsZero() {
			continue
		}
		evt := protoFixed64(1, uint64(event.EventTime.UTC().UnixNano()))
		evt = append(evt, protoString(2, traceEventName(event))...)
		for _, attr := range traceEventAttributePairs(event) {
			evt = append(evt, protoMessage(3, protoKeyValue(attr.Key, attr.Value))...)
		}
		span = append(span, protoMessage(11, evt)...)
	}
	if summary.IsError {
		span = append(span, protoMessage(15, protoVarint(2, 2))...)
	}

	scopeSpans := protoMessage(1, protoInstrumentationScope())
	scopeSpans = append(scopeSpans, protoMessage(2, span)...)
	resourceSpans := protoMessage(1, protoResource(summary.AppID))
	resourceSpans = append(resourceSpans, protoMessage(2, scopeSpans)...)
	return protoMessage(1, resourceSpans)
}

func buildOTLPLogProto(event *devdash.LogEvent) []byte {
	record := protoFixed64(1, uint64(event.Timestamp.UTC().UnixNano()))
	record = append(record, protoVarint(2, logSeverityNumber(event.Level))...)
	record = append(record, protoString(3, strings.ToUpper(event.Level))...)
	record = append(record, protoMessage(5, protoAnyValue(event.Message))...)
	for _, attr := range logAttributePairs(event) {
		record = append(record, protoMessage(6, protoKeyValue(attr.Key, attr.Value))...)
	}
	if traceID := mustHexBytes(validOTLPTraceID(event.TraceID)); len(traceID) > 0 {
		record = append(record, protoBytes(9, traceID)...)
	}
	if spanID := mustHexBytes(validOTLPSpanID(event.SpanID)); len(spanID) > 0 {
		record = append(record, protoBytes(10, spanID)...)
	}

	scopeLogs := protoMessage(1, protoInstrumentationScope())
	scopeLogs = append(scopeLogs, protoMessage(2, record)...)
	resourceLogs := protoMessage(1, protoResource(event.AppID))
	resourceLogs = append(resourceLogs, protoMessage(2, scopeLogs)...)
	return protoMessage(1, resourceLogs)
}

func buildOTLPMetricProto(summary *devdash.TraceSummary) []byte {
	point := protoFixed64(3, uint64(summary.StartedAt.Add(time.Duration(summary.DurationNanos)).UTC().UnixNano()))
	point = append(point, protoDouble(4, float64(summary.DurationNanos)/float64(time.Second))...)
	for _, attr := range metricAttributePairs(summary) {
		point = append(point, protoMessage(7, protoKeyValue(attr.Key, attr.Value))...)
	}
	gauge := protoMessage(1, point)
	metric := protoString(1, sceneryRequestDurationMetricName)
	metric = append(metric, protoString(2, "scenery request duration")...)
	metric = append(metric, protoString(3, "s")...)
	metric = append(metric, protoMessage(5, gauge)...)
	scopeMetrics := protoMessage(1, protoInstrumentationScope())
	scopeMetrics = append(scopeMetrics, protoMessage(2, metric)...)
	resourceMetrics := protoMessage(1, protoResource(summary.AppID))
	resourceMetrics = append(resourceMetrics, protoMessage(2, scopeMetrics)...)
	return protoMessage(1, resourceMetrics)
}

func resourceAttributes(appID string) map[string]any {
	return map[string]any{
		"attributes": attrsToJSON(resourceAttributePairs(appID)),
	}
}

func traceSummaryAttributes(summary *devdash.TraceSummary) []any {
	return attrsToJSON(traceSummaryAttributePairs(summary))
}

func traceEventAttributePairs(event *devdash.TraceEvent) []otlpAttribute {
	attrs := []otlpAttribute{{Key: "scenery.event_id", Value: event.EventID}}
	if event.SessionID != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.session_id", Value: event.SessionID})
	}
	if event.AppRootHash != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.app_root_hash", Value: event.AppRootHash})
	}
	if event.Branch != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.branch", Value: event.Branch})
	}
	if event.Worktree != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.worktree", Value: event.Worktree})
	}
	if len(event.Event) > 0 {
		attrs = append(attrs, otlpAttribute{Key: "scenery.event", Value: event.Event})
	}
	return attrs
}

func logAttributes(event *devdash.LogEvent) []any {
	return attrsToJSON(logAttributePairs(event))
}

type otlpAttribute struct {
	Key   string
	Value any
}

func resourceAttributePairs(appID string) []otlpAttribute {
	return []otlpAttribute{
		{Key: "service.name", Value: appID},
		{Key: "telemetry.sdk.name", Value: "scenery"},
		{Key: "telemetry.sdk.language", Value: "go"},
	}
}

func traceSummaryAttributePairs(summary *devdash.TraceSummary) []otlpAttribute {
	attrs := []otlpAttribute{
		{Key: "scenery.application_id", Value: summary.AppID},
		{Key: "scenery.trace.type", Value: summary.Type},
		{Key: "scenery.is_root", Value: summary.IsRoot},
		{Key: "scenery.is_error", Value: summary.IsError},
		{Key: "scenery.service", Value: summary.ServiceName},
	}
	if isTemporalTraceSummary(summary) {
		attrs = append(attrs, otlpAttribute{Key: "scenery.temporal", Value: true})
	}
	if summary.SessionID != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.session_id", Value: summary.SessionID})
	}
	if summary.AppRootHash != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.app_root_hash", Value: summary.AppRootHash})
	}
	if summary.Branch != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.branch", Value: summary.Branch})
	}
	if summary.Worktree != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.worktree", Value: summary.Worktree})
	}
	if summary.EndpointName != nil {
		attrs = append(attrs, otlpAttribute{Key: "scenery.endpoint", Value: *summary.EndpointName})
	}
	if summary.MessageID != nil {
		attrs = append(attrs, otlpAttribute{Key: "scenery.message_id", Value: *summary.MessageID})
	}
	return attrs
}

func metricAttributePairs(summary *devdash.TraceSummary) []otlpAttribute {
	attrs := []otlpAttribute{
		{Key: "scenery_app", Value: summary.AppID},
		{Key: "scenery_trace_type", Value: summary.Type},
		{Key: "scenery_is_root", Value: summary.IsRoot},
		{Key: "scenery_is_error", Value: summary.IsError},
		{Key: "scenery_service", Value: summary.ServiceName},
	}
	if isTemporalTraceSummary(summary) {
		attrs = append(attrs, otlpAttribute{Key: "scenery_temporal", Value: true})
	}
	if summary.SessionID != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery_session_id", Value: summary.SessionID})
	}
	if summary.AppRootHash != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery_app_root_hash", Value: summary.AppRootHash})
	}
	if summary.Branch != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery_branch", Value: summary.Branch})
	}
	if summary.Worktree != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery_worktree", Value: summary.Worktree})
	}
	if summary.EndpointName != nil {
		attrs = append(attrs, otlpAttribute{Key: "scenery_endpoint", Value: *summary.EndpointName})
	}
	if summary.MessageID != nil {
		attrs = append(attrs, otlpAttribute{Key: "scenery_message_id", Value: *summary.MessageID})
	}
	return attrs
}

func isTemporalTraceSummary(summary *devdash.TraceSummary) bool {
	return summary != nil && strings.HasPrefix(summary.Type, "TEMPORAL_")
}

func logAttributePairs(event *devdash.LogEvent) []otlpAttribute {
	attrs := []otlpAttribute{{Key: "scenery.application_id", Value: event.AppID}}
	if event.SessionID != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.session_id", Value: event.SessionID})
	}
	if event.AppRootHash != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.app_root_hash", Value: event.AppRootHash})
	}
	if event.Branch != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.branch", Value: event.Branch})
	}
	if event.Worktree != "" {
		attrs = append(attrs, otlpAttribute{Key: "scenery.worktree", Value: event.Worktree})
	}
	for key, value := range event.Attrs {
		attrs = append(attrs, otlpAttribute{Key: "scenery.log." + key, Value: value})
	}
	return attrs
}

func attrsToJSON(attrs []otlpAttribute) []any {
	out := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, otlpAttr(attr.Key, attr.Value))
	}
	return out
}

func otlpAttr(key string, value any) map[string]any {
	return map[string]any{
		"key":   key,
		"value": otlpValue(value),
	}
}

func otlpValue(value any) map[string]any {
	switch v := value.(type) {
	case bool:
		return map[string]any{"boolValue": v}
	case int:
		return map[string]any{"intValue": strconv.FormatInt(int64(v), 10)}
	case int64:
		return map[string]any{"intValue": strconv.FormatInt(v, 10)}
	case uint64:
		return map[string]any{"intValue": strconv.FormatUint(v, 10)}
	case float64:
		return map[string]any{"doubleValue": v}
	case string:
		return map[string]any{"stringValue": v}
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return map[string]any{"stringValue": ""}
		}
		return map[string]any{"stringValue": string(data)}
	}
}

func traceSpanName(summary *devdash.TraceSummary) string {
	if summary.EndpointName != nil && *summary.EndpointName != "" {
		if summary.ServiceName != "" {
			return summary.ServiceName + "." + *summary.EndpointName
		}
		return *summary.EndpointName
	}
	if summary.ServiceName != "" {
		return summary.ServiceName
	}
	return "scenery.request"
}

func unixNanoString(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return strconv.FormatInt(t.UTC().UnixNano(), 10)
}

func validOTLPTraceID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 32 || !isLowerHex(value) || value == "00000000000000000000000000000000" {
		return ""
	}
	return value
}

func validOTLPSpanID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 16 || !isLowerHex(value) || value == "0000000000000000" {
		return ""
	}
	return value
}

func isLowerHex(value string) bool {
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func protoResource(appID string) []byte {
	var out []byte
	for _, attr := range resourceAttributePairs(appID) {
		out = append(out, protoMessage(1, protoKeyValue(attr.Key, attr.Value))...)
	}
	return out
}

func protoInstrumentationScope() []byte {
	out := protoString(1, "scenery")
	out = append(out, protoString(2, sceneryVersion)...)
	return out
}

func protoKeyValue(key string, value any) []byte {
	out := protoString(1, key)
	out = append(out, protoMessage(2, protoAnyValue(value))...)
	return out
}

func protoAnyValue(value any) []byte {
	switch v := value.(type) {
	case bool:
		return protoVarint(2, boolVarint(v))
	case int:
		return protoVarint(3, uint64(v))
	case int64:
		return protoVarint(3, uint64(v))
	case uint64:
		return protoVarint(3, v)
	case float64:
		return protoDouble(4, v)
	case string:
		return protoString(1, v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return protoString(1, "")
		}
		return protoString(1, string(data))
	}
}

func logSeverityNumber(level string) uint64 {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace":
		return 1
	case "debug":
		return 5
	case "info":
		return 9
	case "warn", "warning":
		return 13
	case "error":
		return 17
	case "fatal":
		return 21
	default:
		return 0
	}
}

func boolVarint(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func mustHexBytes(value string) []byte {
	if value == "" {
		return nil
	}
	data, err := hex.DecodeString(value)
	if err != nil {
		return nil
	}
	return data
}

func protoMessage(field int, msg []byte) []byte {
	out := protoKey(field, 2)
	out = append(out, protoRawVarint(uint64(len(msg)))...)
	return append(out, msg...)
}

func protoString(field int, value string) []byte {
	return protoBytes(field, []byte(value))
}

func protoBytes(field int, value []byte) []byte {
	out := protoKey(field, 2)
	out = append(out, protoRawVarint(uint64(len(value)))...)
	return append(out, value...)
}

func protoVarint(field int, value uint64) []byte {
	out := protoKey(field, 0)
	return append(out, protoRawVarint(value)...)
}

func protoFixed64(field int, value uint64) []byte {
	out := protoKey(field, 1)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], value)
	return append(out, buf[:]...)
}

func protoDouble(field int, value float64) []byte {
	return protoFixed64(field, math.Float64bits(value))
}

func protoKey(field int, wireType uint64) []byte {
	return protoRawVarint(uint64(field)<<3 | wireType)
}

func protoRawVarint(value uint64) []byte {
	var out []byte
	for value >= 0x80 {
		out = append(out, byte(value)|0x80)
		value >>= 7
	}
	return append(out, byte(value))
}
