package runtime

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"scenery.sh/internal/mcpcontract"
)

// Service processes answer the process host's MCP tool calls, durable receipt
// status and cancellation, and a drain request that stops background work of a
// replaced instance while it still serves requests pinned to its generation.
const (
	processMCPCallPath    = "/__scenery/process/v1/mcp/call"
	processMCPDurablePath = "/__scenery/process/v1/mcp/durable"
	processDrainPath      = "/__scenery/process/v1/drain"
)

type processMCPCallRequest struct {
	Call  mcpcontract.ToolCallContext `json:"call"`
	Name  string                      `json:"name"`
	Input json.RawMessage             `json:"input"`
}

type processMCPDurableRequest struct {
	Operation   string                      `json:"operation"`
	Call        mcpcontract.ToolCallContext `json:"call"`
	ExecutionID string                      `json:"execution_id"`
}

type processMCPResponse struct {
	Outcome *mcpcontract.ToolOutcome `json:"outcome,omitempty"`
	Result  json.RawMessage          `json:"result,omitempty"`
	Error   *processLinkError        `json:"error,omitempty"`
}

var processBackgroundDrain struct {
	sync.Mutex
	drain func(context.Context) error
}

// setProcessBackgroundDrain records how this runtime stops schedules, event
// consumers and durable acquisition without stopping request serving.
func setProcessBackgroundDrain(drain func(context.Context) error) {
	processBackgroundDrain.Lock()
	processBackgroundDrain.drain = drain
	processBackgroundDrain.Unlock()
}

func (s *server) registerProcessServiceRoutes() {
	registerRoute(s.public, processMCPCallPath, []string{http.MethodPost}, s.handleProcessMCPCall)
	registerRoute(s.public, processMCPDurablePath, []string{http.MethodPost}, s.handleProcessMCPDurable)
	registerRoute(s.public, processDrainPath, []string{http.MethodPost}, s.handleProcessDrain)
}

// authorizeProcessLinkRequest requires the session process-link token.
func authorizeProcessLinkRequest(w http.ResponseWriter, req *http.Request) bool {
	config, err := currentProcessLink()
	if err != nil || config == nil {
		http.NotFound(w, req)
		return false
	}
	token, found := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !found || subtle.ConstantTimeCompare([]byte(token), []byte(config.Token)) != 1 {
		writeProcessLinkResponse(w, http.StatusUnauthorized, processLinkResponse{Error: &processLinkError{Kind: "error", Message: "permission_denied: process link token rejected"}})
		return false
	}
	setProcessIdentityHeaders(w.Header())
	return true
}

func (s *server) handleProcessMCPCall(w http.ResponseWriter, req *http.Request, _ routeParams) {
	if !authorizeProcessLinkRequest(w, req) {
		return
	}
	var body processMCPCallRequest
	if err := decodeProcessLinkBody(req, &body); err != nil {
		writeProcessMCPResponse(w, http.StatusBadRequest, processMCPResponse{Error: &processLinkError{Kind: "error", Message: "invalid_argument: malformed process MCP request"}})
		return
	}
	outcome, err := MCPToolDispatcher{}.CallTool(req.Context(), body.Call, body.Name, body.Input)
	if err != nil {
		writeProcessMCPResponse(w, http.StatusOK, processMCPResponse{Error: newProcessLinkError(err)})
		return
	}
	writeProcessMCPResponse(w, http.StatusOK, processMCPResponse{Outcome: &outcome})
}

func (s *server) handleProcessMCPDurable(w http.ResponseWriter, req *http.Request, _ routeParams) {
	if !authorizeProcessLinkRequest(w, req) {
		return
	}
	var body processMCPDurableRequest
	if err := decodeProcessLinkBody(req, &body); err != nil || body.Operation != "status" && body.Operation != "cancel" {
		writeProcessMCPResponse(w, http.StatusBadRequest, processMCPResponse{Error: &processLinkError{Kind: "error", Message: "invalid_argument: malformed process MCP durable request"}})
		return
	}
	var result json.RawMessage
	var err error
	if body.Operation == "status" {
		result, err = MCPToolDispatcher{}.Status(req.Context(), body.Call, body.ExecutionID)
	} else {
		result, err = MCPToolDispatcher{}.Cancel(req.Context(), body.Call, body.ExecutionID)
	}
	if err != nil {
		writeProcessMCPResponse(w, http.StatusOK, processMCPResponse{Error: newProcessLinkError(err)})
		return
	}
	writeProcessMCPResponse(w, http.StatusOK, processMCPResponse{Result: result})
}

func (s *server) handleProcessDrain(w http.ResponseWriter, req *http.Request, _ routeParams) {
	if !authorizeProcessLinkRequest(w, req) {
		return
	}
	processBackgroundDrain.Lock()
	drain := processBackgroundDrain.drain
	processBackgroundDrain.Unlock()
	if drain != nil {
		if err := drain(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeProcessLinkBody(req *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(req.Body, processLinkMaxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing process link request data")
	}
	return nil
}

func writeProcessMCPResponse(w http.ResponseWriter, status int, response processMCPResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}
