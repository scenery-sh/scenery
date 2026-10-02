package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
	"scenery.sh/internal/victoria"
)

type runtimeTraceRequest struct {
	AppID    string    `json:"app_id"`
	TraceID  string    `json:"trace_id,omitempty"`
	Since    time.Time `json:"since,omitempty"`
	Limit    int       `json:"limit,omitempty"`
	Service  string    `json:"service,omitempty"`
	Endpoint string    `json:"endpoint,omitempty"`
	Status   string    `json:"status,omitempty"`
}

func (s *dashboardServer) tracesRPC(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var params runtimeTraceRequest
	if err := decodeRuntimeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.AppID == "" {
		return nil, errors.New("select a registered app")
	}
	if params.Limit < 0 || params.Limit > 500 || (params.Status != "" && params.Status != "ok" && params.Status != "error") {
		return nil, errors.New("invalid trace filters: limit must be 1-500 and status ok or error")
	}
	if method == "traces/get" {
		id, err := hex.DecodeString(params.TraceID)
		if err != nil || len(id) != 16 {
			return nil, errors.New("trace_id must be 32 hexadecimal characters")
		}
	} else if params.TraceID != "" {
		return nil, errors.New("use traces/get to select a trace")
	}
	status, err := s.dashboardStatusFor(ctx, params.AppID)
	if err != nil {
		return nil, err
	}
	if status.SessionID == "" {
		return nil, errors.New("selected application has no runtime session")
	}
	backend := s.dashboardVictoria()
	if backend == nil {
		return nil, &runtimeRPCFailure{Code: "capability_unavailable", Diagnostic: "SCN8004", Message: "trace backend unavailable"}
	}
	// Reports use the stable application ID; the independent session identity
	// isolates worktrees. RuntimeAppID identifies processes, not telemetry.
	appID := firstNonEmpty(status.BaseAppID, status.AppID)
	query := devdash.TraceQuery{AppID: appID, SessionID: status.SessionID, TraceID: params.TraceID, Since: params.Since, Limit: params.Limit, ServiceName: params.Service, EndpointName: params.Endpoint, Status: params.Status}
	if query.Limit == 0 {
		query.Limit = 100
	}
	var result any
	if method == "traces/get" {
		result, err = backend.GetTraceDetail(ctx, query)
	} else {
		var items []*devdash.TraceSummary
		items, err = backend.QueryTraceSummaries(ctx, query)
		if items == nil {
			items = []*devdash.TraceSummary{}
		}
		result = struct {
			Traces []*devdash.TraceSummary `json:"traces"`
		}{items}
	}
	budget := runtimeResultBudget{maxRows: 5000, maxBytes: s.rpc.limits.resultBytes, remedy: "select a smaller trace or narrow the trace filters"}
	if errors.Is(err, victoria.ErrTraceResultTooLarge) {
		return nil, resultTooLarge(budget, 0)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		definition, _ := spec.DiagnosticDefinitionFor("SCN9000")
		token := "rpt_" + strings.ToLower(rand.Text())
		machine.ReportInternalFailure(token, "SCN9000", err.Error())
		return nil, &runtimeRPCFailure{Code: "internal", Diagnostic: "SCN9000", Message: definition.Meaning, ReportToken: token}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	detail, isDetail := result.(*devdash.TraceDetail)
	if len(data) > budget.maxBytes || isDetail && detail != nil && len(detail.Spans) > budget.maxRows {
		return nil, resultTooLarge(budget, 0)
	}
	return result, nil
}
