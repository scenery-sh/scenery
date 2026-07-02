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
	if strings.HasPrefix(method, "symphony/") {
		return s.dispatchSymphonyRPC(ctx, method, raw)
	}
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
	case "logs/list":
		var params struct {
			AppID    string `json:"app_id"`
			Limit    int    `json:"limit"`
			AfterID  int64  `json:"after_id"`
			SourceID string `json:"source_id"`
			Kind     string `json:"kind"`
			Level    string `json:"level"`
			Stream   string `json:"stream"`
			Grep     string `json:"grep"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.dashboardActiveAppID()
		}
		status, err := s.dashboardStatusFor(ctx, params.AppID)
		if err != nil {
			status = devdash.AppStatus{AppID: params.AppID}
		}
		victoria := s.dashboardVictoria()
		if victoria == nil {
			return []dashboardLogEvent{}, nil
		}
		items, err := victoria.ListDevEvents(ctx, devdash.DevEventQuery{
			AppID:     dashboardStoreAppID(status),
			SessionID: status.SessionID,
			SourceID:  params.SourceID,
			Kind:      params.Kind,
			Level:     params.Level,
			Stream:    firstNonEmpty(params.Stream, "all"),
			Grep:      params.Grep,
			AfterID:   params.AfterID,
			Limit:     params.Limit,
		})
		if err != nil {
			return nil, err
		}
		return dashboardLogEventsFromDevEvents(items), nil
	case "observability/status":
		var params struct {
			AppID string `json:"app_id"`
		}
		_ = json.Unmarshal(raw, &params)
		return s.observabilityStatus(ctx, firstNonEmpty(params.AppID, s.dashboardActiveAppID()))
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
	case "sqlite/tables":
		var params dashboardSQLiteRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.sqliteTables(ctx, params)
	case "sqlite/schema":
		var params dashboardSQLiteRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.sqliteSchema(ctx, params)
	case "sqlite/rows":
		var params dashboardSQLiteRowsRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.sqliteRows(ctx, params)
	case "api-call":
		var params devdash.APICallRequest
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.apiCall(ctx, params)
	case "stored-requests/list":
		var params struct {
			AppID string `json:"app_id"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return s.listStoredRequests(ctx, firstNonEmpty(params.AppID, s.dashboardActiveAppID()))
	case "stored-requests/create":
		var params storedRequestRPCParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		created, err := s.createStoredRequest(ctx, params)
		if err != nil {
			return nil, err
		}
		return created.ID, nil
	case "stored-requests/update":
		var params storedRequestRPCParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		updated, err := s.updateStoredRequest(ctx, params)
		if err != nil {
			return nil, err
		}
		return updated.ID, nil
	case "stored-requests/delete":
		var params struct {
			AppID string `json:"app_id"`
			ID    string `json:"id"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		if err := s.deleteStoredRequest(ctx, firstNonEmpty(params.AppID, s.dashboardActiveAppID()), params.ID); err != nil {
			return nil, err
		}
		return true, nil
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

type dashboardLogEvent struct {
	ID        int64                 `json:"id"`
	Time      string                `json:"time"`
	SessionID string                `json:"session_id,omitempty"`
	Source    devdash.DevSource     `json:"source"`
	Level     string                `json:"level"`
	Message   string                `json:"message"`
	Fields    json.RawMessage       `json:"fields,omitempty"`
	Raw       string                `json:"raw,omitempty"`
	Parse     devdash.DevEventParse `json:"parse"`
}

func dashboardLogEventsFromDevEvents(items []devdash.DevEvent) []dashboardLogEvent {
	out := make([]dashboardLogEvent, 0, len(items))
	for _, item := range items {
		createdAt := ""
		if !item.CreatedAt.IsZero() {
			createdAt = item.CreatedAt.Format(time.RFC3339Nano)
		}
		out = append(out, dashboardLogEvent{
			ID:        item.ID,
			Time:      createdAt,
			SessionID: item.SessionID,
			Source:    item.Source,
			Level:     item.Level,
			Message:   item.Message,
			Fields:    item.Fields,
			Raw:       item.Raw,
			Parse:     item.Parse,
		})
	}
	return out
}
