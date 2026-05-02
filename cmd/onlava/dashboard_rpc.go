package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"onlava.com/internal/devdash"
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
	case "pubsub/status":
		var params struct {
			AppID  string `json:"app_id"`
			Period string `json:"period"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		snapshot, err := s.supervisor.store.GetPubSubSnapshot(ctx, params.AppID)
		if err != nil {
			return nil, err
		}
		var updated any
		if !snapshot.UpdatedAt.IsZero() {
			updated = snapshot.UpdatedAt
		}
		period := pubSubHistoryPeriod(params.Period)
		history, err := s.supervisor.store.ListPubSubSnapshots(ctx, params.AppID, time.Now().UTC().Add(-period))
		if err != nil {
			return nil, err
		}
		historyItems := make([]map[string]any, 0, len(history)+1)
		for _, item := range history {
			historyItems = append(historyItems, map[string]any{
				"topics":     json.RawMessage(item.Topics),
				"updated_at": item.UpdatedAt,
			})
		}
		if len(historyItems) == 0 && len(snapshot.Topics) > 0 {
			historyItems = append(historyItems, map[string]any{
				"topics":     json.RawMessage(snapshot.Topics),
				"updated_at": updated,
			})
		}
		return map[string]any{
			"app_id":     snapshot.AppID,
			"topics":     json.RawMessage(snapshot.Topics),
			"updated_at": updated,
			"period":     period.String(),
			"history":    historyItems,
		}, nil
	case "pubsub/messages":
		var params struct {
			AppID     string `json:"app_id"`
			Period    string `json:"period"`
			TopicName string `json:"topic_name"`
			QueueName string `json:"queue_name"`
			Status    string `json:"status"`
			Limit     int    `json:"limit"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		period := pubSubHistoryPeriod(params.Period)
		messages, err := s.supervisor.store.ListPubSubMessages(
			ctx,
			params.AppID,
			time.Now().UTC().Add(-period),
			params.TopicName,
			params.QueueName,
			params.Status,
			params.Limit,
		)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"app_id":     params.AppID,
			"period":     period.String(),
			"topic_name": params.TopicName,
			"queue_name": params.QueueName,
			"status":     params.Status,
			"messages":   messages,
		}, nil
	case "pubsub/message/attempts":
		var params struct {
			AppID            string `json:"app_id"`
			MessageID        string `json:"message_id"`
			SubscriptionName string `json:"subscription_name"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		attempts, err := s.supervisor.store.ListPubSubMessageAttempts(ctx, params.AppID, params.MessageID, params.SubscriptionName)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"app_id":            params.AppID,
			"message_id":        params.MessageID,
			"subscription_name": params.SubscriptionName,
			"attempts":          attempts,
		}, nil
	case "pubsub/clear":
		var params struct {
			AppID string `json:"app_id"`
		}
		_ = json.Unmarshal(raw, &params)
		if params.AppID == "" {
			params.AppID = s.supervisor.activeAppID()
		}
		return s.clearPubSub(ctx, params.AppID)
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
			return nil, fmt.Errorf("%s is unsupported in Onlava", method)
		}
		return nil, fmt.Errorf("method not found: %s", method)
	}
}
