package observability

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	InspectObservabilitySchema = "onlava.inspect.observability.v1"
	LogsQuerySchema            = "onlava.logs.query.v1"
	LogsTailEntrySchema        = "onlava.logs.tail.entry.v1"
	MetricsQuerySchema         = "onlava.metrics.query.v1"
	MetricsLabelsSchema        = "onlava.metrics.labels.v1"
	MetricsSeriesSchema        = "onlava.metrics.series.v1"
)

type QueryScope struct {
	AppID       string `json:"app_id"`
	SessionID   string `json:"session_id"`
	AppRoot     string `json:"app_root"`
	AppRootHash string `json:"app_root_hash"`
	Worktree    string `json:"worktree,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Enforced    bool   `json:"enforced"`
}

type TimeBounds struct {
	Since string    `json:"since,omitempty"`
	Start time.Time `json:"-"`
	End   time.Time `json:"-"`
}

type QueryBackend struct {
	Kind      string `json:"kind"`
	Dialect   string `json:"dialect"`
	BaseURL   string `json:"base_url,omitempty"`
	Ready     bool   `json:"ready"`
	QueryPath string `json:"query_path,omitempty"`
	TailPath  string `json:"tail_path,omitempty"`
}

type LogsQuery struct {
	BaseURL  string
	Scope    QueryScope
	Query    string
	Bounds   TimeBounds
	Limit    int
	Timeout  time.Duration
	Fields   []string
	Warnings []string
}

type LogsQueryRecord struct {
	Query   string   `json:"query"`
	Since   string   `json:"since,omitempty"`
	Start   string   `json:"start"`
	End     string   `json:"end"`
	Limit   int      `json:"limit,omitempty"`
	Timeout string   `json:"timeout,omitempty"`
	Fields  []string `json:"fields,omitempty"`
}

type LogEntry struct {
	Time    string         `json:"time,omitempty"`
	Level   string         `json:"level,omitempty"`
	Source  string         `json:"source,omitempty"`
	Message string         `json:"message,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
	TraceID string         `json:"trace_id,omitempty"`
	SpanID  string         `json:"span_id,omitempty"`
	Raw     map[string]any `json:"raw,omitempty"`
}

type LogsQueryResult struct {
	SchemaVersion string          `json:"schema_version"`
	Scope         QueryScope      `json:"scope"`
	Backend       QueryBackend    `json:"backend"`
	Query         LogsQueryRecord `json:"query"`
	Warnings      []string        `json:"warnings,omitempty"`
	Logs          []LogEntry      `json:"logs"`
}

type LogsTailEntry struct {
	SchemaVersion string          `json:"schema_version"`
	Scope         QueryScope      `json:"scope"`
	Backend       QueryBackend    `json:"backend"`
	Query         LogsQueryRecord `json:"query"`
	Log           LogEntry        `json:"log"`
}

type MetricsQuery struct {
	BaseURL  string
	Scope    QueryScope
	PromQL   string
	Bounds   TimeBounds
	Step     time.Duration
	Instant  bool
	Timeout  time.Duration
	Limit    int
	Warnings []string
}

type MetricsQueryRecord struct {
	PromQL  string `json:"promql"`
	Mode    string `json:"mode"`
	Since   string `json:"since,omitempty"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
	Step    string `json:"step,omitempty"`
	Timeout string `json:"timeout,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type MetricSample struct {
	Time  string `json:"time"`
	Value string `json:"value"`
}

type MetricSeries struct {
	Metric map[string]string `json:"metric"`
	Value  *MetricSample     `json:"value,omitempty"`
	Values []MetricSample    `json:"values,omitempty"`
}

type MetricsQueryResult struct {
	SchemaVersion string             `json:"schema_version"`
	Scope         QueryScope         `json:"scope"`
	Backend       QueryBackend       `json:"backend"`
	Query         MetricsQueryRecord `json:"query"`
	ResultType    string             `json:"result_type"`
	Warnings      []string           `json:"warnings,omitempty"`
	Series        []MetricSeries     `json:"series"`
}

type MetricsCatalogQuery struct {
	BaseURL  string
	Scope    QueryScope
	Bounds   TimeBounds
	Match    string
	Limit    int
	Timeout  time.Duration
	Warnings []string
}

type MetricsCatalogRecord struct {
	Match   string `json:"match,omitempty"`
	Since   string `json:"since,omitempty"`
	Start   string `json:"start"`
	End     string `json:"end"`
	Timeout string `json:"timeout,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type MetricsLabelsResult struct {
	SchemaVersion string               `json:"schema_version"`
	Scope         QueryScope           `json:"scope"`
	Backend       QueryBackend         `json:"backend"`
	Query         MetricsCatalogRecord `json:"query"`
	Warnings      []string             `json:"warnings,omitempty"`
	Labels        []string             `json:"labels"`
}

type MetricsSeriesResult struct {
	SchemaVersion string               `json:"schema_version"`
	Scope         QueryScope           `json:"scope"`
	Backend       QueryBackend         `json:"backend"`
	Query         MetricsCatalogRecord `json:"query"`
	Warnings      []string             `json:"warnings,omitempty"`
	Series        []map[string]string  `json:"series"`
}

func QueryLogs(ctx context.Context, q LogsQuery) (LogsQueryResult, error) {
	result := LogsQueryResult{
		SchemaVersion: LogsQuerySchema,
		Scope:         q.Scope,
		Backend:       logsBackend(q.BaseURL),
		Query:         logsQueryRecord(q),
		Warnings:      append([]string(nil), q.Warnings...),
		Logs:          []LogEntry{},
	}
	if strings.TrimSpace(q.BaseURL) == "" {
		result.Warnings = append(result.Warnings, "VictoriaLogs is unavailable")
		return result, nil
	}
	if q.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, q.Timeout+time.Second)
		defer cancel()
	}
	rows, err := fetchLogs(ctx, q.BaseURL, "/select/logsql/query", logValues(q))
	if err != nil {
		return LogsQueryResult{}, err
	}
	result.Logs = normalizeLogRows(rows, q.Fields, q.Limit)
	return result, nil
}

func TailLogs(ctx context.Context, q LogsQuery, emit func(LogsTailEntry) error) error {
	if strings.TrimSpace(q.BaseURL) == "" {
		return fmt.Errorf("VictoriaLogs is unavailable")
	}
	record := logsQueryRecord(q)
	return streamLogs(ctx, q.BaseURL, "/select/logsql/tail", logTailValues(q), func(row map[string]any) error {
		entries := normalizeLogRows([]map[string]any{row}, q.Fields, 1)
		if len(entries) == 0 {
			return nil
		}
		return emit(LogsTailEntry{
			SchemaVersion: LogsTailEntrySchema,
			Scope:         q.Scope,
			Backend:       logsBackend(q.BaseURL),
			Query:         record,
			Log:           entries[0],
		})
	})
}

func QueryMetrics(ctx context.Context, q MetricsQuery) (MetricsQueryResult, error) {
	result := MetricsQueryResult{
		SchemaVersion: MetricsQuerySchema,
		Scope:         q.Scope,
		Backend:       metricsBackend(q.BaseURL),
		Query:         metricsQueryRecord(q),
		Warnings:      append([]string(nil), q.Warnings...),
		Series:        []MetricSeries{},
	}
	if strings.TrimSpace(q.BaseURL) == "" {
		result.Warnings = append(result.Warnings, "VictoriaMetrics is unavailable")
		return result, nil
	}
	if q.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, q.Timeout+time.Second)
		defer cancel()
	}
	values := url.Values{}
	values.Set("query", q.PromQL)
	if q.Timeout > 0 {
		values.Set("timeout", q.Timeout.String())
	}
	addMetricScope(values, q.Scope)
	path := "/prometheus/api/v1/query_range"
	if q.Instant {
		path = "/prometheus/api/v1/query"
		values.Set("time", formatVictoriaTime(q.Bounds.End))
	} else {
		values.Set("start", formatVictoriaTime(q.Bounds.Start))
		values.Set("end", formatVictoriaTime(q.Bounds.End))
		values.Set("step", q.Step.String())
	}
	payload, err := fetchMetrics(ctx, q.BaseURL, path, values)
	if err != nil {
		return MetricsQueryResult{}, err
	}
	result.ResultType = payload.Data.ResultType
	result.Series = normalizeMetricResults(payload.Data.Result, q.Limit)
	return result, nil
}

func MetricsLabels(ctx context.Context, q MetricsCatalogQuery) (MetricsLabelsResult, error) {
	result := MetricsLabelsResult{
		SchemaVersion: MetricsLabelsSchema,
		Scope:         q.Scope,
		Backend:       metricsBackend(q.BaseURL),
		Query:         metricsCatalogRecord(q),
		Warnings:      append([]string(nil), q.Warnings...),
		Labels:        []string{},
	}
	if strings.TrimSpace(q.BaseURL) == "" {
		result.Warnings = append(result.Warnings, "VictoriaMetrics is unavailable")
		return result, nil
	}
	if q.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, q.Timeout+time.Second)
		defer cancel()
	}
	values := catalogValues(q)
	if q.Match != "" {
		values.Add("match[]", q.Match)
	}
	payload, err := fetchMetrics(ctx, q.BaseURL, "/prometheus/api/v1/labels", values)
	if err != nil {
		return MetricsLabelsResult{}, err
	}
	for _, item := range payload.Data.Strings {
		result.Labels = append(result.Labels, item)
		if q.Limit > 0 && len(result.Labels) >= q.Limit {
			break
		}
	}
	sort.Strings(result.Labels)
	return result, nil
}

func MetricsSeries(ctx context.Context, q MetricsCatalogQuery) (MetricsSeriesResult, error) {
	result := MetricsSeriesResult{
		SchemaVersion: MetricsSeriesSchema,
		Scope:         q.Scope,
		Backend:       metricsBackend(q.BaseURL),
		Query:         metricsCatalogRecord(q),
		Warnings:      append([]string(nil), q.Warnings...),
		Series:        []map[string]string{},
	}
	if strings.TrimSpace(q.BaseURL) == "" {
		result.Warnings = append(result.Warnings, "VictoriaMetrics is unavailable")
		return result, nil
	}
	if q.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, q.Timeout+time.Second)
		defer cancel()
	}
	values := catalogValues(q)
	if q.Match != "" {
		values.Add("match[]", q.Match)
	}
	payload, err := fetchMetrics(ctx, q.BaseURL, "/prometheus/api/v1/series", values)
	if err != nil {
		return MetricsSeriesResult{}, err
	}
	for _, item := range payload.Data.Series {
		result.Series = append(result.Series, item)
		if q.Limit > 0 && len(result.Series) >= q.Limit {
			break
		}
	}
	return result, nil
}

func logsBackend(baseURL string) QueryBackend {
	return QueryBackend{Kind: "victorialogs", Dialect: "LogsQL", BaseURL: baseURL, Ready: strings.TrimSpace(baseURL) != "", QueryPath: "/select/logsql/query", TailPath: "/select/logsql/tail"}
}

func metricsBackend(baseURL string) QueryBackend {
	return QueryBackend{Kind: "victoriametrics", Dialect: "PromQL/MetricsQL", BaseURL: baseURL, Ready: strings.TrimSpace(baseURL) != "", QueryPath: "/prometheus/api/v1/query_range"}
}

func logValues(q LogsQuery) url.Values {
	values := url.Values{}
	values.Set("query", q.Query)
	values.Set("start", formatVictoriaTime(q.Bounds.Start))
	values.Set("end", formatVictoriaTime(q.Bounds.End))
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Timeout > 0 {
		values.Set("timeout", q.Timeout.String())
	}
	for _, filter := range logScopeFilters(q.Scope) {
		values.Add("extra_filters", filter)
	}
	return values
}

func logTailValues(q LogsQuery) url.Values {
	values := url.Values{}
	values.Set("query", q.Query)
	if q.Bounds.Since != "" {
		values.Set("start_offset", q.Bounds.Since)
	}
	if q.Timeout > 0 {
		values.Set("timeout", q.Timeout.String())
	}
	for _, filter := range logScopeFilters(q.Scope) {
		values.Add("extra_filters", filter)
	}
	return values
}

func logScopeFilters(scope QueryScope) []string {
	var out []string
	if scope.AppID != "" {
		out = append(out, fmt.Sprintf(`(onlava.application_id:%s OR onlava_app_id:%s)`, logsQLQuote(scope.AppID), logsQLQuote(scope.AppID)))
	}
	if scope.SessionID != "" {
		out = append(out, fmt.Sprintf(`(onlava.session_id:%s OR onlava_session_id:%s)`, logsQLQuote(scope.SessionID), logsQLQuote(scope.SessionID)))
	}
	return out
}

func logsQLQuote(value string) string {
	return strconv.Quote(value)
}

func fetchLogs(ctx context.Context, baseURL, path string, values url.Values) ([]map[string]any, error) {
	var rows []map[string]any
	err := streamLogs(ctx, baseURL, path, values, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	return rows, err
}

func streamLogs(ctx context.Context, baseURL, path string, values url.Values, emit func(map[string]any) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if detail := strings.TrimSpace(string(data)); detail != "" {
			return fmt.Errorf("VictoriaLogs query failed: %s: %s", resp.Status, detail)
		}
		return fmt.Errorf("VictoriaLogs query failed: %s", resp.Status)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row map[string]any
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&row); err != nil {
			return err
		}
		if err := emit(row); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func normalizeLogRows(rows []map[string]any, fields []string, limit int) []LogEntry {
	out := make([]LogEntry, 0, len(rows))
	for _, row := range rows {
		entry := LogEntry{
			Time:    firstRowString(row, "_time", "time", "created_at"),
			Level:   strings.ToLower(firstRowString(row, "level", "severityText", "severity_text")),
			Source:  firstRowString(row, "source_id", "source", "service.name"),
			Message: firstRowString(row, "_msg", "message", "body"),
			TraceID: firstRowString(row, "trace_id", "traceId", "traceID"),
			SpanID:  firstRowString(row, "span_id", "spanId", "spanID"),
			Raw:     selectedRaw(row, fields),
		}
		if entry.Message == "" {
			entry.Message = firstRowString(row, "raw")
		}
		if rawFields := firstRowString(row, "fields_json"); rawFields != "" {
			var fields map[string]any
			if json.Unmarshal([]byte(rawFields), &fields) == nil && len(fields) > 0 {
				entry.Fields = fields
			}
		}
		if len(entry.Raw) == 0 {
			entry.Raw = nil
		}
		out = append(out, entry)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func selectedRaw(row map[string]any, fields []string) map[string]any {
	if len(fields) == 0 {
		raw := make(map[string]any, len(row))
		for key, value := range row {
			raw[key] = value
		}
		return raw
	}
	raw := make(map[string]any)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if value, ok := row[field]; ok {
			raw[field] = value
		}
	}
	return raw
}

func firstRowString(row map[string]any, keys ...string) string {
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
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		default:
			return fmt.Sprint(v)
		}
	}
	return ""
}

func addMetricScope(values url.Values, scope QueryScope) {
	if scope.AppID != "" {
		values.Add("extra_label", "onlava_app="+scope.AppID)
	}
	if scope.SessionID != "" {
		values.Add("extra_label", "onlava_session_id="+scope.SessionID)
	}
	if scope.AppRootHash != "" {
		values.Add("extra_label", "onlava_app_root_hash="+scope.AppRootHash)
	}
}

func catalogValues(q MetricsCatalogQuery) url.Values {
	values := url.Values{}
	values.Set("start", formatVictoriaTime(q.Bounds.Start))
	values.Set("end", formatVictoriaTime(q.Bounds.End))
	addMetricScope(values, q.Scope)
	return values
}

func fetchMetrics(ctx context.Context, baseURL, path string, values url.Values) (victoriaMetricsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, strings.NewReader(values.Encode()))
	if err != nil {
		return victoriaMetricsResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return victoriaMetricsResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if detail := strings.TrimSpace(string(data)); detail != "" {
			return victoriaMetricsResponse{}, fmt.Errorf("VictoriaMetrics query failed: %s: %s", resp.Status, detail)
		}
		return victoriaMetricsResponse{}, fmt.Errorf("VictoriaMetrics query failed: %s", resp.Status)
	}
	var payload victoriaMetricsResponse
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return victoriaMetricsResponse{}, err
	}
	if payload.Status != "" && payload.Status != "success" {
		return victoriaMetricsResponse{}, fmt.Errorf("VictoriaMetrics query failed: status=%s error=%s", payload.Status, payload.Error)
	}
	return payload, nil
}

type victoriaMetricsResponse struct {
	Status string              `json:"status"`
	Error  string              `json:"error,omitempty"`
	Data   victoriaMetricsData `json:"data"`
}

type victoriaMetricsData struct {
	ResultType string
	Result     []victoriaMetricResult
	Strings    []string
	Series     []map[string]string
}

func (d *victoriaMetricsData) UnmarshalJSON(data []byte) error {
	var raw struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &raw); err == nil && (raw.ResultType != "" || len(raw.Result) > 0) {
		d.ResultType = raw.ResultType
		if len(raw.Result) > 0 && string(raw.Result) != "null" {
			_ = json.Unmarshal(raw.Result, &d.Result)
		}
		return nil
	}
	if err := json.Unmarshal(data, &d.Strings); err == nil {
		return nil
	}
	return json.Unmarshal(data, &d.Series)
}

type victoriaMetricResult struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
	Values [][]any           `json:"values"`
}

func normalizeMetricResults(items []victoriaMetricResult, limit int) []MetricSeries {
	out := make([]MetricSeries, 0, len(items))
	for _, item := range items {
		series := MetricSeries{Metric: item.Metric}
		if sample := metricSample(item.Value); sample != nil {
			series.Value = sample
		}
		for _, raw := range item.Values {
			if sample := metricSample(raw); sample != nil {
				series.Values = append(series.Values, *sample)
			}
		}
		out = append(out, series)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func metricSample(raw []any) *MetricSample {
	if len(raw) < 2 {
		return nil
	}
	timestamp, ok := numberAsFloat(raw[0])
	if !ok {
		return nil
	}
	return &MetricSample{
		Time:  time.Unix(0, int64(timestamp*1e9)).UTC().Format(time.RFC3339Nano),
		Value: fmt.Sprint(raw[1]),
	}
}

func numberAsFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	case string:
		n, err := strconv.ParseFloat(v, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func logsQueryRecord(q LogsQuery) LogsQueryRecord {
	return LogsQueryRecord{
		Query:   q.Query,
		Since:   q.Bounds.Since,
		Start:   q.Bounds.Start.UTC().Format(time.RFC3339Nano),
		End:     q.Bounds.End.UTC().Format(time.RFC3339Nano),
		Limit:   q.Limit,
		Timeout: durationString(q.Timeout),
		Fields:  q.Fields,
	}
}

func metricsQueryRecord(q MetricsQuery) MetricsQueryRecord {
	mode := "range"
	if q.Instant {
		mode = "instant"
	}
	record := MetricsQueryRecord{
		PromQL:  q.PromQL,
		Mode:    mode,
		Since:   q.Bounds.Since,
		End:     q.Bounds.End.UTC().Format(time.RFC3339Nano),
		Timeout: durationString(q.Timeout),
		Limit:   q.Limit,
	}
	if q.Instant {
		return record
	}
	record.Start = q.Bounds.Start.UTC().Format(time.RFC3339Nano)
	record.Step = q.Step.String()
	return record
}

func metricsCatalogRecord(q MetricsCatalogQuery) MetricsCatalogRecord {
	return MetricsCatalogRecord{
		Match:   q.Match,
		Since:   q.Bounds.Since,
		Start:   q.Bounds.Start.UTC().Format(time.RFC3339Nano),
		End:     q.Bounds.End.UTC().Format(time.RFC3339Nano),
		Timeout: durationString(q.Timeout),
		Limit:   q.Limit,
	}
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

func formatVictoriaTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
