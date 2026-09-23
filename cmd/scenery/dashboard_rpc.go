package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"scenery.sh/internal/devdash"
)

// The development runtime RPC is the documented JSON-RPC 2.0 contract served
// at the app origin's /runtime WebSocket (docs/local-contract.md). Every method
// here is part of that contract except traces/clear, which only
// `scenery traces clear` sends.
func (s *dashboardServer) handleRPC(ctx context.Context, req rpcRequest) rpcResponse {
	result, err := s.dispatchRPC(ctx, req.Method, req.Params)
	if err != nil {
		var details any
		if failure, ok := errors.AsType[*dashboardStorageFailure](err); ok {
			details = failure.Failure
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32000,
				Message: err.Error(),
				Data:    details,
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
	if strings.HasPrefix(method, "storage/") {
		return s.storageRPC(ctx, method, raw)
	}
	switch method {
	case "status":
		var params struct {
			AppID string `json:"app_id"`
		}
		if err := decodeRuntimeParams(raw, &params); err != nil {
			return nil, err
		}
		status, err := s.dashboardStatusFor(ctx, firstNonEmpty(params.AppID, s.dashboardActiveAppID()))
		if err != nil {
			return nil, err
		}
		return newRuntimeStatus(status), nil
	case "traces/clear":
		var params struct {
			AppID string `json:"app_id"`
		}
		_ = json.Unmarshal(raw, &params)
		status, err := s.dashboardStatusFor(ctx, firstNonEmpty(params.AppID, s.dashboardActiveAppID()))
		if err != nil {
			return nil, err
		}
		if victoria := s.dashboardVictoria(); victoria != nil {
			victoria.MarkCleared(dashboardStoreAppID(status), time.Now().UTC())
		}
		return "ok", nil
	case "postgres/tables":
		var params dashboardPostgresRequest
		if err := decodeRuntimeParams(raw, &params); err != nil {
			return nil, err
		}
		return s.postgresTables(ctx, params)
	case "postgres/schema":
		var params dashboardPostgresRequest
		if err := decodeRuntimeParams(raw, &params); err != nil {
			return nil, err
		}
		return s.postgresSchema(ctx, params)
	case "postgres/rows":
		var params dashboardPostgresRowsRequest
		if err := decodeRuntimeParams(raw, &params); err != nil {
			return nil, err
		}
		return s.postgresRows(ctx, params)
	case "db/query":
		var params runtimeQueryRequest
		if err := decodeRuntimeParams(raw, &params); err != nil {
			return nil, err
		}
		return s.queryDB(ctx, params)
	default:
		return nil, fmt.Errorf("method not found: %s", method)
	}
}

// Contract methods reject unknown parameters, so a client that drifted from
// the documented shape fails visibly instead of being silently ignored.
func decodeRuntimeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}

type runtimeQueryRequest struct {
	AppID  string `json:"app_id"`
	Query  string `json:"query"`
	Params []any  `json:"params"`
}

type runtimeQueryResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// runtimeStatus is the `status` result: the documented subset of the session
// record, without the app model or substrate endpoints.
type runtimeStatus struct {
	cliPayloadIdentity
	AppID               string                  `json:"app_id"`
	BaseAppID           string                  `json:"base_app_id,omitempty"`
	SessionID           string                  `json:"session_id,omitempty"`
	AppRoot             string                  `json:"app_root"`
	Running             bool                    `json:"running"`
	SessionStatus       string                  `json:"session_status,omitempty"`
	SessionStatusReason string                  `json:"session_status_reason,omitempty"`
	Compiling           bool                    `json:"compiling"`
	CompileError        string                  `json:"compile_error,omitempty"`
	PID                 string                  `json:"pid,omitempty"`
	Routes              map[string]string       `json:"routes"`
	ServiceProcesses    []runtimeServiceProcess `json:"service_processes"`
	Observability       *runtimeObservability   `json:"observability,omitempty"`
}

type runtimeServiceProcess struct {
	Name                   string `json:"name"`
	PID                    string `json:"pid,omitempty"`
	Generation             uint64 `json:"generation,omitempty"`
	ImplementationRevision string `json:"implementation_revision,omitempty"`
	State                  string `json:"state"`
	Reason                 string `json:"reason,omitempty"`
}

type runtimeObservability struct {
	Enabled bool          `json:"enabled"`
	Message string        `json:"message,omitempty"`
	Metrics runtimeSignal `json:"metrics"`
	Logs    runtimeSignal `json:"logs"`
	Traces  runtimeSignal `json:"traces"`
}

type runtimeSignal struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
}

func newRuntimeStatus(status devdash.AppStatus) runtimeStatus {
	out := runtimeStatus{
		cliPayloadIdentity:  newCLIPayloadIdentity("scenery.dev-runtime.status"),
		AppID:               status.AppID,
		BaseAppID:           status.BaseAppID,
		SessionID:           status.SessionID,
		AppRoot:             status.AppRoot,
		Running:             status.Running,
		SessionStatus:       status.SessionStatus,
		SessionStatusReason: status.SessionStatusReason,
		Compiling:           status.Compiling,
		CompileError:        status.CompileError,
		PID:                 status.PID,
		Routes:              status.Routes,
		ServiceProcesses:    make([]runtimeServiceProcess, 0, len(status.ServiceProcesses)),
	}
	if out.Routes == nil {
		out.Routes = map[string]string{}
	}
	for _, process := range status.ServiceProcesses {
		out.ServiceProcesses = append(out.ServiceProcesses, runtimeServiceProcess{
			Name:                   process.Name,
			PID:                    process.PID,
			Generation:             process.Generation,
			ImplementationRevision: process.ImplementationRevision,
			State:                  process.State,
			Reason:                 process.Reason,
		})
	}
	if state := status.Observability; state != nil {
		out.Observability = &runtimeObservability{
			Enabled: state.Enabled,
			Message: state.Message,
			Metrics: newRuntimeSignal(state.Metrics),
			Logs:    newRuntimeSignal(state.Logs),
			Traces:  newRuntimeSignal(state.Traces),
		}
	}
	return out
}

func newRuntimeSignal(state devdash.ObservabilityBackendState) runtimeSignal {
	return runtimeSignal{Enabled: state.Enabled, Available: state.Available, Status: state.Status, Message: state.Message}
}
