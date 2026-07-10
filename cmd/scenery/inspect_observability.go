package main

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	inspectdata "scenery.sh/internal/inspect"
	obs "scenery.sh/internal/observability"
)

const (
	inspectTracesSchema  = "scenery.inspect.traces.v1"
	inspectMetricsSchema = "scenery.inspect.metrics.v1"
)

type inspectTraceQueryOptions struct {
	Limit         int
	Since         time.Duration
	Service       string
	Endpoint      string
	TraceID       string
	Session       string
	Status        string
	MinDurationMS float64
	Slowest       bool
}

type inspectTracesResponse struct {
	SchemaVersion string                  `json:"schema_version"`
	App           inspectdata.AppRef      `json:"app"`
	Query         inspectTraceQueryRecord `json:"query"`
	Warnings      []string                `json:"warnings,omitempty"`
	Traces        []inspectTraceRecord    `json:"traces"`
}

type inspectMetricsResponse struct {
	SchemaVersion string                       `json:"schema_version"`
	App           inspectdata.AppRef           `json:"app"`
	Query         inspectTraceQueryRecord      `json:"query"`
	Warnings      []string                     `json:"warnings,omitempty"`
	Summary       inspectMetricsSummary        `json:"summary"`
	Services      []inspectTraceMetric         `json:"services"`
	Endpoints     []inspectTraceMetric         `json:"endpoints"`
	Logs          []devdash.LogLevelCount      `json:"logs"`
	Meta          inspectObservabilityMetaInfo `json:"meta"`
}

type inspectTraceQueryRecord struct {
	AppID            string   `json:"app_id"`
	SessionID        string   `json:"session_id,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	Since            string   `json:"since,omitempty"`
	SinceTimestamp   string   `json:"since_timestamp,omitempty"`
	Service          string   `json:"service,omitempty"`
	Endpoint         string   `json:"endpoint,omitempty"`
	TraceID          string   `json:"trace_id,omitempty"`
	Status           string   `json:"status,omitempty"`
	MinDurationMS    *float64 `json:"min_duration_ms,omitempty"`
	Sort             string   `json:"sort,omitempty"`
	AvailableFilters []string `json:"available_filters"`
}

type inspectTraceRecord struct {
	TraceID           string  `json:"trace_id"`
	SpanID            string  `json:"span_id"`
	SessionID         string  `json:"session_id,omitempty"`
	AppRootHash       string  `json:"app_root_hash,omitempty"`
	Branch            string  `json:"branch,omitempty"`
	Worktree          string  `json:"worktree,omitempty"`
	Kind              string  `json:"kind"`
	Status            string  `json:"status"`
	Service           string  `json:"service,omitempty"`
	Endpoint          string  `json:"endpoint,omitempty"`
	Topic             string  `json:"topic,omitempty"`
	Subscription      string  `json:"subscription,omitempty"`
	MessageID         string  `json:"message_id,omitempty"`
	StartedAt         string  `json:"started_at"`
	DurationMS        float64 `json:"duration_ms"`
	DurationNanos     uint64  `json:"duration_nanos"`
	ServiceCatalogURL string  `json:"service_catalog_url,omitempty"`
}

type inspectMetricsSummary struct {
	TraceCount        int     `json:"trace_count"`
	ErrorCount        int     `json:"error_count"`
	ErrorRate         float64 `json:"error_rate"`
	EventCount        int64   `json:"event_count"`
	LogCount          int64   `json:"log_count"`
	AvgDurationMS     float64 `json:"avg_duration_ms"`
	MinDurationMS     float64 `json:"min_duration_ms"`
	MaxDurationMS     float64 `json:"max_duration_ms"`
	P50DurationMS     float64 `json:"p50_duration_ms"`
	P95DurationMS     float64 `json:"p95_duration_ms"`
	SlowestTraceID    string  `json:"slowest_trace_id,omitempty"`
	SlowestEndpoint   string  `json:"slowest_endpoint,omitempty"`
	SlowestService    string  `json:"slowest_service,omitempty"`
	SlowestDurationMS float64 `json:"slowest_duration_ms,omitempty"`
}

type inspectTraceMetric struct {
	Service       string  `json:"service,omitempty"`
	Endpoint      string  `json:"endpoint,omitempty"`
	Kind          string  `json:"kind,omitempty"`
	Count         int     `json:"count"`
	ErrorCount    int     `json:"error_count"`
	ErrorRate     float64 `json:"error_rate"`
	AvgDurationMS float64 `json:"avg_duration_ms"`
	MinDurationMS float64 `json:"min_duration_ms"`
	MaxDurationMS float64 `json:"max_duration_ms"`
	P50DurationMS float64 `json:"p50_duration_ms"`
	P95DurationMS float64 `json:"p95_duration_ms"`
}

type inspectObservabilityMetaInfo struct {
	TraceMetricLimit int `json:"trace_metric_limit"`
}

type traceMetricBucket struct {
	Service    string
	Endpoint   string
	Kind       string
	Count      int
	ErrorCount int
	Durations  []uint64
	Total      uint64
}

func buildInspectTracesResponse(ctx context.Context, appRoot string, cfg appcfg.Config, opts inspectTraceQueryOptions) (inspectTracesResponse, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	appID := cfg.AppID()
	sessionID, err := resolveInspectSessionID(ctx, opts.Session, appRoot)
	if err != nil {
		return inspectTracesResponse{}, err
	}
	opts.Session = sessionID
	resp := inspectTracesResponse{
		SchemaVersion: inspectTracesSchema,
		App:           inspectAppInfo(appRoot, cfg, nil),
		Query:         buildInspectTraceQueryRecord(appID, opts),
		Traces:        []inspectTraceRecord{},
	}

	store, warnings, err := openObservabilityStore(ctx, appRoot, cfg, sessionID)
	if err != nil {
		return inspectTracesResponse{}, err
	}
	defer store.Close()
	resp.Warnings = warnings

	query := inspectTraceQuery(appID, opts)
	items, warning, err := queryVictoriaTraceSummaries(ctx, query)
	if err != nil {
		return inspectTracesResponse{}, err
	}
	if warning != "" {
		resp.Warnings = append(resp.Warnings, warning)
	}
	if opts.Slowest {
		devdash.SortTraceSummariesByDuration(items)
	}
	for _, item := range items {
		resp.Traces = append(resp.Traces, inspectTraceRecordFromSummary(item))
	}
	return resp, nil
}

func buildInspectMetricsResponse(ctx context.Context, appRoot string, cfg appcfg.Config, opts inspectTraceQueryOptions) (inspectMetricsResponse, error) {
	if opts.Since == 0 {
		opts.Since = 24 * time.Hour
	}
	if opts.Limit <= 0 {
		opts.Limit = 10000
	}
	appID := cfg.AppID()
	sessionID, err := resolveInspectSessionID(ctx, opts.Session, appRoot)
	if err != nil {
		return inspectMetricsResponse{}, err
	}
	opts.Session = sessionID
	resp := inspectMetricsResponse{
		SchemaVersion: inspectMetricsSchema,
		App:           inspectAppInfo(appRoot, cfg, nil),
		Query:         buildInspectTraceQueryRecord(appID, opts),
		Services:      []inspectTraceMetric{},
		Endpoints:     []inspectTraceMetric{},
		Logs:          []devdash.LogLevelCount{},
		Meta: inspectObservabilityMetaInfo{
			TraceMetricLimit: opts.Limit,
		},
	}

	store, warnings, err := openObservabilityStore(ctx, appRoot, cfg, sessionID)
	if err != nil {
		return inspectMetricsResponse{}, err
	}
	defer store.Close()
	resp.Warnings = warnings

	query := inspectTraceQuery(appID, opts)
	items, warning, err := queryVictoriaTraceSummaries(ctx, query)
	if err != nil {
		return inspectMetricsResponse{}, err
	}
	if warning != "" {
		resp.Warnings = append(resp.Warnings, warning)
	}
	resp.Summary = buildInspectMetricsSummary(items)
	resp.Services = buildInspectTraceMetrics(items, "service")
	resp.Endpoints = buildInspectTraceMetrics(items, "endpoint")
	resp.Warnings = append(resp.Warnings, "trace event counts are not materialized after the devdash JSON observability cutover")
	logs, logWarning, err := queryVictoriaLogCounts(ctx, appID, query.SessionID, opts.Since)
	if err != nil {
		return inspectMetricsResponse{}, err
	}
	if logWarning != "" {
		resp.Warnings = append(resp.Warnings, logWarning)
	}
	resp.Logs = logs
	for _, item := range logs {
		resp.Summary.LogCount += item.Count
	}
	return resp, nil
}

func openObservabilityStore(ctx context.Context, appRoot string, cfg appcfg.Config, sessionID string) (*devdash.Store, []string, error) {
	store, err := openDevdashStore()
	if err != nil {
		return nil, nil, err
	}
	appID := cfg.AppID()
	var warnings []string
	record, sessionRecord, err := devdashAppRecordForRuntime(ctx, store, appID, sessionID, "")
	if err != nil {
		if err == sql.ErrNoRows {
			warnings = append(warnings, "no local observability state found for "+appID+"; run `scenery up` first")
			return store, warnings, nil
		}
		_ = store.Close()
		return nil, nil, err
	}
	if !sessionRecord && record.Root != "" && record.Root != appRoot {
		warnings = append(warnings, "local observability state for "+appID+" belongs to "+record.Root+", not "+appRoot)
	}
	return store, warnings, nil
}

func inspectTraceQuery(appID string, opts inspectTraceQueryOptions) devdash.TraceQuery {
	query := devdash.TraceQuery{
		AppID:        appID,
		SessionID:    opts.Session,
		TraceID:      opts.TraceID,
		ServiceName:  opts.Service,
		EndpointName: opts.Endpoint,
		Status:       opts.Status,
		Limit:        opts.Limit,
	}
	if opts.Since > 0 {
		query.Since = time.Now().UTC().Add(-opts.Since)
	}
	if opts.MinDurationMS > 0 {
		query.MinDurationNanos = uint64(opts.MinDurationMS * float64(time.Millisecond))
	}
	return query
}

func queryVictoriaTraceSummaries(ctx context.Context, query devdash.TraceQuery) ([]*devdash.TraceSummary, string, error) {
	stack := defaultVictoriaQueryStack()
	if stack == nil {
		return nil, "VictoriaTraces is unavailable", nil
	}
	items, err := stack.QueryTraceSummaries(ctx, query)
	if err != nil {
		return nil, "VictoriaTraces query failed: " + err.Error(), nil
	}
	return items, "", nil
}

func queryVictoriaLogCounts(ctx context.Context, appID, sessionID string, since time.Duration) ([]devdash.LogLevelCount, string, error) {
	stack := defaultVictoriaQueryStack()
	if stack == nil || stack.BaseURL("logs") == "" {
		return nil, "VictoriaLogs is unavailable", nil
	}
	result, err := obs.QueryLogs(ctx, obs.LogsQuery{
		BaseURL: stack.BaseURL("logs"),
		Scope: obs.QueryScope{
			AppID:     appID,
			SessionID: sessionID,
			Enforced:  true,
		},
		Query:   "*",
		Bounds:  queryBounds(since, since.String(), time.Time{}, time.Time{}),
		Limit:   2000,
		Timeout: 3 * time.Second,
		Fields:  []string{"level", "severityText", "severity_text"},
	})
	if err != nil {
		return nil, "VictoriaLogs query failed: " + err.Error(), nil
	}
	counts := map[string]int64{}
	for _, entry := range result.Logs {
		level := strings.TrimSpace(entry.Level)
		if level == "" {
			level = "unknown"
		}
		counts[level]++
	}
	items := make([]devdash.LogLevelCount, 0, len(counts))
	for level, count := range counts {
		items = append(items, devdash.LogLevelCount{Level: level, Count: count})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Level < items[j].Level })
	return items, "", nil
}

func buildInspectTraceQueryRecord(appID string, opts inspectTraceQueryOptions) inspectTraceQueryRecord {
	record := inspectTraceQueryRecord{
		AppID:     appID,
		SessionID: opts.Session,
		Limit:     opts.Limit,
		Service:   opts.Service,
		Endpoint:  opts.Endpoint,
		TraceID:   opts.TraceID,
		Status:    opts.Status,
		Sort:      "started_at_desc",
		AvailableFilters: []string{
			"--app-root",
			"--service",
			"--endpoint",
			"--trace-id",
			"--status ok|error",
			"--min-duration-ms",
			"--since",
			"--limit",
			"--slowest",
		},
	}
	if opts.Since > 0 {
		record.Since = opts.Since.String()
		record.SinceTimestamp = time.Now().UTC().Add(-opts.Since).Format(time.RFC3339Nano)
	}
	if opts.MinDurationMS > 0 {
		value := opts.MinDurationMS
		record.MinDurationMS = &value
	}
	if opts.Slowest {
		record.Sort = "duration_desc"
	}
	return record
}

func inspectTraceRecordFromSummary(summary *devdash.TraceSummary) inspectTraceRecord {
	record := inspectTraceRecord{
		TraceID:       summary.TraceID,
		SpanID:        summary.SpanID,
		SessionID:     summary.SessionID,
		AppRootHash:   summary.AppRootHash,
		Branch:        summary.Branch,
		Worktree:      summary.Worktree,
		Kind:          summary.Type,
		Status:        "ok",
		Service:       summary.ServiceName,
		StartedAt:     summary.StartedAt.UTC().Format(time.RFC3339Nano),
		DurationMS:    durationMS(summary.DurationNanos),
		DurationNanos: summary.DurationNanos,
	}
	if summary.IsError {
		record.Status = "error"
	}
	if summary.EndpointName != nil {
		record.Endpoint = *summary.EndpointName
	}
	if summary.MessageID != nil {
		record.MessageID = *summary.MessageID
	}
	return record
}

func buildInspectMetricsSummary(items []*devdash.TraceSummary) inspectMetricsSummary {
	summary := inspectMetricsSummary{
		TraceCount: len(items),
	}
	if len(items) == 0 {
		return summary
	}
	durations := make([]uint64, 0, len(items))
	var total uint64
	for _, item := range items {
		if item.IsError {
			summary.ErrorCount++
		}
		if summary.SlowestTraceID == "" || item.DurationNanos > uint64(summary.SlowestDurationMS*float64(time.Millisecond)) {
			summary.SlowestTraceID = item.TraceID
			summary.SlowestService = item.ServiceName
			summary.SlowestDurationMS = durationMS(item.DurationNanos)
			if item.EndpointName != nil {
				summary.SlowestEndpoint = *item.EndpointName
			}
		}
		total += item.DurationNanos
		durations = append(durations, item.DurationNanos)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	summary.ErrorRate = float64(summary.ErrorCount) / float64(summary.TraceCount)
	summary.AvgDurationMS = durationMS(total / uint64(len(items)))
	summary.MinDurationMS = durationMS(durations[0])
	summary.MaxDurationMS = durationMS(durations[len(durations)-1])
	summary.P50DurationMS = durationMS(percentileDuration(durations, 0.50))
	summary.P95DurationMS = durationMS(percentileDuration(durations, 0.95))
	return summary
}

func buildInspectTraceMetrics(items []*devdash.TraceSummary, mode string) []inspectTraceMetric {
	buckets := make(map[string]*traceMetricBucket)
	for _, item := range items {
		endpoint := ""
		if item.EndpointName != nil {
			endpoint = *item.EndpointName
		}
		service := item.ServiceName
		key := service
		if mode == "endpoint" {
			key = service + "\x00" + endpoint + "\x00" + item.Type
		}
		bucket := buckets[key]
		if bucket == nil {
			bucket = &traceMetricBucket{
				Service:  service,
				Kind:     item.Type,
				Endpoint: endpoint,
			}
			buckets[key] = bucket
		}
		bucket.Count++
		if item.IsError {
			bucket.ErrorCount++
		}
		bucket.Total += item.DurationNanos
		bucket.Durations = append(bucket.Durations, item.DurationNanos)
	}

	metrics := make([]inspectTraceMetric, 0, len(buckets))
	for _, bucket := range buckets {
		sort.Slice(bucket.Durations, func(i, j int) bool { return bucket.Durations[i] < bucket.Durations[j] })
		item := inspectTraceMetric{
			Service:       bucket.Service,
			Kind:          bucket.Kind,
			Count:         bucket.Count,
			ErrorCount:    bucket.ErrorCount,
			ErrorRate:     float64(bucket.ErrorCount) / float64(bucket.Count),
			AvgDurationMS: durationMS(bucket.Total / uint64(bucket.Count)),
			MinDurationMS: durationMS(bucket.Durations[0]),
			MaxDurationMS: durationMS(bucket.Durations[len(bucket.Durations)-1]),
			P50DurationMS: durationMS(percentileDuration(bucket.Durations, 0.50)),
			P95DurationMS: durationMS(percentileDuration(bucket.Durations, 0.95)),
		}
		if mode == "endpoint" {
			item.Endpoint = bucket.Endpoint
		}
		metrics = append(metrics, item)
	}
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].Count == metrics[j].Count {
			if metrics[i].Service == metrics[j].Service {
				return metrics[i].Endpoint < metrics[j].Endpoint
			}
			return metrics[i].Service < metrics[j].Service
		}
		return metrics[i].Count > metrics[j].Count
	})
	return metrics
}

func parseInspectTraceFlags(opts *inspectOptions, flag, value string) error {
	switch flag {
	case "--limit", "-n":
		limit, err := strconv.Atoi(value)
		if err != nil || limit <= 0 {
			return fmt.Errorf("invalid limit %q", value)
		}
		opts.Trace.Limit = limit
	case "--since":
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return fmt.Errorf("invalid since duration %q", value)
		}
		opts.Trace.Since = duration
	case "--service":
		opts.Trace.Service = value
	case "--endpoint":
		opts.Trace.Endpoint = value
	case "--trace-id":
		opts.Trace.TraceID = value
	case "--session":
		opts.Trace.Session = strings.TrimSpace(value)
		if opts.Trace.Session == "" {
			return fmt.Errorf("invalid session %q", value)
		}
	case "--status":
		switch strings.ToLower(value) {
		case "ok", "error":
			opts.Trace.Status = strings.ToLower(value)
		default:
			return fmt.Errorf("invalid status %q", value)
		}
	case "--min-duration-ms":
		ms, err := strconv.ParseFloat(value, 64)
		if err != nil || ms < 0 {
			return fmt.Errorf("invalid min duration %q", value)
		}
		opts.Trace.MinDurationMS = ms
	default:
		return fmt.Errorf("unknown flag %q", flag)
	}
	return nil
}

func resolveInspectSessionID(ctx context.Context, value, appRoot string) (string, error) {
	return resolveLogsSessionID(ctx, value, appRoot)
}

func percentileDuration(sorted []uint64, p float64) uint64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func durationMS(nanos uint64) float64 {
	return float64(nanos) / float64(time.Millisecond)
}
