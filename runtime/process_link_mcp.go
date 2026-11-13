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
	"time"

	"scenery.sh/internal/mcpcontract"
)

// Service processes answer the process host's MCP tool calls, and the supervisor's activation and drain of
// their background work. A service process serves requests as soon as it
// listens but acquires background work only after activation, which the
// supervisor sends once the process's generation is published; drain revokes it
// for the rest of the process's life while requests pinned to its generation
// still complete.
const (
	processMCPCallPath  = "/__scenery/process/v1/mcp/call"
	processActivatePath = "/__scenery/process/v1/activate"
	processDrainPath    = "/__scenery/process/v1/drain"
	// processDrainWait bounds how long a drain request waits for running
	// background work after revoking it; revocation itself is immediate.
	processDrainWait = 100 * time.Millisecond
)

type processMCPCallRequest struct {
	Call  mcpcontract.ToolCallContext `json:"call"`
	Name  string                      `json:"name"`
	Input json.RawMessage             `json:"input"`
}

type processMCPResponse struct {
	Outcome *mcpcontract.ToolOutcome `json:"outcome,omitempty"`
	Error   *processLinkError        `json:"error,omitempty"`
}

type processBackgroundLifecycle interface {
	activate() error
	drain(context.Context) error
}

var processBackground struct {
	sync.Mutex
	lifecycle processBackgroundLifecycle
}

// setProcessBackground records the background work a process-linked runtime
// acquires on activation.
func setProcessBackground(lifecycle processBackgroundLifecycle) {
	processBackground.Lock()
	processBackground.lifecycle = lifecycle
	processBackground.Unlock()
}

func currentProcessBackground() processBackgroundLifecycle {
	processBackground.Lock()
	defer processBackground.Unlock()
	return processBackground.lifecycle
}

func (s *server) registerProcessServiceRoutes() {
	registerRoute(s.public, processMCPCallPath, []string{http.MethodPost}, s.handleProcessMCPCall)
	registerRoute(s.public, processActivatePath, []string{http.MethodPost}, s.handleProcessActivate)
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

func (s *server) handleProcessActivate(w http.ResponseWriter, req *http.Request, _ routeParams) {
	if !authorizeProcessLinkRequest(w, req) {
		return
	}
	if background := currentProcessBackground(); background != nil {
		if err := background.activate(); err != nil {
			// A capability this build does not provide refuses activation until
			// a new build replaces the process; other failures may be repeated.
			status := http.StatusConflict
			if strings.HasPrefix(err.Error(), "capability_unavailable:") {
				status = http.StatusServiceUnavailable
			}
			http.Error(w, err.Error(), status)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleProcessDrain answers 204 when background work stopped and 202 when it
// was revoked but work already running had not stopped within processDrainWait.
func (s *server) handleProcessDrain(w http.ResponseWriter, req *http.Request, _ routeParams) {
	if !authorizeProcessLinkRequest(w, req) {
		return
	}
	if background := currentProcessBackground(); background != nil {
		ctx, cancel := context.WithTimeout(req.Context(), processDrainWait)
		defer cancel()
		if err := background.drain(ctx); errors.Is(err, context.DeadlineExceeded) {
			w.WriteHeader(http.StatusAccepted)
			return
		} else if err != nil {
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
