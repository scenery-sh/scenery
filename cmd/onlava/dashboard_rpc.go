package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/internal/devdash"
)

func (s *dashboardServer) handleRPC(ctx context.Context, req rpcRequest) rpcResponse {
	result, err := s.dispatchRPC(ctx, req.Method, req.Params)
	if err != nil {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32000,
				Message: err.Error(),
			},
		}
	}
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
}

func (s *dashboardServer) dispatchRPC(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "version":
		return map[string]any{"version": onlavaDashboardCompatVersion, "channel": onlavaDashboardCompatChannel}, nil
	case "list-apps":
		return s.supervisor.listApps(ctx)
	case "status":
		var params struct {
			AppID string `json:"app_id"`
		}
		_ = json.Unmarshal(raw, &params)
		return s.supervisor.statusFor(ctx, firstNonEmpty(params.AppID, s.supervisor.activeAppID()))
	case "process/output/list":
		var params struct {
			AppID string `json:"app_id"`
			Limit int    `json:"limit"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		return s.supervisor.store.ListProcessOutput(ctx, params.AppID, params.Limit)
	case "traces/clear":
		var params struct {
			AppID string `json:"app_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		if err := s.supervisor.store.ClearTraces(ctx, params.AppID); err != nil {
			return nil, err
		}
		s.supervisor.victoria.MarkCleared(params.AppID, time.Now().UTC())
		return "ok", nil
	case "traces/list":
		var params struct {
			AppID     string `json:"app_id"`
			MessageID string `json:"message_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		return s.listTraceSummaries(ctx, params.AppID, 100, params.MessageID)
	case "traces/get":
		var params struct {
			AppID   string `json:"app_id"`
			TraceID string `json:"trace_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		return s.traceEventsFor(ctx, params.AppID, params.TraceID)
	case "traces/spans/summaries/list":
		var params struct {
			AppID   string `json:"app_id"`
			TraceID string `json:"trace_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		return s.getTraceSummaries(ctx, params.AppID, params.TraceID)
	case "traces/spans/events/list":
		var params struct {
			AppID   string `json:"app_id"`
			TraceID string `json:"trace_id"`
			SpanID  string `json:"span_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		return s.traceEventsForSpan(ctx, params.AppID, params.TraceID, params.SpanID)
	case "api-call":
		var params devdash.APICallRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.apiCall(ctx, params)
	case "db/query":
		var params devdash.QueryRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.queryDB(ctx, params)
	case "db/transaction":
		var params devdash.TransactionRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.transactionDB(ctx, params)
	case "data/inspect":
		var params dataInspectRPCRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.inspectData(ctx, params)
	case "data/query-records":
		var params dataQueryRecordsRPCRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.queryDataRecords(ctx, params)
	case "data/outbox-events":
		var params dataOutboxEventsRPCRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.dataOutboxEvents(ctx, params)
	case "db-migration-status":
		return []any{}, nil
	case "editors/list":
		return map[string]any{"editors": listEditors()}, nil
	case "editors/open":
		var params struct {
			AppID     string `json:"app_id"`
			Editor    string `json:"editor"`
			File      string `json:"file"`
			StartLine int    `json:"start_line"`
			StartCol  int    `json:"start_col"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return map[string]any{}, openEditor(s.supervisor.root, params.Editor, params.File, params.StartLine, params.StartCol)
	case "onboarding/get":
		return s.supervisor.store.GetOnboarding(ctx)
	case "onboarding/set":
		var params struct {
			Properties []string `json:"properties"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return nil, s.supervisor.store.SetOnboarding(ctx, params.Properties)
	case "telemetry":
		return "ok", nil
	default:
		if strings.HasPrefix(method, "ai/") {
			return nil, fmt.Errorf("%s is unsupported in onlava", method)
		}
		return nil, fmt.Errorf("method not found: %s", method)
	}
}
