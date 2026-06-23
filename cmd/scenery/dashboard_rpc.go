package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"scenery.sh/internal/devdash"
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
		return map[string]any{"version": sceneryDashboardCompatVersion, "channel": sceneryDashboardCompatChannel}, nil
	case "list-apps":
		return s.dashboardListApps(ctx)
	case "status":
		var params struct {
			AppID string `json:"app_id"`
		}
		_ = json.Unmarshal(raw, &params)
		return s.dashboardStatusFor(ctx, firstNonEmpty(params.AppID, s.dashboardActiveAppID()))
	case "process/output/list":
		var params struct {
			AppID string `json:"app_id"`
			Limit int    `json:"limit"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.dashboardActiveAppID()
		}
		status, err := s.dashboardStatusFor(ctx, params.AppID)
		if err != nil {
			return s.dashboardStore().ListProcessOutput(ctx, params.AppID, params.Limit)
		}
		return s.dashboardStore().ListProcessOutputForSession(ctx, dashboardStoreAppID(status), status.SessionID, params.Limit)
	case "traces/clear":
		var params struct {
			AppID string `json:"app_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.dashboardActiveAppID()
		}
		status, err := s.dashboardStatusFor(ctx, params.AppID)
		if err != nil {
			return nil, err
		}
		if victoria := s.dashboardVictoria(); victoria != nil {
			victoria.MarkCleared(dashboardStoreAppID(status), time.Now().UTC())
		}
		return "ok", nil
	case "traces/list":
		var params struct {
			AppID     string `json:"app_id"`
			MessageID string `json:"message_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.dashboardActiveAppID()
		}
		status, err := s.dashboardStatusFor(ctx, params.AppID)
		if err != nil {
			return nil, err
		}
		return s.listTraceSummaries(ctx, dashboardStoreAppID(status), status.SessionID, 100, params.MessageID)
	case "traces/get":
		var params struct {
			AppID   string `json:"app_id"`
			TraceID string `json:"trace_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.dashboardActiveAppID()
		}
		status, err := s.dashboardStatusFor(ctx, params.AppID)
		if err != nil {
			return nil, err
		}
		return s.traceEventsFor(ctx, dashboardStoreAppID(status), status.SessionID, params.TraceID)
	case "traces/spans/summaries/list":
		var params struct {
			AppID   string `json:"app_id"`
			TraceID string `json:"trace_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.dashboardActiveAppID()
		}
		status, err := s.dashboardStatusFor(ctx, params.AppID)
		if err != nil {
			return nil, err
		}
		return s.getTraceSummaries(ctx, dashboardStoreAppID(status), status.SessionID, params.TraceID)
	case "traces/spans/events/list":
		var params struct {
			AppID   string `json:"app_id"`
			TraceID string `json:"trace_id"`
			SpanID  string `json:"span_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.dashboardActiveAppID()
		}
		status, err := s.dashboardStatusFor(ctx, params.AppID)
		if err != nil {
			return nil, err
		}
		return s.traceEventsForSpan(ctx, dashboardStoreAppID(status), status.SessionID, params.TraceID, params.SpanID)
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
		root, err := s.dashboardRootForApp(ctx, params.AppID)
		if err != nil {
			return nil, err
		}
		return map[string]any{}, openEditor(root, params.Editor, params.File, params.StartLine, params.StartCol)
	case "onboarding/get":
		return s.dashboardStore().GetOnboarding(ctx)
	case "onboarding/set":
		var params struct {
			Properties []string `json:"properties"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return nil, s.dashboardStore().SetOnboarding(ctx, params.Properties)
	case "telemetry":
		return "ok", nil
	default:
		if strings.HasPrefix(method, "ai/") {
			return nil, fmt.Errorf("%s is unsupported in scenery", method)
		}
		return nil, fmt.Errorf("method not found: %s", method)
	}
}
